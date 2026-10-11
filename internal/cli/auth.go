package cli

import (
	"fmt"
	"strings"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/config"
	"github.com/Checkend/checkend-cli/internal/docs"
	"github.com/Checkend/checkend-cli/internal/jv"
	"github.com/Checkend/checkend-cli/internal/output"
	"github.com/Checkend/checkend-cli/internal/urlcheck"
)

func authCommands() []*Command {
	return []*Command{loginCommand(), logoutCommand(), statusCommand()}
}

func loginCommand() *Command {
	return &Command{
		Path:    []string{"login"},
		Summary: "Log in to a Checkend instance and save the token",
		Usage:   "[--url <url>] [--with-token]",
		Details: []string{
			"  --url <url>      The instance, for example https://checkend.example.com",
			"  --with-token     Read the token from stdin (for scripts and CI)",
			"",
			"The token goes to the OS keychain. Without a keychain, it goes to the config file (mode 600).",
		},
		Options: map[string]Kind{"with-token": Bool},
		Run: func(s *Session) error {
			h := s.Host
			input := s.String("url")
			if input == "" {
				input = h.Getenv("CHECKEND_URL")
			}
			if input == "" && h.Interactive {
				var err error
				if input, err = h.Prompt("Checkend URL: ", false); err != nil {
					return err
				}
			}
			if input == "" {
				return clierr.UsageError("Give the URL of your Checkend instance.", "Run `ce login --url <url>`.")
			}
			url, err := urlcheck.Normalize(input)
			if err != nil {
				return err
			}
			token := s.String("token")
			if s.Bool("with-token") {
				if token, err = h.ReadStdin(); err != nil {
					return err
				}
			} else if token == "" && h.Interactive {
				if token, err = h.Prompt("API token: ", true); err != nil {
					return err
				}
			}
			token = strings.TrimSpace(token)
			if token == "" {
				return clierr.UsageError("Give the API token.",
					fmt.Sprintf(`Pipe it on stdin: echo "$CHECKEND_TOKEN" | ce login --url %s --with-token`, url))
			}
			s.Out.Hide(token)

			client := api.New(h, url, token)
			health, _, err := client.Health()
			if err != nil {
				return err
			}
			if health.Status != "ok" {
				return clierr.Fail(fmt.Sprintf("The Checkend instance at %s is not healthy (%s).", url, health.Status))
			}
			if _, err := client.Get("/v1/projects", nil); err != nil {
				if clierr.CodeOf(err) == clierr.Auth {
					return clierr.New(clierr.Auth, fmt.Sprintf("%s rejected the token.", url), "Use an API token or the ADMIN_TOKEN secret of your Checkend instance.")
				}
				return err
			}
			where, err := config.SaveToken(h, url, token)
			if err != nil {
				return err
			}
			if where != config.StorageKeychain {
				s.Out.Notice(fmt.Sprintf("The OS keychain is not available, so the token is in %s (mode 600).", where))
			}
			result := struct {
				URL     string `json:"url"`
				Version any    `json:"version"`
				Storage string `json:"storage"`
			}{url, health.Version, where}
			if _, ok := health.Version.(string); !ok {
				result.Version = nil
			}
			s.Out.Data(result, func(d any) string {
				return fmt.Sprintf("Logged in to %s (Checkend %s).", jv.Str(jv.Get(d, "url")), jv.Or(jv.Get(d, "version"), "?"))
			})
			return nil
		},
	}
}

func logoutCommand() *Command {
	return &Command{
		Path:    []string{"logout"},
		Summary: "Remove the saved token of the current instance",
		Usage:   "[--url <url>]",
		Run: func(s *Session) error {
			cfg, err := config.ReadUserConfig(s.Host)
			if err != nil {
				return err
			}
			input := s.String("url")
			if input == "" {
				input = cfg.Current
			}
			url := ""
			if input != "" {
				if url, err = urlcheck.Normalize(input); err != nil {
					return err
				}
			}
			removed := false
			if url != "" {
				if removed, err = config.DeleteToken(s.Host, url); err != nil {
					return err
				}
			}
			if !removed {
				var u any
				if url != "" {
					u = url
				}
				s.Out.Data(map[string]any{"url": u, "logged_out": false}, func(any) string { return "Not logged in." })
				return nil
			}
			s.Out.Data(map[string]any{"url": url, "logged_out": true}, func(d any) string {
				return fmt.Sprintf("Logged out of %s.", jv.Str(jv.Get(d, "url")))
			})
			return nil
		},
	}
}

type statusResult struct {
	URL           string `json:"url"`
	Authenticated bool   `json:"authenticated"`
	Health        any    `json:"health"`
	Project       any    `json:"project"`
}

func statusCommand() *Command {
	return &Command{
		Path:    []string{"status"},
		Summary: "Show the instance, its health and the linked project",
		Usage:   "[--project <id|slug>]",
		Options: map[string]Kind{"project": String, "server": Bool},
		Run: func(s *Session) error {
			if s.Bool("server") {
				s.Out.Notice("`ce status --server` is now `ce server status`. The old name goes away in the next release.")
				return withDocs(serverStatus(s), docs.CheckInstall)
			}
			url, err := s.URL()
			if err != nil {
				return err
			}
			token, err := s.Token(url)
			if err != nil {
				return err
			}
			client := api.New(s.Host, url, token)
			health, rawHealth, err := client.Health()
			if err != nil {
				return err
			}
			var projects []map[string]any
			authenticated := false
			if token != "" {
				projects, err = Projects(client)
				if err != nil && clierr.CodeOf(err) != clierr.Auth {
					return err
				}
				authenticated = err == nil
			}
			ref, err := s.ProjectRef()
			if err != nil {
				return err
			}
			var project any
			if ref != "" && authenticated {
				project = map[string]any{"link": ref, "found": false}
				for _, p := range projects {
					if jv.Str(p["id"]) == ref || strings.EqualFold(jv.Str(p["slug"]), ref) {
						project = map[string]any{"id": p["id"], "slug": p["slug"], "name": p["name"]}
						break
					}
				}
			}
			result := statusResult{URL: url, Authenticated: authenticated, Health: rawHealth, Project: project}
			s.Out.Data(result, func(d any) string { return renderStatus(s.Out, d, ref) })
			if health.Status != "ok" {
				return Exit(clierr.Failure)
			}
			if !authenticated {
				if token != "" {
					s.Out.Note(fmt.Sprintf("The token was rejected. Run `ce login --url %s`.", url))
				} else {
					s.Out.Note(fmt.Sprintf("Not logged in to %s. Run `ce login --url %s`.", url, url))
				}
				return Exit(clierr.Auth)
			}
			return nil
		},
	}
}

func renderStatus(out *output.Output, d any, ref string) string {
	health := jv.Get(d, "health")
	project := jv.Get(d, "project")
	mode := jv.Or(jv.Get(health, "ingest", "mode"), "-")
	linked := "none (run `ce init`)"
	switch {
	case jv.Has(project, "slug"):
		linked = fmt.Sprintf("%s (%s)", jv.Str(jv.Get(project, "slug")), jv.Str(jv.Get(project, "id")))
	case project != nil:
		linked = out.Red(jv.Str(jv.Get(project, "link")) + " (not found)")
	case ref != "":
		linked = ref + " (log in to check it)"
	}
	healthText := out.Green("ok")
	if status := jv.Str(jv.Get(health, "status")); status != "ok" {
		healthText = out.Red(jv.Or(status, "-"))
	}
	if mode != "full" {
		mode = out.Yellow(mode)
	}
	loggedIn := "yes"
	if jv.Get(d, "authenticated") != true {
		loggedIn = out.Red("no")
	}
	return out.Fields([]output.Row{
		{Label: "Instance", Value: jv.Get(d, "url")},
		{Label: "Version", Value: jv.Or(jv.Get(health, "version"), "-")},
		{Label: "Health", Value: healthText},
		{Label: "Ingest", Value: mode},
		{Label: "Logged in", Value: loggedIn},
		{Label: "Project", Value: linked},
	})
}
