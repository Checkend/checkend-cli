package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/docs"
	"github.com/Checkend/checkend-cli/internal/serverrelease"
	"github.com/Checkend/checkend-cli/internal/version"
)

const cliReleaseAPI = "https://api.github.com/repos/Checkend/checkend-cli"

// updateMethods are the ways to install ce, with the command that updates each one.
var updateMethods = []struct{ name, command string }{
	{"Homebrew", "brew upgrade checkend"},
	{"npm", "npm install -g @checkend/cli@latest"},
	{"the install script", "curl -fsSL https://raw.githubusercontent.com/Checkend/checkend-cli/main/install.sh | sh"},
	{"go install", "go install github.com/Checkend/checkend-cli/cmd/ce@latest"},
}

// installMethod tells how this ce was installed, from the path of its binary: -1 when unknown.
func installMethod(s *Session) int {
	exe := filepath.ToSlash(s.Host.Executable)
	home := filepath.ToSlash(s.Host.Home)
	gobin := filepath.ToSlash(s.Host.Getenv("GOBIN"))
	gopath := filepath.ToSlash(s.Host.Getenv("GOPATH"))
	switch {
	case strings.Contains(exe, "/Cellar/checkend/"):
		return 0
	case strings.Contains(exe, "/checkend/cli/") && (strings.Contains(exe, "/Caches/") || strings.Contains(exe, "/.cache/") || strings.Contains(exe, "/AppData/")):
		return 1
	case exe == home+"/.local/bin/ce" || exe == home+"/.local/bin/checkend":
		return 2
	case gobin != "" && strings.HasPrefix(exe, gobin+"/"), gopath != "" && strings.HasPrefix(exe, gopath+"/bin/"), strings.HasPrefix(exe, home+"/go/bin/"):
		return 3
	}
	return -1
}

// latestCLI is the newest released version of ce, or "" when none is published.
func latestCLI(s *Session) (string, error) {
	base := cliReleaseAPI
	if s.Host.CLIReleaseAPI != "" {
		base = s.Host.CLIReleaseAPI
	}
	client := http.Client{Timeout: 30 * time.Second}
	if s.Host.HTTP != nil {
		client = *s.Host.HTTP
	}
	req, _ := http.NewRequest("GET", base+"/releases?per_page=20", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "checkend-cli/"+version.Version)
	resp, err := client.Do(req)
	if err != nil {
		return "", clierr.New(clierr.Failure, fmt.Sprintf("Cannot reach GitHub to look for a new ce: %v", err), "Check your network, then run `ce update` again.")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", clierr.New(clierr.Failure, fmt.Sprintf("GitHub answered HTTP %d.", resp.StatusCode), "Try `ce update` again later.")
	}
	var list []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if json.Unmarshal(data, &list) != nil {
		return "", clierr.New(clierr.Failure, "GitHub returned an answer that cannot be read.", "Try `ce update` again later.")
	}
	latest := ""
	for _, r := range list {
		v := strings.TrimPrefix(r.Tag, "v")
		if !r.Draft && !r.Prerelease && serverrelease.Compare(v, latest) > 0 {
			latest = v
		}
	}
	return latest, nil
}

func updateCommand() *Command {
	return &Command{
		Path:    []string{"update"},
		Summary: "Update ce itself to the latest release",
		Usage:   "[--yes]",
		Details: []string{
			"  --yes    Do not ask (for agents)",
			"",
			"Finds how ce was installed (Homebrew, npm, the install script or go install) and runs",
			"the update for it. When it cannot tell, it prints the command for each method.",
			"To update a Checkend server that you run yourself, use `ce server upgrade`.",
		},
		Docs:    docs.ConnectAgent,
		Options: map[string]Kind{"yes": Bool},
		Run: func(s *Session) error {
			latest, err := latestCLI(s)
			if err != nil {
				return err
			}
			goInstall := updateMethods[3].command
			if latest == "" {
				s.Out.Data(map[string]any{"current": version.Version, "latest": nil, "command": goInstall},
					func(any) string {
						return fmt.Sprintf("No ce release is published yet. This is ce %s.\nTo build the latest version from source:\n  %s", version.Version, goInstall)
					})
				return nil
			}
			if serverrelease.Compare(latest, version.Version) <= 0 {
				s.Out.Data(map[string]any{"current": version.Version, "latest": latest, "updated": false},
					func(any) string { return fmt.Sprintf("ce %s is the latest release.", version.Version) })
				return nil
			}
			method := installMethod(s)
			if method < 0 {
				var lines []string
				for _, m := range updateMethods {
					lines = append(lines, fmt.Sprintf("  %-20s %s", m.name+":", m.command))
				}
				return clierr.New(clierr.Failure, fmt.Sprintf("ce %s is available (this is %s), but ce cannot tell how it was installed.", latest, version.Version),
					"Run the command of your install method:\n"+strings.Join(lines, "\n"))
			}
			m := updateMethods[method]
			if err := needApproval(s, "ce update"); err != nil {
				return err
			}
			if err := approve(s, []string{fmt.Sprintf("Update ce from %s to %s with %s: %s", version.Version, latest, m.name, m.command)}); err != nil {
				return err
			}
			args := strings.Fields(m.command)
			if method == 2 {
				args = []string{"sh", "-c", m.command}
			}
			if _, err := s.Host.Run(nil, args[0], args[1:]...); err != nil {
				return clierr.New(clierr.Failure, fmt.Sprintf("The update failed: %v", err), "Run it yourself: "+m.command)
			}
			s.Out.Data(map[string]any{"from": version.Version, "to": latest, "method": m.name, "command": m.command, "updated": true},
				func(any) string {
					return fmt.Sprintf("Updated ce from %s to %s (%s).", version.Version, latest, m.name)
				})
			return nil
		},
	}
}
