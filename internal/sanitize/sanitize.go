// Package sanitize cleans untrusted text. Server text (event messages, frames, request data,
// project names) is untrusted, and a terminal runs escape sequences, so all server text is
// cleaned before it is printed.
package sanitize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var escapeSequences = regexp.MustCompile(strings.Join([]string{
	`\x1b\[[0-?]*[ -/]*[@-~]?`,           // CSI
	`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?`, // OSC, ended by BEL or ST
	`\x1b[PX^_][^\x1b]*(?:\x1b\\)?`,      // DCS, SOS, PM, APC
	`\x1b[ -/]*[0-~]?`,                   // all other escapes
}, "|"))

// C0 controls except tab and newline, DEL, C1 controls, and bidirectional controls.
var controls = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x{7f}-\x{9f}\x{61c}\x{200e}\x{200f}\x{202a}-\x{202e}\x{2066}-\x{2069}]`)

// Text removes escape sequences and control characters. Newlines and tabs stay.
func Text(text string) string {
	text = strings.ToValidUTF8(text, "\ufffd")
	return controls.ReplaceAllString(escapeSequences.ReplaceAllString(text, ""), "")
}

// OneLine is Text with newlines and tabs as spaces, for table cells and messages.
func OneLine(text string) string {
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(Text(text))
}

// Deep returns a copy of a decoded JSON value with every string and object key cleaned.
func Deep(value any) any {
	switch v := value.(type) {
	case string:
		return Text(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = Deep(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[Text(key)] = Deep(item)
		}
		return out
	default:
		return v
	}
}

// encoding/json escapes C0 controls, U+2028 and U+2029, but not DEL, C1 or bidi controls.
var jsonUnsafe = regexp.MustCompile(`[\x{7f}-\x{9f}\x{61c}\x{200e}\x{200f}\x{2028}\x{2029}\x{202a}-\x{202e}\x{2066}-\x{2069}]`)

func escapeUnsafe(text []byte) []byte {
	return jsonUnsafe.ReplaceAllFunc(text, func(char []byte) []byte {
		return fmt.Appendf(nil, `\u%04x`, []rune(string(char))[0])
	})
}

// JSON is the output form of a value: indented, and with the characters that a terminal could
// act on written as \u escapes. The parsed value is unchanged. A json.RawMessage keeps its key
// order and numbers.
func JSON(value any) []byte {
	if raw, ok := value.(json.RawMessage); ok {
		return Indent(raw)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		panic(err) // values that the CLI builds always encode
	}
	return escapeUnsafe(bytes.TrimRight(buf.Bytes(), "\n"))
}

// Indent indents raw JSON from the server and escapes unsafe characters. Key order and
// numbers stay as the server sent them.
func Indent(raw []byte) []byte {
	var buf bytes.Buffer
	if err := json.Indent(&buf, bytes.ToValidUTF8(raw, []byte("\ufffd")), "", "  "); err != nil {
		return JSON(string(raw))
	}
	return escapeUnsafe(buf.Bytes())
}
