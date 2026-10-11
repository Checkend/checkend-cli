package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func configPath(cli *Harness) string {
	return filepath.Join(cli.Home, ".config", "checkend", "config.json")
}

func readConfig(t *testing.T, cli *Harness) map[string]any {
	t.Helper()
	data, err := os.ReadFile(configPath(cli))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func login(t *testing.T, cli *Harness, api *FakeAPI) {
	t.Helper()
	Code(t, cli.Run([]string{"login", "--url", api.URL, "--with-token"}, Options{Stdin: api.Token}), 0)
}

func TestLoginAsksOnATerminalChecksAndSavesInTheKeychain(t *testing.T) {
	api, cli, _ := setup(t)
	r := cli.Run([]string{"login"}, Options{TTY: true, Answers: []string{api.URL + "/", api.Token}})
	Code(t, r, 0)
	if len(cli.Prompts) != 2 || cli.Prompts[0].Secret || !cli.Prompts[1].Secret {
		t.Errorf("prompts: %#v", cli.Prompts)
	}
	Contains(t, r.Stdout, "Logged in to "+api.URL)
	NotContains(t, r.All, api.Token)
	Equal(t, api.Paths(), []string{"GET /health", "GET /v1/projects"})
	if api.Requests()[0].Authorization != "" {
		t.Error("/health got the token")
	}
	Equal(t, readConfig(t, cli), map[string]any{"current": api.URL, "hosts": map[string]any{api.URL: map[string]any{"storage": "keychain"}}})
	if cli.Keyring.Secrets[api.URL] != api.Token {
		t.Errorf("keychain: %#v", cli.Keyring.Secrets)
	}
	info, _ := os.Stat(configPath(cli))
	dir, _ := os.Stat(filepath.Dir(configPath(cli)))
	if runtime.GOOS != "windows" && (info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700) {
		t.Errorf("modes %o %o", info.Mode().Perm(), dir.Mode().Perm())
	}
}

func TestLoginFallsBackToTheFileWhenTheKeychainIsNotAvailable(t *testing.T) {
	api, cli, _ := setup(t)
	cli.Keyring.Broken = true
	r := cli.Run([]string{"login", "--url", api.URL, "--with-token"}, Options{Stdin: api.Token, TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stderr, "keychain is not available")
	Contains(t, r.Stderr, configPath(cli))
	NotContains(t, r.All, api.Token)
	Equal(t, readConfig(t, cli)["hosts"], map[string]any{api.URL: map[string]any{"token": api.Token}})
	Code(t, cli.Run([]string{"project", "list"}), 0)
}

func TestLoginReadsTheTokenFromStdin(t *testing.T) {
	api, cli, _ := setup(t)
	r := cli.Run([]string{"login", "--url", api.URL, "--with-token"}, Options{Stdin: api.Token + "\n"})
	Code(t, r, 0)
	Equal(t, r.JSON(t), map[string]any{"url": api.URL, "version": "0.2.0", "storage": "keychain"})
	if cli.Keyring.Secrets[api.URL] != api.Token {
		t.Error("token not saved")
	}
}

func TestLoginUsesXDGConfigHome(t *testing.T) {
	api, cli, _ := setup(t)
	xdg := filepath.Join(cli.Home, "xdg")
	cli.Run([]string{"login", "--url", api.URL, "--with-token"}, Options{Stdin: api.Token, Env: map[string]string{"XDG_CONFIG_HOME": xdg}})
	if !Exists(filepath.Join(xdg, "checkend", "config.json")) {
		t.Error("no config in XDG_CONFIG_HOME")
	}
}

func TestLoginNeedsATokenWhenItCannotAsk(t *testing.T) {
	api, cli, _ := setup(t)
	r := cli.Run([]string{"login", "--url", api.URL})
	Code(t, r, 2)
	Contains(t, r.ErrJSON(t)["hint"].(string), "--with-token")
	if len(api.Requests()) != 0 {
		t.Error("request made")
	}
}

func TestLoginRefusesAWrongTokenAndSavesNothing(t *testing.T) {
	api, cli, _ := setup(t)
	r := cli.Run([]string{"login", "--url", api.URL, "--with-token"}, Options{Stdin: "wrong"})
	Code(t, r, 5)
	Matches(t, r.ErrJSON(t)["error"].(string), `(?i)token`)
	if Exists(configPath(cli)) || len(cli.Keyring.Secrets) != 0 {
		t.Error("saved something")
	}
}

func TestLoginRefusesPlainHTTPToARemoteHost(t *testing.T) {
	_, cli, _ := setup(t)
	r := cli.Run([]string{"login", "--url", "http://errors.example.com", "--with-token"}, Options{Stdin: "x"})
	Code(t, r, 2)
	Contains(t, r.ErrJSON(t)["error"].(string), "HTTPS")
}

func TestLoginReportsAnInstanceThatItCannotReach(t *testing.T) {
	api, cli, _ := setup(t)
	api.Stop()
	r := cli.Run([]string{"login", "--url", api.URL, "--with-token"}, Options{Stdin: "x"})
	Code(t, r, 1)
	Contains(t, r.ErrJSON(t)["error"].(string), "Cannot reach "+api.URL)
}

func TestLoginRefusesAServerThatIsNotCheckend(t *testing.T) {
	api, cli, _ := setup(t)
	api.Override = func(r *Request) *Reply {
		if r.Path == "/health" {
			return &Reply{Status: 200, Raw: "<html>"}
		}
		return nil
	}
	r := cli.Run([]string{"login", "--url", api.URL, "--with-token"}, Options{Stdin: api.Token})
	Code(t, r, 1)
	Contains(t, r.ErrJSON(t)["error"].(string), "not a Checkend")
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	api, cli, env := setup(t)
	other := NewFakeAPI(t)
	api.Override = func(r *Request) *Reply {
		return &Reply{Status: 302, Headers: map[string]string{"Location": other.URL + "/v1/projects"}}
	}
	r := cli.Run([]string{"project", "list"}, Options{Env: env})
	Code(t, r, 1)
	if len(other.Requests()) != 0 {
		t.Error("the redirect was followed")
	}
}

func TestLoginKeepsOtherInstances(t *testing.T) {
	api, cli, _ := setup(t)
	other := NewFakeAPI(t)
	login(t, cli, api)
	Code(t, cli.Run([]string{"login", "--url", other.URL, "--with-token"}, Options{Stdin: other.Token}), 0)
	cfg := readConfig(t, cli)
	if cfg["current"] != other.URL || len(cfg["hosts"].(map[string]any)) != 2 {
		t.Errorf("config: %#v", cfg)
	}
}

func TestLogoutRemovesTheTokenOfTheCurrentInstance(t *testing.T) {
	api, cli, _ := setup(t)
	login(t, cli, api)
	r := cli.Run([]string{"logout"}, Options{TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, "Logged out of "+api.URL)
	Equal(t, readConfig(t, cli), map[string]any{"hosts": map[string]any{}})
	if len(cli.Keyring.Secrets) != 0 {
		t.Error("the keychain entry is still there")
	}
	status := cli.Run([]string{"status"})
	Code(t, status, 5)
	Contains(t, status.ErrJSON(t)["hint"].(string), "ce login")
}

func TestLogoutWhenNobodyIsLoggedIn(t *testing.T) {
	_, cli, _ := setup(t)
	r := cli.Run([]string{"logout"}, Options{TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, "Not logged in")
}

func loggedIn(t *testing.T) (*FakeAPI, *Harness) {
	api, cli, _ := setup(t)
	login(t, cli, api)
	api.Reset()
	return api, cli
}

func TestStatusShowsTheInstanceHealthIngestAndProject(t *testing.T) {
	api, cli := loggedIn(t)
	project := api.AddProject("My App")
	cli.Write(".checkend.json", `{"project":"`+jsonString(project["id"])+`","slug":"my-app"}`)
	r := cli.Run([]string{"status"}, Options{TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, api.URL)
	Contains(t, r.Stdout, "0.2.0")
	Matches(t, r.Stdout, `Health\s+ok`)
	Matches(t, r.Stdout, `Ingest\s+full`)
	Contains(t, r.Stdout, "my-app ("+jsonString(project["id"])+")")
	NotContains(t, r.All, api.Token)
}

func TestStatusPrintsJSON(t *testing.T) {
	api, cli := loggedIn(t)
	r := cli.Run([]string{"status"})
	Code(t, r, 0)
	s := r.JSON(t)
	if s["url"] != api.URL || s["authenticated"] != true || s["project"] != nil {
		t.Errorf("status: %#v", s)
	}
	health := s["health"].(map[string]any)
	if health["status"] != "ok" || health["version"] != "0.2.0" {
		t.Errorf("health: %#v", health)
	}
}

func TestStatusExits5WhenTheTokenIsRejected(t *testing.T) {
	api, cli := loggedIn(t)
	api.Token = "rotated"
	r := cli.Run([]string{"status"})
	Code(t, r, 5)
	if r.JSON(t)["authenticated"] != false {
		t.Error("authenticated")
	}
}

func TestStatusExits1WhenTheInstanceIsNotHealthy(t *testing.T) {
	api, cli := loggedIn(t)
	api.Override = func(r *Request) *Reply {
		if r.Path == "/health" {
			return &Reply{Status: 503, Body: J{"status": "error"}}
		}
		return nil
	}
	Code(t, cli.Run([]string{"status"}), 1)
}

func TestStatusShowsCountersOnlyIngest(t *testing.T) {
	api, cli := loggedIn(t)
	api.Health["ingest"] = J{"mode": "counters_only"}
	Matches(t, cli.Run([]string{"status"}, Options{TTY: true}).Stdout, `Ingest\s+counters_only`)
}

func TestConfigFromTheEnvironment(t *testing.T) {
	api, cli, _ := setup(t)
	Code(t, cli.Run([]string{"status"}, Options{Env: map[string]string{"CHECKEND_URL": api.URL, "CHECKEND_TOKEN": api.Token}}), 0)
	Code(t, cli.Run([]string{"status"}, Options{Env: map[string]string{"CHECKEND_URL": api.URL, "CHECKEND_ADMIN_TOKEN": api.Token}}), 0)
	Code(t, cli.Run([]string{"status", "--token", api.Token}, Options{Env: map[string]string{"CHECKEND_URL": api.URL, "CHECKEND_TOKEN": "wrong"}}), 0)
	Code(t, cli.Run([]string{"status"}, Options{Env: map[string]string{"CHECKEND_URL": "http://errors.example.com", "CHECKEND_TOKEN": "x"}}), 2)
}

func TestASavedTokenNeverGoesToAnotherInstance(t *testing.T) {
	api, cli, _ := setup(t)
	login(t, cli, api)
	other := NewFakeAPI(t)
	r := cli.Run([]string{"project", "list", "--url", other.URL})
	Code(t, r, 5)
	Contains(t, r.ErrJSON(t)["hint"].(string), "ce login --url "+other.URL)
	for _, req := range other.Requests() {
		if req.Authorization != "" {
			t.Error("token sent to the other instance")
		}
	}
}

func TestATokenOrURLInCheckendJSONIsIgnored(t *testing.T) {
	api, cli, _ := setup(t)
	cli.Write(".checkend.json", `{"url":"`+api.URL+`","token":"`+api.Token+`"}`)
	Code(t, cli.Run([]string{"project", "list"}), 5)
	if len(api.Requests()) != 0 {
		t.Error("request made")
	}
}

func TestCheckendJSONInAParentDirectory(t *testing.T) {
	api, cli, env := setup(t)
	project := api.AddProject("My App")
	cli.Write(".checkend.json", `{"project":"my-app"}`)
	deep := filepath.Join(cli.Repo, "src", "deep")
	_ = os.MkdirAll(deep, 0o755)
	r := cli.Run([]string{"status"}, Options{Cwd: deep, Env: env})
	p := r.JSON(t)["project"].(map[string]any)
	Equal(t, p["id"], project["id"])
	Equal(t, p["slug"], "my-app")
}

func TestABrokenCheckendJSON(t *testing.T) {
	_, cli, env := setup(t)
	cli.Write(".checkend.json", "{not json")
	r := cli.Run([]string{"status"}, Options{Env: env})
	Code(t, r, 1)
	Contains(t, r.ErrJSON(t)["error"].(string), ".checkend.json")
}

func TestTheTokenIsHiddenWhenTheServerEchoesIt(t *testing.T) {
	api, cli, env := setup(t)
	api.Override = func(r *Request) *Reply {
		if r.Path == "/v1/projects" {
			return &Reply{Status: 500, Body: J{"error": "bad token " + r.Authorization}}
		}
		return nil
	}
	for _, tty := range []bool{true, false} {
		r := cli.Run([]string{"project", "list"}, Options{TTY: tty, Env: env})
		Code(t, r, 1)
		NotContains(t, r.All, api.Token)
	}
}

func TestATokenFromTheKeychainIsHidden(t *testing.T) {
	api, cli := loggedIn(t)
	api.Override = func(r *Request) *Reply {
		if r.Path == "/v1/projects" {
			return &Reply{Status: 500, Body: J{"error": "bad token " + r.Authorization}}
		}
		return nil
	}
	r := cli.Run([]string{"project", "list"}, Options{TTY: true})
	Code(t, r, 1)
	NotContains(t, r.All, api.Token)
}

func jsonString(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
