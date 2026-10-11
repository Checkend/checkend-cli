package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/config"
	"github.com/Checkend/checkend-cli/internal/credstore"
	"github.com/Checkend/checkend-cli/internal/sanitize"
	"github.com/Checkend/checkend-cli/internal/serverrelease"
	"github.com/Checkend/checkend-cli/internal/version"
)

const (
	cloudflareAPI = "https://api.cloudflare.com/client/v4"
	// cloudflareKeychain is the keychain account of the Cloudflare API token.
	cloudflareKeychain = "cloudflare:api-token"
	r2Bucket           = "checkend-blobs"
)

// tokenPermissions are the permissions of the API token: minimal for an install.
var tokenPermissions = []map[string]string{
	{"key": "workers_scripts", "type": "edit"},
	{"key": "d1", "type": "edit"},
	{"key": "account_settings", "type": "read"},
}

var (
	workerNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	accountPattern    = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
	migrationFile     = regexp.MustCompile(`^\d{4}_[a-z0-9_]+\.sql$`)
)

// tokenTemplate is the link that opens the API token form of the dashboard, filled in.
func tokenTemplate(r2 bool) string {
	perms := slices.Clone(tokenPermissions)
	if r2 {
		perms = append(perms, map[string]string{"key": "workers_r2", "type": "edit"})
	}
	keys, _ := json.Marshal(perms)
	q := url.Values{"permissionGroupKeys": {string(keys)}, "accountId": {"*"}, "zoneId": {"all"}, "name": {"Checkend install"}}
	return "https://dash.cloudflare.com/profile/api-tokens?" + q.Encode()
}

// cfClient calls the Cloudflare API with a token. It follows no redirects.
type cfClient struct {
	base, token string
	http        *http.Client
}

type cfError struct {
	status int
	msg    string
}

func (e *cfError) Error() string { return e.msg }

func (c *cfClient) do(method, path, contentType string, body []byte, out any) error {
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "checkend-cli/"+version.Version)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return clierr.New(clierr.Failure, fmt.Sprintf("Cannot reach the Cloudflare API: %v", err), "Check your network.")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	var env struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(data, &env)
	if resp.StatusCode >= 300 || !env.Success {
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if len(env.Errors) > 0 {
			msg = sanitize.OneLine(env.Errors[0].Message)
		}
		return &cfError{status: resp.StatusCode, msg: fmt.Sprintf("Cloudflare: %s %s: %s", method, path, msg)}
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func (c *cfClient) json(method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	return c.do(method, path, "application/json", body, out)
}

func notFound(err error) bool {
	var e *cfError
	return errors.As(err, &e) && e.status == 404
}

func cfFail(err error, hint string) error {
	var e *cfError
	if errors.As(err, &e) {
		if e.status == 401 || e.status == 403 {
			return clierr.New(clierr.Auth, e.msg, "The API token needs: Workers Scripts edit, D1 edit, Account Settings read. Make one: "+tokenTemplate(false))
		}
		return clierr.New(clierr.Failure, e.msg, hint)
	}
	return err
}

// query runs SQL on D1 and returns the rows of the first statement.
func (c *cfClient) query(account, db, sql string) ([]map[string]any, error) {
	var results []struct {
		Results []map[string]any `json:"results"`
	}
	if err := c.json("POST", "/accounts/"+account+"/d1/database/"+db+"/query", map[string]string{"sql": sql}, &results); err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0].Results, nil
}

func names(rows []map[string]any) []string {
	var out []string
	for _, r := range rows {
		if n, ok := r["name"].(string); ok {
			out = append(out, n)
		}
	}
	return out
}

var moduleTypes = map[string]string{
	"esm": "application/javascript+module", "cjs": "application/javascript", "text": "text/plain",
	"data": "application/octet-stream", "wasm": "application/wasm", "sourcemap": "application/source-map",
}

// upload sends the Worker: the metadata and each module of the bundle, as multipart.
func (c *cfClient) upload(account, name string, metadata map[string]any, m *serverrelease.Manifest, files map[string][]byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	meta, _ := json.Marshal(metadata)
	part, _ := w.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="metadata"`}, "Content-Type": {"application/json"}})
	_, _ = part.Write(meta)
	for _, mod := range m.Worker.Modules {
		typ, ok := moduleTypes[mod.Type]
		if !ok {
			return clierr.Fail(fmt.Sprintf("The Worker bundle has a module of an unknown type %q.", mod.Type))
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, mod.Name, mod.Name))
		header.Set("Content-Type", typ)
		part, _ := w.CreatePart(header)
		_, _ = part.Write(files[mod.Name])
	}
	_ = w.Close()
	return c.do("PUT", "/accounts/"+account+"/workers/scripts/"+name, w.FormDataContentType(), buf.Bytes(), nil)
}

// newCFClient is a client of the Cloudflare API with this token (hidden in the output).
func newCFClient(s *Session, token string) *cfClient {
	s.Out.Hide(token)
	base := cloudflareAPI
	if s.Host.CloudflareAPI != "" {
		base = s.Host.CloudflareAPI
	}
	httpClient := http.Client{Timeout: 2 * time.Minute}
	if s.Host.HTTP != nil {
		httpClient = *s.Host.HTTP
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &cfClient{base: base, token: token, http: &httpClient}
}

// cloudflareToken is CLOUDFLARE_API_TOKEN, else the token in the keychain, else (on a
// terminal) a token that the user makes with the template link. fresh is true for a new one.
func cloudflareToken(s *Session, r2 bool) (token string, fresh bool, err error) {
	if token = s.Host.Getenv("CLOUDFLARE_API_TOKEN"); token != "" {
		return token, false, nil
	}
	if s.Host.Keyring != nil {
		if token, err = s.Host.Keyring.Get(cloudflareKeychain); err == nil && token != "" {
			return token, false, nil
		}
		if err != nil && !errors.Is(err, credstore.ErrNotFound) {
			return "", false, clierr.Fail(fmt.Sprintf("Cannot read the Cloudflare API token from the keychain: %v", err))
		}
	}
	link := tokenTemplate(r2)
	if !s.Host.Interactive {
		return "", false, clierr.UsageError("ce install cloudflare needs a Cloudflare API token.",
			"Make one with this link (the permissions are filled in), then set CLOUDFLARE_API_TOKEN:\n"+link)
	}
	s.Out.Menu([]string{"ce needs a Cloudflare API token. Open this link: the permissions are filled in", "(Workers Scripts edit, D1 edit, Account Settings read). Then create the token and paste it.", "  " + link})
	token, err = s.Host.Prompt("Cloudflare API token: ", true)
	return strings.TrimSpace(token), true, err
}

type cfAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func pickAccount(s *Session, c *cfClient) (cfAccount, error) {
	var accounts []cfAccount
	if err := c.json("GET", "/accounts?per_page=50", nil, &accounts); err != nil {
		return cfAccount{}, cfFail(err, "")
	}
	if want := s.String("account"); want != "" {
		for _, a := range accounts {
			if a.ID == want {
				return a, nil
			}
		}
		return cfAccount{}, clierr.New(clierr.Failure, fmt.Sprintf("The API token has no access to account %s.", want), "Check --account, or the account of the token.")
	}
	if len(accounts) == 1 {
		return accounts[0], nil
	}
	if len(accounts) == 0 {
		return cfAccount{}, clierr.New(clierr.Auth, "The API token has access to no account.", "Make the token again with the template link: "+tokenTemplate(false))
	}
	var lines []string
	for i, a := range accounts {
		lines = append(lines, fmt.Sprintf("%d. %s (%s)", i+1, sanitize.OneLine(a.Name), a.ID))
	}
	if !s.Host.Interactive {
		return cfAccount{}, clierr.UsageError("The API token has more than one account:\n  "+strings.Join(lines, "\n  "), "Choose one with --account <id>.")
	}
	s.Out.Menu(append([]string{"Accounts:"}, lines...))
	answer, err := s.Host.Prompt("Account number: ", false)
	if err != nil {
		return cfAccount{}, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(answer))
	if err != nil || n < 1 || n > len(accounts) {
		return cfAccount{}, clierr.UsageError("That is not an account number. Nothing changed.", "Run the command again, or give --account <id>.")
	}
	return accounts[n-1], nil
}

// d1State is what an existing D1 database has.
type d1State struct {
	id      string
	exists  bool
	applied []string
}

func inspectD1(c *cfClient, account, name string, known []string) (d1State, error) {
	var list []struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	}
	if err := c.json("GET", "/accounts/"+account+"/d1/database?name="+url.QueryEscape(name), nil, &list); err != nil {
		return d1State{}, cfFail(err, "")
	}
	for _, db := range list {
		if db.Name != name {
			continue
		}
		st := d1State{id: db.UUID, exists: true}
		rows, err := c.query(account, db.UUID, "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
		if err != nil {
			return st, cfFail(err, "")
		}
		var others []string
		for _, t := range names(rows) {
			if t != "d1_migrations" && !strings.HasPrefix(t, "_cf_") && !strings.HasPrefix(t, "sqlite_") {
				others = append(others, t)
			}
		}
		if !slices.Contains(names(rows), "d1_migrations") {
			if len(others) > 0 {
				return st, clierr.New(clierr.Failure, fmt.Sprintf("The D1 database %s has tables (%s) but no migrations: it is not a Checkend database.", name, strings.Join(others, ", ")),
					"Choose another name with --database.")
			}
			return st, nil
		}
		if rows, err = c.query(account, db.UUID, "SELECT name FROM d1_migrations ORDER BY id"); err != nil {
			return st, cfFail(err, "")
		}
		st.applied = names(rows)
		for _, a := range st.applied {
			if !slices.Contains(known, a) {
				return st, clierr.New(clierr.Failure, fmt.Sprintf("The D1 database %s has migration %s, which this release does not have: it is newer.", name, a),
					"Install a newer release (--version), or choose another database with --database.")
			}
		}
		return st, nil
	}
	return d1State{}, nil
}

// workerState is what an existing Worker has.
type workerState struct {
	exists  bool
	secrets []string
	blobs   string
}

func inspectWorker(c *cfClient, account, name string) (workerState, error) {
	var settings struct {
		Bindings []map[string]any `json:"bindings"`
	}
	err := c.json("GET", "/accounts/"+account+"/workers/scripts/"+name+"/settings", nil, &settings)
	if notFound(err) {
		return workerState{}, nil
	}
	if err != nil {
		return workerState{}, cfFail(err, "")
	}
	st := workerState{exists: true}
	checkend := false
	for _, b := range settings.Bindings {
		switch {
		case b["type"] == "d1" && b["name"] == "DB":
			checkend = true
		case b["type"] == "secret_text":
			if n, ok := b["name"].(string); ok {
				st.secrets = append(st.secrets, n)
			}
		case b["type"] == "r2_bucket" && b["name"] == "BLOBS":
			st.blobs, _ = b["bucket_name"].(string)
		}
	}
	if !checkend {
		return st, clierr.New(clierr.Failure, fmt.Sprintf("The Worker %s exists and is not a Checkend install (it has no D1 binding DB).", name),
			"Choose another name with --name.")
	}
	return st, nil
}

func installCloudflareCommand() *Command {
	return &Command{
		Path:    []string{"install", "cloudflare"},
		Summary: "Install Checkend on your Cloudflare account (a Worker and D1)",
		Usage:   "[--account <id>] [--name <worker>] [--database <name>] [--r2] [--version <v>] [--yes]",
		Details: []string{
			"  --account <id>       The Cloudflare account (needed when the token has more than one)",
			"  --name <worker>      The Worker name (default: checkend)",
			"  --database <name>    The D1 database (default: the Worker name)",
			"  --r2                 Also create an R2 bucket for blobs (source maps, large events)",
			"  --version <v>        A server release (default: the latest one)",
			"  --yes                Do not ask (for agents); the plan is still printed on stderr",
			"",
			"Needs a Cloudflare API token: CLOUDFLARE_API_TOKEN, or the token in the OS keychain.",
			"On a terminal, ce prints a link that fills in the minimal permissions and keeps the",
			"token in the keychain. Creates the D1 database, records a Time Travel bookmark before",
			"the migrations, uploads the Worker bundle of the signed release, sets the secrets",
			"through the API (never on a command line), and prints the setup link.",
		},
		Options: map[string]Kind{"account": String, "name": String, "database": String, "r2": Bool, "version": String, "yes": Bool},
		Run: func(s *Session) error {
			name := s.String("name")
			if name == "" {
				name = "checkend"
			}
			database := s.String("database")
			if database == "" {
				database = name
			}
			if !workerNamePattern.MatchString(name) || !workerNamePattern.MatchString(database) {
				return clierr.UsageError("A Worker or database name has lower-case letters, digits and \"-\" (at most 63).", "Example: --name checkend")
			}
			if a := s.String("account"); a != "" && !accountPattern.MatchString(a) {
				return clierr.UsageError(fmt.Sprintf("%q is not a Cloudflare account ID.", a), "Find it in the URL of the dashboard.")
			}
			if err := needApproval(s, "ce install cloudflare"); err != nil {
				return err
			}
			token, fresh, err := cloudflareToken(s, s.Bool("r2"))
			if err != nil {
				return err
			}
			c := newCFClient(s, token)
			account, err := pickAccount(s, c)
			if err != nil {
				return err
			}
			acc := account.ID
			var sub struct {
				Subdomain string `json:"subdomain"`
			}
			if err := c.json("GET", "/accounts/"+acc+"/workers/subdomain", nil, &sub); err != nil || sub.Subdomain == "" {
				if err != nil && !notFound(err) {
					return cfFail(err, "")
				}
				return clierr.New(clierr.Failure, "The account has no workers.dev subdomain yet.",
					"Open Workers & Pages in the Cloudflare dashboard once to choose it, then run the command again.")
			}
			workerURL := "https://" + name + "." + sub.Subdomain + ".workers.dev"

			rel, err := pickRelease(s)
			if err != nil {
				return err
			}
			m := &rel.Manifest
			migrationFiles, err := rel.Files(m.Migrations.Asset)
			if err != nil {
				return err
			}
			var known []string
			for file := range migrationFiles {
				if n, ok := strings.CutPrefix(file, "migrations/"); ok && strings.HasSuffix(n, ".sql") {
					if !migrationFile.MatchString(n) {
						return clierr.Fail(fmt.Sprintf("The migrations of the release have an invalid file name %q.", n))
					}
					known = append(known, n)
				}
			}
			sort.Strings(known)
			if len(known) == 0 || known[len(known)-1] != m.Migrations.Latest {
				return clierr.Fail("The migrations of the release do not match its manifest.")
			}
			bundle, err := rel.Files(m.Worker.Asset)
			if err != nil {
				return err
			}
			for _, mod := range m.Worker.Modules {
				if _, ok := bundle[mod.Name]; !ok {
					return clierr.Fail(fmt.Sprintf("The Worker bundle has no module %s.", mod.Name))
				}
			}
			worker, err := inspectWorker(c, acc, name)
			if err != nil {
				return err
			}
			db, err := inspectD1(c, acc, database, known)
			if err != nil {
				return err
			}
			var pending []string
			for _, n := range known {
				if !slices.Contains(db.applied, n) {
					pending = append(pending, n)
				}
			}
			blobs := worker.blobs
			if s.Bool("r2") && blobs == "" {
				blobs = r2Bucket
			}
			adminToken := ""
			if slices.Contains(worker.secrets, "ADMIN_TOKEN") {
				adminToken, _ = config.SavedToken(s.Host, workerURL)
			}
			s.Out.Hide(adminToken)

			plan := []string{releaseLine(s, rel), fmt.Sprintf("Cloudflare account: %s (%s).", account.Name, acc)}
			if db.exists {
				plan = append(plan, fmt.Sprintf("Use the D1 database %s (%d migrations applied).", database, len(db.applied)))
			} else {
				plan = append(plan, fmt.Sprintf("Create the D1 database %s.", database))
			}
			if len(pending) > 0 {
				plan = append(plan, fmt.Sprintf("Record a D1 Time Travel bookmark, then apply %d migrations (%s … %s).", len(pending), pending[0], pending[len(pending)-1]))
			} else {
				plan = append(plan, "No new migrations.")
			}
			if blobs != "" && blobs != worker.blobs {
				plan = append(plan, fmt.Sprintf("Create the R2 bucket %s for BLOBS (if it does not exist).", blobs))
			} else if blobs != "" {
				plan = append(plan, fmt.Sprintf("Keep the R2 bucket %s for BLOBS.", blobs))
			}
			verb := "Upload"
			if worker.exists {
				verb = "Replace"
			}
			plan = append(plan, fmt.Sprintf("%s the Worker %s (compatibility date %s) and enable %s.", verb, name, m.Worker.CompatibilityDate, workerURL))
			var optional []string
			for _, b := range m.Worker.Bindings {
				if !b.Required && !(b.Type == "r2" && b.Name == "BLOBS" && blobs != "") {
					optional = append(optional, b.Name+" ("+b.Type+")")
				}
			}
			if len(optional) > 0 {
				hint := ""
				if blobs == "" {
					hint = "; add BLOBS with --r2"
				}
				plan = append(plan, "Not set up (optional): "+strings.Join(optional, ", ")+hint+".")
			}
			keep := []string{}
			for _, k := range []string{"ADMIN_TOKEN", "ENCRYPTION_KEYS"} {
				if slices.Contains(worker.secrets, k) {
					keep = append(keep, k)
				}
			}
			if len(keep) == 2 {
				plan = append(plan, "Keep ADMIN_TOKEN and ENCRYPTION_KEYS; set a new SETUP_CODE (through the API).")
			} else {
				plan = append(plan, "Set the secrets ADMIN_TOKEN, ENCRYPTION_KEYS and SETUP_CODE through the API (never on a command line).")
			}
			if fresh {
				plan = append(plan, "Save the Cloudflare API token in the OS keychain.")
			}
			if err := approve(s, plan); err != nil {
				return err
			}

			if !db.exists {
				var created struct {
					UUID string `json:"uuid"`
				}
				if err := c.json("POST", "/accounts/"+acc+"/d1/database", map[string]string{"name": database}, &created); err != nil {
					return cfFail(err, "")
				}
				db.id = created.UUID
			}
			var bookmark struct {
				Bookmark string `json:"bookmark"`
			}
			if err := c.json("GET", "/accounts/"+acc+"/d1/database/"+db.id+"/time_travel/bookmark", nil, &bookmark); err != nil || bookmark.Bookmark == "" {
				return clierr.New(clierr.Failure, "Cannot record a D1 Time Travel bookmark, so no migration ran.",
					"Run the command again later. Migrations without a way back are not run.")
			}
			s.Out.Notice(fmt.Sprintf("Bookmark before migrations: %s\nTo restore: npx wrangler d1 time-travel restore %s --bookmark=%s", bookmark.Bookmark, database, bookmark.Bookmark))
			if _, err := c.query(acc, db.id, "CREATE TABLE IF NOT EXISTS d1_migrations (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE, applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL)"); err != nil {
				return cfFail(err, "")
			}
			for _, n := range pending {
				sql := strings.TrimSpace(string(migrationFiles["migrations/"+n]))
				if !strings.HasSuffix(sql, ";") {
					sql += ";"
				}
				// The name matches migrationFile (checked above), so it is safe in SQL text.
				sql += "\nINSERT INTO d1_migrations (name) VALUES ('" + n + "');"
				if _, err := c.query(acc, db.id, sql); err != nil {
					return cfFail(err, fmt.Sprintf("Migration %s failed. To go back: npx wrangler d1 time-travel restore %s --bookmark=%s", n, database, bookmark.Bookmark))
				}
			}
			if blobs != "" && blobs != worker.blobs {
				var buckets struct {
					Buckets []struct {
						Name string `json:"name"`
					} `json:"buckets"`
				}
				if err := c.json("GET", "/accounts/"+acc+"/r2/buckets", nil, &buckets); err != nil {
					return cfFail(err, "The token needs Workers R2 Storage edit for --r2: "+tokenTemplate(true))
				}
				if !slices.ContainsFunc(buckets.Buckets, func(b struct {
					Name string `json:"name"`
				}) bool {
					return b.Name == blobs
				}) {
					if err := c.json("POST", "/accounts/"+acc+"/r2/buckets", map[string]string{"name": blobs}, nil); err != nil {
						return cfFail(err, "The token needs Workers R2 Storage edit for --r2: "+tokenTemplate(true))
					}
				}
			}
			bindings := []any{
				map[string]any{"type": "d1", "name": "DB", "id": db.id},
				map[string]any{"type": "plain_text", "name": "PUBLIC_URL", "text": workerURL},
			}
			if blobs != "" {
				bindings = append(bindings, map[string]any{"type": "r2_bucket", "name": "BLOBS", "bucket_name": blobs})
			}
			flags := m.Worker.CompatibilityFlags
			if flags == nil {
				flags = []string{}
			}
			metadata := map[string]any{
				"main_module": m.Worker.MainModule, "compatibility_date": m.Worker.CompatibilityDate,
				"compatibility_flags": flags, "bindings": bindings, "keep_bindings": []string{"secret_text"},
				"observability": map[string]any{"enabled": m.Worker.Observability},
			}
			if err := c.upload(acc, name, metadata, m, bundle); err != nil {
				return cfFail(err, "")
			}
			secrets := map[string]string{}
			if !slices.Contains(worker.secrets, "ADMIN_TOKEN") {
				adminToken = randomHex(32)
				secrets["ADMIN_TOKEN"] = adminToken
			}
			if !slices.Contains(worker.secrets, "ENCRYPTION_KEYS") {
				secrets["ENCRYPTION_KEYS"] = newEncryptionKeys()
			}
			code := ""
			if s.upgrade == nil {
				var stored string
				stored, code = newSetupCode(s.Host.Now())
				secrets["SETUP_CODE"] = stored
			}
			for _, k := range []string{"ADMIN_TOKEN", "ENCRYPTION_KEYS", "SETUP_CODE"} {
				v, ok := secrets[k]
				if !ok {
					continue
				}
				s.Out.Hide(v)
				if err := c.json("PUT", "/accounts/"+acc+"/workers/scripts/"+name+"/secrets", map[string]string{"name": k, "text": v, "type": "secret_text"}, nil); err != nil {
					return cfFail(err, "Run the command again: it keeps what is done.")
				}
			}
			crons := []map[string]string{}
			for _, cron := range m.Worker.Crons {
				crons = append(crons, map[string]string{"cron": cron})
			}
			if err := c.json("PUT", "/accounts/"+acc+"/workers/scripts/"+name+"/schedules", crons, nil); err != nil {
				return cfFail(err, "")
			}
			if err := c.json("POST", "/accounts/"+acc+"/workers/scripts/"+name+"/subdomain", map[string]bool{"enabled": true, "previews_enabled": false}, nil); err != nil {
				return cfFail(err, "")
			}
			if fresh && s.Host.Keyring != nil {
				_ = s.Host.Keyring.Set(cloudflareKeychain, token)
			}

			result := installResult{Kind: "cloudflare", URL: workerURL, Version: m.Version, Next: []string{}}
			install := config.Install{Kind: "cloudflare", Version: m.Version, Migrations: m.Migrations.Latest,
				Account: acc, Worker: name, Database: database, DatabaseID: db.id}
			if adminToken != "" {
				if result.Login, err = recordInstall(s, workerURL, install, adminToken); err != nil {
					return err
				}
			} else {
				if _, err := recordInstall(s, workerURL, install, ""); err != nil {
					return err
				}
				result.Next = append(result.Next, "ce has no copy of the kept ADMIN_TOKEN: run `ce login --url "+workerURL+"` with it.")
			}
			setupOpen := true
			if _, raw, err := api.New(s.Host, workerURL, "").Health(); err == nil {
				var health struct {
					SetupOpen *bool `json:"setup_open"`
				}
				if json.Unmarshal(raw, &health) == nil && health.SetupOpen != nil {
					setupOpen = *health.SetupOpen
				}
			} else {
				result.Next = append(result.Next, "The Worker does not answer yet; workers.dev can take a minute. Then open the link.")
			}
			if setupOpen && code != "" {
				result.SetupURL = workerURL + "/setup#" + code
			} else if code != "" {
				result.Next = append(result.Next, "Setup is done: sign in at "+workerURL)
			}
			s.Out.Data(result, func(any) string {
				return renderInstall(result, fmt.Sprintf("Installed Checkend %s on Cloudflare at %s.", result.Version, workerURL))
			})
			return nil
		},
	}
}
