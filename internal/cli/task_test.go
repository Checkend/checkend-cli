package cli_test

import (
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestDeployReportsTheHEADCommitOfTheLinkedProject(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	cli.Write(".checkend.json", `{"project":"app"}`)
	cli.Git = map[string]string{"rev-parse HEAD": sha}
	r := cli.Run([]string{"deploy"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, api.Last().Body, J{"project": "app", "commit": sha})
	Equal(t, r.JSON(t)["data"].(map[string]any)["release"], sha)
}

func TestDeploySendsTheOptions(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	cli.Git = map[string]string{"rev-list --max-count=1000 abcdef1..HEAD": "1111111111111111111111111111111111111111\n2222222222222222222222222222222222222222"}
	r := cli.Run([]string{"deploy", "--project", "app", "--release", "v1.2.3", "--environment", "staging",
		"--commit", "abcdef0", "--commits-since", "abcdef1", "--pr", "https://github.com/o/r/pull/7"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	Equal(t, api.Last().Body, J{"project": "app", "commit": "abcdef0", "release": "v1.2.3", "environment": "staging",
		"commits":       []any{"1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"},
		"pull_requests": []any{"https://github.com/o/r/pull/7"}})
	Contains(t, r.Stdout, "Reported the deploy of v1.2.3 to staging")
}

func TestDeployAcceptsEnvAsAShortName(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	Code(t, cli.Run([]string{"deploy", "--project", "app", "--commit", "abcdef0", "--env", "staging"}, Options{Env: env}), 0)
	Equal(t, api.Last().Body.(map[string]any)["environment"], "staging")
}

func TestDeployErrors(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	// No commit and no git repo: exit 2 before any request.
	r := cli.Run([]string{"deploy", "--project", "app"}, Options{Env: env})
	Code(t, r, 2)
	Contains(t, r.ErrJSON(t)["hint"].(string), "--commit")
	if len(api.Requests()) != 0 {
		t.Error("request made")
	}
	// No project.
	Code(t, cli.Run([]string{"deploy", "--commit", "abcdef0"}, Options{Env: env}), 2)
	// Unknown project.
	unknown := cli.Run([]string{"deploy", "--project", "nope", "--commit", "abcdef0"}, Options{Env: env})
	Code(t, unknown, 3)
	Contains(t, unknown.ErrJSON(t)["hint"].(string), "ce project list")
	// The server rejects the commit.
	Code(t, cli.Run([]string{"deploy", "--project", "app", "--commit", "zzz"}, Options{Env: env}), 2)
}

func TestProjectDeleteNeedsYesWithoutATerminal(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	r := cli.Run([]string{"project", "delete", "app"}, Options{Env: env})
	Code(t, r, 2)
	Contains(t, r.ErrJSON(t)["hint"].(string), "--yes")
	if len(api.Requests()) != 0 || len(api.Projects) != 1 {
		t.Error("did something")
	}
}

func TestProjectDeleteWithYes(t *testing.T) {
	api, cli, env := setup(t)
	p := api.AddProject("App")
	r := cli.Run([]string{"project", "delete", "app", "--yes"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, api.Paths(), []string{"GET /v1/projects", "DELETE /v1/projects/" + id(p)})
	Equal(t, r.JSON(t), J{"data": J{"id": p["id"], "slug": "app", "deleted": true}})
}

func TestProjectDeleteAsksForTheNameOnATerminal(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("My App")
	r := cli.Run([]string{"project", "delete", "my-app"}, Options{Env: env, TTY: true, Answers: []string{"My App"}})
	Code(t, r, 0)
	Contains(t, r.Stderr, "cannot be undone")
	Contains(t, cli.Prompts[0].Question, "name")
	Contains(t, r.Stdout, "Deleted my-app")
	if len(api.Projects) != 0 {
		t.Error("not deleted")
	}
}

func TestProjectDeleteWrongNameDoesNothing(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("My App")
	r := cli.Run([]string{"project", "delete", "my-app"}, Options{Env: env, TTY: true, Answers: []string{"my app?"}})
	Code(t, r, 2)
	for _, p := range api.Paths() {
		if strings.HasPrefix(p, "DELETE") {
			t.Error("deleted")
		}
	}
}

func TestProjectDeleteNeedsApproval(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	api.Override = func(r *Request) *Reply {
		if r.Method == "DELETE" {
			return &Reply{Status: 202, Body: J{"status": "approval_required", "message": "REQUIRE_APPROVAL has delete_project."}}
		}
		return nil
	}
	r := cli.Run([]string{"project", "delete", "app", "--yes"}, Options{Env: env, TTY: true})
	Code(t, r, 6)
	Contains(t, r.Stdout, "needs approval")
	js := cli.Run([]string{"project", "delete", "app", "--yes"}, Options{Env: env})
	Code(t, js, 6)
	Equal(t, js.JSON(t)["status"], "approval_required")
}

func TestProjectDeleteUnknownProject(t *testing.T) {
	_, cli, env := setup(t)
	Code(t, cli.Run([]string{"project", "delete", "nope", "--yes"}, Options{Env: env}), 3)
}

func TestDSNRotateMakesANewKey(t *testing.T) {
	api, cli, env := setup(t)
	p := api.AddProject("App")
	cli.Write(".checkend.json", `{"project":"app"}`)
	r := cli.Run([]string{"dsn", "rotate"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	Equal(t, api.Last().Method+" "+api.Last().Path, "POST /v1/projects/app/keys")
	newDSN := p["dsns"].([]any)[1].(string)
	Contains(t, r.Stdout, "New DSN: "+newDSN)
	Contains(t, r.Stdout, "ce dsn rotate --revoke-old")
}

func TestDSNRotateRevokesTheOldKeys(t *testing.T) {
	api, cli, env := setup(t)
	p := api.AddProject("App")
	old := strings.Split(strings.TrimPrefix(dsn(p), "http://"), "@")[0]
	Code(t, cli.Run([]string{"dsn", "rotate", "--project", "app"}, Options{Env: env}), 0)
	r := cli.Run([]string{"dsn", "rotate", "--project", "app", "--revoke-old", "--yes"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, api.Last().Method+" "+api.Last().Path, "DELETE /v1/projects/app/keys/"+old)
	Equal(t, len(p["dsns"].([]any)), 1)
	Equal(t, r.JSON(t)["data"].(map[string]any)["revoked"], []any{old})
}

func TestDSNRotateRevokeNeedsYesWithoutATerminal(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	r := cli.Run([]string{"dsn", "rotate", "--project", "app", "--revoke-old"}, Options{Env: env})
	Code(t, r, 2)
	if len(api.Requests()) != 0 {
		t.Error("request made")
	}
}

func TestDSNRotateRevokeWithOneKeyDoesNothing(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	r := cli.Run([]string{"dsn", "rotate", "--project", "app", "--revoke-old", "--yes"}, Options{Env: env})
	Code(t, r, 4)
	Contains(t, r.ErrJSON(t)["hint"].(string), "ce dsn rotate")
	for _, p := range api.Paths() {
		if strings.HasPrefix(p, "DELETE") {
			t.Error("revoked the only key")
		}
	}
}
