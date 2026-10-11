package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func setup(t *testing.T) (*FakeAPI, *Harness, map[string]string) {
	api := NewFakeAPI(t)
	return api, NewHarness(t), map[string]string{"CHECKEND_URL": api.URL, "CHECKEND_TOKEN": api.Token}
}

func commandsSection(help string) string {
	after, _, _ := strings.Cut(strings.SplitN(help, "Commands:", 2)[1], "Options")
	return after
}

func TestHelpListsOnlyTheCommandsThatExist(t *testing.T) {
	_, cli, _ := setup(t)
	r := cli.Run([]string{"--help"})
	Code(t, r, 0)
	for _, c := range []string{"login", "logout", "status", "init", "next", "issue", "event", "project"} {
		Contains(t, r.Stdout, c)
	}
	section := commandsSection(r.Stdout)
	for _, later := range []string{"brief", "claim", "note", "fix", "release", "token", "resolve"} {
		NotMatches(t, section, `(?m)^\s+`+later+`\b`)
	}
}

func TestHelpWithoutACommand(t *testing.T) {
	_, cli, _ := setup(t)
	r := cli.Run(nil)
	Code(t, r, 0)
	Contains(t, r.Stdout, "Usage: ce <command>")
}

func TestHelpForAGroup(t *testing.T) {
	api, cli, _ := setup(t)
	r := cli.Run([]string{"project", "--help"})
	Code(t, r, 0)
	Contains(t, r.Stdout, "ce project list")
	Contains(t, r.Stdout, "ce project create --name <name>")
	r = cli.Run([]string{"issue", "--help"})
	Contains(t, r.Stdout, "ce issue list")
	Contains(t, r.Stdout, "ce issue view <id>")
	r = cli.Run([]string{"help", "issue", "list"})
	Code(t, r, 0)
	Contains(t, r.Stdout, "--status")
	if len(api.Requests()) != 0 {
		t.Error("help made a request")
	}
}

func TestVersion(t *testing.T) {
	_, cli, _ := setup(t)
	for _, arg := range []string{"--version", "-V"} {
		r := cli.Run([]string{arg})
		Code(t, r, 0)
		Matches(t, strings.TrimSpace(r.Stdout), `^\d+\.\d+\.\d+`)
	}
}

func TestATypoNeverDoesAnything(t *testing.T) {
	cases := [][]string{
		{"cd"}, {"isue", "list"}, {"project", "frob"}, {"project"},
		{"project", "list", "--bogus"}, {"project", "list", "extra"}, {"project", "dsn", "a", "b"},
		{"init", "--project"}, {"issue", "frob"}, {"issue"}, {"issue", "list", "--bogus"},
		{"issue", "list", "extra"}, {"issue", "view"}, {"issue", "view", "abc"},
		{"issue", "view", "1", "2"}, {"event", "view", "1.5"}, {"next", "1"}, {"status", "extra"},
		{"project", "create"}, {"login", "--url"}, {"--json"}, {"brief", "1"}, {"token", "list"},
		{"issue", "list", "--json=yes"}, {"issue", "list", "--limit", "--json"}, {"issue", "list", "-x"},
		{"login", "--with-token=1"},
	}
	for _, argv := range cases {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			api, cli, env := setup(t)
			r := cli.Run(argv, Options{Env: env})
			Code(t, r, 2)
			if r.Stderr == "" {
				t.Error("no message")
			}
			if n := len(api.Requests()); n != 0 {
				t.Errorf("%d requests", n)
			}
			if entries, _ := os.ReadDir(cli.Home); len(entries) != 0 {
				t.Errorf("files in home: %v", entries)
			}
			if Exists(filepath.Join(cli.Repo, ".checkend.json")) {
				t.Error(".checkend.json written")
			}
		})
	}
}

func TestUsageErrorAsJSONWhenStdoutIsNotATerminal(t *testing.T) {
	_, cli, _ := setup(t)
	r := cli.Run([]string{"cd"})
	Equal(t, r.ErrJSON(t), map[string]any{"error": `Unknown command "cd".`, "hint": "Run `ce --help` to see the commands."})
	if r.Stdout != "" {
		t.Errorf("stdout: %q", r.Stdout)
	}
}

func TestUsageErrorAsTextOnATerminal(t *testing.T) {
	_, cli, _ := setup(t)
	r := cli.Run([]string{"cd"}, Options{TTY: true})
	Contains(t, r.Stderr, `Unknown command "cd".`)
	Contains(t, r.Stderr, "ce --help")
}

func TestAnUnknownCommandNameIsCleaned(t *testing.T) {
	_, cli, _ := setup(t)
	r := cli.Run([]string{"x\x1b]0;evil\x07"}, Options{TTY: true})
	Code(t, r, 2)
	if regexp.MustCompile("[\x1b\x07]").MatchString(r.All) {
		t.Errorf("escape in output: %q", r.All)
	}
}
