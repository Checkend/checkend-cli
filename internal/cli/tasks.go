package cli

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/config"
	"github.com/Checkend/checkend-cli/internal/jv"
)

// needYes checks a destructive command before any request: on a terminal it can ask, else it
// needs --yes.
func needYes(s *Session, command string) error {
	if s.Bool("yes") || s.Host.Interactive {
		return nil
	}
	return clierr.UsageError("This command changes data and needs a confirmation.", fmt.Sprintf("Run `%s --yes` to confirm.", command))
}

// confirm asks the user to type want, unless --yes was given.
func confirm(s *Session, warning []string, question, want string) error {
	if s.Bool("yes") {
		return nil
	}
	s.Out.Menu(warning)
	answer, err := s.Host.Prompt(question, false)
	if err != nil {
		return err
	}
	if strings.TrimSpace(answer) != want {
		return clierr.UsageError("The answer did not match, so nothing changed.", "Run the command again.")
	}
	return nil
}

// requireProject is the project from the argument, --project or .checkend.json, or exit 2.
func requireProject(s *Session) (string, error) {
	if len(s.Args) > 0 && s.Args[0] != "" {
		return s.Args[0], nil
	}
	ref, err := s.ProjectRef()
	if err != nil {
		return "", err
	}
	if ref == "" {
		return "", clierr.UsageError("No project given and no .checkend.json found.", "Run `ce init` to link this repo, or give --project.")
	}
	return ref, nil
}

func projectNotFound(raw []byte, err error, ref string) ([]byte, error) {
	return api.OrNotFound(raw, err, fmt.Sprintf("Project %q not found.", ref), "Run `ce project list` to see the projects.")
}

func deployCommand() *Command {
	return &Command{
		Path:    []string{"deploy"},
		Summary: "Report a deploy (in CI), so that Checkend can verify fixes",
		Usage:   "[--release <v>] [--environment <name>] [--commit <sha>] [--commits-since <sha>] [--pr <url>]... [--project <id|slug>] [--dry-run]",
		Details: []string{
			"  --release <version>    The release of the SDK (default: the commit)",
			"  --environment <name>   Default: production (--env is a short form)",
			"  --commit <sha>         Default: git HEAD",
			"  --commits-since <sha>  Add the commits since the last deploy (git rev-list <sha>..HEAD)",
			"  --pr <url>             A pull request in the deploy (more than one is OK)",
			"  --project <id|slug>    Default: the project in .checkend.json",
			"  --dry-run              Print what it would send; send and record nothing",
			"",
			"Needs a token with the releases:write scope, for example in CHECKEND_TOKEN",
			"(make one with `ce token create --scope releases:write`).",
		},
		Options: map[string]Kind{"release": String, "environment": String, "env": String, "commit": String,
			"commits-since": String, "pr": List, "project": String, "dry-run": Bool},
		Run: func(s *Session) error {
			ref, err := requireProject(s)
			if err != nil {
				return err
			}
			root := config.FindRepoRoot(s.Host.Cwd)
			if root == "" {
				root = s.Host.Cwd
			}
			commit := s.String("commit")
			if commit == "" {
				if commit, err = s.Host.Git(root, "rev-parse", "HEAD"); err != nil || commit == "" {
					return clierr.UsageError("Cannot read the commit from git.", "Give the commit with --commit <sha>.")
				}
			}
			body := map[string]any{"project": strings.ToLower(strings.TrimSpace(ref)), "commit": commit}
			if v := s.String("release"); v != "" {
				body["release"] = v
			}
			env := s.String("environment")
			if env == "" {
				env = s.String("env")
			}
			if env != "" {
				body["environment"] = env
			}
			if since := s.String("commits-since"); since != "" {
				out, err := s.Host.Git(root, "rev-list", "--max-count=1000", since+"..HEAD")
				if err != nil {
					return clierr.UsageError(fmt.Sprintf("Cannot list the commits since %s with git.", since), "Check the commit, or leave out --commits-since.")
				}
				body["commits"] = append([]string{}, strings.Fields(out)...)
			}
			if prs := s.List("pr"); len(prs) > 0 {
				body["pull_requests"] = prs
			}
			client, err := s.API()
			if err != nil {
				return err
			}
			if s.Bool("dry-run") {
				return dryRun(s, client.URL, body)
			}
			raw, err := client.Post("/v1/deploys", body)
			if raw, err = projectNotFound(raw, err, ref); err != nil {
				return err
			}
			s.Out.Data(raw, func(d any) string {
				data := jv.Get(d, "data")
				return fmt.Sprintf("Reported the deploy of %s to %s (commit %s).", jv.Str(jv.Get(data, "release")),
					jv.Str(jv.Get(data, "environment")), short(jv.Str(jv.Get(data, "commit"))))
			})
			return nil
		},
	}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func projectDeleteCommand() *Command {
	return &Command{
		Path:    []string{"project", "delete"},
		Summary: "Delete a project and all its data (asks for the name; --yes in CI)",
		Usage:   "<id|slug> [--yes]",
		Details: []string{
			"  <id|slug>  The project",
			"  --yes      Do not ask (for scripts). Needs an admin token.",
			"",
			"The DSN keys stop at once; the issues and events are purged. It cannot be undone.",
			"Exit code 6: the server accepted the delete, and it waits for an approval",
			"(REQUIRE_APPROVAL). Another person or admin completes it in Checkend.",
		},
		Options: map[string]Kind{"yes": Bool},
		Args:    []string{"project"},
		Run: func(s *Session) error {
			ref := s.Args[0]
			if err := needYes(s, "ce project delete "+ref); err != nil {
				return err
			}
			client, err := s.API()
			if err != nil {
				return err
			}
			project, err := s.Project(client, ref)
			if err != nil {
				return err
			}
			name, slug := jv.Str(project["name"]), jv.Str(project["slug"])
			if err := confirm(s, []string{
				fmt.Sprintf("Delete project %s (%s) and all its issues and events?", slug, jv.Str(project["id"])),
				"Its DSN keys stop at once. This cannot be undone.",
			}, fmt.Sprintf("Type the project name (%s) to confirm: ", name), name); err != nil {
				return err
			}
			raw, err := client.Delete("/v1/projects/" + jv.Str(project["id"]))
			if raw, err = projectNotFound(raw, err, ref); err != nil {
				return err
			}
			body, _ := jv.Decode(raw)
			if jv.Str(jv.Get(body, "status")) == "approval_required" {
				s.Out.Data(raw, func(d any) string {
					return fmt.Sprintf("The delete of %s needs approval: %s", slug, jv.Str(jv.Get(d, "message")))
				})
				return Exit(clierr.Pending)
			}
			result := map[string]any{"data": map[string]any{"id": project["id"], "slug": project["slug"], "deleted": true}}
			s.Out.Data(result, func(any) string {
				return fmt.Sprintf("Deleted %s. Remove the Checkend setup from the app code too.", slug)
			})
			return nil
		},
	}
}

func dsnRotateCommand() *Command {
	return &Command{
		Path:    []string{"dsn", "rotate"},
		Summary: "Make a new DSN key; with --revoke-old, revoke the old keys",
		Usage:   "[--project <id|slug>] [--revoke-old [--yes]]",
		Details: []string{
			"  --project <id|slug>  Default: the project in .checkend.json",
			"  --revoke-old         Revoke all keys except the newest (after the new DSN is deployed)",
			"  --yes                Do not ask (for scripts)",
		},
		Options: map[string]Kind{"project": String, "revoke-old": Bool, "yes": Bool},
		Run: func(s *Session) error {
			ref, err := requireProject(s)
			if err != nil {
				return err
			}
			path := "/v1/projects/" + url.PathEscape(strings.ToLower(strings.TrimSpace(ref))) + "/keys"
			if !s.Bool("revoke-old") {
				client, err := s.API()
				if err != nil {
					return err
				}
				raw, err := client.Post(path, map[string]any{})
				if raw, err = projectNotFound(raw, err, ref); err != nil {
					return err
				}
				s.Out.Data(raw, func(d any) string {
					return strings.Join([]string{
						"New DSN: " + jv.Str(jv.Get(d, "data", "dsn")),
						"The old DSN still works. Put the new DSN in the configuration of every deployment and deploy.",
						"Then run `ce dsn rotate --revoke-old` to revoke the old keys.",
					}, "\n")
				})
				return nil
			}
			if err := needYes(s, "ce dsn rotate --revoke-old"); err != nil {
				return err
			}
			client, err := s.API()
			if err != nil {
				return err
			}
			project, err := s.Project(client, ref)
			if err != nil {
				return err
			}
			var keys []string
			for _, d := range jv.List(project["dsns"]) {
				if u, err := url.Parse(jv.Str(d)); err == nil && u.User != nil {
					keys = append(keys, u.User.Username())
				}
			}
			if len(keys) < 2 {
				return clierr.New(clierr.Conflict, "The project has only one DSN key, so there is nothing to revoke.", "Run `ce dsn rotate` first to make a new key.")
			}
			old := keys[:len(keys)-1]
			if err := confirm(s, []string{
				fmt.Sprintf("Revoke %d old DSN key(s) of %s? Events sent with them get 401.", len(old), jv.Str(project["slug"])),
				"Keep the newest key: " + keys[len(keys)-1],
			}, "Type revoke to confirm: ", "revoke"); err != nil {
				return err
			}
			for _, key := range old {
				if _, err := client.Delete(path + "/" + url.PathEscape(key)); err != nil {
					return err
				}
			}
			result := map[string]any{"data": map[string]any{"project": project["id"], "revoked": old, "kept": keys[len(keys)-1]}}
			s.Out.Data(result, func(any) string {
				return fmt.Sprintf("Revoked %d old key(s). The newest key stays active.", len(old))
			})
			return nil
		},
	}
}
