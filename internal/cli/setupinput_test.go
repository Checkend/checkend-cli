package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func TestSetupReadsTheKeyFromStdin(t *testing.T) {
	api, cli, _ := setup(t)
	r := cli.Run([]string{"setup", "--url", api.URL, "--key-stdin"}, Options{Stdin: api.SetupKey + "\r\n"})
	Code(t, r, 0)
	NotContains(t, r.All, api.SetupKey)
	NotContains(t, r.Stderr, "shell history")
	Equal(t, api.Requests()[1].Authorization, "Bearer "+api.SetupKey)

	report := cli.Run([]string{"setup", "report", "sdk_installed", "--key-stdin"}, Options{Stdin: api.SetupKey + "\n"})
	Code(t, report, 0)
	Equal(t, api.Last().Authorization, "Bearer "+api.SetupKey)
}

func TestSetupAsksForTheKeyWithAHiddenPromptOnATerminal(t *testing.T) {
	api, cli, _ := setup(t)
	r := cli.Run([]string{"setup", "--url", api.URL, "--json"}, Options{TTY: true, Answers: []string{api.SetupKey}})
	Code(t, r, 0)
	Equal(t, cli.Prompts, []Prompt{{Question: "Setup key: ", Secret: true}})
	NotContains(t, r.All, api.SetupKey)

	// Another machine (no agent token, no login) asks for the key to report a step.
	other := NewHarness(t)
	report := other.Run([]string{"setup", "report", "sdk_installed", "--project", jsonString(r.JSON(t)["project"].(map[string]any)["id"])},
		Options{TTY: true, Answers: []string{api.SetupKey}, Env: map[string]string{"CHECKEND_URL": api.URL}})
	Code(t, report, 0)
	Equal(t, other.Prompts, []Prompt{{Question: "Setup key: ", Secret: true}})
	Equal(t, api.Last().Authorization, "Bearer "+api.SetupKey)
}

func TestSetupWithoutAKeyAndNoTerminalPointsToKeyStdin(t *testing.T) {
	api, cli, _ := setup(t)
	r := cli.Run([]string{"setup", "--url", api.URL})
	Code(t, r, 2)
	Contains(t, r.ErrJSON(t)["hint"].(string), "--key-stdin")
	Equal(t, len(api.Requests()), 0)
	for _, argv := range [][]string{
		{"setup", "--url", api.URL, "--key-stdin", "--key", api.SetupKey},
		{"setup", "--url", api.URL, "--key-stdin"},
	} {
		r := cli.Run(argv, Options{Stdin: ""})
		Code(t, r, 2)
	}
	r = cli.Run([]string{"setup", "--url", api.URL, "--key-stdin"}, Options{Stdin: "ck_not_a_setup_key_0123456789\n"})
	Code(t, r, 2)
	Equal(t, len(api.Requests()), 0)
}

func TestSetupWarnsThatKeyIsDeprecated(t *testing.T) {
	api, cli, _ := setup(t)
	r := cli.Run(setupArgs(api))
	Code(t, r, 0)
	Contains(t, r.Stderr, "--key puts the setup key in your shell history and in `ps`")
	Contains(t, r.Stderr, "--key-stdin")
	NotContains(t, r.All, api.SetupKey)
	// CHECKEND_SETUP_KEY is not argv: no warning.
	r = cli.Run([]string{"setup", "--url", api.URL}, Options{Env: map[string]string{"CHECKEND_SETUP_KEY": api.SetupKey}})
	Code(t, r, 0)
	NotContains(t, r.Stderr, "shell history")
}

// The MCP commands name the ce that runs, when `ce` on PATH is another file (or none).
func TestMCPCommandsNameTheRunningBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "ce")
	_ = os.MkdirAll(filepath.Dir(exe), 0o755)
	_ = os.WriteFile(exe, []byte("x"), 0o755)
	link := filepath.Join(dir, "path", "ce")
	_ = os.MkdirAll(filepath.Dir(link), 0o755)
	_ = os.Symlink(exe, link)
	other := filepath.Join(dir, "other", "ce")
	_ = os.MkdirAll(filepath.Dir(other), 0o755)
	_ = os.WriteFile(other, []byte("y"), 0o755)

	for name, c := range map[string]struct {
		path string
		want string
	}{
		"ce on PATH is this binary": {link, "ce"},
		"ce on PATH is another":     {other, exe},
		"no ce on PATH":             {"", exe},
	} {
		api, cli, _ := setup(t)
		cli.Executable, cli.CEOnPath = exe, c.path
		r := cli.Run(setupArgs(api))
		Code(t, r, 0)
		mcp := r.JSON(t)["mcp"].(map[string]any)
		Equal(t, mcp["claude_code"], "claude mcp add checkend -- "+c.want+" mcp --url "+api.URL)
		Equal(t, mcp["codex"], "codex mcp add checkend -- "+c.want+" mcp --url "+api.URL)
		Contains(t, mcp["cursor"].(string), `"command":"`+c.want+`"`)
		help := cli.Run([]string{"mcp", "--help"})
		if !strings.Contains(help.Stdout, "claude mcp add checkend -- "+c.want+" mcp --url <url>") {
			t.Errorf("%s: help:\n%s", name, help.Stdout)
		}
	}
}
