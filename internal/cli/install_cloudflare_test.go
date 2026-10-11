package cli_test

import (
	"net/url"
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func cloudflare(t *testing.T) (*FakeReleases, *FakeCloudflare, *Harness, map[string]string) {
	f, cli := installer(t)
	f.Publish("0.3.0")
	cf := NewFakeCloudflare(t)
	cli.Cloudflare, cli.HTTP = cf, cf.Client()
	return f, cf, cli, map[string]string{"CLOUDFLARE_API_TOKEN": cf.Token}
}

const workerURL = "https://checkend.example.workers.dev"

func TestInstallCloudflareNeedsAnAPITokenFromTheTemplateLink(t *testing.T) {
	_, cf, cli, _ := cloudflare(t)
	r := cli.Run([]string{"install", "cloudflare", "--yes"})
	Code(t, r, 2)
	link := regexpFind(t, r.ErrJSON(t)["hint"].(string), `https://dash\.cloudflare\.com/profile/api-tokens\?\S+`)
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	Equal(t, u.Query().Get("permissionGroupKeys"), `[{"key":"workers_scripts","type":"edit"},{"key":"d1","type":"edit"},{"key":"account_settings","type":"read"}]`)
	Equal(t, u.Query().Get("name"), "Checkend install")
	Equal(t, len(cf.Calls), 0)

	// On a terminal it asks for the token (hidden) and keeps it in the keychain.
	r = cli.Run([]string{"install", "cloudflare"}, Options{TTY: true, Answers: []string{cf.Token, "y"}})
	Code(t, r, 0)
	Equal(t, cli.Prompts[0].Secret, true)
	Equal(t, cli.Keyring.Secrets["cloudflare:api-token"], cf.Token)
	NotContains(t, r.All, cf.Token)
	// The next run uses the token of the keychain.
	Code(t, cli.Run([]string{"install", "cloudflare", "--yes"}), 0)
}

func regexpFind(t *testing.T, text, pattern string) string {
	t.Helper()
	Matches(t, text, pattern)
	for _, f := range strings.Fields(text) {
		if strings.HasPrefix(f, "https://dash.cloudflare.com/") {
			return f
		}
	}
	return ""
}

func TestInstallCloudflareCreatesD1MigratesUploadsAndSetsSecrets(t *testing.T) {
	_, cf, cli, env := cloudflare(t)
	r := cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: env})
	Code(t, r, 0)
	db := cf.DB("checkend")
	if db == nil {
		t.Fatal("no D1 database")
	}
	Equal(t, db.Migrations, []string{"0001_init.sql", "0002_projects.sql"})
	Equal(t, db.Bookmarks, 1)
	// The bookmark comes before the first migration.
	bookmark, migrate := indexOf(cf.Calls, "GET /accounts/"+cf.Account()+"/d1/database/"+db.ID+"/time_travel/bookmark"), indexOf(cf.Calls, "POST /accounts/"+cf.Account()+"/d1/database/"+db.ID+"/query")
	if bookmark < 0 || migrate < 0 || bookmark > migrate {
		t.Errorf("bookmark %d, first query %d: %v", bookmark, migrate, cf.Calls)
	}
	Contains(t, strings.Join(db.SQL, "\n"), Migrations["migrations/0001_init.sql"])

	script := cf.Scripts["checkend"]
	Equal(t, script.Metadata["main_module"], "worker.js")
	Equal(t, script.Metadata["compatibility_date"], "2026-10-06")
	Equal(t, script.Types["worker.js"], "application/javascript+module")
	Contains(t, script.Modules["worker.js"], "v0.3.0")
	Equal(t, script.Metadata["bindings"], []any{
		J{"type": "d1", "name": "DB", "id": db.ID},
		J{"type": "plain_text", "name": "PUBLIC_URL", "text": workerURL},
	})
	Equal(t, script.Metadata["keep_bindings"], []any{"secret_text"})
	Equal(t, script.Schedules, []string{"*/5 * * * *"})
	Equal(t, script.WorkersDev, true)
	Matches(t, script.Secrets["ADMIN_TOKEN"], `^[0-9a-f]{64}$`)
	Matches(t, script.Secrets["ENCRYPTION_KEYS"], `^k1:`)
	Matches(t, script.Secrets["SETUP_CODE"], `^1791633600\.`)
	// The setup link has the code of SETUP_CODE; the other secrets are never printed.
	NotContains(t, r.All, script.Secrets["ADMIN_TOKEN"])
	NotContains(t, r.All, script.Secrets["ENCRYPTION_KEYS"])
	for _, c := range cli.Calls {
		t.Errorf("ran a program: %v", c)
	}

	out := r.JSON(t)
	Equal(t, out["url"], workerURL)
	Equal(t, out["setup_url"], workerURL+"/setup#"+strings.SplitN(script.Secrets["SETUP_CODE"], ".", 2)[1])
	Contains(t, r.Stderr, "Bookmark")
	Contains(t, r.Stderr, "wrangler d1 time-travel restore checkend --bookmark=00000001")
	Equal(t, cli.Keyring.Secrets[workerURL], script.Secrets["ADMIN_TOKEN"])
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

func TestInstallCloudflareAgainKeepsTheSecretsAndMigratesOnlyWhatIsNew(t *testing.T) {
	f, cf, cli, env := cloudflare(t)
	Code(t, cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: env}), 0)
	first := map[string]string{}
	for k, v := range cf.Scripts["checkend"].Secrets {
		first[k] = v
	}
	db := cf.DB("checkend")
	queries := len(db.SQL)
	f.Publish("0.4.0")
	cf.SetupOpen = false
	r := cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: env})
	Code(t, r, 0)
	second := cf.Scripts["checkend"].Secrets
	Equal(t, second["ADMIN_TOKEN"], first["ADMIN_TOKEN"])
	Equal(t, second["ENCRYPTION_KEYS"], first["ENCRYPTION_KEYS"])
	Equal(t, len(cf.Databases), 1)
	Equal(t, db.Migrations, []string{"0001_init.sql", "0002_projects.sql"})
	for _, sql := range db.SQL[queries:] {
		NotContains(t, sql, "CREATE TABLE settings")
	}
	Contains(t, cf.Scripts["checkend"].Modules["worker.js"], "v0.4.0")
	// Setup is done: no setup link.
	_, ok := r.JSON(t)["setup_url"]
	Equal(t, ok, false)
}

func TestInstallCloudflareRefusesAWorkerOrADatabaseThatIsNotCheckend(t *testing.T) {
	_, cf, cli, env := cloudflare(t)
	cf.Scripts["checkend"] = &FakeScript{Metadata: J{"bindings": []any{J{"type": "kv_namespace", "name": "CACHE"}}}, Secrets: map[string]string{}}
	r := cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: env})
	Code(t, r, 1)
	Contains(t, r.Stderr, "--name")
	Equal(t, cf.Scripts["checkend"].Metadata["bindings"].([]any)[0].(map[string]any)["name"], "CACHE")

	_, cf, cli, env = cloudflare(t)
	cf.Databases["db-other"] = &FakeD1{ID: "db-other", Name: "checkend", Tables: []string{"orders"}}
	r = cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: env})
	Code(t, r, 1)
	Contains(t, r.Stderr, "not a Checkend database")
	Equal(t, len(cf.Databases["db-other"].SQL) <= 2, true)
	Equal(t, len(cf.Scripts), 0)

	_, cf, cli, env = cloudflare(t)
	cf.Databases["db-new"] = &FakeD1{ID: "db-new", Name: "checkend", Tables: []string{"d1_migrations"}, Migrations: []string{"0001_init.sql", "0002_projects.sql", "0003_future.sql"}}
	r = cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: env})
	Code(t, r, 1)
	Contains(t, r.Stderr, "0003_future.sql")
	Equal(t, len(cf.Scripts), 0)
}

func TestInstallCloudflareStopsWithoutABookmark(t *testing.T) {
	_, cf, cli, env := cloudflare(t)
	cf.Fail = func(call string) bool { return strings.HasSuffix(call, "/time_travel/bookmark") }
	r := cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: env})
	Code(t, r, 1)
	Contains(t, r.Stderr, "bookmark")
	Equal(t, len(cf.DB("checkend").Migrations), 0)
	Equal(t, len(cf.Scripts), 0)
}

func TestInstallCloudflareChecksItsInputAndTheAccount(t *testing.T) {
	_, cf, cli, env := cloudflare(t)
	for _, argv := range [][]string{
		{"install", "cloudflare", "--yes", "--name", "Bad_Name"},
		{"install", "cloudflare", "--yes", "--account", "x;y"},
		{"install", "cloudflare", "--yes", "--bogus"},
	} {
		Code(t, cli.Run(argv, Options{Env: env}), 2)
	}
	Equal(t, len(cf.Calls), 0)
	r := cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: map[string]string{"CLOUDFLARE_API_TOKEN": "cf-test-wrong-token-0123456789"}})
	Code(t, r, 5)
	Contains(t, r.Stderr, "token")
	cf.Accounts = append(cf.Accounts, J{"id": "acc2", "name": "Second"})
	r = cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: env})
	Code(t, r, 2)
	Contains(t, r.Stderr, "--account")
	Contains(t, r.Stderr, "Second")
	cf.Subdomain = ""
	r = cli.Run([]string{"install", "cloudflare", "--yes", "--account", cf.Account()}, Options{Env: env})
	Code(t, r, 1)
	Contains(t, r.Stderr, "workers.dev")
}

func TestInstallCloudflareWithR2ForBlobs(t *testing.T) {
	_, cf, cli, env := cloudflare(t)
	r := cli.Run([]string{"install", "cloudflare", "--yes", "--r2"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, cf.Buckets["checkend-blobs"], true)
	Contains(t, strings.Join(toJSONStrings(cf.Scripts["checkend"].Metadata["bindings"]), "\n"), `"bucket_name":"checkend-blobs"`)
	r = cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: env})
	Code(t, r, 0)
	Contains(t, r.Stderr, "BLOBS")
}

func toJSONStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, jsonString(x))
	}
	return out
}
