package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

// cliReleases fakes the GitHub releases of the CLI repo with these tags (newest first).
func cliReleases(t *testing.T, cli *Harness, tags ...string) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/Checkend/checkend-cli/releases" {
			http.NotFound(w, r)
			return
		}
		list := []any{}
		for _, tag := range tags {
			list = append(list, J{"tag_name": tag, "draft": false, "prerelease": strings.Contains(tag, "-")})
		}
		_ = json.NewEncoder(w).Encode(list)
	}))
	t.Cleanup(server.Close)
	cli.CLIReleaseAPI = server.URL + "/repos/Checkend/checkend-cli"
}

func updateCalls(cli *Harness) []string {
	var out []string
	for _, c := range cli.Calls {
		out = append(out, c.Name+" "+strings.Join(c.Args, " "))
	}
	return out
}

func TestUpdateSaysWhenNoReleaseExists(t *testing.T) {
	cli := NewHarness(t)
	cliReleases(t, cli)
	cli.Executable = filepath.Join(cli.Home, "go", "bin", "ce")
	r := cli.Run([]string{"update"}, Options{TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, "No ce release is published yet")
	Contains(t, r.Stdout, "go install github.com/Checkend/checkend-cli/cmd/ce@latest")
	Equal(t, len(cli.Calls), 0)
}

func TestUpdateUsesTheInstallMethod(t *testing.T) {
	for exe, want := range map[string]string{
		"/opt/homebrew/Cellar/checkend/0.2.0/bin/ce": "brew upgrade checkend",
		"HOME/Library/Caches/checkend/cli/0.2.0/ce":  "npm install -g @checkend/cli@latest",
		"HOME/.cache/checkend/cli/0.2.0/ce":          "npm install -g @checkend/cli@latest",
		"HOME/.local/bin/ce":                         "sh -c curl -fsSL https://raw.githubusercontent.com/Checkend/checkend-cli/main/install.sh | sh",
		"HOME/go/bin/ce":                             "go install github.com/Checkend/checkend-cli/cmd/ce@latest",
	} {
		cli := NewHarness(t)
		cliReleases(t, cli, "v0.3.0-rc.1", "v0.2.9", "v0.3.0")
		cli.Executable = strings.Replace(exe, "HOME", cli.Home, 1)
		cli.Exec = func([]byte, string, ...string) ([]byte, error) { return nil, nil }
		r := cli.Run([]string{"update", "--yes"})
		Code(t, r, 0)
		Equal(t, updateCalls(cli), []string{want})
		out := r.JSON(t)
		Equal(t, out["from"], "0.2.0")
		Equal(t, out["to"], "0.3.0")
	}
}

func TestUpdateAsksFirstAndKnowsWhenItIsCurrent(t *testing.T) {
	cli := NewHarness(t)
	cliReleases(t, cli, "v0.2.0")
	cli.Executable = filepath.Join(cli.Home, "go", "bin", "ce")
	r := cli.Run([]string{"update"})
	Code(t, r, 0)
	Contains(t, r.Stdout, "0.2.0")
	Contains(t, r.Stdout, "latest")

	cliReleases(t, cli, "v0.3.0")
	r = cli.Run([]string{"update"})
	Code(t, r, 2)
	Contains(t, r.Stderr, "--yes")
	r = cli.Run([]string{"update"}, Options{TTY: true, Answers: []string{"n"}})
	Code(t, r, 2)
	Equal(t, len(cli.Calls), 0)
}

func TestUpdatePrintsTheCommandsWhenItCannotTell(t *testing.T) {
	cli := NewHarness(t)
	cliReleases(t, cli, "v0.3.0")
	cli.Executable = "/usr/local/bin/ce"
	r := cli.Run([]string{"update", "--yes"}, Options{TTY: true})
	Code(t, r, 1)
	for _, c := range []string{"brew upgrade checkend", "npm install -g @checkend/cli@latest", "install.sh | sh", "go install github.com/Checkend/checkend-cli/cmd/ce@latest"} {
		Contains(t, r.Stderr, c)
	}
	Contains(t, r.Stderr, "Docs: ")
	Equal(t, len(cli.Calls), 0)
}
