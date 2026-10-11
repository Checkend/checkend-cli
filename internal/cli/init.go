package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/config"
	"github.com/Checkend/checkend-cli/internal/jv"
	"github.com/Checkend/checkend-cli/internal/snippets"
)

type initResult struct {
	Project struct {
		ID   any    `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"project"`
	Created  bool   `json:"created"`
	DSN      string `json:"dsn"`
	Platform any    `json:"platform"`
	File     string `json:"file"`
	Snippet  string `json:"snippet"`
}

func initCommand() *Command {
	return &Command{
		Path:    []string{"init"},
		Summary: "Link this repo to a project (or create one) and print the SDK setup",
		Usage:   "[--name <name> | --project <id|slug>] [--platform <platform>]",
		Details: []string{
			"  --name <name>          Create a project with this name",
			"  --project <id|slug>    Link an existing project",
			"  --platform <platform>  Override the detected platform",
			"",
			"Writes .checkend.json (the project link only, never a token; safe to commit).",
		},
		Options: map[string]Kind{"name": String, "project": String, "platform": String},
		Run: func(s *Session) error {
			h := s.Host
			root := config.FindRepoRoot(h.Cwd)
			if root == "" {
				root = h.Cwd
			}
			name := s.String("name")
			ref := s.String("project")
			if ref == "" && name == "" {
				link, err := s.Link()
				if err != nil {
					return err
				}
				if link != nil {
					ref = link.Project
				}
			}
			if name == "" && ref == "" && !h.Interactive {
				return clierr.UsageError("Give a project.", `Use --name "My App" to create a project or --project <id|slug> to link one.`)
			}
			client, err := s.API()
			if err != nil {
				return err
			}
			platform := s.String("platform")
			if platform == "" {
				platform = snippets.DetectPlatform(root)
			}

			var project map[string]any
			var dsn any
			created := false
			if ref != "" {
				if project, err = s.Project(client, ref); err != nil {
					return err
				}
				dsn = jv.Get(project, "dsns", 0)
			} else {
				choice, chosen, err := chooseProject(s, client, name, filepath.Base(root))
				if err != nil {
					return err
				}
				if chosen != nil {
					project, dsn = chosen, jv.Get(chosen, "dsns", 0)
				} else {
					if project, err = createProject(client, choice, platform); err != nil {
						return err
					}
					dsn, created = project["dsn"], true
				}
			}
			checked, err := snippets.CheckDSN(dsn)
			if err != nil {
				return err
			}
			file, err := writeLink(root, project)
			if err != nil {
				return err
			}
			var r initResult
			r.Project.ID, r.Project.Slug, r.Project.Name = project["id"], jv.Str(project["slug"]), jv.Str(project["name"])
			r.Created, r.DSN, r.File, r.Snippet = created, checked, file, snippets.Snippet(platform, checked)
			if platform != "" {
				r.Platform = platform
			}
			s.Out.Data(r, func(d any) string {
				verb := "Linked"
				if jv.Get(d, "created") == true {
					verb = "Created"
				}
				indented := regexp.MustCompile(`(?m)^`).ReplaceAllString(jv.Str(jv.Get(d, "snippet")), "    ")
				return strings.Join([]string{
					fmt.Sprintf("%s project %s (%s).", verb, jv.Str(jv.Get(d, "project", "slug")), jv.Str(jv.Get(d, "project", "id"))),
					fmt.Sprintf("Wrote %s (the project link only, no token; safe to commit).", jv.Str(jv.Get(d, "file"))),
					"",
					"DSN: " + jv.Str(jv.Get(d, "dsn")),
					"",
					"Set up the Sentry SDK in your app (Checkend is Sentry SDK compatible):",
					"",
					indented,
					"",
					"Next: run `ce status`. When errors arrive, run `ce next`.",
				}, "\n")
			})
			return nil
		},
	}
}

// createProject calls POST /v1/projects and returns the project (with "dsn").
func createProject(client *api.Client, name, platform string) (map[string]any, error) {
	raw, err := client.Post("/v1/projects", createBody(name, platform))
	if err != nil {
		return nil, err
	}
	data, _ := jv.Decode(raw)
	project, ok := jv.Get(data, "data").(map[string]any)
	if !ok {
		return nil, clierr.Fail("The server returned a project that cannot be read.")
	}
	return project, nil
}

// writeLink writes .checkend.json: the public ID and the slug, never a token.
func writeLink(root string, project map[string]any) (string, error) {
	file := filepath.Join(root, config.LinkFile)
	data, _ := json.MarshalIndent(map[string]string{"project": jv.Str(project["id"]), "slug": jv.Str(project["slug"])}, "", "  ")
	if err := os.WriteFile(file, append(data, '\n'), 0o644); err != nil {
		return "", clierr.Fail(fmt.Sprintf("Cannot write %s: %v", file, err))
	}
	return file, nil
}

// chooseProject returns the name for a new project, or a chosen existing project. With
// --name it does not ask.
func chooseProject(s *Session, client *api.Client, name, defaultName string) (string, map[string]any, error) {
	if name != "" {
		return name, nil, nil
	}
	projects, err := Projects(client)
	if err != nil {
		return "", nil, err
	}
	var lines []string
	if len(projects) > 0 {
		lines = append(lines, "Projects:")
	}
	for i, p := range projects {
		lines = append(lines, fmt.Sprintf("  %d) %s (%s)", i+1, jv.Str(p["slug"]), jv.Str(p["id"])))
	}
	lines = append(lines, fmt.Sprintf("  n) Create a new project %q", defaultName))
	s.Out.Menu(lines)
	answer, err := s.Host.Prompt("Choose a number, or press Enter for n: ", false)
	if err != nil {
		return "", nil, err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" || strings.EqualFold(answer, "n") {
		return defaultName, nil, nil
	}
	if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(projects) {
		return "", projects[n-1], nil
	}
	return "", nil, clierr.UsageError(fmt.Sprintf("%q is not a choice.", answer), "Run `ce init` again.")
}
