package cli_test

// End-to-end test against a running Checkend server, for example `wrangler dev`. It runs only
// when CHECKEND_E2E_URL and CHECKEND_E2E_TOKEN are set. See README.md. With
// CHECKEND_E2E_BASE_PATH (for example /k7q2x9), the CLI reaches the server through a front stub
// that serves it under that base path only, as a hosted install.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func TestEndToEnd(t *testing.T) {
	base, token := os.Getenv("CHECKEND_E2E_URL"), os.Getenv("CHECKEND_E2E_TOKEN")
	if base == "" || token == "" {
		t.Skip("set CHECKEND_E2E_URL and CHECKEND_E2E_TOKEN")
	}
	if path := os.Getenv("CHECKEND_E2E_BASE_PATH"); path != "" {
		base = basePathFront(t, base, path)
	}
	cli := NewHarness(t)
	login := cli.Run([]string{"login", "--url", base, "--with-token"}, Options{Stdin: token})
	Code(t, login, 0)

	status := cli.Run([]string{"status"})
	Code(t, status, 0)
	Equal(t, status.JSON(t)["health"].(map[string]any)["status"], "ok")

	name := fmt.Sprintf("cli-e2e-%d", time.Now().UnixMilli())
	init := cli.Run([]string{"init", "--name", name, "--platform", "node"})
	Code(t, init, 0)
	out := init.JSON(t)
	dsn := out["dsn"].(string)
	project := out["project"].(map[string]any)
	link, _ := os.ReadFile(filepath.Join(cli.Repo, ".checkend.json"))
	Equal(t, readLink(t, cli), J{"project": jsonString(project["id"]), "slug": project["slug"]})
	NotContains(t, string(link), token)

	projectDSN := cli.Run([]string{"project", "dsn"})
	Contains(t, projectDSN.Stdout, dsn)

	// Send one error, as a Sentry SDK does.
	parsed, _ := url.Parse(dsn)
	// The server scrubs long numbers in messages, so the message has a base-36 suffix.
	message := "e2e boom " + strconv.FormatInt(time.Now().UnixNano(), 36)
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	body, _ := json.Marshal(J{
		"event_id": hex.EncodeToString(id), "timestamp": float64(time.Now().UnixMilli()) / 1000,
		"level": "error", "platform": "node", "release": "e2e-1", "environment": "test",
		"exception": J{"values": []any{J{"type": "TypeError", "value": message, "stacktrace": J{"frames": []any{
			J{"filename": "src/e2e.ts", "function": "runE2e", "lineno": 7, "colno": 3, "in_app": true},
		}}}}},
		"tags": J{"suite": "cli-e2e"},
	})
	req, _ := http.NewRequest("POST", IngestURL(parsed, "store"), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_version=7, sentry_key="+parsed.User.Username()+", sentry_client=checkend-cli-e2e/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("store: %d %s", resp.StatusCode, text)
	}

	next := cli.Run([]string{"next"})
	Code(t, next, 0)
	issue := next.JSON(t)["data"].(map[string]any)
	Contains(t, issue["title"].(string), message)

	list := cli.Run([]string{"issue", "list", "--status", "unresolved"})
	Contains(t, list.Stdout, `"id": `+jsonString(issue["id"]))

	view := cli.Run([]string{"issue", "view", fmt.Sprintf("%s-%s", project["slug"], jsonString(issue["id"]))}, Options{TTY: true})
	Code(t, view, 0)
	Contains(t, view.Stdout, message)

	event := cli.Run([]string{"event", "view", jsonString(issue["latest_event_id"])}, Options{TTY: true})
	Code(t, event, 0)
	Contains(t, event.Stdout, "runE2e (src/e2e.ts:7:3)")
	Matches(t, event.Stdout, `suite\s+cli-e2e`)

	// An unknown project exits 3 (the server answers 404 project_not_found).
	unknown := cli.Run([]string{"issue", "list", "--project", "nope"})
	Code(t, unknown, 3)
	Equal(t, unknown.ErrJSON(t)["error"], `Project "nope" not found.`)

	// An unknown command does nothing, even with a real server.
	Code(t, cli.Run([]string{"isue", "list"}), 2)

	// The token never appears in any output.
	for _, r := range []Result{login, status, init, projectDSN, next, list, view, event} {
		NotContains(t, r.All, token)
	}

	// The task commands, with the admin token.
	deploy := cli.Run([]string{"deploy", "--commit", "0123456789abcdef0123456789abcdef01234567", "--release", "e2e-1", "--environment", "test"})
	Code(t, deploy, 0)
	Equal(t, deploy.JSON(t)["data"].(map[string]any)["environment"], "test")

	rotate := cli.Run([]string{"dsn", "rotate"})
	Code(t, rotate, 0)
	newDSN := rotate.JSON(t)["data"].(map[string]any)["dsn"].(string)
	revoke := cli.Run([]string{"dsn", "rotate", "--revoke-old", "--yes"})
	Code(t, revoke, 0)
	Equal(t, len(revoke.JSON(t)["data"].(map[string]any)["revoked"].([]any)), 1)
	Equal(t, strings.TrimSpace(cli.Run([]string{"project", "dsn"}, Options{TTY: true}).Stdout), newDSN)

	cli.Write("dist/static/app.min.js.map", `{"version":3,"sources":["src/app.ts"],"names":["boom"],"mappings":"AAAAA","sourcesContent":["boom()"]}`)
	maps := cli.Run([]string{"sourcemaps", "upload", "--release", "e2e-1", "dist"})
	if os.Getenv("CHECKEND_E2E_BLOBS") != "" {
		Code(t, maps, 0)
		Equal(t, maps.JSON(t)["data"].([]any)[0].(map[string]any)["name"], "~/static/app.min.js")
	} else {
		Code(t, maps, 2)
		Contains(t, maps.ErrJSON(t)["error"].(string), "R2")
	}

	// Agent-first setup with a setup key, in a new home and repo.
	keyReq, _ := http.NewRequest("POST", base+"/v1/setup/keys", nil)
	keyReq.Header.Set("Authorization", "Bearer "+token)
	keyResp, err := http.DefaultClient.Do(keyReq)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Data struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	_ = json.NewDecoder(keyResp.Body).Decode(&created)
	keyResp.Body.Close()
	if !strings.HasPrefix(created.Data.Key, "cks_") {
		t.Fatalf("no setup key: %d", keyResp.StatusCode)
	}
	agent := NewHarness(t)
	agent.Write("package.json", `{"dependencies":{"@sentry/node":"9","express":"5"}}`)
	agent.Write(".env.example", "SENTRY_DSN=https://0123456789abcdef0123456789abcdef@o1.ingest.sentry.io/1\n")
	setupRun := agent.Run([]string{"setup", "--url", base, "--key", created.Data.Key}, Options{Env: map[string]string{"CLAUDECODE": "1"}})
	Code(t, setupRun, 0)
	setupOut := setupRun.JSON(t)
	Equal(t, setupOut["test_event"], "sent")
	Equal(t, setupOut["token"].(map[string]any)["created"], true)
	Contains(t, setupOut["steps"].(string), "ce setup")
	NotContains(t, setupRun.All, created.Data.Key)
	Code(t, agent.Run([]string{"setup", "report", "sdk_installed"}), 0)
	Code(t, agent.Run([]string{"next"}), 0)

	mcp := agent.Run([]string{"mcp"}, Options{Stdin: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"ce-e2e","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"})
	Code(t, mcp, 0)
	Contains(t, mcp.Stdout, `"list_issues"`)
	for _, r := range []Result{setupRun, mcp} {
		for _, secret := range agent.Keyring.Secrets {
			NotContains(t, r.All, secret)
		}
	}

	migrate := agent.Run([]string{"migrate-from-sentry"})
	Code(t, migrate, 0)
	Equal(t, migrate.JSON(t)["sentry"].(map[string]any)["sdks"], []any{"@sentry/node"})
	NotContains(t, migrate.All, "0123456789abcdef0123456789abcdef")

	// Delete the first project; then it is gone.
	del := cli.Run([]string{"project", "delete", jsonString(project["id"]), "--yes"})
	Code(t, del, 0)
	Code(t, cli.Run([]string{"project", "dsn", jsonString(project["id"])}), 3)
}

// basePathFront serves the server at upstream under path, as the front Worker of a hosted
// install does: requests outside the path get 404 and fail the test. The server has no base path
// in self-host mode, so the stub puts it into the absolute URLs of the answers (DSNs, MCP, steps).
func basePathFront(t *testing.T, upstream, path string) string {
	target, err := url.Parse(upstream)
	if err != nil {
		t.Fatal(err)
	}
	var front *httptest.Server
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.Out.Host = target.Host
		r.Out.Header.Del("Accept-Encoding")
	}}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			return nil
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		data = bytes.ReplaceAll(data, []byte(target.Host), []byte(strings.TrimPrefix(front.URL, "http://")+path))
		resp.Body = io.NopCloser(bytes.NewReader(data))
		resp.ContentLength = int64(len(data))
		resp.Header.Set("Content-Length", strconv.Itoa(len(data)))
		return nil
	}
	front = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, path)
		if !ok || (rest != "" && !strings.HasPrefix(rest, "/")) {
			t.Errorf("a request outside the base path: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		r.URL.Path, r.URL.RawPath = rest, ""
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	return front.URL + path
}
