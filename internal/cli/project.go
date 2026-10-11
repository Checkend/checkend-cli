package cli

import (
	"fmt"
	"strings"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/jv"
)

func projectCommands() []*Command {
	return []*Command{projectListCommand(), projectCreateCommand(), projectDSNCommand()}
}

func projectListCommand() *Command {
	return &Command{
		Path:    []string{"project", "list"},
		Summary: "List the projects",
		Run: func(s *Session) error {
			client, err := s.API()
			if err != nil {
				return err
			}
			raw, err := client.Get("/v1/projects", nil)
			if err != nil {
				return err
			}
			s.Out.Data(raw, func(d any) string {
				data := jv.List(jv.Get(d, "data"))
				if len(data) == 0 {
					return "No projects yet. Run `ce project create --name \"My App\"` or `ce init`."
				}
				var rows [][]any
				for _, p := range data {
					rows = append(rows, []any{jv.Get(p, "id"), jv.Get(p, "slug"), jv.Get(p, "name"), jv.Or(jv.Get(p, "platform"), "-")})
				}
				return s.Out.Table([]string{"ID", "SLUG", "NAME", "PLATFORM"}, rows)
			})
			return nil
		},
	}
}

func createBody(name, platform string) map[string]string {
	body := map[string]string{"name": name}
	if platform != "" {
		body["platform"] = platform
	}
	return body
}

func projectCreateCommand() *Command {
	return &Command{
		Path:    []string{"project", "create"},
		Summary: "Create a project and print its DSN",
		Usage:   "--name <name> [--platform <platform>]",
		Details: []string{
			"  --name <name>          1 to 100 characters",
			"  --platform <platform>  For example node, javascript, python, ruby, go, php",
		},
		Options: map[string]Kind{"name": String, "platform": String},
		Run: func(s *Session) error {
			if !s.Has("name") {
				return clierr.UsageError("Missing --name.", `Example: ce project create --name "My App"`)
			}
			client, err := s.API()
			if err != nil {
				return err
			}
			raw, err := client.Post("/v1/projects", createBody(s.String("name"), s.String("platform")))
			if err != nil {
				return err
			}
			s.Out.Data(raw, func(d any) string {
				p := jv.Get(d, "data")
				return strings.Join([]string{
					fmt.Sprintf("Created project %s (%s).", jv.Str(jv.Get(p, "slug")), jv.Str(jv.Get(p, "id"))),
					"DSN: " + jv.Str(jv.Get(p, "dsn")),
					"Run `ce init` in the repo of the app to link it and get the SDK snippet.",
				}, "\n")
			})
			return nil
		},
	}
}

func projectDSNCommand() *Command {
	return &Command{
		Path:    []string{"project", "dsn"},
		Summary: "Print the DSN of a project (default: the linked project)",
		Usage:   "[<id|slug>] [--project <id|slug>]",
		Options: map[string]Kind{"project": String},
		Args:    []string{"[project]"},
		Run: func(s *Session) error {
			var ref string
			if len(s.Args) > 0 {
				ref = s.Args[0]
			} else {
				var err error
				if ref, err = s.ProjectRef(); err != nil {
					return err
				}
			}
			if ref == "" {
				return clierr.UsageError("No project given and no .checkend.json found.", "Run `ce init` to link this repo, or give a project.")
			}
			client, err := s.API()
			if err != nil {
				return err
			}
			project, err := s.Project(client, ref)
			if err != nil {
				return err
			}
			dsns := jv.List(project["dsns"])
			if dsns == nil {
				dsns = []any{}
			}
			body := map[string]any{"data": map[string]any{"id": project["id"], "slug": project["slug"], "dsns": dsns}}
			s.Out.Data(body, func(d any) string {
				var lines []string
				for _, x := range jv.List(jv.Get(d, "data", "dsns")) {
					lines = append(lines, jv.Str(x))
				}
				if len(lines) == 0 {
					return fmt.Sprintf("Project %s has no DSN.", jv.Str(jv.Get(d, "data", "slug")))
				}
				return strings.Join(lines, "\n")
			})
			return nil
		},
	}
}
