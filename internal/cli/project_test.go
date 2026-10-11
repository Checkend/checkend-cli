package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func readLink(t *testing.T, cli *Harness) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cli.Repo, ".checkend.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return out
}

func id(p J) string { return jsonString(p["id"]) }

func dsn(p J) string { return p["dsns"].([]any)[0].(string) }

func TestProjectListPrintsATable(t *testing.T) {
	api, cli, env := setup(t)
	a := api.AddProject("API Server")
	api.AddProject("Web", "javascript")
	r := cli.Run([]string{"project", "list"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	Matches(t, r.Stdout, `ID\s+SLUG\s+NAME\s+PLATFORM`)
	Matches(t, r.Stdout, id(a)+`\s+api-server\s+API Server\s+node`)
	Contains(t, r.Stdout, "web")
}

func TestProjectListPrintsTheAPIJSON(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("API Server")
	Equal(t, cli.Run([]string{"project", "list"}, Options{Env: env}).JSON(t), J{"data": api.Projects})
}

func TestProjectListKeepsTheServerKeyOrder(t *testing.T) {
	api, cli, env := setup(t)
	api.Override = func(r *Request) *Reply {
		return &Reply{Status: 200, Raw: `{"data":[{"z":1,"id":12345678901234567890,"a":"x"}],"extra":true}`}
	}
	r := cli.Run([]string{"project", "list"}, Options{Env: env})
	Code(t, r, 0)
	want := "{\n  \"data\": [\n    {\n      \"z\": 1,\n      \"id\": 12345678901234567890,\n      \"a\": \"x\"\n    }\n  ],\n  \"extra\": true\n}\n"
	if r.Stdout != want {
		t.Errorf("stdout:\n%s", r.Stdout)
	}
}

func TestProjectListSaysHowToCreateTheFirstProject(t *testing.T) {
	_, cli, env := setup(t)
	Contains(t, cli.Run([]string{"project", "list"}, Options{Env: env, TTY: true}).Stdout, "ce project create --name")
}

func TestProjectListCleansServerText(t *testing.T) {
	api, cli, env := setup(t)
	name := "Evil\x1b]0;owned\x07\x1b[2J name\u009b"
	api.AddProject(name)
	tty := cli.Run([]string{"project", "list"}, Options{Env: env, TTY: true})
	Contains(t, tty.Stdout, "Evil name")
	NotMatches(t, tty.Stdout, "[\x1b\x07\u009b]")
	js := cli.Run([]string{"project", "list"}, Options{Env: env})
	NotMatches(t, js.Stdout, "[\x1b\x07\u009b]")
	if got := js.JSON(t)["data"].([]any)[0].(map[string]any)["name"]; got != name {
		t.Errorf("name: %q", got)
	}
}

func TestProjectCreatePrintsTheDSN(t *testing.T) {
	api, cli, env := setup(t)
	r := cli.Run([]string{"project", "create", "--name", "My App", "--platform", "node"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	last := api.Last()
	if last.Method != "POST" || last.Path != "/v1/projects" {
		t.Errorf("request: %s %s", last.Method, last.Path)
	}
	Equal(t, last.Body, J{"name": "My App", "platform": "node"})
	p := api.Projects[0]
	Contains(t, r.Stdout, "Created project my-app ("+id(p)+")")
	Contains(t, r.Stdout, dsn(p))
}

func TestProjectCreatePrintsJSON(t *testing.T) {
	api, cli, env := setup(t)
	r := cli.Run([]string{"project", "create", "--name", "My App"}, Options{Env: env})
	data := r.JSON(t)["data"].(map[string]any)
	if data["slug"] != "my-app" || data["dsn"] == nil {
		t.Errorf("data: %#v", data)
	}
	Equal(t, api.Last().Body, J{"name": "My App"})
}

func TestProjectCreateExits2WhenTheServerRejectsTheInput(t *testing.T) {
	_, cli, env := setup(t)
	r := cli.Run([]string{"project", "create", "--name", " "}, Options{Env: env})
	Code(t, r, 2)
	Contains(t, r.ErrJSON(t)["error"].(string), "name")
}

func TestProjectDSNBySlugOrPublicID(t *testing.T) {
	api, cli, env := setup(t)
	p := api.AddProject("My App")
	for _, ref := range []string{"my-app", "MY-APP", id(p)} {
		r := cli.Run([]string{"project", "dsn", ref}, Options{Env: env, TTY: true})
		Code(t, r, 0)
		if strings.TrimSpace(r.Stdout) != dsn(p) {
			t.Errorf("%s: %q", ref, r.Stdout)
		}
	}
}

func TestProjectDSNUsesTheFlagThenTheLink(t *testing.T) {
	api, cli, env := setup(t)
	a := api.AddProject("A")
	b := api.AddProject("B")
	cli.Write(".checkend.json", `{"project":"`+id(a)+`"}`)
	if got := strings.TrimSpace(cli.Run([]string{"project", "dsn"}, Options{Env: env, TTY: true}).Stdout); got != dsn(a) {
		t.Errorf("linked: %q", got)
	}
	if got := strings.TrimSpace(cli.Run([]string{"project", "dsn", "--project", "b"}, Options{Env: env, TTY: true}).Stdout); got != dsn(b) {
		t.Errorf("flag: %q", got)
	}
}

func TestProjectDSNPrintsJSON(t *testing.T) {
	api, cli, env := setup(t)
	p := api.AddProject("My App")
	Equal(t, cli.Run([]string{"project", "dsn", "my-app"}, Options{Env: env}).JSON(t),
		J{"data": J{"id": p["id"], "slug": "my-app", "dsns": p["dsns"]}})
}

func TestProjectDSNErrors(t *testing.T) {
	_, cli, env := setup(t)
	unknown := cli.Run([]string{"project", "dsn", "nope"}, Options{Env: env})
	Code(t, unknown, 3)
	Contains(t, unknown.ErrJSON(t)["hint"].(string), "ce project list")
	none := cli.Run([]string{"project", "dsn"}, Options{Env: env})
	Code(t, none, 2)
	Contains(t, none.ErrJSON(t)["hint"].(string), "ce init")
}

func TestInitCreatesAProjectAndPrintsTheSnippet(t *testing.T) {
	api, cli, env := setup(t)
	cli.Write("package.json", `{"dependencies":{"express":"5"}}`)
	r := cli.Run([]string{"init", "--name", "My App"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	p := api.Projects[0]
	Equal(t, api.Last().Body, J{"name": "My App", "platform": "node"})
	Equal(t, readLink(t, cli), J{"project": id(p), "slug": "my-app"})
	Contains(t, r.Stdout, dsn(p))
	Contains(t, r.Stdout, `import * as Sentry from "@sentry/node"`)
	Contains(t, r.Stdout, `Sentry.init({ dsn: "`+dsn(p)+`" })`)
	Contains(t, r.Stdout, ".checkend.json")
}

func TestInitNeverWritesTheTokenOrURL(t *testing.T) {
	api, cli, env := setup(t)
	cli.Run([]string{"init", "--name", "My App"}, Options{Env: env})
	data, _ := os.ReadFile(filepath.Join(cli.Repo, ".checkend.json"))
	NotContains(t, string(data), api.Token)
	var keys []string
	for k := range readLink(t, cli) {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	Equal(t, keys, []string{"project", "slug"})
}

func TestInitLinksAnExistingProject(t *testing.T) {
	api, cli, env := setup(t)
	p := api.AddProject("Existing")
	r := cli.Run([]string{"init", "--project", id(p)}, Options{Env: env})
	Code(t, r, 0)
	for _, req := range api.Requests() {
		if req.Method == "POST" {
			t.Error("POST made")
		}
	}
	Equal(t, readLink(t, cli), J{"project": id(p), "slug": "existing"})
	out := r.JSON(t)
	Equal(t, out["project"].(map[string]any)["id"], p["id"])
	if out["dsn"] != dsn(p) || out["created"] != false {
		t.Errorf("out: %#v", out)
	}
}

func TestInitWritesTheLinkAtTheRepoRoot(t *testing.T) {
	_, cli, env := setup(t)
	sub := filepath.Join(cli.Repo, "packages", "web")
	_ = os.MkdirAll(sub, 0o755)
	cli.Run([]string{"init", "--name", "Web"}, Options{Env: env, Cwd: sub})
	if readLink(t, cli)["slug"] != "web" || Exists(filepath.Join(sub, ".checkend.json")) {
		t.Error("wrong place")
	}
}

func TestInitAsksEnterCreatesAProjectNamedAfterTheRepo(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("Other")
	r := cli.Run([]string{"init"}, Options{Env: env, TTY: true, Answers: []string{""}})
	Code(t, r, 0)
	Contains(t, cli.Prompts[0].Question, "Choose")
	Contains(t, r.Stderr, "1) other")
	NotContains(t, r.Stdout, "1) other")
	if api.Last().Body.(map[string]any)["name"] != "repo" || readLink(t, cli)["slug"] != "repo" {
		t.Error("not created as repo")
	}
}

func TestInitAsksANumberLinksThatProject(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("Other")
	second := api.AddProject("Second")
	cli.Run([]string{"init"}, Options{Env: env, TTY: true, Answers: []string{"2"}})
	if readLink(t, cli)["project"] != id(second) {
		t.Error("wrong project")
	}
}

func TestInitWrongChoiceWritesNothing(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("Other")
	Code(t, cli.Run([]string{"init"}, Options{Env: env, TTY: true, Answers: []string{"7"}}), 2)
	if Exists(filepath.Join(cli.Repo, ".checkend.json")) {
		t.Error("written")
	}
}

func TestInitNeedsANameOrProjectWhenItCannotAsk(t *testing.T) {
	api, cli, env := setup(t)
	r := cli.Run([]string{"init"}, Options{Env: env})
	Code(t, r, 2)
	Matches(t, r.ErrJSON(t)["hint"].(string), `--name.*--project|--project.*--name`)
	if len(api.Requests()) != 0 || Exists(filepath.Join(cli.Repo, ".checkend.json")) {
		t.Error("did something")
	}
}

func TestInitUsesTheLinkedProject(t *testing.T) {
	api, cli, env := setup(t)
	p := api.AddProject("Linked")
	cli.Write(".checkend.json", `{"project":"linked"}`)
	r := cli.Run([]string{"init"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	if len(cli.Prompts) != 0 {
		t.Error("asked")
	}
	Contains(t, r.Stdout, dsn(p))
}

func TestInitDetectsThePlatform(t *testing.T) {
	api, _, env := setup(t)
	cases := [][4]string{
		{"package.json", `{"dependencies":{"react":"19"}}`, "javascript", "@sentry/browser"},
		{"Gemfile", `gem "rails"`, "ruby", "Sentry.init do |config|"},
		{"pyproject.toml", "[project]", "python", "sentry_sdk.init("},
		{"go.mod", "module x", "go", "sentry.Init("},
		{"composer.json", "{}", "php", `\Sentry\init(`},
	}
	for _, c := range cases {
		repo := NewHarness(t)
		repo.Write(c[0], c[1])
		out := repo.Run([]string{"init", "--name", "App " + c[2]}, Options{Env: env}).JSON(t)
		if out["platform"] != c[2] {
			t.Errorf("%s: platform %v", c[0], out["platform"])
		}
		Contains(t, out["snippet"].(string), c[3])
	}
	_ = api
}

func TestInitUsesThePlatformFlag(t *testing.T) {
	_, cli, env := setup(t)
	cli.Write("Gemfile", "")
	if p := cli.Run([]string{"init", "--name", "X", "--platform", "python"}, Options{Env: env}).JSON(t)["platform"]; p != "python" {
		t.Errorf("platform %v", p)
	}
}

func TestInitRefusesADSNThatCouldInjectCode(t *testing.T) {
	api, cli, env := setup(t)
	api.Override = func(r *Request) *Reply {
		if r.Method == "POST" {
			return &Reply{Status: 201, Body: J{"data": J{"id": 1, "slug": "x", "name": "x", "platform": "node", "dsn": `https://k@h/1" }); evil(); ("`}}}
		}
		return nil
	}
	r := cli.Run([]string{"init", "--name", "X"}, Options{Env: env, TTY: true})
	Code(t, r, 1)
	NotContains(t, r.Stdout, "evil")
	if Exists(filepath.Join(cli.Repo, ".checkend.json")) {
		t.Error("written")
	}
}
