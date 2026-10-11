package cli_test

import (
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func normalized(overrides J) J {
	event := J{
		"version": 1, "event_id": "abc", "timestamp": "2026-10-10T11:00:00.000Z", "level": "error",
		"platform": "node", "release": "1.2.3", "environment": "production", "server_name": "web-1",
		"message": nil, "message_template": nil,
		"exceptions": []any{J{
			"type": "TypeError", "value": "Cannot read properties of undefined (reading 'id')",
			"module": nil, "mechanism": J{"type": "onerror", "handled": false, "synthetic": false},
			"frames": []any{
				J{"filename": "node:internal/process", "abs_path": nil, "module": nil, "function": "processTicks",
					"lineno": 95, "colno": 5, "in_app": false, "context_line": nil, "pre_context": []any{}, "post_context": []any{}},
				J{"filename": "src/handler.ts", "abs_path": "/app/src/handler.ts", "module": nil, "function": "loadUser",
					"lineno": 42, "colno": 17, "in_app": true, "context_line": "  return user.id;", "pre_context": []any{}, "post_context": []any{}},
			},
		}},
		"tags":     []any{[]any{"browser", "Chrome"}, []any{"route", "/users/:id"}},
		"user":     nil,
		"request":  J{"url": "https://app.example.com/users/7", "method": "GET", "query_string": "tab=1", "headers": []any{[]any{"User-Agent", "Mozilla/5.0"}}},
		"contexts": nil, "extra": nil, "breadcrumbs": []any{},
		"sdk": J{"name": "sentry.javascript.node", "version": "11.6.0"}, "fingerprint": nil, "truncated": false,
	}
	for k, v := range overrides {
		event[k] = v
	}
	return event
}

func issueSetup(t *testing.T) (*FakeAPI, *Harness, map[string]string, J, J) {
	api, cli, env := setup(t)
	return api, cli, env, api.AddProject("App"), api.AddProject("Web")
}

func ids(t *testing.T, r Result) []any {
	var out []any
	for _, i := range r.JSON(t)["data"].([]any) {
		out = append(out, i.(map[string]any)["id"])
	}
	return out
}

func listSetup(t *testing.T) (*FakeAPI, *Harness, map[string]string, J, J) {
	api, cli, env, app, web := issueSetup(t)
	api.AddIssue(J{"project_id": app["id"], "title": "TypeError: old", "last_seen": "2026-10-10T08:00:00.000Z"})
	api.AddIssue(J{"project_id": app["id"], "title": "Error: newest", "times_seen": 1234, "last_seen": "2026-10-10T11:55:00.000Z"})
	api.AddIssue(J{"project_id": web["id"], "title": "Error: web", "status": "resolved"})
	return api, cli, env, app, web
}

func query(r *Request) map[string]string {
	out := map[string]string{}
	for k := range r.Query {
		out[k] = r.Query.Get(k)
	}
	return out
}

func TestIssueListPrintsATableNewestFirst(t *testing.T) {
	_, cli, env, _, _ := listSetup(t)
	r := cli.Run([]string{"issue", "list"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
	Matches(t, lines[0], `ID\s+STATUS\s+LEVEL\s+EVENTS\s+LAST SEEN\s+TITLE`)
	Matches(t, lines[1], `2\s+unresolved\s+error\s+1234\s+5m ago\s+Error: newest`)
	Contains(t, r.Stdout, "Error: web")
}

func TestIssueListPrintsTheAPIJSON(t *testing.T) {
	_, cli, env, _, _ := listSetup(t)
	r := cli.Run([]string{"issue", "list"}, Options{Env: env})
	Equal(t, ids(t, r), []any{2, 3, 1})
	body := r.JSON(t)
	if _, ok := body["next_cursor"]; !ok || body["next_cursor"] != nil {
		t.Error("next_cursor")
	}
	if _, ok := body["ingest"]; !ok {
		t.Error("ingest")
	}
}

func TestIssueListFiltersByStatusLimitAndTheLinkedProject(t *testing.T) {
	api, cli, env, _, _ := listSetup(t)
	cli.Write(".checkend.json", `{"project":"app"}`)
	r := cli.Run([]string{"issue", "list", "--status", "unresolved", "--limit", "1"}, Options{Env: env})
	last := api.Last()
	if last.Path != "/v1/issues" {
		t.Errorf("path %s", last.Path)
	}
	Equal(t, query(last), map[string]string{"project": "app", "status": "unresolved", "limit": "1"})
	if r.JSON(t)["next_cursor"] != "1" {
		t.Error("next_cursor")
	}
}

func TestIssueListPassesASlugOrIDWithoutALookup(t *testing.T) {
	api, cli, env, app, web := listSetup(t)
	for _, c := range [][2]string{{"app", "app"}, {"APP", "app"}, {id(web), id(web)}} {
		api.Reset()
		r := cli.Run([]string{"issue", "list", "--project", c[0]}, Options{Env: env})
		Code(t, r, 0)
		Equal(t, api.Paths(), []string{"GET /v1/issues"})
		if got := api.Requests()[0].Query.Get("project"); got != c[1] {
			t.Errorf("project=%q", got)
		}
		want := app["id"]
		if c[1] != "app" {
			want = web["id"]
		}
		for _, i := range r.JSON(t)["data"].([]any) {
			Equal(t, i.(map[string]any)["project_id"], want)
		}
	}
}

func TestIssueListFlagBeforeLink(t *testing.T) {
	api, cli, env, _, web := listSetup(t)
	cli.Write(".checkend.json", `{"project":"app"}`)
	cli.Run([]string{"issue", "list", "--project", id(web)}, Options{Env: env})
	if got := api.Last().Query.Get("project"); got != id(web) {
		t.Errorf("project=%q", got)
	}
}

func TestIssueListShowsTheNextPage(t *testing.T) {
	_, cli, env, _, _ := listSetup(t)
	r := cli.Run([]string{"issue", "list", "--limit", "2"}, Options{Env: env, TTY: true})
	Contains(t, r.Stdout, "ce issue list --limit 2 --cursor 2")
	Equal(t, ids(t, cli.Run([]string{"issue", "list", "--limit", "2", "--cursor", "2"}, Options{Env: env})), []any{1})
}

func TestIssueListExits3ForAnUnknownProject(t *testing.T) {
	api, cli, env, _, _ := listSetup(t)
	r := cli.Run([]string{"issue", "list", "--project", "nope"}, Options{Env: env})
	Code(t, r, 3)
	Equal(t, r.ErrJSON(t), J{"error": `Project "nope" not found.`, "hint": "Run `ce project list` to see the projects."})
	Equal(t, api.Paths(), []string{"GET /v1/issues"})
}

func TestIssueListExits2WhenTheServerRejectsAFilter(t *testing.T) {
	api, cli, env, _, _ := listSetup(t)
	api.Override = func(r *Request) *Reply {
		if r.Path == "/v1/issues" {
			return &Reply{Status: 400, Body: J{"error": "invalid_request", "details": []any{J{"path": "status", "message": "Invalid"}}}}
		}
		return nil
	}
	r := cli.Run([]string{"issue", "list", "--status", "open"}, Options{Env: env})
	Code(t, r, 2)
	Contains(t, r.ErrJSON(t)["error"].(string), "status")
}

func TestIssueListWarnsWhenIngestStoresCountersOnly(t *testing.T) {
	api, cli, env, _, _ := listSetup(t)
	api.Ingest = J{"mode": "counters_only"}
	Contains(t, cli.Run([]string{"issue", "list"}, Options{Env: env, TTY: true}).Stderr, "counters only")
}

func TestIssueListSaysWhenThereAreNoIssues(t *testing.T) {
	api, cli, env, _, _ := listSetup(t)
	api.Issues = nil
	Contains(t, cli.Run([]string{"issue", "list"}, Options{Env: env, TTY: true}).Stdout, "No issues.")
}

func viewSetup(t *testing.T) (*FakeAPI, *Harness, map[string]string, J) {
	api, cli, env, app, _ := issueSetup(t)
	issue := api.AddIssue(J{"project_id": app["id"], "title": "TypeError: Cannot read properties of undefined",
		"culprit": "loadUser(src/handler.ts)", "times_seen": 12, "stored_events": 3,
		"first_release": "1.2.0", "last_release": "1.2.3"})
	api.AddEvent(J{"issue_id": issue["id"], "event": normalized(nil)})
	api.AddEvent(J{"issue_id": issue["id"], "event": normalized(nil)})
	return api, cli, env, issue
}

func TestIssueViewPrintsACard(t *testing.T) {
	_, cli, env, _ := viewSetup(t)
	r := cli.Run([]string{"issue", "view", "1"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, "#1 TypeError: Cannot read properties of undefined")
	Matches(t, r.Stdout, `Status\s+unresolved`)
	Matches(t, r.Stdout, `Events\s+12 \(3 stored\)`)
	Matches(t, r.Stdout, `Culprit\s+loadUser\(src/handler.ts\)`)
	Matches(t, r.Stdout, `Releases\s+1.2.0 … 1.2.3`)
	Contains(t, r.Stdout, "ce event view 2")
}

func TestIssueViewPrintsTheAPIJSON(t *testing.T) {
	_, cli, env, issue := viewSetup(t)
	want := J{}
	for k, v := range issue {
		want[k] = v
	}
	want["latest_event_id"] = 2
	Equal(t, cli.Run([]string{"issue", "view", "1"}, Options{Env: env}).JSON(t), J{"data": want})
}

func TestIssueViewChecksAProjectPrefix(t *testing.T) {
	_, cli, env, _ := viewSetup(t)
	Code(t, cli.Run([]string{"issue", "view", "APP-1"}, Options{Env: env}), 0)
	wrong := cli.Run([]string{"issue", "view", "web-1"}, Options{Env: env})
	Code(t, wrong, 3)
	Equal(t, wrong.ErrJSON(t)["error"], "Issue 1 is not in project web.")
}

func TestIssueViewExits3ForAnUnknownIssue(t *testing.T) {
	_, cli, env, _ := viewSetup(t)
	r := cli.Run([]string{"issue", "view", "99"}, Options{Env: env})
	Code(t, r, 3)
	Equal(t, r.ErrJSON(t)["error"], "Issue 99 not found.")
}

func eventSetup(t *testing.T) (*FakeAPI, *Harness, map[string]string) {
	api, cli, env, app, _ := issueSetup(t)
	issue := api.AddIssue(J{"project_id": app["id"]})
	api.AddEvent(J{"issue_id": issue["id"], "release": "1.2.3", "event": normalized(nil)})
	return api, cli, env
}

func TestEventViewPrintsTheStackRequestAndTags(t *testing.T) {
	_, cli, env := eventSetup(t)
	r := cli.Run([]string{"event", "view", "1"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	out := r.Stdout
	Contains(t, out, "TypeError: Cannot read properties of undefined (reading 'id')")
	Contains(t, out, "loadUser (src/handler.ts:42:17)")
	Contains(t, out, "return user.id;")
	Contains(t, out, "processTicks (node:internal/process:95:5)")
	Contains(t, out, "GET https://app.example.com/users/7?tab=1")
	Matches(t, out, `route\s+/users/:id`)
	Matches(t, out, `Release\s+1.2.3`)
	if strings.Index(out, "loadUser") > strings.Index(out, "processTicks") {
		t.Error("not the most recent call first")
	}
}

func TestEventViewOneSection(t *testing.T) {
	_, cli, env := eventSetup(t)
	tags := cli.Run([]string{"event", "view", "1", "--section", "tags"}, Options{Env: env, TTY: true})
	Matches(t, tags.Stdout, `browser\s+Chrome`)
	NotContains(t, tags.Stdout, "loadUser")
	req := cli.Run([]string{"event", "view", "1", "--section", "request"}, Options{Env: env, TTY: true})
	Contains(t, req.Stdout, "User-Agent: Mozilla/5.0")
	NotContains(t, req.Stdout, "loadUser")
}

func TestEventViewUnknownSectionBeforeAnyRequest(t *testing.T) {
	api, cli, env := eventSetup(t)
	Code(t, cli.Run([]string{"event", "view", "1", "--section", "nope"}, Options{Env: env}), 2)
	if len(api.Requests()) != 0 {
		t.Error("request made")
	}
}

func TestEventViewPrintsTheAPIJSON(t *testing.T) {
	api, cli, env := eventSetup(t)
	Equal(t, cli.Run([]string{"event", "view", "1"}, Options{Env: env}).JSON(t), J{"data": api.Events[0]})
}

func TestEventViewExits3ForAnUnknownEvent(t *testing.T) {
	_, cli, env := eventSetup(t)
	r := cli.Run([]string{"event", "view", "99"}, Options{Env: env})
	Code(t, r, 3)
	Equal(t, r.ErrJSON(t)["error"], "Event 99 not found.")
}

func TestEventViewCleansUntrustedText(t *testing.T) {
	api, cli, env := eventSetup(t)
	hostile := normalized(J{
		"message": "boom\x1b]52;c;cm0gLXJmIH4=\x07 \x1b[8mhidden",
		"exceptions": []any{J{"type": "Error\x1b[2J", "value": "x\u009b6n", "module": nil, "mechanism": nil,
			"frames": []any{J{"filename": "a.js\x1b]8;;https://evil\x1b\\", "function": "f\x07", "lineno": 1, "colno": 1,
				"in_app": true, "context_line": "\x1b[1Aevil()", "abs_path": nil, "module": nil,
				"pre_context": []any{}, "post_context": []any{}}}}},
		"tags":    []any{[]any{"k\x1b[31m", "v\r\x1b[K"}},
		"request": J{"url": "https://x\x1b[0m", "method": "GET", "query_string": nil, "headers": []any{}},
	})
	api.Events[0]["event"] = hostile
	r := cli.Run([]string{"event", "view", "1"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	NotMatches(t, r.All, "[\x00-\x08\x0b-\x1f\u007f-\u009f]")
	Contains(t, r.Stdout, "boom")
	Contains(t, r.Stdout, "evil()")
	js := cli.Run([]string{"event", "view", "1"}, Options{Env: env})
	NotMatches(t, js.Stdout, "[\x00-\x09\x0b-\x1f\u007f-\u009f]")
	Equal(t, js.JSON(t)["data"].(map[string]any)["event"], hostile)
}

func TestNextShowsTheMostRecentUnresolvedIssue(t *testing.T) {
	api, cli, env, app, _ := issueSetup(t)
	api.AddIssue(J{"project_id": app["id"], "title": "Error: older", "last_seen": "2026-10-10T09:00:00.000Z"})
	top := api.AddIssue(J{"project_id": app["id"], "title": "Error: top", "last_seen": "2026-10-10T11:00:00.000Z"})
	api.AddIssue(J{"project_id": app["id"], "title": "Error: resolved", "status": "resolved", "last_seen": "2026-10-10T11:59:00.000Z"})
	api.AddEvent(J{"issue_id": top["id"]})
	r := cli.Run([]string{"next"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, "#2 Error: top")
	Contains(t, r.Stdout, "ce issue view 2")
	Contains(t, r.Stdout, "ce event view 1")
	Equal(t, query(api.Requests()[0]), map[string]string{"status": "unresolved", "limit": "1"})
}

func TestNextPrintsJSON(t *testing.T) {
	api, cli, env, app, _ := issueSetup(t)
	issue := api.AddIssue(J{"project_id": app["id"]})
	want := J{}
	for k, v := range issue {
		want[k] = v
	}
	want["latest_event_id"] = nil
	Equal(t, cli.Run([]string{"next"}, Options{Env: env}).JSON(t), J{"data": want})
}

func TestNextUsesTheLinkedProject(t *testing.T) {
	api, cli, env, app, web := issueSetup(t)
	api.AddIssue(J{"project_id": app["id"]})
	cli.Write(".checkend.json", `{"project":"`+id(web)+`"}`)
	r := cli.Run([]string{"next"}, Options{Env: env})
	Equal(t, api.Paths(), []string{"GET /v1/issues"})
	if api.Requests()[0].Query.Get("project") != id(web) {
		t.Error("project")
	}
	Equal(t, r.JSON(t), J{"data": nil})
}

func TestNextPassesASlug(t *testing.T) {
	api, cli, env, app, web := issueSetup(t)
	api.AddIssue(J{"project_id": app["id"], "title": "Error: in app"})
	api.AddIssue(J{"project_id": web["id"], "title": "Error: in web", "last_seen": "2026-10-10T11:59:00.000Z"})
	r := cli.Run([]string{"next", "--project", "App"}, Options{Env: env})
	if api.Requests()[0].Query.Get("project") != "app" {
		t.Error("project")
	}
	for _, p := range api.Paths() {
		if p == "GET /v1/projects" {
			t.Error("project lookup")
		}
	}
	Equal(t, r.JSON(t)["data"].(map[string]any)["title"], "Error: in app")
}

func TestNextExits3ForAnUnknownProject(t *testing.T) {
	_, cli, env, _, _ := issueSetup(t)
	cli.Write(".checkend.json", `{"project":"gone"}`)
	r := cli.Run([]string{"next"}, Options{Env: env})
	Code(t, r, 3)
	Equal(t, r.ErrJSON(t), J{"error": `Project "gone" not found.`, "hint": "Run `ce project list` to see the projects."})
}

func TestNextSaysWhenThereIsNothingToDo(t *testing.T) {
	_, cli, env, _, _ := issueSetup(t)
	r := cli.Run([]string{"next"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, "No unresolved issues.")
}
