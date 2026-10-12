package cli

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/docs"
	"github.com/Checkend/checkend-cli/internal/jv"
)

// appRef is a public app ID or a slug.
var appRef = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// tokenScopes are the scopes of POST /v1/tokens.
var tokenScopes = []string{"issues:read", "issues:write", "releases:write", "admin"}

func tokenCommands() []*Command {
	return []*Command{tokenCreateCommand(), tokenListCommand(), tokenRevokeCommand()}
}

// forbidden is a 403 of the API: the token has no scope for the request.
func forbidden(err error) bool {
	var cerr *clierr.Error
	return errors.As(err, &cerr) && cerr.Docs == docs.Scopes
}

// tokenAdminError explains who can make or see tokens: the admin scope, or (on a hosted space,
// which has a base path) an owner in the web UI.
func tokenAdminError(s *Session, base string) error {
	if u, err := url.Parse(base); err == nil && u.Path != "" {
		return clierr.New(clierr.Auth, "On a hosted space, only an owner can make tokens, and only in the web UI.",
			"Ask an owner of the space to make one in Settings → Tokens, then set it as CHECKEND_TOKEN.").WithDocs(docs.Tokens)
	}
	return clierr.New(clierr.Auth, "Tokens need a token with the admin scope (or the ADMIN_TOKEN of the server).",
		"Run `ce login` with an admin token, or make the token in the web UI: Settings → Tokens.").WithDocs(docs.Tokens)
}

func tokenCreateCommand() *Command {
	return &Command{
		Path:    []string{"token", "create"},
		Summary: "Create an API token and print it once (to a pipe, for example gh secret set)",
		Usage:   "--scope <scope>... [--project <id|slug>] [--name <name>] [--show]",
		Details: []string{
			"  --scope <scope>   issues:read, issues:write, releases:write or admin (more than one is OK)",
			"  --project <app>   Only for this app: it writes deploys and source maps only there",
			"  --name <name>     A name to find the token later (default: ce token create)",
			"  --show            Print the token also to a terminal",
			"",
			"Only the token goes to stdout, and only once: Checkend cannot show it again. Messages go",
			"to stderr. With --json, stdout is {\"token\": …}. For example, for CI:",
			"  ce token create --scope releases:write --name github-deploy | gh secret set CHECKEND_TOKEN",
			"Needs a token with the admin scope. On a hosted space, an owner makes tokens in the web UI.",
		},
		Options: map[string]Kind{"scope": List, "name": String, "show": Bool, "project": String},
		Run: func(s *Session) error {
			var scopes []string
			for _, sc := range s.List("scope") {
				if !slices.Contains(tokenScopes, sc) {
					return clierr.UsageError(fmt.Sprintf("Unknown scope %q.", sc), "Use: "+strings.Join(tokenScopes, ", ")+".")
				}
				if !slices.Contains(scopes, sc) {
					scopes = append(scopes, sc)
				}
			}
			if len(scopes) == 0 {
				return clierr.UsageError("Give at least one --scope.", "For a deploy in CI: --scope releases:write. Scopes: "+strings.Join(tokenScopes, ", ")+".")
			}
			name := strings.TrimSpace(s.String("name"))
			if name == "" {
				name = "ce token create"
			}
			if len(name) > 100 {
				return clierr.UsageError("A token name has at most 100 characters.")
			}
			project := strings.ToLower(strings.TrimSpace(s.String("project")))
			if s.Has("project") && !appRef.MatchString(project) {
				return clierr.UsageError(fmt.Sprintf("%q is not an app ID or slug.", s.String("project")), "Run `ce project list` to see the apps.")
			}
			if s.Host.Stdout.IsTTY && !s.Bool("show") {
				return clierr.UsageError("ce token create does not print a token to a terminal, so that it does not stay in your scrollback.",
					"Pipe it, for example: ce token create --scope releases:write | gh secret set CHECKEND_TOKEN. Or add --show.")
			}
			client, err := s.API()
			if err != nil {
				return err
			}
			request := map[string]any{"name": name, "scopes": scopes}
			if project != "" {
				request["project"] = project
			}
			raw, err := client.Post("/v1/tokens", request)
			if forbidden(err) {
				return tokenAdminError(s, client.URL)
			}
			if raw, err = api.OrNotFound(raw, err, fmt.Sprintf("App %q not found, so no token was made.", project), "Run `ce project list` to see the apps."); err != nil {
				return err
			}
			body, _ := jv.Decode(raw)
			data := jv.Get(body, "data")
			token := jv.Str(jv.Get(data, "token"))
			if !strings.HasPrefix(token, "ck_") {
				return clierr.Fail("The server did not return a token.")
			}
			app := ""
			if n := jv.Str(jv.Get(data, "project_name")); n != "" {
				app = " for the app " + n
			}
			s.Out.Notice(fmt.Sprintf("Created the token %s%s (%s, prefix %s). It is shown only this once.",
				jv.Str(jv.Get(data, "name")), app, strings.Join(scopes, ", "), jv.Str(jv.Get(data, "prefix"))))
			if s.Bool("json") {
				s.Out.Data(map[string]any{"token": token, "id": jv.Get(data, "id"), "name": jv.Get(data, "name"),
					"scopes": jv.Get(data, "scopes"), "prefix": jv.Get(data, "prefix")}, func(any) string { return token })
				return nil
			}
			// The token alone, also when stdout is not a terminal: it goes into a pipe.
			s.Out.Write(token + "\n")
			return nil
		},
	}
}

func tokenListCommand() *Command {
	return &Command{
		Path:    []string{"token", "list"},
		Summary: "List the API tokens (never the tokens themselves)",
		Run: func(s *Session) error {
			client, err := s.API()
			if err != nil {
				return err
			}
			raw, err := client.Get("/v1/tokens", nil)
			if forbidden(err) {
				return tokenAdminError(s, client.URL)
			}
			if err != nil {
				return err
			}
			s.Out.Data(raw, func(d any) string {
				var rows [][]any
				for _, t := range jv.List(jv.Get(d, "data")) {
					var scopes []string
					for _, sc := range jv.List(jv.Get(t, "scopes")) {
						scopes = append(scopes, jv.Str(sc))
					}
					state := "active"
					if r := jv.Str(jv.Get(t, "revoked_at")); r != "" {
						state = "revoked"
					}
					rows = append(rows, []any{jv.Str(jv.Get(t, "id")), jv.Str(jv.Get(t, "name")), strings.Join(scopes, " "),
						jv.Str(jv.Get(t, "prefix")), jv.Str(jv.Get(t, "project_name")), jv.Str(jv.Get(t, "last_used_at")), state})
				}
				if len(rows) == 0 {
					return "No tokens."
				}
				return s.Out.Table([]string{"ID", "NAME", "SCOPES", "PREFIX", "APP", "LAST USED", "STATE"}, rows)
			})
			return nil
		},
	}
}

func tokenRevokeCommand() *Command {
	return &Command{
		Path:    []string{"token", "revoke"},
		Summary: "Revoke an API token (asks first; --yes in CI)",
		Usage:   "<id> [--yes]",
		Args:    []string{"id"},
		Options: map[string]Kind{"yes": Bool},
		Run: func(s *Session) error {
			id, err := strconv.ParseInt(s.Args[0], 10, 64)
			if err != nil || id < 1 {
				return clierr.UsageError(fmt.Sprintf("%q is not a token ID.", s.Args[0]), "Run `ce token list` to see the IDs.")
			}
			if err := needYes(s, "ce token revoke "+s.Args[0]); err != nil {
				return err
			}
			if err := confirm(s, []string{fmt.Sprintf("Token %d stops working at once. This cannot be undone.", id)},
				fmt.Sprintf("Type %d to confirm: ", id), strconv.FormatInt(id, 10)); err != nil {
				return err
			}
			client, err := s.API()
			if err != nil {
				return err
			}
			_, err = client.Delete(fmt.Sprintf("/v1/tokens/%d", id))
			if forbidden(err) {
				return tokenAdminError(s, client.URL)
			}
			if _, err := api.OrNotFound(nil, err, fmt.Sprintf("Token %d not found, or it is revoked already.", id), "Run `ce token list` to see the tokens."); err != nil {
				return err
			}
			s.Out.Data(map[string]any{"id": id, "revoked": true}, func(any) string { return fmt.Sprintf("Revoked token %d.", id) })
			return nil
		},
	}
}

// dryRun prints the request of `ce deploy` and records nothing. It asks the server which
// token it has (GET /v1/tokens/current), to check the scope and the app; an older server
// has no such endpoint, and then the dry run says so and goes on.
func dryRun(s *Session, client *api.Client, body map[string]any) error {
	result := map[string]any{
		"dry_run":       true,
		"request":       map[string]any{"method": "POST", "path": "/v1/deploys", "url": client.URL + "/v1/deploys", "body": body},
		"scope_checked": false,
	}
	raw, err := client.Get("/v1/tokens/current", nil)
	var nf *api.NotFound
	switch {
	case errors.As(err, &nf):
		result["note"] = "Nothing was sent. The scope check is not available on this server (it has no /v1/tokens/current): the token needs releases:write."
	case err != nil:
		return err
	default:
		decoded, _ := jv.Decode(raw)
		data := jv.Get(decoded, "data")
		name := jv.Str(jv.Get(data, "name"))
		var scopes []string
		for _, sc := range jv.List(jv.Get(data, "scopes")) {
			scopes = append(scopes, jv.Str(sc))
		}
		if !slices.Contains(scopes, "releases:write") && !slices.Contains(scopes, "admin") {
			return clierr.New(clierr.Auth, fmt.Sprintf("The token %s has no releases:write scope (it has %s), so the deploy would fail.", name, strings.Join(scopes, ", ")),
				"Make one: ce token create --scope releases:write --project <app> | gh secret set CHECKEND_TOKEN").WithDocs(docs.Tokens)
		}
		result["scope_checked"] = true
		result["token"] = map[string]any{"type": jv.Get(data, "type"), "name": name, "prefix": jv.Get(data, "prefix"),
			"scopes": scopes, "project_id": jv.Get(data, "project_id"), "project_name": jv.Get(data, "project_name")}
		note := fmt.Sprintf("Nothing was sent. The token %s has releases:write", name)
		if appID := jv.Str(jv.Get(data, "project_id")); appID != "" {
			app := fmt.Sprintf("%s (%s)", jv.Str(jv.Get(data, "project_name")), appID)
			ref := fmt.Sprint(body["project"])
			if _, numeric := strconv.ParseInt(ref, 10, 64); numeric == nil && ref != appID {
				return clierr.New(clierr.Auth, fmt.Sprintf("The token %s writes only for the app %s, not for the app %s, so the deploy would fail.", name, app, ref),
					"Use a token of this app: ce token create --scope releases:write --project "+ref).WithDocs(docs.Tokens)
			}
			note += fmt.Sprintf(" for the app %s only; check that the app of this deploy (%s) is that app", app, ref)
		}
		result["note"] = note + "."
	}
	s.Out.Data(result, func(d any) string {
		lines := []string{"Would send POST " + client.URL + "/v1/deploys:"}
		req := jv.Get(d, "request", "body")
		for _, k := range []string{"project", "commit", "release", "environment"} {
			if v := jv.Str(jv.Get(req, k)); v != "" {
				lines = append(lines, fmt.Sprintf("  %-12s %s", k+":", v))
			}
		}
		if n := len(jv.List(jv.Get(req, "commits"))); n > 0 {
			lines = append(lines, fmt.Sprintf("  %-12s %d", "commits:", n))
		}
		if n := len(jv.List(jv.Get(req, "pull_requests"))); n > 0 {
			lines = append(lines, fmt.Sprintf("  %-12s %d", "pull requests:", n))
		}
		return strings.Join(append(lines, "", jv.Str(jv.Get(d, "note"))), "\n")
	})
	return nil
}
