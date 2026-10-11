// Package output prints in two modes: short tables and cards on a terminal; the API JSON when
// stdout is not a terminal or with --json. All server text is cleaned before it is printed,
// and known secrets are hidden in all output.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/docs"
	"github.com/Checkend/checkend-cli/internal/host"
	"github.com/Checkend/checkend-cli/internal/jv"
	"github.com/Checkend/checkend-cli/internal/sanitize"
)

const hidden = "[hidden]"

// Omit marks a Fields row that is left out.
var Omit = &struct{ omit bool }{true}

// Output writes to stdout and stderr.
type Output struct {
	JSON    bool
	Color   bool
	Columns int
	host    *host.Host
	secrets []string
}

// New makes the output. jsonFlag is --json; plain is --plain (text also when stdout is not a
// terminal). --json wins over --plain.
func New(h *host.Host, jsonFlag, plain bool) *Output {
	o := &Output{host: h, Columns: h.Stdout.Columns}
	o.JSON = jsonFlag || (!h.Stdout.IsTTY && !plain)
	o.Color = !o.JSON && h.Stdout.IsTTY && h.Getenv("NO_COLOR") == ""
	if o.Columns <= 0 {
		o.Columns = 100
	}
	return o
}

// Hide hides this value in everything that the CLI prints.
func (o *Output) Hide(secret string) {
	if len(secret) >= 4 {
		o.secrets = append(o.secrets, secret)
	}
}

// Data prints the value as JSON, or the text from render. render gets a cleaned copy of the
// value, decoded with json.Number. A json.RawMessage is printed with its own key order.
func (o *Output) Data(value any, render func(clean any) string) {
	if b, ok := value.([]byte); ok {
		value = json.RawMessage(b)
	}
	if o.JSON {
		o.Write(string(sanitize.JSON(value)) + "\n")
		return
	}
	raw, ok := value.(json.RawMessage)
	if !ok {
		raw, _ = json.Marshal(value)
	}
	decoded, err := jv.Decode(raw)
	if err != nil {
		decoded = nil
	}
	o.Write(ensureNewline(render(sanitize.Deep(decoded))))
}

// Info is a message for people. It is not printed in JSON mode.
func (o *Output) Info(text string) {
	if !o.JSON {
		o.Write(ensureNewline(text))
	}
}

// Note is a note on stderr, not in JSON mode.
func (o *Output) Note(text string) {
	if !o.JSON {
		o.writeTo(o.host.Stderr, ensureNewline(text))
	}
}

// Notice is a line on stderr in all modes, for steps that a person or an agent must see.
func (o *Output) Notice(text string) {
	o.writeTo(o.host.Stderr, ensureNewline(sanitize.Text(text)))
}

// Menu writes the lines of a question on stderr, in all modes. Server text in them is cleaned.
func (o *Output) Menu(lines []string) {
	clean := make([]string, len(lines))
	for i, line := range lines {
		clean[i] = sanitize.OneLine(line)
	}
	o.writeTo(o.host.Stderr, strings.Join(clean, "\n")+"\n")
}

// Error prints an error on stderr: JSON {"error","hint"} in JSON mode, else text.
func (o *Output) Error(e *clierr.Error) {
	msg := sanitize.Text(e.Msg)
	hint := sanitize.Text(e.Hint)
	var text string
	if o.JSON {
		body := map[string]string{"error": msg}
		if hint != "" {
			body["hint"] = hint
		}
		if e.Docs != "" {
			body["docs"] = docs.URL(e.Docs)
		}
		text = string(sanitize.JSON(body)) + "\n"
	} else {
		text = o.Red("Error:") + " " + msg + "\n"
		if hint != "" {
			text += hint + "\n"
		}
		if e.Docs != "" {
			text += "Docs: " + docs.URL(e.Docs) + "\n"
		}
	}
	o.writeTo(o.host.Stderr, text)
}

// Write writes text to stdout, with secrets hidden.
func (o *Output) Write(text string) { o.writeTo(o.host.Stdout, text) }

func (o *Output) writeTo(w io.Writer, text string) {
	for _, s := range o.secrets {
		text = strings.ReplaceAll(text, s, hidden)
	}
	_, _ = io.WriteString(w, text)
}

func (o *Output) paint(code, text string) string {
	if !o.Color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (o *Output) Bold(text string) string   { return o.paint("1", text) }
func (o *Output) Dim(text string) string    { return o.paint("2", text) }
func (o *Output) Red(text string) string    { return o.paint("31", text) }
func (o *Output) Green(text string) string  { return o.paint("32", text) }
func (o *Output) Yellow(text string) string { return o.paint("33", text) }

// Level colours an event level.
func (o *Output) Level(level any) string {
	text := jv.Str(level)
	switch text {
	case "fatal", "error":
		return o.Red(text)
	case "warning":
		return o.Yellow(text)
	}
	return text
}

func width(s string) int { return utf8.RuneCountInString(s) }

func pad(s string, n int) string {
	if w := width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// Table makes a table. Cells are one line; the last column is cut to fit the terminal.
func (o *Output) Table(headers []string, rows [][]any) string {
	cells := make([][]string, len(rows))
	for i, row := range rows {
		cells[i] = make([]string, len(row))
		for j, cell := range row {
			cells[i][j] = sanitize.OneLine(jv.Str(cell))
		}
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = width(h)
		for _, row := range cells {
			if i < len(row) {
				widths[i] = max(widths[i], width(row[i]))
			}
		}
	}
	last := len(headers) - 1
	fixed := 0
	for _, w := range widths[:last] {
		fixed += w + 2
	}
	widths[last] = max(10, min(widths[last], o.Columns-fixed))
	line := func(row []string, paint func(string) string) string {
		parts := make([]string, len(row))
		for i, cell := range row {
			if width(cell) > widths[i] {
				cell = string([]rune(cell)[:widths[i]-1]) + "…"
			}
			if i == last {
				parts[i] = paint(cell)
			} else {
				parts[i] = paint(pad(cell, widths[i]))
			}
		}
		return strings.TrimRight(strings.Join(parts, "  "), " ")
	}
	out := []string{line(headers, o.Bold)}
	for _, row := range cells {
		out = append(out, line(row, func(s string) string { return s }))
	}
	return strings.Join(out, "\n")
}

// Row is a label and a value for Fields.
type Row struct {
	Label string
	Value any
}

// Fields makes label/value lines. Rows with the value Omit are left out; null shows "-".
func (o *Output) Fields(rows []Row) string {
	n := 0
	for _, r := range rows {
		if r.Value != Omit {
			n = max(n, width(r.Label))
		}
	}
	var out []string
	for _, r := range rows {
		if r.Value == Omit {
			continue
		}
		out = append(out, o.Dim(pad(r.Label, n))+"  "+jv.Or(r.Value, "-"))
	}
	return strings.Join(out, "\n")
}

func ensureNewline(text string) string {
	if strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

// Ago is "5m ago", "3h ago" or "2d ago"; "-" for a time that cannot be read.
func Ago(iso any, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, jv.Str(iso))
	if err != nil {
		return "-"
	}
	s := max(0, int(now.Sub(t).Round(time.Second).Seconds()))
	switch {
	case s < 60:
		return fmt.Sprintf("%ds ago", s)
	case s < 3600:
		return fmt.Sprintf("%dm ago", s/60)
	case s < 86400:
		return fmt.Sprintf("%dh ago", s/3600)
	}
	return fmt.Sprintf("%dd ago", s/86400)
}
