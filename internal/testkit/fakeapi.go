// Package testkit has the in-process fake of the Checkend API and the CLI harness for tests.
package testkit

import (
	"bytes"
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
	Header        http.Header
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
	Token string
	// SetupKey opens the /v1/setup routes. Tokens are more valid API tokens (agent tokens).
	SetupKey string
	Tokens   []string
	// AgentApps is the app (public project ID) of each agent token.
	AgentApps  map[string]string
	SetupMD    string
	TestEvents map[string]bool
	Reports    []string
	MCPHeaders []http.Header
	Uploads    []*Request
	URL        string
	Health     J
	Ingest     J
	Projects   []J
	Issues     []J
	Events     []J
	Override   func(r *Request) *Reply

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
		Token:      "ce_test_token_0123456789",
		SetupKey:   "cks_test_setup_key_0123456789abcdef",
		TestEvents: map[string]bool{},
		AgentApps:  map[string]string{},
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
	f.SetupMD = "# Set up Checkend error tracking\n\nCheckend (" + f.URL + ") receives errors from Sentry SDKs.\n\n## 3. Install the Sentry SDK\n\nInstall the official Sentry SDK.\n"
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
		Header:        req.Header.Clone(),
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
	if reply.Status == 204 || reply.Status == 202 && reply.Body == nil && reply.Raw == "" {
		w.WriteHeader(reply.Status)
		return
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
	if r.Method == "GET" && r.Path == "/agent/setup.md" {
		return &Reply{Status: 200, Raw: f.SetupMD, Headers: map[string]string{"Content-Type": "text/markdown; charset=utf-8"}}
	}
	if r.Method == "GET" && strings.HasPrefix(r.Path, "/agent/tasks/") {
		return &Reply{Status: 200, Raw: "# " + strings.TrimPrefix(r.Path, "/agent/tasks/") + "\n\n## Steps\n\n1. Replace the Sentry DSN.\n", Headers: map[string]string{"Content-Type": "text/markdown; charset=utf-8"}}
	}
	if r.Path == "/mcp" {
		return f.mcp(r)
	}
	if strings.HasPrefix(r.Path, "/v1/setup/") {
		return f.setup(r)
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
	if !f.validToken(r.Authorization) {
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
	case r.Method == "POST" && r.Path == "/v1/sourcemaps":
		if r.ContentType != "application/x-checkend-sourcemap-index" && r.ContentType != "application/json" {
			return &Reply{Status: 415, Body: J{"error": "unsupported_media_type"}}
		}
		if f.FindProject(r.Query.Get("project")) == nil {
			return notFound("project_not_found")
		}
		if r.Query.Get("release") == "" || r.Query.Get("name") == "" {
			return invalid("release")
		}
		if !bytes.HasPrefix(r.Raw, []byte("CKSI")) {
			return &Reply{Status: 400, Body: J{"error": "invalid_sourcemap", "reason": "Not an index."}}
		}
		f.Uploads = append(f.Uploads, r)
		return &Reply{Status: 201, Body: J{"data": J{"id": len(f.Uploads), "release": r.Query.Get("release"), "path": r.Query.Get("name"), "bytes": len(r.Raw)}}}
	case r.Method == "POST" && r.Path == "/v1/deploys":
		body, _ := r.Body.(J)
		ref := fmt.Sprint(body["project"])
		if f.FindProject(ref) == nil {
			return notFound("project_not_found")
		}
		if c, _ := body["commit"].(string); !regexp.MustCompile(`^[0-9a-f]{7,40}$`).MatchString(c) {
			return invalid("commit")
		}
		out := J{"id": 1, "project_id": f.FindProject(ref)["id"], "commit": body["commit"], "release": body["release"], "environment": body["environment"]}
		if out["release"] == nil {
			out["release"] = body["commit"]
		}
		if out["environment"] == nil {
			out["environment"] = "production"
		}
		return &Reply{Status: 201, Body: J{"data": out}}
	}
	if m := regexp.MustCompile(`^/v1/projects/([^/]+)(/keys(?:/([^/]+))?)?$`).FindStringSubmatch(r.Path); m != nil {
		project := f.FindProject(m[1])
		if project == nil {
			return notFound("not_found")
		}
		dsns := project["dsns"].([]any)
		switch {
		case r.Method == "DELETE" && m[2] == "":
			f.Projects = slices.DeleteFunc(f.Projects, func(p J) bool { return fmt.Sprint(p["id"]) == fmt.Sprint(project["id"]) })
			return &Reply{Status: 204}
		case r.Method == "POST" && m[2] == "/keys" && m[3] == "":
			key := fmt.Sprintf("rotated%d", len(dsns)+1)
			host := strings.TrimPrefix(f.URL, "http://")
			d := fmt.Sprintf("http://%s@%s/%v", key, host, project["id"])
			project["dsns"] = append(dsns, d)
			return &Reply{Status: 201, Body: J{"data": J{"public_key": key, "project_id": project["id"], "created_at": "2026-10-10T12:00:00.000Z", "dsn": d}}}
		case r.Method == "DELETE" && m[3] != "":
			for i, d := range dsns {
				u, _ := url.Parse(d.(string))
				if u.User.Username() == m[3] {
					if len(dsns) == 1 {
						return &Reply{Status: 409, Body: J{"error": "last_key", "message": "Add a new key first; the only active key is not revoked."}}
					}
					project["dsns"] = slices.Delete(slices.Clone(dsns), i, i+1)
					return &Reply{Status: 204}
				}
			}
			return notFound("not_found")
		}
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

func (f *FakeAPI) validToken(authorization string) bool {
	if authorization == "Bearer "+f.Token {
		return true
	}
	for _, t := range f.Tokens {
		if authorization == "Bearer "+t {
			return true
		}
	}
	return false
}

// setup is /v1/setup: a setup key or the admin token.
func (f *FakeAPI) setup(r *Request) *Reply {
	key := r.Authorization == "Bearer "+f.SetupKey
	if !key && r.Authorization != "Bearer "+f.Token {
		if !f.validToken(r.Authorization) {
			return &Reply{Status: 401, Body: J{"error": "unauthorized"}}
		}
		// An agent token can report the steps of its own app only.
		app := f.AgentApps[strings.TrimPrefix(r.Authorization, "Bearer ")]
		if m := regexp.MustCompile(`^/v1/setup/apps/([^/]+)/report$`).FindStringSubmatch(r.Path); m == nil || app == "" || f.FindProject(m[1]) == nil || fmt.Sprint(f.FindProject(m[1])["id"]) != app {
			return &Reply{Status: 403, Body: J{"error": "forbidden", "required_scope": "admin"}}
		}
	}
	body, _ := r.Body.(J)
	if r.Method == "POST" && r.Path == "/v1/setup/apps" {
		name, _ := body["name"].(string)
		if strings.TrimSpace(name) == "" {
			return invalid("name")
		}
		for _, p := range f.Projects {
			if p["name"] == name {
				return &Reply{Status: 200, Body: J{"data": f.setupApp(p, false)}}
			}
		}
		var platform []string
		if p, ok := body["platform"].(string); ok {
			platform = append(platform, p)
		}
		p := f.AddProject(name, platform...)
		if len(platform) == 0 {
			p["platform"] = nil
		}
		return &Reply{Status: 201, Body: J{"data": f.setupApp(p, true)}}
	}
	if r.Method == "POST" && r.Path == "/v1/setup/tokens" {
		ref, _ := body["project"].(string)
		name, _ := body["name"].(string)
		if f.FindProject(ref) == nil {
			return notFound("not_found")
		}
		token := fmt.Sprintf("ck_test_agent_token_%04d_0123456789", len(f.Tokens)+1)
		f.Tokens = append(f.Tokens, token)
		f.AgentApps[token] = fmt.Sprint(f.FindProject(ref)["id"])
		return &Reply{Status: 201, Body: J{"data": J{"id": len(f.Tokens), "name": name, "scopes": []any{"issues:read", "issues:write"}, "created_at": "2026-10-10T12:00:00.000Z", "token": token}}}
	}
	if m := regexp.MustCompile(`^/v1/setup/apps/([^/]+)/(test-event|report)$`).FindStringSubmatch(r.Path); m != nil && r.Method == "POST" {
		if f.FindProject(m[1]) == nil {
			return notFound("not_found")
		}
		if m[2] == "test-event" {
			if f.TestEvents[m[1]] {
				return &Reply{Status: 409, Body: J{"error": "already_sent", "message": "This app already has its test event."}}
			}
			f.TestEvents[m[1]] = true
			return &Reply{Status: 202, Body: J{"data": J{"sent": true}}}
		}
		if body["step"] != "sdk_installed" {
			return invalid("step")
		}
		f.Reports = append(f.Reports, m[1]+" "+body["step"].(string))
		return &Reply{Status: 204}
	}
	return notFound("not_found")
}

func (f *FakeAPI) setupApp(p J, created bool) J {
	return J{"created": created, "project": J{"id": p["id"], "slug": p["slug"], "name": p["name"], "platform": p["platform"]}, "dsn": p["dsns"].([]any)[0]}
}

// mcp answers JSON-RPC requests with {"echo": method}; notifications get 202.
func (f *FakeAPI) mcp(r *Request) *Reply {
	f.mu.Lock()
	f.MCPHeaders = append(f.MCPHeaders, r.Header)
	f.mu.Unlock()
	if r.Method != "POST" {
		return &Reply{Status: 405, Body: J{"error": "method_not_allowed"}}
	}
	if !f.validToken(r.Authorization) {
		return &Reply{Status: 401, Body: J{"error": "unauthorized"}}
	}
	msg, _ := r.Body.(J)
	if _, ok := msg["id"]; !ok {
		return &Reply{Status: 202}
	}
	return &Reply{Status: 200, Body: J{"jsonrpc": "2.0", "id": msg["id"], "result": J{"echo": msg["method"]}}}
}
