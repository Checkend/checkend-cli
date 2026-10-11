package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/jv"
	"github.com/Checkend/checkend-cli/internal/output"
)

var cursorPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

func issueCommands() []*Command { return []*Command{issueListCommand(), issueViewCommand()} }

// listFilters are the issue list options that go to the server unchanged: option -> parameter.
var listFilters = [][2]string{
	{"status", "status"},
	{"project", "project"},
	{"release", "release"},
	{"env", "environment"},
	{"since", "since"},
	{"search", "search"},
	{"limit", "limit"},
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`)

// shellQuote quotes a value for a command that people can copy.
func shellQuote(value string) string {
	if shellSafe.MatchString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func issueListCommand() *Command {
	return &Command{
		Path:    []string{"issue", "list"},
		Summary: "List issues, the most recent first (default: the linked project)",
		Usage:   "[--status <s>] [--release <v>] [--env <name>] [--since <t>] [--search <text>] [--project <id|slug>] [--limit <n>] [--cursor <c>]",
		Details: []string{
			"  --status <status>    unresolved, resolved or ignored",
			"  --release <version>  Issues seen in this release",
			"  --env <name>         Issues seen in this environment",
			"  --since <time>       Seen since 30m, 24h, 7d or an ISO time",
			"  --search <text>      Text in the title or the culprit",
			"  --project <id|slug>  Default: the project in .checkend.json, else all projects",
			"  --limit <n>          1 to 100 (default 25)",
			"  --cursor <cursor>    The next page, from the previous list",
		},
		Options: map[string]Kind{"status": String, "project": String, "release": String, "env": String,
			"since": String, "search": String, "limit": String, "cursor": String},
		Run: func(s *Session) error {
			client, err := s.API()
			if err != nil {
				return err
			}
			project, err := s.ProjectFilter()
			if err != nil {
				return err
			}
			query := map[string]string{"cursor": s.String("cursor")}
			for _, f := range listFilters {
				query[f[1]] = s.String(f[0])
			}
			query["project"] = project
			raw, err := listIssues(s, client, query)
			if err != nil {
				return err
			}
			s.Out.Data(raw, func(p any) string {
				data := jv.List(jv.Get(p, "data"))
				if len(data) == 0 {
					return "No issues."
				}
				now := s.Host.Now()
				var rows [][]any
				for _, i := range data {
					rows = append(rows, []any{jv.Get(i, "id"), jv.Get(i, "status"), jv.Get(i, "level"), jv.Get(i, "times_seen"), output.Ago(jv.Get(i, "last_seen"), now), jv.Get(i, "title")})
				}
				table := s.Out.Table([]string{"ID", "STATUS", "LEVEL", "EVENTS", "LAST SEEN", "TITLE"}, rows)
				next := jv.Str(jv.Get(p, "next_cursor"))
				if !cursorPattern.MatchString(next) {
					return table
				}
				var args []string
				for _, f := range listFilters {
					value := s.String(f[0])
					if f[0] == "project" {
						value = project
					}
					if s.Has(f[0]) && value != "" {
						args = append(args, "--"+f[0]+" "+shellQuote(value))
					}
				}
				args = append(args, "--cursor "+next)
				return table + "\n\nMore: ce issue list " + strings.Join(args, " ")
			})
			return nil
		},
	}
}

var issueRef = regexp.MustCompile(`^(?:(.+)-)?(\d+)$`)

func issueViewCommand() *Command {
	return &Command{
		Path:    []string{"issue", "view"},
		Summary: "Show one issue",
		Usage:   "<id>",
		Details: []string{"  <id>  The issue number (42), or with a project prefix (api-42) to check the project"},
		Args:    []string{"id"},
		Run: func(s *Session) error {
			m := issueRef.FindStringSubmatch(s.Args[0])
			if m == nil {
				m = []string{"", "", ""}
			}
			id, err := ParseID(m[2], "The issue ID")
			if err != nil {
				return err
			}
			client, err := s.API()
			if err != nil {
				return err
			}
			raw, err := client.Get(fmt.Sprintf("/v1/issues/%d", id), nil)
			if raw, err = api.OrNotFound(raw, err, fmt.Sprintf("Issue %d not found.", id), ""); err != nil {
				return err
			}
			if prefix := m[1]; prefix != "" {
				project, err := s.Project(client, prefix)
				if err != nil {
					return err
				}
				body, _ := jv.Decode(raw)
				if jv.Str(project["id"]) != jv.Str(jv.Get(body, "data", "project_id")) {
					return clierr.New(clierr.NotFound, fmt.Sprintf("Issue %d is not in project %s.", id, jv.Str(project["slug"])), "")
				}
			}
			s.Out.Data(raw, func(d any) string {
				issue := jv.Get(d, "data")
				latest := jv.Get(issue, "latest_event_id")
				return card(s, issue, []nextStep{
					{fmt.Sprintf("ce event view %s", jv.Str(latest)), "the latest event", latest != nil},
					{"ce issue list", "all issues", true},
				})
			})
			return nil
		},
	}
}

func nextCommand() *Command {
	return &Command{
		Path:    []string{"next"},
		Summary: "Show the issue to work on next (the most recent unresolved issue)",
		Usage:   "[--project <id|slug>]",
		Options: map[string]Kind{"project": String},
		Run: func(s *Session) error {
			client, err := s.API()
			if err != nil {
				return err
			}
			project, err := s.ProjectFilter()
			if err != nil {
				return err
			}
			raw, err := listIssues(s, client, map[string]string{"project": project, "status": "unresolved", "limit": "1"})
			if err != nil {
				return err
			}
			page, _ := jv.Decode(raw)
			top := jv.Get(page, "data", 0)
			if top == nil {
				s.Out.Data(map[string]any{"data": nil}, func(any) string { return "No unresolved issues." })
				return nil
			}
			body, err := client.Get("/v1/issues/"+jv.Str(jv.Get(top, "id")), nil)
			if err != nil {
				return err
			}
			s.Out.Data(body, func(d any) string {
				issue := jv.Get(d, "data")
				latest := jv.Get(issue, "latest_event_id")
				return card(s, issue, []nextStep{
					{"ce issue view " + jv.Str(jv.Get(issue, "id")), "the issue", true},
					{"ce event view " + jv.Str(latest), "the latest event", latest != nil},
				})
			})
			return nil
		},
	}
}

// listIssues calls GET /v1/issues. The server answers 404 project_not_found for an unknown
// ?project=. It warns when ingest stores counters only.
func listIssues(s *Session, client *api.Client, query map[string]string) (json.RawMessage, error) {
	raw, err := client.Get("/v1/issues", query)
	ref, _ := s.ProjectRef()
	raw, err = api.OrNotFound(raw, err, fmt.Sprintf("Project %q not found.", strings.TrimSpace(ref)), "Run `ce project list` to see the projects.")
	if err != nil {
		return nil, err
	}
	page, _ := jv.Decode(raw)
	if jv.Str(jv.Get(page, "ingest", "mode")) == "counters_only" {
		s.Out.Note("Note: ingest stores counters only (budget mode) until 00:00 UTC. New events are counted, not stored.")
	}
	return raw, nil
}

type nextStep struct {
	command, what string
	show          bool
}

func card(s *Session, issue any, steps []nextStep) string {
	out := s.Out
	now := s.Host.Now()
	when := func(v any) string {
		if !jv.Truthy(v) {
			return "-"
		}
		return fmt.Sprintf("%s (%s)", output.Ago(v, now), jv.Str(v))
	}
	var releases []string
	for _, r := range []any{jv.Get(issue, "first_release"), jv.Get(issue, "last_release")} {
		if jv.Truthy(r) && (len(releases) == 0 || releases[0] != jv.Str(r)) {
			releases = append(releases, jv.Str(r))
		}
	}
	releaseText := "-"
	if len(releases) > 0 {
		releaseText = strings.Join(releases, " … ")
	}
	width := 0
	for _, st := range steps {
		if st.show {
			width = max(width, len(st.command))
		}
	}
	lines := []string{
		out.Bold(fmt.Sprintf("#%s %s", jv.Str(jv.Get(issue, "id")), jv.Str(jv.Get(issue, "title")))),
		"",
		out.Fields([]output.Row{
			{Label: "Status", Value: jv.Get(issue, "status")},
			{Label: "Level", Value: out.Level(jv.Get(issue, "level"))},
			{Label: "Culprit", Value: jv.Get(issue, "culprit")},
			{Label: "Events", Value: fmt.Sprintf("%s (%s stored)", jv.Str(jv.Get(issue, "times_seen")), jv.Str(jv.Get(issue, "stored_events")))},
			{Label: "First seen", Value: when(jv.Get(issue, "first_seen"))},
			{Label: "Last seen", Value: when(jv.Get(issue, "last_seen"))},
			{Label: "Releases", Value: releaseText},
			{Label: "Project", Value: jv.Get(issue, "project_id")},
		}),
		"",
		"Next:",
	}
	for _, st := range steps {
		if st.show {
			lines = append(lines, fmt.Sprintf("  %-*s  %s", width, st.command, out.Dim(st.what)))
		}
	}
	return strings.Join(lines, "\n")
}
