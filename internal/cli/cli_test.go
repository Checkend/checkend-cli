package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const testToken = "ck_test_0123456789"

// stub is a fake Checkend server: the MCP tools with canned answers, and
// the export endpoint. It records the arguments of each tool call.
type stub struct {
	mu    sync.Mutex
	calls map[string][]map[string]any
	sites []map[string]any
	fail  map[string]string // tool → error text
	srv   *httptest.Server
}

func newStub(t *testing.T, noKPIs ...bool) *stub {
	t.Helper()
	st := &stub{calls: map[string][]map[string]any{}, fail: map[string]string{},
		sites: []map[string]any{{"id": 1, "domain": "stepsies.com", "timezone": "UTC", "visitors_24h": 1012, "visitors_change": -12.0,
			"live": 3, "top_event": map[string]any{"name": "Tool Start", "count": 716}, "name_problems": nil, "waiting_for_first_event": false}}}
	s := sdk.NewServer(&sdk.Implementation{Name: "checkend", Version: "test"}, nil)
	tool := func(name string, answer func(in map[string]any) any) {
		sdk.AddTool(s, &sdk.Tool{Name: name}, func(ctx context.Context, _ *sdk.CallToolRequest, in map[string]any) (*sdk.CallToolResult, any, error) {
			st.mu.Lock()
			st.calls[name] = append(st.calls[name], in)
			msg := st.fail[name]
			st.mu.Unlock()
			if msg != "" {
				return nil, nil, errors.New(msg)
			}
			return nil, answer(in), nil
		})
	}
	tool("list_sites", func(map[string]any) any {
		st.mu.Lock()
		defer st.mu.Unlock()
		return map[string]any{"items": st.sites}
	})
	tool("query", func(in map[string]any) any {
		meta := map[string]any{"site": in["site"], "from": "2026-10-28T00:00:00Z", "to": "2026-11-25T00:00:00Z", "timezone": "America/Toronto"}
		if dims, _ := in["dimensions"].([]any); len(dims) > 0 {
			d := dims[0].(string)
			return map[string]any{"rows": []map[string]any{{d: "/pricing", "visitors": 300, "pageviews": 410}, {d: "/", "visitors": 150, "pageviews": 160}}, "meta": meta}
		}
		if in["interval"] != nil {
			return map[string]any{"rows": []map[string]any{{"date": "2026-11-02T05:00:00Z", "events": 7}, {"date": "2026-11-01T04:00:00Z", "events": 3}}, "meta": meta}
		}
		return map[string]any{"rows": []map[string]any{{"visitors": 19500, "visits": 19800, "pageviews": 23400, "bounce_rate": 90.1, "visit_duration": 65, "events": 3112}}, "meta": meta}
	})
	if len(noKPIs) == 0 || !noKPIs[0] {
		tool("kpis", func(map[string]any) any {
			return map[string]any{
				"current":  map[string]any{"visitors": 19500, "visits": 19800, "pageviews": 23400, "views_per_visit": 1.18, "bounce_rate": 90.1, "visit_duration": 65, "events": 3112},
				"previous": map[string]any{"visitors": 15000, "visits": 15200, "pageviews": 20000, "views_per_visit": 1.32, "bounce_rate": 88.0, "visit_duration": 70, "events": 0},
				"change":   map[string]any{"visitors": 30.0, "visits": 30.3, "pageviews": 17.0, "views_per_visit": -10.6, "bounce_rate": 2.1, "visit_duration": -7.1, "events": nil},
				"meta": map[string]any{"site": "stepsies.com", "timezone": "America/Toronto", "from": "2026-11-18T05:00:00Z", "to": "2026-11-25T05:00:00Z",
					"previous_from": "2026-11-11T05:00:00Z", "previous_to": "2026-11-18T05:00:00Z"}}
		})
	}
	tool("list_events", func(map[string]any) any {
		return map[string]any{"events": []map[string]any{{"name": "ArticleViewed", "count": 13185, "visitors": 8402, "properties": []string{"slug"}, "new": true, "hidden": false, "flags": []any{}}},
			"problems": []map[string]any{{"text": "ArticleViewed = Article Viewed", "fix": map[string]any{"from": "ArticleViewed", "to": "Article Viewed", "label": "Merge"}}}}
	})
	tool("create_funnel", func(in map[string]any) any { return map[string]any{"id": 4, "name": in["name"], "steps": in["steps"]} })
	tool("funnel", func(map[string]any) any {
		return map[string]any{"id": 4, "name": "Visit to signup", "conversion": 2.5, "steps": []map[string]any{
			{"label": "Visit", "visitors": 1000, "rate_from_previous": 100, "rate_overall": 100},
			{"label": "Signup", "visitors": 25, "rate_from_previous": 2.5, "rate_overall": 2.5}}}
	})
	tool("rename_event", func(in map[string]any) any {
		return map[string]any{"id": 1, "from": in["from"], "to": in["to"], "hidden": in["hidden"]}
	})
	tool("list_changes", func(map[string]any) any {
		return map[string]any{"items": []map[string]any{{"id": 1, "site_id": 1, "domain": "stepsies.com", "ts": "2026-11-20T14:00:00Z", "text": "New CTA",
			"source": "agent", "agent": "Claude Code", "result": map[string]any{"label": "Signup +42%", "verdict": "up"}}}}
	})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	mux := http.NewServeMux()
	mux.Handle("/mcp", h)
	mux.HandleFunc("GET /api/v1/export", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("from") == "" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"bad_request","message":"from and to are needed","request_id":"ab12cd34"}`))
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="../stepsies-events.csv"`)
		_, _ = w.Write([]byte("ts,name\n2026-11-01,pageview\n"))
	})
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized","message":"bad token"}`))
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func (st *stub) last(tool string) map[string]any {
	st.mu.Lock()
	defer st.mu.Unlock()
	c := st.calls[tool]
	if len(c) == 0 {
		return nil
	}
	return c[len(c)-1]
}

// run runs ce with a temp config and returns stdout, stderr and the code.
func run(t *testing.T, dir string, env map[string]string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	a := &App{Version: "test", Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Now: func() time.Time { return time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC) },
		Env: func(k string) string {
			if k == "CE_CONFIG" {
				return filepath.Join(dir, "ce.json")
			}
			return env[k]
		}}
	code := a.Run(context.Background(), args)
	return out.String(), errb.String(), code
}

func login(t *testing.T, st *stub) string {
	t.Helper()
	dir := t.TempDir()
	out, errs, code := run(t, dir, nil, "login", "--url", st.srv.URL, "--token", testToken)
	if code != 0 {
		t.Fatalf("login: code %d, %s", code, errs)
	}
	if !strings.Contains(out, "Default site: stepsies.com") {
		t.Fatalf("login output: %q", out)
	}
	return dir
}

func TestLoginSavesConfig(t *testing.T) {
	st := newStub(t)
	dir := login(t, st)
	p := filepath.Join(dir, "ce.json")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	var c Config
	b, _ := os.ReadFile(p)
	_ = json.Unmarshal(b, &c)
	if c.URL != st.srv.URL || c.Token != testToken || c.Site != "stepsies.com" {
		t.Errorf("config %+v", c)
	}
}

func TestLoginBadToken(t *testing.T) {
	st := newStub(t)
	dir := t.TempDir()
	_, errs, code := run(t, dir, nil, "login", "--url", st.srv.URL, "--token", "ck_wrong")
	if code != 1 || !strings.Contains(errs, "refused the token") {
		t.Fatalf("code %d, stderr %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "ce.json")); err == nil {
		t.Error("a refused token was saved")
	}
}

func TestNotLoggedIn(t *testing.T) {
	_, errs, code := run(t, t.TempDir(), nil, "stats")
	if code != 1 || !strings.Contains(errs, "ce login") {
		t.Fatalf("code %d, stderr %q", code, errs)
	}
}

func TestEnvLogin(t *testing.T) {
	st := newStub(t)
	out, errs, code := run(t, t.TempDir(), map[string]string{"CHECKEND_URL": st.srv.URL, "CHECKEND_TOKEN": testToken}, "sites")
	if code != 0 || !strings.Contains(out, "stepsies.com") || !strings.Contains(out, "Tool Start (716)") || !strings.Contains(out, "-12.0%") {
		t.Fatalf("code %d, out %q, err %q", code, out, errs)
	}
}

func TestStats(t *testing.T) {
	st := newStub(t)
	dir := login(t, st)
	out, errs, code := run(t, dir, nil, "stats", "-p", "7d", "-f", "source=Google")
	if code != 0 {
		t.Fatal(errs)
	}
	for _, want := range []string{"stepsies.com · last 7 days · source=Google", "compared with Nov 11, 2026 to Nov 17, 2026",
		"NOW", "BEFORE", "CHANGE", "19,500", "15,000", "+30.0%", "+2.1 pts", "1m 05s", "1m 10s", "1.18", "-10.6%"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats lacks %q:\n%s", want, out)
		}
	}
	// Events had none before: no change.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Events") && !strings.HasSuffix(strings.TrimSpace(line), "-") {
			t.Errorf("events line %q", line)
		}
	}
	k := st.last("kpis")
	b, _ := json.Marshal(k)
	if k["period"] != "7d" || k["site"] != "stepsies.com" || !strings.Contains(string(b), `{"dim":"source","op":"is","value":"Google"}`) {
		t.Errorf("kpis args %s", b)
	}
}

func TestStatsOldServer(t *testing.T) {
	st := newStub(t, true)
	dir := login(t, st)
	out, errs, code := run(t, dir, nil, "stats", "-p", "7d")
	if code != 0 {
		t.Fatal(errs)
	}
	for _, want := range []string{"no comparison yet", "19,500", "90.1%", "Views per visit  1.18"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats lacks %q:\n%s", want, out)
		}
	}
}

func TestTopSendsDimsAndFilters(t *testing.T) {
	st := newStub(t)
	dir := login(t, st)
	out, errs, code := run(t, dir, nil, "top", "page", "-n", "5", "-f", "source=Google", "--filter", "country!=US", "-f", "page~/blog")
	if code != 0 {
		t.Fatal(errs)
	}
	q := st.last("query")
	b, _ := json.Marshal(q)
	for _, want := range []string{`"dimensions":["page"]`, `"metrics":["visitors","pageviews"]`, `"limit":5`,
		`{"dim":"source","op":"is","value":"Google"}`, `{"dim":"country","op":"is_not","value":"US"}`, `{"dim":"page","op":"contains","value":"/blog"}`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("query lacks %s: %s", want, b)
		}
	}
	if !strings.Contains(out, "/pricing") || !strings.Contains(out, "PAGEVIEWS") || !strings.Contains(out, "█") {
		t.Errorf("top output:\n%s", out)
	}
}

func TestChartEventMetric(t *testing.T) {
	st := newStub(t)
	dir := login(t, st)
	out, errs, code := run(t, dir, nil, "chart", "-m", "event:Signup")
	if code != 0 {
		t.Fatal(errs)
	}
	q := st.last("query")
	b, _ := json.Marshal(q)
	if !strings.Contains(string(b), `"metrics":["events"]`) || !strings.Contains(string(b), `"interval":"day"`) ||
		!strings.Contains(string(b), `{"dim":"event","op":"is","value":"Signup"}`) {
		t.Errorf("chart query %s", b)
	}
	// Sorted by time and shown in the site timezone.
	i1, i2 := strings.Index(out, "Sun Nov 01"), strings.Index(out, "Mon Nov 02")
	if i1 < 0 || i2 < 0 || i1 > i2 {
		t.Errorf("chart output:\n%s", out)
	}
}

func TestEventsShowsFix(t *testing.T) {
	st := newStub(t)
	dir := login(t, st)
	out, _, code := run(t, dir, nil, "events")
	if code != 0 || !strings.Contains(out, "13,185") || !strings.Contains(out, "fix: ce rename ArticleViewed 'Article Viewed'") {
		t.Fatalf("code %d:\n%s", code, out)
	}
}

func TestFunnels(t *testing.T) {
	st := newStub(t)
	dir := login(t, st)
	_, errs, code := run(t, dir, nil, "funnels", "add", "Visit to signup", "--step", "page:*", "--step", "event:Signup")
	if code != 0 {
		t.Fatal(errs)
	}
	b, _ := json.Marshal(st.last("create_funnel"))
	if !strings.Contains(string(b), `"steps":[{"type":"page","value":"*"},{"type":"event","value":"Signup"}]`) {
		t.Errorf("create_funnel %s", b)
	}
	_, errs, code = run(t, dir, nil, "funnels", "add", "x", "--step", "nope")
	if code != 2 || !strings.Contains(errs, "bad step") {
		t.Errorf("bad step: code %d %q", code, errs)
	}
	out, _, code := run(t, dir, nil, "funnel", "Visit to signup")
	if code != 0 || !strings.Contains(out, "2. Signup") || !strings.Contains(out, "Conversion: 2.5%") {
		t.Errorf("funnel: code %d\n%s", code, out)
	}
}

func TestToolErrorIsShown(t *testing.T) {
	st := newStub(t)
	dir := login(t, st)
	st.fail["rename_event"] = "this token can only read"
	_, errs, code := run(t, dir, nil, "rename", "a", "b")
	if code != 1 || !strings.Contains(errs, "this token can only read") {
		t.Fatalf("code %d, stderr %q", code, errs)
	}
}

func TestHideAndChanges(t *testing.T) {
	st := newStub(t)
	dir := login(t, st)
	if _, errs, code := run(t, dir, nil, "hide", "Debug"); code != 0 {
		t.Fatal(errs)
	}
	if h := st.last("rename_event"); h["hidden"] != true || h["from"] != "Debug" {
		t.Errorf("hide args %v", h)
	}
	out, _, code := run(t, dir, nil, "changes", "--all")
	if code != 0 || !strings.Contains(out, "Claude Code") || !strings.Contains(out, "Signup +42%") || !strings.Contains(out, "SITE") {
		t.Errorf("changes: %d\n%s", code, out)
	}
	if c := st.last("list_changes"); c["site"] != "" {
		t.Errorf("--all sent site %v", c["site"])
	}
}

func TestSiteChoice(t *testing.T) {
	st := newStub(t)
	st.sites = append(st.sites, map[string]any{"id": 2, "domain": "biller.app", "timezone": "UTC"})
	dir := t.TempDir()
	if _, errs, code := run(t, dir, nil, "login", "--url", st.srv.URL, "--token", testToken); code != 0 {
		t.Fatal(errs)
	}
	_, errs, code := run(t, dir, nil, "stats")
	if code != 2 || !strings.Contains(errs, "biller.app, stepsies.com") {
		t.Fatalf("code %d, stderr %q", code, errs)
	}
	if out, errs, code := run(t, dir, nil, "use", "2"); code != 0 || !strings.Contains(out, "biller.app") {
		t.Fatalf("use: %d %q %q", code, out, errs)
	}
	if _, errs, code := run(t, dir, nil, "stats"); code != 0 {
		t.Fatal(errs)
	}
	if q := st.last("kpis"); q["site"] != "biller.app" {
		t.Errorf("site %v", q["site"])
	}
	if _, errs, code := run(t, dir, nil, "use", "nope.com"); code != 1 || !strings.Contains(errs, "no site") {
		t.Errorf("use unknown: %d %q", code, errs)
	}
}

func TestExport(t *testing.T) {
	st := newStub(t)
	dir := login(t, st)
	wd, _ := os.Getwd()
	work := t.TempDir()
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()
	_, errs, code := run(t, dir, nil, "export")
	if code != 0 {
		t.Fatal(errs)
	}
	// The server's name, without its path.
	b, err := os.ReadFile(filepath.Join(work, "stepsies-events.csv"))
	if err != nil || !strings.HasPrefix(string(b), "ts,name") {
		t.Fatalf("file %q %v", b, err)
	}
	if _, errs, code := run(t, dir, nil, "export"); code != 1 || !strings.Contains(errs, "exists") {
		t.Errorf("second export: %d %q", code, errs)
	}
	out, _, code := run(t, dir, nil, "export", "-o", "-")
	if code != 0 || !strings.HasPrefix(out, "ts,name") {
		t.Errorf("stdout export: %d %q", code, out)
	}
}

func TestLogout(t *testing.T) {
	st := newStub(t)
	dir := login(t, st)
	if out, _, code := run(t, dir, nil, "logout"); code != 0 || !strings.Contains(out, "Logged out") {
		t.Fatal(out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ce.json")); err == nil {
		t.Error("config still there")
	}
}

func TestUnknownCommand(t *testing.T) {
	if _, errs, code := run(t, t.TempDir(), nil, "nope"); code != 2 || !strings.Contains(errs, "unknown command") {
		t.Fatalf("%d %q", code, errs)
	}
	if out, _, code := run(t, t.TempDir(), nil); code != 0 || !strings.Contains(out, "Usage: ce") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestParseFilter(t *testing.T) {
	cases := map[string]filter{
		"source=Google":   {"source", "is", "Google"},
		"country!=US":     {"country", "is_not", "US"},
		"page~/blog":      {"page", "contains", "/blog"},
		"page=/a?b=c":     {"page", "is", "/a?b=c"},
		"prop:plan=pro":   {"prop:plan", "is", "pro"},
		"utm_source!=a=b": {"utm_source", "is_not", "a=b"},
	}
	for in, want := range cases {
		got, err := parseFilter(in)
		if err != nil || got != want {
			t.Errorf("%q: %+v %v", in, got, err)
		}
	}
	for _, bad := range []string{"=x", "source", "source!x"} {
		if _, err := parseFilter(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestFormat(t *testing.T) {
	for in, want := range map[float64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4200: "-4,200", 1.18: "1.18", 2.5: "2.5"} {
		if got := num(in); got != want {
			t.Errorf("num(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[float64]string{19: "19s", 65: "1m 05s", 3700: "1h 01m"} {
		if got := duration(in); got != want {
			t.Errorf("duration(%v) = %q, want %q", in, got, want)
		}
	}
	if got := normURL("app.checkend.com/"); got != "https://app.checkend.com" {
		t.Error(got)
	}
	if got := normURL("localhost:8484"); got != "http://localhost:8484" {
		t.Error(got)
	}
	if got := shellQuote("Article Viewed"); got != "'Article Viewed'" {
		t.Error(got)
	}
}
