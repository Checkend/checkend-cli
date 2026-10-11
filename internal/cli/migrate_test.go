package cli_test

import (
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

const sentryDSN = "https://0123456789abcdef0123456789abcdef@o1.ingest.sentry.io/456"

func sentryRepo(cli *Harness) {
	cli.Write("package.json", `{"dependencies":{"@sentry/node":"9","express":"5"}}`)
	cli.Write(".env", "PORT=3000\nSENTRY_DSN="+sentryDSN+"\n")
	cli.Write("src/app.ts", "import * as Sentry from \"@sentry/node\";\nSentry.init({ dsn: process.env.SENTRY_DSN });\n")
	cli.Write("node_modules/x/index.js", "SENTRY_DSN")
}

func TestMigrateFromSentryFindsTheSetupAndRegistersTheApp(t *testing.T) {
	api, cli, env := setup(t)
	sentryRepo(cli)
	r := cli.Run([]string{"migrate-from-sentry"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, api.Requests()[0].Method+" "+api.Requests()[0].Path, "POST /v1/projects")
	Equal(t, api.Requests()[0].Body, J{"name": "repo", "platform": "node-express"})
	out := r.JSON(t)
	p := api.Projects[0]
	Equal(t, out["dsn"], dsn(p))
	Equal(t, out["created"], true)
	Equal(t, out["sentry"], J{
		"sdks": []any{"@sentry/node"},
		"references": []any{
			J{"file": ".env", "line": 2, "variable": "SENTRY_DSN", "host": "o1.ingest.sentry.io"},
			J{"file": "src/app.ts", "line": 2, "variable": "SENTRY_DSN"},
		},
	})
	Contains(t, out["steps"].(string), "Replace the Sentry DSN")
	NotContains(t, r.All, "0123456789abcdef0123456789abcdef")
	Equal(t, readLink(t, cli)["project"], id(p))
}

func TestMigrateFromSentryOnATerminal(t *testing.T) {
	api, cli, env := setup(t)
	sentryRepo(cli)
	r := cli.Run([]string{"migrate-from-sentry"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, "Sentry SDK: @sentry/node (keep it: Checkend is Sentry SDK compatible)")
	Contains(t, r.Stdout, ".env:2  SENTRY_DSN (o1.ingest.sentry.io)")
	Contains(t, r.Stdout, "New DSN: "+dsn(api.Projects[0]))
	NotContains(t, r.All, "0123456789abcdef0123456789abcdef")
}

func TestMigrateFromSentryUsesTheLinkedProject(t *testing.T) {
	api, cli, env := setup(t)
	p := api.AddProject("App")
	sentryRepo(cli)
	cli.Write(".checkend.json", `{"project":"app"}`)
	r := cli.Run([]string{"migrate-from-sentry"}, Options{Env: env})
	Code(t, r, 0)
	for _, req := range api.Requests() {
		if req.Method == "POST" {
			t.Error("created a project")
		}
	}
	Equal(t, r.JSON(t)["dsn"], dsn(p))
}

func TestMigrateFromSentryWithASetupKey(t *testing.T) {
	api, cli, _ := setup(t)
	sentryRepo(cli)
	r := cli.Run([]string{"migrate-from-sentry", "--url", api.URL, "--key", api.SetupKey})
	Code(t, r, 0)
	Equal(t, api.Requests()[0].Path, "/v1/setup/apps")
	NotContains(t, r.All, api.SetupKey)
}

func TestMigrateFromSentryWithoutSentry(t *testing.T) {
	_, cli, env := setup(t)
	r := cli.Run([]string{"migrate-from-sentry", "--name", "Plain"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, "No Sentry SDK or DSN found")
}
