package testkit

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// FakeCloudflare is a fake of the parts of the Cloudflare API that `ce install cloudflare`
// uses: token verify, accounts, the workers.dev subdomain, D1 (create, query, Time Travel
// bookmarks), Worker uploads, secrets, schedules and R2 buckets.
type FakeCloudflare struct {
	URL       string
	Token     string
	Accounts  []J
	Subdomain string
	Databases map[string]*FakeD1
	Scripts   map[string]*FakeScript
	Buckets   map[string]bool
	// Calls are "METHOD /path" of each request.
	Calls []string
	// SetupOpen is the setup_open of /health on a workers.dev host.
	SetupOpen bool
	// Fail makes a request fail with HTTP 500 when it returns true ("METHOD /path").
	Fail func(call string) bool

	mu     sync.Mutex
	nextID int
	origin string
}

// FakeD1 is a D1 database: its migrations (d1_migrations) and other tables.
type FakeD1 struct {
	ID         string
	Name       string
	Migrations []string
	// SQL is each statement batch that ran, in order.
	SQL       []string
	Tables    []string
	Bookmarks int
}

// FakeScript is a Worker.
type FakeScript struct {
	Metadata   map[string]any
	Modules    map[string]string
	Types      map[string]string
	Secrets    map[string]string
	Schedules  []string
	WorkersDev bool
}

// NewFakeCloudflare starts the fake with one account and a workers.dev subdomain.
func NewFakeCloudflare(t testing.TB) *FakeCloudflare {
	f := &FakeCloudflare{
		Token:     "cf-test-token-0123456789abcdef0123456789",
		Accounts:  []J{{"id": "acc0123456789abcdef0123456789abcd", "name": "Example Account"}},
		Subdomain: "example",
		Databases: map[string]*FakeD1{},
		Scripts:   map[string]*FakeScript{},
		Buckets:   map[string]bool{},
		SetupOpen: true,
	}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	f.URL = server.URL + "/client/v4"
	f.origin = server.URL
	t.Cleanup(server.Close)
	return f
}

// Client is an HTTP client that sends requests for *.workers.dev to the fake (the installed
// Worker answers /health there) and all other requests to their host.
func (f *FakeCloudflare) Client() *http.Client {
	return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Hostname(), ".workers.dev") {
			out := req.Clone(req.Context())
			out.URL.Scheme, out.URL.Host = "http", strings.TrimPrefix(f.origin, "http://")
			out.Header.Set("X-Forwarded-Host", req.URL.Host)
			return http.DefaultTransport.RoundTrip(out)
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (r roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return r(req) }

// Account is the ID of the first account.
func (f *FakeCloudflare) Account() string { return f.Accounts[0]["id"].(string) }

// DB finds a database by name.
func (f *FakeCloudflare) DB(name string) *FakeD1 {
	for _, db := range f.Databases {
		if db.Name == name {
			return db
		}
	}
	return nil
}

func cfReply(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := J{"success": status < 300, "errors": []any{}, "messages": []any{}, "result": result}
	if status >= 300 {
		body["errors"] = []any{J{"code": 10000 + status, "message": fmt.Sprintf("fake error %d", status)}}
		body["result"] = nil
	}
	_ = json.NewEncoder(w).Encode(body)
}

var (
	cfAccount  = regexp.MustCompile(`^/client/v4/accounts/([^/]+)(/.*)?$`)
	cfDatabase = regexp.MustCompile(`^/d1/database/([^/]+)(/.*)?$`)
	cfScript   = regexp.MustCompile(`^/workers/scripts/([^/]+)(/.*)?$`)
)

func (f *FakeCloudflare) serve(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if host := req.Header.Get("X-Forwarded-Host"); host != "" {
		name := strings.Split(host, ".")[0]
		if req.URL.Path == "/health" && f.Scripts[name] != nil && f.Scripts[name].WorkersDev {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(J{"status": "ok", "setup_open": f.SetupOpen})
			return
		}
		http.NotFound(w, req)
		return
	}
	call := req.Method + " " + strings.TrimPrefix(req.URL.Path, "/client/v4")
	f.Calls = append(f.Calls, call)
	if req.Header.Get("Authorization") != "Bearer "+f.Token {
		cfReply(w, 403, nil)
		return
	}
	if f.Fail != nil && f.Fail(call) {
		cfReply(w, 500, nil)
		return
	}
	path := req.URL.Path
	if path == "/client/v4/user/tokens/verify" {
		cfReply(w, 200, J{"id": "tok", "status": "active"})
		return
	}
	if path == "/client/v4/accounts" {
		cfReply(w, 200, f.Accounts)
		return
	}
	m := cfAccount.FindStringSubmatch(path)
	if m == nil || m[1] != f.Account() {
		cfReply(w, 404, nil)
		return
	}
	rest := m[2]
	switch {
	case rest == "/workers/subdomain" && req.Method == "GET":
		if f.Subdomain == "" {
			cfReply(w, 404, nil)
			return
		}
		cfReply(w, 200, J{"subdomain": f.Subdomain})
	case rest == "/d1/database" && req.Method == "GET":
		list := []any{}
		for _, db := range f.Databases {
			if name := req.URL.Query().Get("name"); name == "" || name == db.Name {
				list = append(list, J{"uuid": db.ID, "name": db.Name})
			}
		}
		cfReply(w, 200, list)
	case rest == "/d1/database" && req.Method == "POST":
		var body J
		_ = json.NewDecoder(req.Body).Decode(&body)
		f.nextID++
		db := &FakeD1{ID: fmt.Sprintf("db-%04d-0000-0000-0000-000000000000", f.nextID), Name: body["name"].(string)}
		f.Databases[db.ID] = db
		cfReply(w, 200, J{"uuid": db.ID, "name": db.Name})
	case cfDatabase.MatchString(rest):
		dm := cfDatabase.FindStringSubmatch(rest)
		db := f.Databases[dm[1]]
		if db == nil {
			cfReply(w, 404, nil)
			return
		}
		f.d1(w, req, db, dm[2])
	case rest == "/r2/buckets" && req.Method == "GET":
		list := []any{}
		for name := range f.Buckets {
			list = append(list, J{"name": name})
		}
		cfReply(w, 200, J{"buckets": list})
	case rest == "/r2/buckets" && req.Method == "POST":
		var body J
		_ = json.NewDecoder(req.Body).Decode(&body)
		f.Buckets[body["name"].(string)] = true
		cfReply(w, 200, J{"name": body["name"]})
	case cfScript.MatchString(rest):
		sm := cfScript.FindStringSubmatch(rest)
		f.script(w, req, sm[1], sm[2])
	default:
		cfReply(w, 404, nil)
	}
}

func (f *FakeCloudflare) d1(w http.ResponseWriter, req *http.Request, db *FakeD1, rest string) {
	switch {
	case rest == "/time_travel/bookmark" && req.Method == "GET":
		db.Bookmarks++
		cfReply(w, 200, J{"bookmark": fmt.Sprintf("%08d-00000000-00004f6d-test", db.Bookmarks)})
	case rest == "/query" && req.Method == "POST":
		var body struct {
			SQL string `json:"sql"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		db.SQL = append(db.SQL, body.SQL)
		sql := strings.TrimSpace(body.SQL)
		switch {
		case strings.HasPrefix(sql, "CREATE TABLE IF NOT EXISTS d1_migrations"):
			if !contains(db.Tables, "d1_migrations") {
				db.Tables = append(db.Tables, "d1_migrations")
			}
			cfReply(w, 200, []any{J{"results": []any{}, "success": true}})
		case strings.HasPrefix(sql, "SELECT name FROM d1_migrations"):
			rows := []any{}
			for _, name := range db.Migrations {
				rows = append(rows, J{"name": name})
			}
			cfReply(w, 200, []any{J{"results": rows, "success": true}})
		case strings.HasPrefix(sql, "SELECT name FROM sqlite_master"):
			rows := []any{}
			for _, name := range db.Tables {
				rows = append(rows, J{"name": name})
			}
			cfReply(w, 200, []any{J{"results": rows, "success": true}})
		default:
			if m := regexp.MustCompile(`INSERT INTO d1_migrations \(name\) VALUES \('([^']+)'\)`).FindStringSubmatch(sql); m != nil {
				db.Migrations = append(db.Migrations, m[1])
			}
			cfReply(w, 200, []any{J{"results": []any{}, "success": true}})
		}
	default:
		cfReply(w, 404, nil)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (f *FakeCloudflare) script(w http.ResponseWriter, req *http.Request, name, rest string) {
	s := f.Scripts[name]
	switch {
	case rest == "" && req.Method == "PUT":
		mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			cfReply(w, 400, nil)
			return
		}
		next := &FakeScript{Modules: map[string]string{}, Types: map[string]string{}, Secrets: map[string]string{}}
		if s != nil {
			next.Schedules, next.WorkersDev = s.Schedules, s.WorkersDev
		}
		reader := multipart.NewReader(req.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				cfReply(w, 400, nil)
				return
			}
			data, _ := io.ReadAll(part)
			if part.FormName() == "metadata" {
				_ = json.Unmarshal(data, &next.Metadata)
				continue
			}
			next.Modules[part.FormName()] = string(data)
			next.Types[part.FormName()] = part.Header.Get("Content-Type")
		}
		if s != nil {
			keep, _ := next.Metadata["keep_bindings"].([]any)
			for _, k := range keep {
				if k == "secret_text" {
					next.Secrets = s.Secrets
				}
			}
		}
		f.Scripts[name] = next
		cfReply(w, 200, J{"id": name})
	case s == nil:
		cfReply(w, 404, nil)
	case rest == "/settings" && req.Method == "GET":
		bindings := []any{}
		if b, ok := s.Metadata["bindings"].([]any); ok {
			bindings = b
		}
		for k := range s.Secrets {
			bindings = append(bindings, J{"type": "secret_text", "name": k})
		}
		cfReply(w, 200, J{"bindings": bindings})
	case rest == "/secrets" && req.Method == "GET":
		list := []any{}
		for k := range s.Secrets {
			list = append(list, J{"name": k, "type": "secret_text"})
		}
		cfReply(w, 200, list)
	case rest == "/secrets" && req.Method == "PUT":
		var body J
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body["type"] != "secret_text" {
			cfReply(w, 400, nil)
			return
		}
		s.Secrets[body["name"].(string)] = body["text"].(string)
		cfReply(w, 200, J{"name": body["name"], "type": "secret_text"})
	case rest == "/schedules" && req.Method == "PUT":
		var body []J
		_ = json.NewDecoder(req.Body).Decode(&body)
		s.Schedules = nil
		for _, c := range body {
			s.Schedules = append(s.Schedules, c["cron"].(string))
		}
		cfReply(w, 200, J{"schedules": body})
	case rest == "/subdomain" && req.Method == "POST":
		var body J
		_ = json.NewDecoder(req.Body).Decode(&body)
		s.WorkersDev = body["enabled"] == true
		cfReply(w, 200, J{"enabled": s.WorkersDev})
	default:
		cfReply(w, 404, nil)
	}
}
