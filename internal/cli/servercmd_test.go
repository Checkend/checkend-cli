package cli_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Checkend/checkend-cli/internal/cli"
	"github.com/Checkend/checkend-cli/internal/credstore"
	"github.com/Checkend/checkend-cli/internal/docs"
	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func TestHelpShowsServerAndHidesTheOldNames(t *testing.T) {
	cli := NewHarness(t)
	help := commandsSection(cli.Run([]string{"--help"}).Stdout)
	Contains(t, help, "server <action>")
	Contains(t, help, "update")
	for _, old := range []string{"\n  install", "\n  upgrade", "\n  backup", "\n  uninstall"} {
		NotContains(t, help, old)
	}
	group := cli.Run([]string{"server", "--help"}).Stdout
	for _, c := range []string{"ce server install docker", "ce server install ssh", "ce server install cloudflare", "ce server upgrade", "ce server backup", "ce server status", "ce server uninstall"} {
		Contains(t, group, c)
	}
}

func TestServerInstallWithoutATargetListsTheTargets(t *testing.T) {
	cli := NewHarness(t)
	for _, argv := range [][]string{{"server", "install"}, {"install"}} {
		r := cli.Run(argv, Options{TTY: true})
		Code(t, r, 2)
		for _, line := range []string{"ce server install docker", "ce server install ssh <user@host> --domain <domain>", "ce server install cloudflare"} {
			Contains(t, r.Stderr, line)
		}
		Contains(t, r.Stderr, "Docs: "+docs.URL(docs.SelfHost))
	}
	r := cli.Run([]string{"server"}, Options{TTY: true})
	Code(t, r, 2)
	Contains(t, r.Stderr, "ce server upgrade")
	r = cli.Run([]string{"server", "install", "podman"})
	Code(t, r, 2)
	Contains(t, r.ErrJSON(t)["error"].(string), `"ce server install podman"`)
}

func TestTheOldNamesStillWorkWithAHint(t *testing.T) {
	f, cli := installer(t)
	f.Publish("0.3.0")
	api := NewFakeAPI(t)
	port := api.URL[strings.LastIndex(api.URL, ":")+1:]
	r := cli.Run([]string{"install", "docker", "--yes", "--port", port})
	Code(t, r, 0)
	Contains(t, r.Stderr, "`ce install docker` is now `ce server install docker`")
	running := false
	dockerHost(cli, &running)
	for old, now := range map[string][]string{
		"upgrade":   {"upgrade", "--yes"},
		"status":    {"status", "--server"},
		"backup":    {"backup"},
		"uninstall": {"uninstall", "--yes"},
	} {
		r := cli.Run(now)
		Contains(t, r.Stderr, "is now `ce server "+old+"`")
	}
}

func TestServerCommandsExplainAHostedSpace(t *testing.T) {
	_, cli := installer(t)
	for _, argv := range [][]string{{"server", "upgrade", "--yes"}, {"server", "backup"}, {"server", "status"}, {"server", "uninstall", "--yes"}, {"upgrade", "--yes"}} {
		r := cli.Run(append(argv, "--url", "https://hosted.example/4qm63ns"), Options{TTY: true})
		Code(t, r, 1)
		Contains(t, r.Stderr, "This space is hosted by Checkend and updates itself.")
		Contains(t, r.Stderr, "`ce server` is only for servers that you run yourself.")
		Contains(t, r.Stderr, "Docs: "+docs.URL(docs.SelfHost))
	}
	r := cli.Run([]string{"server", "upgrade", "--yes", "--url", "https://errors.example.com"}, Options{TTY: true})
	Code(t, r, 1)
	Contains(t, r.Stderr, "Docs: "+docs.URL(docs.Upgrade))
}

func TestEveryErrorHasANextStepAndADocsLink(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	cases := map[string]struct {
		argv  []string
		env   map[string]string
		topic docs.Topic
		reply *Reply
	}{
		"unknown command": {argv: []string{"isue"}, topic: docs.QuickStart},
		"not logged in":   {argv: []string{"issue", "list"}, env: map[string]string{}, topic: docs.Authentication},
		"bad url":         {argv: []string{"status", "--url", "http://errors.example.com"}, env: map[string]string{}, topic: docs.PublicURL},
		"401":             {argv: []string{"project", "list"}, env: env, topic: docs.Authentication, reply: &Reply{Status: 401, Body: J{"error": "unauthorized"}}},
		"403":             {argv: []string{"project", "list"}, env: env, topic: docs.Scopes, reply: &Reply{Status: 403, Body: J{"error": "forbidden"}}},
		"404":             {argv: []string{"issue", "view", "999"}, env: env, topic: docs.Issues},
		"413":             {argv: []string{"project", "list"}, env: env, topic: docs.RequestLimits, reply: &Reply{Status: 413, Body: J{"error": "payload_too_large"}}},
		"429":             {argv: []string{"project", "list"}, env: env, topic: docs.BackPressure, reply: &Reply{Status: 429, Body: J{"error": "rate_limited"}}},
		"no release":      {argv: []string{"server", "install", "docker", "--yes"}, topic: docs.Upgrade},
		"usage":           {argv: []string{"issue", "list", "--bogus"}, env: env, topic: docs.Issues},
	}
	for name, c := range cases {
		api.Override = func(*Request) *Reply { return c.reply }
		cli.Releases = NewFakeReleases(t)
		for _, tty := range []bool{true, false} {
			r := cli.Run(c.argv, Options{Env: c.env, TTY: tty})
			if r.Code == 0 {
				t.Fatalf("%s: exit 0", name)
			}
			if tty {
				lines := strings.Split(strings.TrimSpace(r.Stderr), "\n")
				if len(lines) < 3 {
					t.Errorf("%s: want the error, the next step and the docs link:\n%s", name, r.Stderr)
				}
				Equal(t, lines[len(lines)-1], "Docs: "+docs.URL(c.topic))
			} else {
				out := r.ErrJSON(t)
				Equal(t, out["docs"], docs.URL(c.topic))
				if out["hint"] == nil || out["hint"] == "" {
					t.Errorf("%s: no hint: %v", name, out)
				}
			}
		}
	}
}

func TestAKeychainThatFailsHasADocsLink(t *testing.T) {
	api, cli, _ := setup(t)
	Code(t, cli.Run([]string{"login", "--url", api.URL, "--with-token"}, Options{Stdin: api.Token}), 0)
	cli.Keyring.Broken = true
	r := cli.Run([]string{"project", "list"})
	Code(t, r, 5)
	Equal(t, r.ErrJSON(t)["docs"], docs.URL(docs.ConnectAgent))
	_ = credstore.Memory{}
}

func TestEveryCommandHasADocsTopic(t *testing.T) {
	for _, c := range cli.Commands() {
		if c.Docs == "" || !slices.Contains(docs.All, c.Docs) {
			t.Errorf("ce %s: docs topic %q is not in the docs table", strings.Join(c.Path, " "), c.Docs)
		}
	}
}

var topicPattern = regexp.MustCompile(`^[a-z0-9-]+(#[a-z0-9_-]+)?$`)

// Every docs.X that the code uses is a topic of the table, and every topic has a valid form.
func TestEveryDocsLinkIsInTheTable(t *testing.T) {
	names := map[string]bool{}
	fset := token.NewFileSet()
	table, err := parser.ParseFile(fset, "../docs/docs.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(table, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.ValueSpec:
			for _, id := range v.Names {
				names[id.Name] = true
			}
		case *ast.TypeSpec:
			names[v.Name.Name] = true
		case *ast.FuncDecl:
			names[v.Name.Name] = true
		}
		return true
	})
	files, _ := filepath.Glob("../*/*.go")
	used := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || strings.Contains(file, "/docs/") {
			continue
		}
		src, _ := os.ReadFile(file)
		f, err := parser.ParseFile(fset, file, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "docs" {
					used++
					if !names[sel.Sel.Name] {
						t.Errorf("%s: docs.%s is not in the docs table", file, sel.Sel.Name)
					}
				}
			}
			return true
		})
	}
	if used < 20 {
		t.Errorf("only %d docs links in the code", used)
	}
	for _, topic := range docs.All {
		if !topicPattern.MatchString(string(topic)) {
			t.Errorf("topic %q is not <page>#<anchor>", topic)
		}
	}
}
