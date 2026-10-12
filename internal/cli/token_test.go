package cli_test

import (
	"strings"
	"testing"

	"github.com/Checkend/checkend-cli/internal/docs"
	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func TestTokenCreatePrintsOnlyTheTokenToAPipe(t *testing.T) {
	api, cli, env := setup(t)
	r := cli.Run([]string{"token", "create", "--scope", "releases:write", "--name", "github-deploy"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, len(api.APITokens), 1)
	token := api.APITokens[0]["token"].(string)
	Equal(t, r.Stdout, token+"\n")
	Contains(t, r.Stderr, "Created the token github-deploy")
	Contains(t, r.Stderr, "releases:write")
	NotContains(t, r.Stderr, token)
	Equal(t, api.Last().Body, J{"name": "github-deploy", "scopes": []any{"releases:write"}})
}

func TestTokenCreateWithJSON(t *testing.T) {
	api, cli, env := setup(t)
	r := cli.Run([]string{"token", "create", "--scope", "issues:read", "--scope", "issues:write", "--json"}, Options{Env: env})
	Code(t, r, 0)
	out := r.JSON(t)
	Equal(t, out["token"], api.APITokens[0]["token"])
	Equal(t, out["scopes"], []any{"issues:read", "issues:write"})
	Equal(t, api.Last().Body.(map[string]any)["name"], "ce token create")
}

func TestTokenCreateRefusesATerminalWithoutShow(t *testing.T) {
	api, cli, env := setup(t)
	r := cli.Run([]string{"token", "create", "--scope", "releases:write"}, Options{Env: env, TTY: true})
	Code(t, r, 2)
	Contains(t, r.Stderr, "--show")
	Contains(t, r.Stderr, "gh secret set")
	Equal(t, len(api.Requests()), 0)
	r = cli.Run([]string{"token", "create", "--scope", "releases:write", "--show"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	Equal(t, strings.TrimSpace(r.Stdout), api.APITokens[0]["token"])
}

func TestTokenCreateChecksItsInputFirst(t *testing.T) {
	api, cli, env := setup(t)
	for _, argv := range [][]string{
		{"token", "create"},
		{"token", "create", "--scope", "everything"},
		{"token", "create", "--scope", "releases:write", "--name", strings.Repeat("x", 101)},
		{"token", "create", "--scope", "releases:write", "--project", "Not A Slug!"},
	} {
		Code(t, cli.Run(argv, Options{Env: env}), 2)
	}
	Equal(t, len(api.Requests()), 0)
}

func TestTokenCreateExplainsWhoCanMakeTokens(t *testing.T) {
	api, cli, _ := setup(t)
	agent := "ck_test_agent_token_0123456789abcdef"
	api.Tokens = append(api.Tokens, agent)
	r := cli.Run([]string{"token", "create", "--scope", "releases:write"}, Options{Env: map[string]string{"CHECKEND_URL": api.URL, "CHECKEND_TOKEN": agent}})
	Code(t, r, 5)
	Contains(t, r.ErrJSON(t)["error"].(string), "admin scope")
	Equal(t, r.ErrJSON(t)["docs"], docs.URL(docs.Tokens))

	hosted := NewFakeAPIAt(t, "/4qm63ns")
	hosted.Tokens = append(hosted.Tokens, agent)
	r = cli.Run([]string{"token", "create", "--scope", "releases:write"}, Options{Env: map[string]string{"CHECKEND_URL": hosted.URL, "CHECKEND_TOKEN": agent}})
	Code(t, r, 5)
	Contains(t, r.ErrJSON(t)["error"].(string), "only an owner can make tokens")
	Contains(t, r.ErrJSON(t)["hint"].(string), "Settings → Tokens")
}

func TestTokenListAndRevoke(t *testing.T) {
	api, cli, env := setup(t)
	Code(t, cli.Run([]string{"token", "create", "--scope", "releases:write", "--name", "deploy"}, Options{Env: env}), 0)
	token := api.APITokens[0]["token"].(string)
	list := cli.Run([]string{"token", "list"}, Options{Env: env})
	Code(t, list, 0)
	Equal(t, list.JSON(t)["data"].([]any)[0].(map[string]any)["name"], "deploy")
	NotContains(t, list.All, token)
	table := cli.Run([]string{"token", "list"}, Options{Env: env, TTY: true})
	Code(t, table, 0)
	Contains(t, table.Stdout, "deploy")
	Contains(t, table.Stdout, "releases:write")

	id := jsonString(api.APITokens[0]["id"])
	Code(t, cli.Run([]string{"token", "revoke", id}, Options{Env: env}), 2)
	Code(t, cli.Run([]string{"token", "revoke", "abc", "--yes"}, Options{Env: env}), 2)
	r := cli.Run([]string{"token", "revoke", id, "--yes"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, api.Last().Method+" "+api.Last().Path, "DELETE /v1/tokens/"+id)
	Code(t, cli.Run([]string{"token", "revoke", id, "--yes"}, Options{Env: env}), 3)
}

func TestDeployDryRunSendsNothing(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	cli.Write(".checkend.json", `{"project":"app"}`)
	cli.Git = map[string]string{
		"rev-parse HEAD":                         "0123456789abcdef0123456789abcdef01234567",
		"rev-list --max-count=1000 abc123..HEAD": "0123456789abcdef0123456789abcdef01234567\n89abcdef0123456789abcdef0123456789abcdef",
	}
	r := cli.Run([]string{"deploy", "--dry-run", "--release", "v1.2.3", "--env", "staging", "--commits-since", "abc123"}, Options{Env: env})
	Code(t, r, 0)
	// It only asks which token it has; it records nothing.
	Equal(t, api.Paths(), []string{"GET /v1/tokens/current"})
	out := r.JSON(t)
	Equal(t, out["dry_run"], true)
	Equal(t, out["request"], J{"method": "POST", "path": "/v1/deploys", "url": api.URL + "/v1/deploys", "body": J{
		"project": "app", "commit": "0123456789abcdef0123456789abcdef01234567", "release": "v1.2.3", "environment": "staging",
		"commits": []any{"0123456789abcdef0123456789abcdef01234567", "89abcdef0123456789abcdef0123456789abcdef"},
	}})
	// The ADMIN_TOKEN has all scopes.
	Equal(t, out["scope_checked"], true)
	text := cli.Run([]string{"deploy", "--dry-run"}, Options{Env: env, TTY: true})
	Code(t, text, 0)
	Contains(t, text.Stdout, "Would send POST "+api.URL+"/v1/deploys")
	Contains(t, text.Stdout, "releases:write")
	NotContains(t, text.All, api.Token)
	// Without a token, the dry run fails as the deploy would.
	api.Reset()
	r = cli.Run([]string{"deploy", "--dry-run"}, Options{Env: map[string]string{"CHECKEND_URL": api.URL}})
	Code(t, r, 5)
	Equal(t, len(api.Requests()), 0)
}

func TestTokenCreateForOneApp(t *testing.T) {
	api, cli, env := setup(t)
	app := api.AddProject("App")
	r := cli.Run([]string{"token", "create", "--scope", "releases:write", "--project", "app"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, api.Last().Body.(map[string]any)["project"], "app")
	Equal(t, api.APITokens[0]["project_id"], app["id"])
	Contains(t, r.Stderr, "for the app App")
	Equal(t, r.Stdout, api.APITokens[0]["token"].(string)+"\n")

	r = cli.Run([]string{"token", "create", "--scope", "releases:write", "--project", "nope"}, Options{Env: env})
	Code(t, r, 3)
	Contains(t, r.ErrJSON(t)["error"].(string), `App "nope" not found`)
	Equal(t, r.Stdout, "")
	Equal(t, len(api.APITokens), 1)
}

// dryRunSetup is a linked repo with a deploy token of the given scopes (and app).
func dryRunSetup(t *testing.T, scopes []string, project string) (*FakeAPI, *Harness, map[string]string) {
	api, cli, env := setup(t)
	api.AddProject("App")
	api.AddProject("Web")
	cli.Write(".checkend.json", `{"project":"app"}`)
	cli.Git = map[string]string{"rev-parse HEAD": "0123456789abcdef0123456789abcdef01234567"}
	args := []string{"token", "create", "--name", "deploy"}
	for _, sc := range scopes {
		args = append(args, "--scope", sc)
	}
	if project != "" {
		args = append(args, "--project", project)
	}
	r := cli.Run(args, Options{Env: env})
	Code(t, r, 0)
	api.Reset()
	return api, cli, map[string]string{"CHECKEND_URL": api.URL, "CHECKEND_TOKEN": strings.TrimSpace(r.Stdout)}
}

func TestDeployDryRunChecksTheScopeOfTheToken(t *testing.T) {
	api, cli, env := dryRunSetup(t, []string{"releases:write"}, "app")
	r := cli.Run([]string{"deploy", "--dry-run"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, api.Paths(), []string{"GET /v1/tokens/current"})
	out := r.JSON(t)
	Equal(t, out["scope_checked"], true)
	Equal(t, out["token"].(map[string]any)["name"], "deploy")
	NotContains(t, r.All, env["CHECKEND_TOKEN"])
	text := cli.Run([]string{"deploy", "--dry-run"}, Options{Env: env, TTY: true})
	Contains(t, text.Stdout, "The token deploy has releases:write")

	api, cli, env = dryRunSetup(t, []string{"issues:read"}, "")
	r = cli.Run([]string{"deploy", "--dry-run"}, Options{Env: env})
	Code(t, r, 5)
	Contains(t, r.ErrJSON(t)["error"].(string), "no releases:write scope")
	Contains(t, r.ErrJSON(t)["hint"].(string), "ce token create --scope releases:write")
	Equal(t, api.Paths(), []string{"GET /v1/tokens/current"})
}

func TestDeployDryRunChecksTheAppOfTheToken(t *testing.T) {
	api, cli, env := dryRunSetup(t, []string{"releases:write"}, "web")
	web := api.FindProject("web")
	r := cli.Run([]string{"deploy", "--dry-run", "--project", jsonString(api.FindProject("app")["id"])}, Options{Env: env})
	Code(t, r, 5)
	Contains(t, r.ErrJSON(t)["error"].(string), "only for the app Web")
	// A slug cannot be compared with the public ID without another request: a note, not an error.
	r = cli.Run([]string{"deploy", "--dry-run"}, Options{Env: env})
	Code(t, r, 0)
	Contains(t, r.JSON(t)["note"].(string), "the app Web ("+jsonString(web["id"])+")")
}

func TestDeployDryRunOnAnOlderServer(t *testing.T) {
	api, cli, env := dryRunSetup(t, []string{"releases:write"}, "")
	api.Override = func(r *Request) *Reply {
		if r.Path == "/v1/tokens/current" {
			return &Reply{Status: 404, Body: J{"error": "not_found"}}
		}
		return nil
	}
	r := cli.Run([]string{"deploy", "--dry-run"}, Options{Env: env})
	Code(t, r, 0)
	out := r.JSON(t)
	Equal(t, out["scope_checked"], false)
	Contains(t, out["note"].(string), "not available on this server")
}

func TestDeployDryRunWithAWrongToken(t *testing.T) {
	api, cli, env := dryRunSetup(t, []string{"releases:write"}, "")
	env["CHECKEND_TOKEN"] = "ck_test_wrong_token_0123456789abcdef"
	r := cli.Run([]string{"deploy", "--dry-run"}, Options{Env: env})
	Code(t, r, 5)
	Equal(t, api.Paths(), []string{"GET /v1/tokens/current"})
}
