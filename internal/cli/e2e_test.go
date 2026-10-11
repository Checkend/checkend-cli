package cli_test

// End-to-end test against a running Checkend server, for example `wrangler dev`. It runs only
// when CHECKEND_E2E_URL and CHECKEND_E2E_TOKEN are set. See README.md.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func TestEndToEnd(t *testing.T) {
	base, token := os.Getenv("CHECKEND_E2E_URL"), os.Getenv("CHECKEND_E2E_TOKEN")
	if base == "" || token == "" {
		t.Skip("set CHECKEND_E2E_URL and CHECKEND_E2E_TOKEN")
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
	message := "e2e boom " + name
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
	req, _ := http.NewRequest("POST", base+"/api"+parsed.Path+"/store/", bytes.NewReader(body))
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
}
