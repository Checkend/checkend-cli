package cli

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"
)

// table writes aligned columns. The header is upper case.
type table struct {
	tw *tabwriter.Writer
}

func newTable(w io.Writer, header ...string) *table {
	t := &table{tw: tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)}
	if len(header) > 0 {
		for i := range header {
			header[i] = strings.ToUpper(header[i])
		}
		t.row(header...)
	}
	return t
}

func (t *table) row(cells ...string) {
	for i := range cells {
		cells[i] = strings.NewReplacer("\t", " ", "\n", " ").Replace(cells[i])
	}
	fmt.Fprintln(t.tw, strings.Join(cells, "\t"))
}

func (t *table) flush() { _ = t.tw.Flush() }

// num formats a count with thousands separators, or a fraction with up to 2
// decimals.
func num(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "-"
	}
	if f != math.Trunc(f) {
		s := strconv.FormatFloat(f, 'f', 2, 64)
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
		return s
	}
	neg := f < 0
	s := strconv.FormatInt(int64(math.Abs(f)), 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// pct formats a share in percent: 6.1%.
func pct(f float64) string {
	return strconv.FormatFloat(f, 'f', 1, 64) + "%"
}

// change formats a change in percent with its sign, or "-" for none.
func change(f *float64) string {
	if f == nil {
		return "-"
	}
	s := strconv.FormatFloat(*f, 'f', 1, 64) + "%"
	if *f > 0 {
		return "+" + s
	}
	return s
}

// duration formats seconds as 1m 05s or 19s.
func duration(sec float64) string {
	d := time.Duration(math.Round(sec)) * time.Second
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// bar draws v as a bar of at most width cells, relative to max.
func bar(v, max float64, width int) string {
	if max <= 0 || v <= 0 {
		return ""
	}
	eighths := int(math.Round(v / max * float64(width*8)))
	if eighths == 0 {
		eighths = 1
	}
	parts := []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}
	return strings.Repeat("█", eighths/8) + parts[eighths%8]
}

// clip shortens s to n runes with an ellipsis.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// cell formats one value of a query row.
func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case float64:
		return num(x)
	case string:
		if x == "" {
			return "(none)"
		}
		return x
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

// metricCell formats a metric value by its name.
func metricCell(name string, v any) string {
	f, ok := v.(float64)
	if !ok {
		return cell(v)
	}
	switch name {
	case "bounce_rate":
		return pct(f)
	case "visit_duration":
		return duration(f)
	}
	return num(f)
}

// periodLabel names a period for a heading.
func periodLabel(p string) string {
	switch p {
	case "realtime":
		return "last 30 minutes"
	case "today":
		return "today"
	case "24h":
		return "last 24 hours"
	case "12m":
		return "last 12 months"
	case "custom":
		return "custom range"
	}
	if strings.HasSuffix(p, "d") {
		return "last " + strings.TrimSuffix(p, "d") + " days"
	}
	return p
}
