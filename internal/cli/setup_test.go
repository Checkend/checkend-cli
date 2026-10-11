package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

// fakeSetupKey is a fake setup key for tests.
func fakeSetupKey(name string) string { return "cks_test_" + name + "_key_0123456789abcdef" }

func setupArgs(api *FakeAPI) []string {
	return []string{"setup", "--url", api.URL, "--key", api.SetupKey}
}

func TestSetupRegistersTheAppStoresTheTokenAndSendsATestEvent(t *testing.T) {
	api, cli, _ := setup(t)
	cli.Write("package.json", `{"dependencies":{"next":"15","react":"19"}}`)
	r := cli.Run(setupArgs(api), Options{Env: map[string]string{"CLAUDECODE": "1"}})
	Code(t, r, 0)
	Equal(t, api.Paths(), []string{
		"GET /health", "POST /v1/setup/apps", "POST /v1/setup/tokens",
		"POST /v1/setup/apps/" + id(api.Projects[0]) + "/test-event", "GET /agent/setup.md",
	})
	for _, req := range api.Requests()[1:4] {
		if req.Authorization != "Bearer "+api.SetupKey {
			t.Errorf("%s: authorization %q", req.Path, req.Authorization)
		}
	}
	Equal(t, api.Requests()[1].Body, J{"name": "repo", "platform": "javascript-nextjs"})
	Equal(t, api.Requests()[2].Body, J{"name": "Claude Code (ce setup)", "project": id(api.Projects[0])})

	out := r.JSON(t)
	p := api.Projects[0]
	Equal(t, out["project"], J{"id": p["id"], "slug": "repo", "name": "repo", "platform": "javascript-nextjs"})
	Equal(t, out["dsn"], dsn(p))
	Equal(t, out["created"], true)
	Equal(t, out["stack"], J{"platform": "javascript-nextjs", "framework": "nextjs", "label": "Node / Next.js", "sdk": "@sentry/nextjs"})
	Equal(t, out["token"], J{"created": true, "storage": "keychain"})
	Equal(t, out["test_event"], "sent")
	Equal(t, out["mcp"].(map[string]any)["claude_code"], "claude mcp add checkend -- ce mcp --url "+api.URL)
	Contains(t, out["steps"].(string), "Install the official Sentry SDK")
	Equal(t, readLink(t, cli), J{"project": id(p), "slug": "repo"})

	if cli.Keyring.Secrets[api.URL] != api.Tokens[0] {
		t.Errorf("keychain: %#v", cli.Keyring.Secrets)
	}
	NotContains(t, r.All, api.SetupKey)
	NotContains(t, r.All, api.Tokens[0])
	cfg, _ := os.ReadFile(configPath(cli))
	NotContains(t, string(cfg), api.Tokens[0])

	// The saved agent token works for the other commands.
	Code(t, cli.Run([]string{"project", "list"}), 0)
}

func TestSetupPrintsTheStepsOnATerminal(t *testing.T) {
	api, cli, _ := setup(t)
	cli.Write("Gemfile", `gem "rails"`)
	r := cli.Run(setupArgs(api), Options{TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, "Connected repo (Ruby / Rails)")
	Contains(t, r.Stdout, "DSN: "+dsn(api.Projects[0]))
	Contains(t, r.Stdout, "Sent a test event")
	Contains(t, r.Stdout, "claude mcp add checkend -- ce mcp --url "+api.URL)
	Contains(t, r.Stdout, "codex mcp add checkend -- ce mcp --url "+api.URL)
	Contains(t, r.Stdout, `{"mcpServers":{"checkend":{"command":"ce","args":["mcp","--url","`+api.URL+`"]}}}`)
	Contains(t, r.Stdout, "Install the official Sentry SDK")
	Contains(t, r.Stdout, "sentry-rails")
	NotContains(t, r.All, api.SetupKey)
}

func TestSetupReadsTheKeyFromTheEnvironment(t *testing.T) {
	api, cli, _ := setup(t)
	r := cli.Run([]string{"setup", "--url", api.URL}, Options{Env: map[string]string{"CHECKEND_SETUP_KEY": api.SetupKey}})
	Code(t, r, 0)
	NotContains(t, r.All, api.SetupKey)
}

func TestSetupUsesTheNameAndPlatformFlags(t *testing.T) {
	api, cli, _ := setup(t)
	Code(t, cli.Run(append(setupArgs(api), "--name", "Shop API", "--platform", "python-django")), 0)
	Equal(t, api.Requests()[1].Body, J{"name": "Shop API", "platform": "python-django"})
}

func TestSetupKeepsASavedTokenOfTheInstance(t *testing.T) {
	api, cli, _ := setup(t)
	login(t, cli, api)
	api.Reset()
	r := cli.Run(setupArgs(api), Options{TTY: true})
	Code(t, r, 0)
	for _, p := range api.Paths() {
		if p == "POST /v1/setup/tokens" {
			t.Error("made a new token")
		}
	}
	Contains(t, r.Stdout, "Kept the saved token")
	if cli.Keyring.Secrets[api.URL] != api.Token {
		t.Error("the saved token changed")
	}
}

func TestSetupAgainWithTheSameKeyKeepsTheApp(t *testing.T) {
	api, cli, _ := setup(t)
	Code(t, cli.Run(setupArgs(api)), 0)
	r := cli.Run(setupArgs(api))
	Code(t, r, 0)
	out := r.JSON(t)
	Equal(t, out["created"], false)
	Equal(t, out["test_event"], "already_sent")
	if len(api.Projects) != 1 {
		t.Errorf("%d projects", len(api.Projects))
	}
}

func TestSetupInputErrorsDoNothing(t *testing.T) {
	for name, argv := range map[string][]string{
		"no key":      {"setup", "--url", "https://checkend.example.com"},
		"no url":      {"setup", "--key", "cks_test_setup_key_0123456789abcdef"},
		"bad key":     {"setup", "--url", "https://checkend.example.com", "--key", "ck_test_api_token_0123456789abc"},
		"http remote": {"setup", "--url", "http://checkend.example.com", "--key", "cks_test_setup_key_0123456789abcdef"},
		"extra arg":   {"setup", "x", "--url", "https://checkend.example.com", "--key", "cks_test_setup_key_0123456789abcdef"},
	} {
		t.Run(name, func(t *testing.T) {
			api, cli, _ := setup(t)
			r := cli.Run(argv)
			Code(t, r, 2)
			NotContains(t, r.All, "cks_test_setup_key_0123456789abcdef")
			NotContains(t, r.All, "ck_test_api_token_0123456789abc")
			if len(api.Requests()) != 0 || Exists(filepath.Join(cli.Repo, ".checkend.json")) {
				t.Error("did something")
			}
		})
	}
}

func TestSetupWithAnOldKeyExits5WithTheNextStep(t *testing.T) {
	api, cli, _ := setup(t)
	api.SetupKey = fakeSetupKey("other")
	r := cli.Run([]string{"setup", "--url", api.URL, "--key", "cks_test_expired_key_0123456789abcd"})
	Code(t, r, 5)
	Contains(t, r.ErrJSON(t)["hint"].(string), "new setup key")
	if Exists(filepath.Join(cli.Repo, ".checkend.json")) {
		t.Error("wrote the link")
	}
}

func TestSetupReportsTheAppLimit(t *testing.T) {
	api, cli, _ := setup(t)
	api.Override = func(r *Request) *Reply {
		if r.Path == "/v1/setup/apps" {
			return &Reply{Status: 429, Body: J{"error": "limit", "message": "This setup key registered its maximum of apps."}}
		}
		return nil
	}
	r := cli.Run(setupArgs(api))
	Code(t, r, 1)
	Contains(t, r.ErrJSON(t)["error"].(string), "maximum of apps")
}

func TestSetupHidesTheKeyWhenTheServerEchoesIt(t *testing.T) {
	api, cli, _ := setup(t)
	api.Override = func(r *Request) *Reply {
		if r.Path == "/v1/setup/apps" {
			return &Reply{Status: 500, Body: J{"error": "bad key " + r.Authorization}}
		}
		return nil
	}
	for _, tty := range []bool{true, false} {
		r := cli.Run(setupArgs(api), Options{TTY: tty})
		Code(t, r, 1)
		NotContains(t, r.All, api.SetupKey)
	}
}

func TestSetupCleansTheRecipe(t *testing.T) {
	api, cli, _ := setup(t)
	api.SetupMD = "# Steps\x1b]0;evil\x07\n\x1b[2Jdo this\u202e\n"
	r := cli.Run(setupArgs(api), Options{TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, "do this")
	NotMatches(t, r.All, "[\x1b\x07\u202e]")
	js := cli.Run(setupArgs(api))
	NotMatches(t, js.Stdout, "[\x1b\x07\u202e]")
}

func TestSetupStillWorksWhenTheRecipeIsMissing(t *testing.T) {
	api, cli, _ := setup(t)
	api.Override = func(r *Request) *Reply {
		if r.Path == "/agent/setup.md" {
			return &Reply{Status: 404, Body: J{"error": "not_found"}}
		}
		return nil
	}
	r := cli.Run(setupArgs(api), Options{TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, api.URL+"/agent/setup.md")
}

func TestSetupReportSendsAStep(t *testing.T) {
	api, cli, _ := setup(t)
	Code(t, cli.Run(setupArgs(api)), 0)
	p := id(api.Projects[0])
	r := cli.Run([]string{"setup", "report", "sdk_installed", "--key", api.SetupKey})
	Code(t, r, 0)
	Equal(t, api.Reports, []string{p + " sdk_installed"})
	Equal(t, api.Last().Authorization, "Bearer "+api.SetupKey)
	Equal(t, r.JSON(t), J{"project": p, "step": "sdk_installed", "reported": true})
	NotContains(t, r.All, api.SetupKey)
}

func TestSetupReportExplainsAForbiddenToken(t *testing.T) {
	api, cli, _ := setup(t)
	Code(t, cli.Run(setupArgs(api)), 0)
	r := cli.Run([]string{"setup", "report", "sdk_installed"})
	Code(t, r, 5)
	Contains(t, r.ErrJSON(t)["hint"].(string), "--key")
}

func TestSetupReportChecksTheStepBeforeAnyRequest(t *testing.T) {
	api, cli, env := setup(t)
	cli.Write(".checkend.json", `{"project":"app"}`)
	r := cli.Run([]string{"setup", "report", "done"}, Options{Env: env})
	Code(t, r, 2)
	if len(api.Requests()) != 0 {
		t.Error("request made")
	}
}

func TestSetupHelpShowsBothCommands(t *testing.T) {
	_, cli, _ := setup(t)
	r := cli.Run([]string{"--help"})
	Matches(t, r.Stdout, `(?m)^\s+setup\b`)
	h := cli.Run([]string{"setup", "--help"})
	Contains(t, h.Stdout, "--key")
	Contains(t, h.Stdout, "ce setup report")
	if strings.Contains(h.Stdout, "cks_") && !strings.Contains(h.Stdout, "cks_…") {
		t.Error("help shows a key")
	}
}
