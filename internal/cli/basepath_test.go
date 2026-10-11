package cli_test

import (
	"net/url"
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

// A hosted Checkend lives under a path: https://hosted.example/<id>. Every command must keep
// that base path. The fake answers 404 outside it and records the request in Outside.

const space = "/k7q2x9"

func hosted(t *testing.T) (*FakeAPI, *Harness) {
	api := NewFakeAPIAt(t, space)
	if !strings.HasSuffix(api.URL, space) {
		t.Fatalf("URL %q", api.URL)
	}
	return api, NewHarness(t)
}

func noneOutside(t *testing.T, api *FakeAPI) {
	t.Helper()
	if len(api.Outside) > 0 {
		t.Errorf("requests outside the base path: %v", api.Outside)
	}
}

func TestEveryCommandKeepsTheBasePath(t *testing.T) {
	api, cli := hosted(t)
	app := api.AddProject("App")
	Contains(t, app["dsns"].([]any)[0].(string), space+"/")
	cli.Git = map[string]string{"rev-parse HEAD": "0123456789abcdef0123456789abcdef01234567"}
	Code(t, cli.Run([]string{"login", "--url", api.URL + "/", "--with-token"}, Options{Stdin: api.Token}), 0)
	cli.Write(".checkend.json", `{"project":"app"}`)
	cli.Write("dist/app.js.map", mapText)
	for _, argv := range [][]string{
		{"status"},
		{"project", "list"},
		{"project", "dsn"},
		{"issue", "list"},
		{"next"},
		{"deploy"},
		{"dsn", "rotate"},
		{"sourcemaps", "upload", "--release", "1", "dist"},
	} {
		Code(t, cli.Run(argv), 0)
	}
	init := cli.Run([]string{"init", "--name", "Web", "--platform", "node"}, Options{Cwd: t.TempDir()})
	Code(t, init, 0)
	Contains(t, init.JSON(t)["dsn"].(string), space+"/")
	noneOutside(t, api)
	for _, path := range api.Paths() {
		if strings.HasPrefix(strings.Fields(path)[1], space) {
			t.Errorf("the fake got the base path twice: %s", path)
		}
	}
	Contains(t, strings.Join(api.Paths(), "\n"), "POST /v1/sourcemaps")
}

func TestSetupAndMCPKeepTheBasePath(t *testing.T) {
	api, cli := hosted(t)
	r := cli.Run([]string{"setup", "--url", api.URL, "--key", api.SetupKey})
	Code(t, r, 0)
	out := r.JSON(t)
	Equal(t, out["url"], api.URL)
	Equal(t, out["mcp"].(map[string]any)["server"], api.URL+"/mcp")
	Equal(t, out["mcp"].(map[string]any)["claude_code"], "claude mcp add checkend -- ce mcp --url "+api.URL)
	Equal(t, out["steps_url"], api.URL+"/agent/setup.md")
	Contains(t, out["steps"].(string), "Set up Checkend")
	Contains(t, out["dsn"].(string), space+"/")
	api.Reset()
	mcp := cli.Run([]string{"mcp", "--url", api.URL}, Options{Stdin: mcpInput})
	Code(t, mcp, 0)
	Equal(t, len(lines(t, mcp.Stdout)), 2)
	Equal(t, api.Paths(), []string{"POST /mcp", "POST /mcp", "POST /mcp"})
	report := cli.Run([]string{"setup", "report", "sdk_installed", "--url", api.URL})
	Code(t, report, 0)
	noneOutside(t, api)
}

func TestTwoSpacesOnOneHostNeverShareAToken(t *testing.T) {
	api, cli := hosted(t)
	Code(t, cli.Run([]string{"login", "--url", api.URL, "--with-token"}, Options{Stdin: api.Token}), 0)
	other := api.Origin + "/a2b3c4"
	r := cli.Run([]string{"issue", "list", "--url", other})
	Code(t, r, 5)
	for _, req := range api.Outside {
		if req.Authorization != "" {
			t.Errorf("the token of %s went to %s", space, req.Path)
		}
	}
	// The agent token of one space is not the agent token of the other.
	Code(t, cli.Run([]string{"setup", "--url", api.URL, "--key", api.SetupKey}), 0)
	Code(t, cli.Run([]string{"mcp", "--url", other}, Options{Stdin: mcpInput}), 5)
	if _, ok := cli.Keyring.Secrets[api.URL]; !ok {
		t.Errorf("the keychain has no entry for %s: %v", api.URL, cli.Keyring.Secrets)
	}
	for key := range cli.Keyring.Secrets {
		if key != api.URL && !strings.HasPrefix(key, api.URL+"#agent:") {
			t.Errorf("keychain account %q is not of %s", key, api.URL)
		}
	}
}

func TestTheBasePathIsCheckedBeforeAnyRequest(t *testing.T) {
	api, cli := hosted(t)
	for _, bad := range []string{"/k7q2x9/../a2b3c4", "/./k7q2x9", "//k7q2x9", "/k7q2x9//", "/k7q2x9/%2e%2e", "/k7%2Fq2"} {
		r := cli.Run([]string{"status", "--url", api.Origin + bad})
		Code(t, r, 2)
		Contains(t, r.Stderr, "path")
	}
	Equal(t, len(api.Requests()), 0)
}

func TestTheIngestURLOfAHostedDSNKeepsTheBasePath(t *testing.T) {
	api, _ := hosted(t)
	dsn, _ := url.Parse(api.AddProject("App")["dsns"].([]any)[0].(string))
	Equal(t, IngestURL(dsn, "store"), api.URL+"/api/"+strings.TrimPrefix(dsn.Path, space+"/")+"/store/")
}
