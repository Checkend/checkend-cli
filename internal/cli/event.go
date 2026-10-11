package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/jv"
	"github.com/Checkend/checkend-cli/internal/output"
)

var sections = []string{"stack", "request", "tags"}

func eventCommands() []*Command { return []*Command{eventViewCommand()} }

func eventViewCommand() *Command {
	return &Command{
		Path:    []string{"event", "view"},
		Summary: "Show one event: stack, request and tags",
		Usage:   "<id> [--section stack|request|tags]",
		Details: []string{
			"  <id>                 The Checkend event ID (from `ce issue view`)",
			"  --section <name>     Show only stack, request or tags",
		},
		Options: map[string]Kind{"section": String},
		Args:    []string{"id"},
		Run: func(s *Session) error {
			id, err := ParseID(s.Args[0], "The event ID")
			if err != nil {
				return err
			}
			section := s.String("section")
			if s.Has("section") && !slices.Contains(sections, section) {
				return clierr.UsageError(fmt.Sprintf("Unknown section %q.", section), "Use one of: "+strings.Join(sections, ", ")+".")
			}
			client, err := s.API()
			if err != nil {
				return err
			}
			raw, err := client.Get(fmt.Sprintf("/v1/events/%d", id), nil)
			if raw, err = api.OrNotFound(raw, err, fmt.Sprintf("Event %d not found.", id), "Run `ce issue view <id>` to see the events of an issue."); err != nil {
				return err
			}
			s.Out.Data(raw, func(d any) string {
				data := jv.Get(d, "data")
				var parts []string
				if section != "" {
					parts = []string{renderSection(s.Out, data, section)}
				} else {
					parts = append(parts, eventHeader(s, data))
					for _, sec := range sections {
						parts = append(parts, renderSection(s.Out, data, sec))
					}
				}
				return strings.Join(parts, "\n\n")
			})
			return nil
		},
	}
}

// field returns the value of key, or output.Omit when the key is missing.
func field(v any, key string) any {
	if !jv.Has(v, key) {
		return output.Omit
	}
	return jv.Get(v, key)
}

func eventHeader(s *Session, data any) string {
	out := s.Out
	event := jv.Get(data, "event")
	timeText := "-"
	if ts := jv.Get(data, "timestamp"); jv.Truthy(ts) {
		timeText = fmt.Sprintf("%s (%s)", output.Ago(ts, s.Host.Now()), jv.Str(ts))
	}
	message := field(event, "message")
	if message == nil {
		message = output.Omit
	}
	return strings.Join([]string{
		out.Bold(fmt.Sprintf("Event %s (%s)", jv.Str(jv.Get(data, "id")), jv.Str(jv.Get(data, "event_id")))),
		out.Fields([]output.Row{
			{Label: "Time", Value: timeText},
			{Label: "Level", Value: out.Level(jv.Get(data, "level"))},
			{Label: "Release", Value: field(data, "release")},
			{Label: "Environment", Value: field(data, "environment")},
			{Label: "Server", Value: field(event, "server_name")},
			{Label: "Issue", Value: "#" + jv.Str(jv.Get(data, "issue_id"))},
			{Label: "Message", Value: message},
		}),
	}, "\n")
}

func renderSection(out *output.Output, data any, section string) string {
	event := jv.Get(data, "event")
	switch section {
	case "stack":
		exceptions := slices.Clone(jv.List(jv.Get(event, "exceptions")))
		if len(exceptions) == 0 {
			return out.Bold("Stack") + "\n  (no stack trace)"
		}
		slices.Reverse(exceptions)
		lines := []string{out.Bold("Stack (most recent call first)")}
		for _, ex := range exceptions {
			lines = append(lines, out.Red(jv.Or(jv.Get(ex, "type"), "Error")+": "+jv.Str(jv.Get(ex, "value"))))
			frames := slices.Clone(jv.List(jv.Get(ex, "frames")))
			slices.Reverse(frames)
			for _, f := range frames {
				file := jv.Or(jv.Get(f, "filename"), jv.Or(jv.Get(f, "abs_path"), "?"))
				var pos []string
				for _, k := range []string{"lineno", "colno"} {
					if v := jv.Get(f, k); v != nil {
						pos = append(pos, jv.Str(v))
					}
				}
				where := file
				if len(pos) > 0 {
					where += ":" + strings.Join(pos, ":")
				}
				text := fmt.Sprintf("  at %s (%s)", jv.Or(jv.Get(f, "function"), "?"), where)
				inApp := jv.Truthy(jv.Get(f, "in_app"))
				if inApp {
					lines = append(lines, text)
				} else {
					lines = append(lines, out.Dim(text))
				}
				if ctx := jv.Str(jv.Get(f, "context_line")); inApp && ctx != "" {
					lines = append(lines, "       "+strings.TrimSpace(ctx))
				}
			}
		}
		return strings.Join(lines, "\n")
	case "request":
		req := jv.Get(event, "request")
		if !jv.Truthy(jv.Get(req, "url")) && !jv.Truthy(jv.Get(req, "method")) {
			return out.Bold("Request") + "\n  (no request)"
		}
		query := ""
		if q := jv.Str(jv.Get(req, "query_string")); q != "" {
			query = "?" + q
		}
		lines := []string{out.Bold("Request"), strings.TrimSpace(jv.Str(jv.Get(req, "method")) + " " + jv.Str(jv.Get(req, "url")) + query)}
		for _, hdr := range jv.List(jv.Get(req, "headers")) {
			lines = append(lines, fmt.Sprintf("  %s: %s", jv.Str(jv.Get(hdr, 0)), jv.Str(jv.Get(hdr, 1))))
		}
		return strings.Join(lines, "\n")
	default:
		tags := jv.List(jv.Get(event, "tags"))
		if len(tags) == 0 {
			return out.Bold("Tags") + "\n  (no tags)"
		}
		width := 0
		for _, t := range tags {
			width = max(width, len([]rune(jv.Str(jv.Get(t, 0)))))
		}
		lines := []string{out.Bold("Tags")}
		for _, t := range tags {
			key := jv.Str(jv.Get(t, 0))
			lines = append(lines, fmt.Sprintf("  %s%s  %s", key, strings.Repeat(" ", width-len([]rune(key))), jv.Str(jv.Get(t, 1))))
		}
		return strings.Join(lines, "\n")
	}
}
