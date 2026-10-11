package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/config"
	"github.com/Checkend/checkend-cli/internal/jv"
	"github.com/Checkend/checkend-cli/internal/snippets"
)

type migrateResult struct {
	Project any    `json:"project"`
	Created bool   `json:"created"`
	DSN     string `json:"dsn"`
	Stack   any    `json:"stack"`
	File    string `json:"file"`
	Sentry  struct {
		SDKs       []string             `json:"sdks"`
		References []snippets.SentryRef `json:"references"`
	} `json:"sentry"`
	StepsURL string `json:"steps_url"`
	Steps    string `json:"steps"`
}

func migrateCommand() *Command {
	return &Command{
		Path:    []string{"migrate-from-sentry"},
		Summary: "Find the Sentry setup of this repo and get a Checkend DSN for it",
		Usage:   "[--name <name>] [--platform <platform>] [--key <cks_…>]",
		Details: []string{
			"  --name <name>          The app name (default: the repo directory)",
			"  --platform <platform>  Override the detected stack",
			"  --key <cks_…>          Register the app with a setup key (or CHECKEND_SETUP_KEY)",
			"",
			"Lists the Sentry SDKs and the files that name a Sentry DSN, registers the app (or uses",
			"the project in .checkend.json), and prints the new DSN and the steps. It changes no",
			"code: the agent replaces the DSN. The old DSN is never printed, only its host.",
		},
		Options: map[string]Kind{"name": String, "platform": String, "key": String},
		Run: func(s *Session) error {
			h := s.Host
			key, err := setupKey(s)
			if err != nil {
				return err
			}
			root := config.FindRepoRoot(h.Cwd)
			if root == "" {
				root = h.Cwd
			}
			stack := snippets.DetectStack(root)
			platform := s.String("platform")
			if platform == "" {
				platform = stack.Platform
			}
			name := s.String("name")
			if name == "" {
				name = filepath.Base(root)
			}
			var r migrateResult
			r.Stack = stack
			r.Sentry.SDKs, r.Sentry.References = snippets.ScanSentry(root)
			if r.Sentry.SDKs == nil {
				r.Sentry.SDKs = []string{}
			}
			if r.Sentry.References == nil {
				r.Sentry.References = []snippets.SentryRef{}
			}

			var client *api.Client
			var project map[string]any
			var dsn any
			link, err := s.Link()
			if err != nil {
				return err
			}
			switch {
			case key != "":
				url, err := s.URL()
				if err != nil {
					return err
				}
				client = api.New(h, url, key)
				raw, err := client.Post("/v1/setup/apps", createBody(name, platform))
				if err != nil {
					return keyError(err)
				}
				body, _ := jv.Decode(raw)
				project, _ = jv.Get(body, "data", "project").(map[string]any)
				dsn, r.Created = jv.Get(body, "data", "dsn"), jv.Get(body, "data", "created") == true
			case link != nil:
				if client, err = s.API(); err != nil {
					return err
				}
				if project, err = s.Project(client, link.Project); err != nil {
					return err
				}
				dsn = jv.Get(project, "dsns", 0)
			default:
				if client, err = s.API(); err != nil {
					return err
				}
				if project, err = createProject(client, name, platform); err != nil {
					return err
				}
				dsn, r.Created = project["dsn"], true
			}
			if project == nil {
				return clierr.Fail("The server returned an app that cannot be read.")
			}
			if r.DSN, err = snippets.CheckDSN(dsn); err != nil {
				return err
			}
			if r.File, err = writeLink(root, project); err != nil {
				return err
			}
			r.Project = map[string]any{"id": project["id"], "slug": project["slug"], "name": project["name"]}
			r.StepsURL = client.URL + "/agent/tasks/migrate-from-sentry.md"
			if steps, err := client.Text("/agent/tasks/migrate-from-sentry.md"); err == nil {
				r.Steps = steps
			}
			s.Out.Data(r, renderMigrate)
			return nil
		},
	}
}

func renderMigrate(d any) string {
	project := jv.Get(d, "project")
	verb := "Linked"
	if jv.Get(d, "created") == true {
		verb = "Created"
	}
	lines := []string{fmt.Sprintf("%s app %s (%s).", verb, jv.Str(jv.Get(project, "slug")), jv.Str(jv.Get(project, "id"))), ""}
	sdks := jv.List(jv.Get(d, "sentry", "sdks"))
	refs := jv.List(jv.Get(d, "sentry", "references"))
	if len(sdks) == 0 && len(refs) == 0 {
		lines = append(lines, "No Sentry SDK or DSN found in this repo. Run `ce setup` steps to add the SDK.")
	}
	for _, sdk := range sdks {
		lines = append(lines, "Sentry SDK: "+jv.Str(sdk)+" (keep it: Checkend is Sentry SDK compatible)")
	}
	if len(refs) > 0 {
		lines = append(lines, "", "Replace the Sentry DSN in these places:")
		for _, ref := range refs {
			what := jv.Str(jv.Get(ref, "variable"))
			if host := jv.Str(jv.Get(ref, "host")); host != "" {
				what = strings.TrimSpace(what + " (" + host + ")")
			}
			lines = append(lines, fmt.Sprintf("  %s:%s  %s", jv.Str(jv.Get(ref, "file")), jv.Str(jv.Get(ref, "line")), what))
		}
	}
	lines = append(lines, "", "New DSN: "+jv.Str(jv.Get(d, "dsn")), "")
	if steps := strings.TrimSpace(jv.Str(jv.Get(d, "steps"))); steps != "" {
		lines = append(lines, "Steps (from "+jv.Str(jv.Get(d, "steps_url"))+"):", "", steps)
	} else {
		lines = append(lines, "Read the steps at "+jv.Str(jv.Get(d, "steps_url"))+".")
	}
	return strings.Join(lines, "\n")
}
