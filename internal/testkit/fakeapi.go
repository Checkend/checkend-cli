// Package testkit has the in-process fake of the Checkend API and the CLI harness for tests.
package testkit

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// J is a JSON object.
type J = map[string]any

// Request is one request that the fake received.
type Request struct {
	Method        string
	Path          string
	Query         url.Values
	Authorization string
	ContentType   string
	Body          any
	Raw           []byte
}

// Reply replaces a response of the fake.
type Reply struct {
	Status  int
	Body    any
	Headers map[string]string
	Raw     string
}

// FakeAPI is a fake of the Checkend API (GET /health and /v1). It follows guides/api.md of
// the server. Tests change its state and can replace any response with Override.
type FakeAPI struct {
	Token    string
	URL      string
	Health   J
	Ingest   J
	Projects []J
	Issues   []J
	Events   []J
	Override func(r *Request) *Reply

	mu       sync.Mutex
	requests []*Request
	server   *httptest.Server
	nextID   int64
	routes   []route
}

type route struct {
	method  string
	pattern *regexp.Regexp
	handle  func(r *Request, match []string) *Reply
}

// NewFakeAPI starts a fake on 127.0.0.1. It stops at the end of the test.
func NewFakeAPI(t testing.TB) *FakeAPI {
	f := &FakeAPI{
		Token: "ce_test_token_0123456789",
		Health: J{
			"status":     "ok",
			"version":    "0.2.0",
			"migrations": J{"level": 3, "latest": "0003_tokens.sql", "applied": 3},
			"ingest":     J{"mode": "full", "write_failures_today": 0, "retry_after_seconds": 0},
			"time":       "2026-10-10T12:00:00.000Z",
		},
		Ingest:   J{"mode": "full", "rows_written_today": 10},
		Projects: []J{},
		nextID:   4817305926,
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	f.URL = f.server.URL
	t.Cleanup(f.Stop)
	return f
}

// Stop stops the server. Requests after Stop fail to connect.
func (f *FakeAPI) Stop() { f.server.Close() }

// Requests returns the requests so far.
func (f *FakeAPI) Requests() []*Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// Last returns the last request, or nil.
func (f *FakeAPI) Last() *Request {
	r := f.Requests()
	if len(r) == 0 {
		return nil
	}
	return r[len(r)-1]
}

// Paths returns "METHOD /path" for each request.
func (f *FakeAPI) Paths() []string {
	var out []string
	for _, r := range f.Requests() {
		out = append(out, r.Method+" "+r.Path)
	}
	return out
}

// Reset forgets the requests so far.
func (f *FakeAPI) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = nil
}

// Handle adds a route (for endpoints that only some tests need). pattern matches the path.
func (f *FakeAPI) Handle(method, pattern string, handle func(r *Request, match []string) *Reply) {
	f.routes = append(f.routes, route{method, regexp.MustCompile("^" + pattern + "$"), handle})
}

// AddProject adds a project with a 10-digit public ID and a DSN.
func (f *FakeAPI) AddProject(name string, platform ...string) J {
	id := f.nextID
	f.nextID += 1111111
	host := strings.TrimPrefix(f.URL, "http://")
	var p any = "node"
	if len(platform) > 0 {
		p = platform[0]
	}
	project := J{
		"id":         id,
		"name":       name,
		"slug":       regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(name), "-"),
		"platform":   p,
		"created_at": "2026-10-10T12:00:00.000Z",
		"dsns":       []any{fmt.Sprintf("http://key%d@%s/%d", id, host, id)},
	}
	f.Projects = append(f.Projects, project)
	return project
}

// AddIssue adds an issue. fields must have project_id.
func (f *FakeAPI) AddIssue(fields J) J {
	id := len(f.Issues) + 1
	issue := J{
		"id": id, "title": fmt.Sprintf("Error: issue %d", id), "culprit": "app.js in handler",
		"type": "Error", "level": "error", "platform": "node", "status": "unresolved",
		"times_seen": 1, "stored_events": 1,
		"first_seen": "2026-10-10T10:00:00.000Z", "last_seen": "2026-10-10T11:00:00.000Z",
		"first_release": nil, "last_release": nil,
		"fingerprint": fmt.Sprintf("fp%d", id), "fingerprint_version": 1,
	}
	for k, v := range fields {
		issue[k] = v
	}
	f.Issues = append(f.Issues, issue)
	return issue
}

// AddEvent adds an event. fields must have issue_id.
func (f *FakeAPI) AddEvent(fields J) J {
	id := len(f.Events) + 1
	var projectID any
	for _, issue := range f.Issues {
		if num(issue["id"]) == num(fields["issue_id"]) {
			projectID = issue["project_id"]
		}
	}
	event := J{
		"id": id, "project_id": projectID,
		"event_id":  fmt.Sprintf("e%031d", id),
		"timestamp": "2026-10-10T11:00:00.000Z", "received_at": "2026-10-10T11:00:01.000Z",
		"level": "error", "release": nil, "environment": "production",
	}
	for k, v := range fields {
		event[k] = v
	}
	f.Events = append(f.Events, event)
	return event
}

// Remarshal returns v as the JSON decoder gives it (numbers as float64), to compare with output.
func Remarshal(v any) any {
	data, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}

func num(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return -1
}

func (f *FakeAPI) serve(w http.ResponseWriter, req *http.Request) {
	raw, _ := io.ReadAll(req.Body)
	r := &Request{
		Method: req.Method, Path: req.URL.Path, Query: req.URL.Query(),
		Authorization: req.Header.Get("Authorization"),
		ContentType:   req.Header.Get("Content-Type"),
		Raw:           raw,
	}
	if len(raw) > 0 {
		if json.Unmarshal(raw, &r.Body) != nil {
			r.Body = string(raw)
		}
	}
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()
	var reply *Reply
	if f.Override != nil {
		reply = f.Override(r)
	}
	if reply == nil {
		reply = f.route(r)
	}
	w.Header().Set("Content-Type", "application/json")
	for k, v := range reply.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(reply.Status)
	switch {
	case reply.Raw != "":
		_, _ = io.WriteString(w, reply.Raw)
	case reply.Body != nil:
		_ = json.NewEncoder(w).Encode(reply.Body)
	}
}

func notFound(code string) *Reply { return &Reply{Status: 404, Body: J{"error": code}} }

func invalid(path string) *Reply {
	return &Reply{Status: 400, Body: J{"error": "invalid_request", "details": []any{J{"path": path, "message": "Invalid"}}}}
}

var projectRef = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// FindProject finds a project by public ID or slug.
func (f *FakeAPI) FindProject(ref string) J {
	for _, p := range f.Projects {
		if fmt.Sprint(p["id"]) == ref || p["slug"] == ref {
			return p
		}
	}
	return nil
}

func (f *FakeAPI) route(r *Request) *Reply {
	if r.Method == "GET" && r.Path == "/health" {
		return &Reply{Status: 200, Body: f.Health}
	}
	if !strings.HasPrefix(r.Path, "/v1/") {
		return notFound("not_found")
	}
	for _, rt := range f.routes {
		if m := rt.pattern.FindStringSubmatch(r.Path); m != nil && rt.method == r.Method {
			if reply := rt.handle(r, m); reply != nil {
				return reply
			}
		}
	}
	if r.Authorization != "Bearer "+f.Token {
		return &Reply{Status: 401, Body: J{"error": "unauthorized"}}
	}
	switch {
	case r.Method == "GET" && r.Path == "/v1/projects":
		return &Reply{Status: 200, Body: J{"data": f.Projects}}
	case r.Method == "POST" && r.Path == "/v1/projects":
		input, _ := r.Body.(J)
		name, _ := input["name"].(string)
		if strings.TrimSpace(name) == "" {
			return invalid("name")
		}
		platform := []string{}
		if p, ok := input["platform"].(string); ok {
			platform = append(platform, p)
		}
		project := f.AddProject(name, platform...)
		if len(platform) == 0 {
			project["platform"] = nil
		}
		out := J{}
		for k, v := range project {
			if k != "dsns" {
				out[k] = v
			}
		}
		out["dsn"] = project["dsns"].([]any)[0]
		return &Reply{Status: 201, Body: J{"data": out}}
	case r.Method == "GET" && r.Path == "/v1/issues":
		return f.listIssues(r)
	}
	if m := regexp.MustCompile(`^/v1/issues/(\d+)(/events)?$`).FindStringSubmatch(r.Path); m != nil && r.Method == "GET" {
		id, _ := strconv.Atoi(m[1])
		var issue J
		for _, i := range f.Issues {
			if num(i["id"]) == int64(id) {
				issue = i
			}
		}
		if issue == nil {
			return notFound("not_found")
		}
		var events []J
		for _, e := range f.Events {
			if num(e["issue_id"]) == int64(id) {
				events = append(events, e)
			}
		}
		slices.Reverse(events)
		if m[2] != "" {
			list := []any{}
			for _, e := range events {
				c := J{}
				for k, v := range e {
					if k != "event" {
						c[k] = v
					}
				}
				list = append(list, c)
			}
			return &Reply{Status: 200, Body: J{"data": list}}
		}
		out := J{}
		for k, v := range issue {
			out[k] = v
		}
		out["latest_event_id"] = nil
		if len(events) > 0 {
			out["latest_event_id"] = events[0]["id"]
		}
		return &Reply{Status: 200, Body: J{"data": out}}
	}
	if m := regexp.MustCompile(`^/v1/events/(\d+)$`).FindStringSubmatch(r.Path); m != nil && r.Method == "GET" {
		id, _ := strconv.Atoi(m[1])
		for _, e := range f.Events {
			if num(e["id"]) == int64(id) {
				return &Reply{Status: 200, Body: J{"data": e}}
			}
		}
		return notFound("not_found")
	}
	return notFound("not_found")
}

func (f *FakeAPI) listIssues(r *Request) *Reply {
	issues := slices.Clone(f.Issues)
	slices.SortStableFunc(issues, func(a, b J) int {
		return strings.Compare(fmt.Sprint(b["last_seen"]), fmt.Sprint(a["last_seen"]))
	})
	// The project filter takes the public ID or the slug (lowercase).
	if r.Query.Has("project") {
		ref := r.Query.Get("project")
		if !projectRef.MatchString(ref) {
			return invalid("project")
		}
		project := f.FindProject(ref)
		if project == nil {
			return notFound("project_not_found")
		}
		issues = slices.DeleteFunc(issues, func(i J) bool { return num(i["project_id"]) != num(project["id"]) })
	}
	if status := r.Query.Get("status"); status != "" {
		issues = slices.DeleteFunc(issues, func(i J) bool { return i["status"] != status })
	}
	limit := 25
	if l := r.Query.Get("limit"); l != "" {
		limit, _ = strconv.Atoi(l)
	}
	start, _ := strconv.Atoi(r.Query.Get("cursor"))
	end := min(start+limit, len(issues))
	start = min(start, end)
	var next any
	if start+limit < len(issues) {
		next = strconv.Itoa(start + limit)
	}
	page := issues[start:end]
	if page == nil {
		page = []J{}
	}
	return &Reply{Status: 200, Body: J{"data": page, "next_cursor": next, "ingest": f.Ingest}}
}
