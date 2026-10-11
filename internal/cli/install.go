package cli

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/config"
	"github.com/Checkend/checkend-cli/internal/serverrelease"
)

// The install commands install a released, signed server version only. Each one shows the plan
// and asks first (--yes for agents), can run again safely, never prints a secret, and ends with
// the setup link of the web UI.

func installCommands() []*Command {
	return []*Command{installDockerCommand(), installSSHCommand(), installCloudflareCommand()}
}

// releaseSource is the source of the server releases (tests replace it through the host).
func releaseSource(s *Session) serverrelease.Source {
	src := serverrelease.Default(s.Host.HTTP)
	if s.Host.ReleaseAPI != "" {
		src.API, src.Keys = s.Host.ReleaseAPI, s.Host.ReleaseKeys
	}
	return src
}

// pickRelease is the release of --version, else the latest one. For `ce upgrade`, it refuses
// a downgrade (unless --allow-downgrade) and always an older migration level.
func pickRelease(s *Session) (*serverrelease.Release, error) {
	var rel *serverrelease.Release
	var err error
	if v := s.String("version"); v != "" {
		rel, err = releaseSource(s).Get(v)
	} else {
		rel, err = releaseSource(s).Latest()
	}
	if err != nil || s.upgrade == nil {
		return rel, err
	}
	return rel, checkUpgrade(s.upgrade, &rel.Manifest, s.allowDowngrade)
}

// errUpToDate ends `ce upgrade` when the install has the release already.
var errUpToDate = errors.New("up to date")

// checkUpgrade compares a release with the installed one. Migrations are forward-only, so a
// release with an older migration level is refused even with --allow-downgrade.
func checkUpgrade(installed *config.Install, m *serverrelease.Manifest, allowDowngrade bool) error {
	if installed.Migrations != "" && m.Migrations.Latest < installed.Migrations {
		return clierr.New(clierr.Failure,
			fmt.Sprintf("Checkend %s has migration level %s; the install has %s. Migrations never run backwards.", m.Version, m.Migrations.Latest, installed.Migrations),
			"Choose a release with the same or a newer migration level.")
	}
	switch serverrelease.Compare(m.Version, installed.Version) {
	case -1:
		if !allowDowngrade {
			return clierr.New(clierr.Failure, fmt.Sprintf("Checkend %s is older than the installed %s.", m.Version, installed.Version),
				"Run `ce upgrade --version "+m.Version+" --allow-downgrade` if you mean it (the data stays at its migration level).")
		}
	case 0:
		if m.Migrations.Latest == installed.Migrations || installed.Migrations == "" {
			return errUpToDate
		}
	}
	return nil
}

// needApproval checks, before any request or file write, that the command can ask or has --yes.
func needApproval(s *Session, command string) error {
	if s.Bool("yes") || s.Host.Interactive {
		return nil
	}
	return clierr.UsageError(fmt.Sprintf("`%s` changes a system and needs a confirmation.", command),
		fmt.Sprintf("Run it on a terminal, or run `%s --yes` (for agents) after you read its plan.", command))
}

// approve prints the plan on stderr and asks, unless --yes was given.
func approve(s *Session, plan []string) error {
	lines := []string{"The plan:"}
	for _, step := range plan {
		lines = append(lines, "  - "+step)
	}
	s.Out.Menu(lines)
	if s.Bool("yes") {
		return nil
	}
	answer, err := s.Host.Prompt("Continue? [y/N] ", false)
	if err != nil {
		return err
	}
	if a := strings.ToLower(strings.TrimSpace(answer)); a == "y" || a == "yes" {
		return nil
	}
	return clierr.UsageError("You did not confirm. Nothing changed.", "Run the command again to install.")
}

func releaseLine(s *Session, r *serverrelease.Release) string {
	if s.upgrade != nil {
		return fmt.Sprintf("Upgrade Checkend from %s to %s (commit %s, signed with key %s).", s.upgrade.Version, r.Manifest.Version, short(r.Manifest.Commit), r.Signer)
	}
	return fmt.Sprintf("Install Checkend %s (commit %s, signed with key %s).", r.Manifest.Version, short(r.Manifest.Commit), r.Signer)
}

// randomHex is n random bytes in hex.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// newSetupCode is the SETUP_CODE value "<unix seconds>.<code>" and the code for the link.
func newSetupCode(now time.Time) (string, string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	code := base64.RawURLEncoding.EncodeToString(b)
	return strconv.FormatInt(now.Unix(), 10) + "." + code, code
}

// newEncryptionKeys is an ENCRYPTION_KEYS value with one new 32-byte key.
func newEncryptionKeys() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "k1:" + base64.StdEncoding.EncodeToString(b)
}

// envFile is a .env file: the order of the keys is kept.
type envFile struct {
	keys   []string
	values map[string]string
}

func parseEnv(text string) *envFile {
	e := &envFile{values: map[string]string{}}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			e.set(strings.TrimSpace(k), v)
		}
	}
	return e
}

func (e *envFile) set(k, v string) {
	if _, ok := e.values[k]; !ok {
		e.keys = append(e.keys, k)
	}
	e.values[k] = v
}

func (e *envFile) String() string {
	var b strings.Builder
	b.WriteString("# Checkend settings, written by `ce install`. Secrets: keep this file private (mode 600).\n")
	for _, k := range e.keys {
		fmt.Fprintf(&b, "%s=%s\n", k, e.values[k])
	}
	return b.String()
}

// secrets fills ADMIN_TOKEN and ENCRYPTION_KEYS when they are missing, and writes a new setup
// code unless the one there is stays valid long enough (an upgrade always keeps it). It returns the setup code for the link ("" when
// kept) and the keys that it kept.
func (e *envFile) secrets(now time.Time, newCode bool) (code string, kept []string) {
	if len(e.values["ADMIN_TOKEN"]) >= 32 {
		kept = append(kept, "ADMIN_TOKEN")
	} else {
		e.set("ADMIN_TOKEN", randomHex(32))
	}
	if e.values["ENCRYPTION_KEYS"] != "" {
		kept = append(kept, "ENCRYPTION_KEYS")
	} else {
		e.set("ENCRYPTION_KEYS", newEncryptionKeys())
	}
	if !newCode && e.values["SETUP_CODE"] != "" {
		return "", kept
	}
	// A code that is valid for at least 4 more hours stays, so that a second run changes nothing.
	if unix, old, ok := strings.Cut(e.values["SETUP_CODE"], "."); ok && old != "" {
		if at, err := strconv.ParseInt(unix, 10, 64); err == nil {
			if age := now.Sub(time.Unix(at, 0)); age >= -5*time.Minute && age < 20*time.Hour {
				return old, kept
			}
		}
	}
	stored, code := newSetupCode(now)
	e.set("SETUP_CODE", stored)
	return code, kept
}

func secretsLine(file string, kept []string, isNew bool, sameCode bool) string {
	if !isNew && len(kept) == 2 {
		code := "write a new setup code"
		if sameCode {
			code = "keep the setup code"
		}
		return fmt.Sprintf("Write %s (mode 600): keep ADMIN_TOKEN and ENCRYPTION_KEYS; %s.", file, code)
	}
	return fmt.Sprintf("Write %s (mode 600): a new ADMIN_TOKEN (64 characters), ENCRYPTION_KEYS and setup code.", file)
}

// composeOptions describe compose.yaml.
type composeOptions struct {
	Image string
	// Port publishes Checkend on 127.0.0.1 (docker). 0: not published (a proxy is in front).
	Port int
	// Proxy is "", "caddy" or "tunnel"; ProxyImage is its pinned image.
	Proxy      string
	ProxyImage string
}

// compose is compose.yaml: Checkend, a nightly backup, a volume and restarts.
func compose(o composeOptions) string {
	var b strings.Builder
	b.WriteString("# Checkend, written by `ce install`. The image is pinned by its digest; `ce upgrade` changes it.\n")
	b.WriteString("services:\n  checkend:\n")
	fmt.Fprintf(&b, "    image: %s\n    restart: unless-stopped\n    env_file: .env\n", o.Image)
	if o.Port > 0 {
		fmt.Fprintf(&b, "    ports:\n      - \"127.0.0.1:%d:8787\"\n", o.Port)
	}
	b.WriteString("    volumes:\n      - checkend-data:/data\n")
	b.WriteString(`  backup:
    # A consistent copy of the database every night at 03:00 UTC; the last 7 stay in the volume.
    image: ` + o.Image + `
    restart: unless-stopped
    volumes:
      - checkend-data:/data
    healthcheck:
      disable: true
    entrypoint: ["sh", "-c"]
    command:
      - |
        mkdir -p /data/backups
        while :; do
          now=$$(date -u +%s); next=$$(( now / 86400 * 86400 + 10800 ))
          if [ "$$next" -le "$$now" ]; then next=$$(( next + 86400 )); fi
          sleep $$(( next - now ))
          file=/data/backups/checkend-$$(date -u +%Y%m%d).sqlite
          rm -f "$$file"; node scripts/backup.ts "$$file"
          find /data/backups -name 'checkend-*.sqlite' -mtime +7 -delete
        done
`)
	switch o.Proxy {
	case "caddy":
		fmt.Fprintf(&b, `  caddy:
    # HTTPS with a certificate from Let's Encrypt for the domain in Caddyfile.
    image: %s
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy-data:/data
`, o.ProxyImage)
	case "tunnel":
		fmt.Fprintf(&b, `  cloudflared:
    # A Cloudflare Tunnel: no open ports. Route the public hostname to http://checkend:8787.
    image: %s
    restart: unless-stopped
    command: ["tunnel", "--no-autoupdate", "run"]
    environment:
      TUNNEL_TOKEN: ${TUNNEL_TOKEN:?set TUNNEL_TOKEN in .env}
`, o.ProxyImage)
	}
	b.WriteString("volumes:\n  checkend-data:\n")
	if o.Proxy == "caddy" {
		b.WriteString("  caddy-data:\n")
	}
	return b.String()
}

// recordInstall saves the install record and the admin token (the login of the instance).
func recordInstall(s *Session, url string, install config.Install, adminToken string) (string, error) {
	installs, err := config.ReadInstalls(s.Host)
	if err != nil {
		return "", err
	}
	now := s.Host.Now().UTC().Format(time.RFC3339)
	if old, ok := installs[url]; ok && old.InstalledAt != "" {
		install.InstalledAt, install.UpgradedAt = old.InstalledAt, now
	} else {
		install.InstalledAt = now
	}
	if err := config.SaveInstall(s.Host, url, &install); err != nil {
		return "", err
	}
	if adminToken == "" {
		return "", nil
	}
	return config.SaveToken(s.Host, url, adminToken)
}

type installResult struct {
	Kind     string   `json:"kind"`
	URL      string   `json:"url"`
	Version  string   `json:"version"`
	Dir      string   `json:"dir,omitempty"`
	Target   string   `json:"target,omitempty"`
	Files    []string `json:"files,omitempty"`
	Login    string   `json:"login,omitempty"`
	SetupURL string   `json:"setup_url,omitempty"`
	Next     []string `json:"next"`
}

func renderInstall(r installResult, title string) string {
	lines := []string{title}
	if len(r.Files) > 0 {
		lines = append(lines, "Wrote "+strings.Join(r.Files, ", ")+" (.env has the secrets; mode 600).")
	}
	if r.Login != "" {
		where := "the OS keychain"
		if r.Login != config.StorageKeychain {
			where = r.Login + " (mode 600)"
		}
		lines = append(lines, fmt.Sprintf("Saved the admin token for %s in %s.", r.URL, where))
	}
	lines = append(lines, "")
	if len(r.Next) > 0 {
		lines = append(lines, "Next:")
		for _, n := range r.Next {
			lines = append(lines, "  "+n)
		}
		lines = append(lines, "")
	}
	if r.SetupURL != "" {
		lines = append(lines, "Open the setup link (valid for 24 hours) to register the first member with a passkey:", "  "+r.SetupURL)
	}
	return strings.Join(lines, "\n")
}

func portOption(s *Session, fallback int) (int, error) {
	value := s.String("port")
	if value == "" {
		return fallback, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, clierr.UsageError(fmt.Sprintf("%q is not a port.", value), "Use a number from 1 to 65535.")
	}
	return port, nil
}

func installDockerCommand() *Command {
	return &Command{
		Path:    []string{"install", "docker"},
		Summary: "Write compose.yaml and .env for Checkend with Docker on this machine",
		Usage:   "[--dir <dir>] [--port <port>] [--version <v>] [--yes]",
		Details: []string{
			"  --dir <dir>      The folder for compose.yaml and .env (default: ./checkend)",
			"  --port <port>    The port on 127.0.0.1 (default: 8787)",
			"  --version <v>    A server release (default: the latest one)",
			"  --yes            Do not ask (for agents); the plan is still printed on stderr",
			"",
			"Installs a released server version only: the image is pinned by the digest in the",
			"signed release manifest. Generates the secrets, saves the admin token in the OS",
			"keychain, and prints the setup link. Run it again to update the files: the secrets stay.",
		},
		Options: map[string]Kind{"dir": String, "port": String, "version": String, "yes": Bool},
		Run: func(s *Session) error {
			port, err := portOption(s, 8787)
			if err != nil {
				return err
			}
			if err := needApproval(s, "ce install docker"); err != nil {
				return err
			}
			dir := s.String("dir")
			if dir == "" {
				dir = "checkend"
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(s.Host.Cwd, dir)
			}
			rel, err := pickRelease(s)
			if err != nil {
				return err
			}
			url := fmt.Sprintf("http://localhost:%d", port)
			envPath, composePath := filepath.Join(dir, ".env"), filepath.Join(dir, "compose.yaml")
			old, readErr := os.ReadFile(envPath)
			env := parseEnv(string(old))
			oldCode := env.values["SETUP_CODE"]
			code, kept := env.secrets(s.Host.Now(), s.upgrade == nil)
			env.set("PUBLIC_URL", url)
			s.Out.Hide(env.values["ADMIN_TOKEN"])
			s.Out.Hide(env.values["ENCRYPTION_KEYS"])
			running := s.upgrade != nil && dockerRunning(s, dir)
			plan := []string{
				releaseLine(s, rel),
				"Image: " + rel.Manifest.Image.Ref(),
				fmt.Sprintf("Write %s (Checkend on 127.0.0.1:%d, a nightly backup, a volume, restarts).", composePath, port),
				secretsLine(envPath, kept, readErr != nil, oldCode != "" && env.values["SETUP_CODE"] == oldCode),
				fmt.Sprintf("Save the admin token for %s in the OS keychain.", url),
			}
			backup := fmt.Sprintf("/data/backups/checkend-before-%s-%s.sqlite", rel.Manifest.Version, s.Host.Now().UTC().Format("20060102-150405"))
			if running {
				plan = append(plan, "Back up the database to "+backup+" first, then restart with `docker compose up -d`.")
			} else {
				plan = append(plan, "Start nothing: you run `docker compose up -d` after this.")
			}
			if err := approve(s, plan); err != nil {
				return err
			}
			if running {
				if err := dockerBackup(s, dir, backup); err != nil {
					return err
				}
			}
			text := compose(composeOptions{Image: rel.Manifest.Image.Ref(), Port: port})
			if err := config.WriteFile(composePath, []byte(text), 0o644); err != nil {
				return clierr.Fail(fmt.Sprintf("Cannot write %s: %v", composePath, err))
			}
			if err := config.WriteFile(envPath, []byte(env.String()), 0o600); err != nil {
				return clierr.Fail(fmt.Sprintf("Cannot write %s: %v", envPath, err))
			}
			login, err := recordInstall(s, url, config.Install{Kind: "docker", Version: rel.Manifest.Version,
				Migrations: rel.Manifest.Migrations.Latest, Dir: dir}, env.values["ADMIN_TOKEN"])
			if err != nil {
				return err
			}
			result := installResult{Kind: "docker", URL: url, Version: rel.Manifest.Version, Dir: dir,
				Files: []string{composePath, envPath}, Login: login, Next: []string{}}
			if code != "" {
				result.SetupURL = url + "/setup#" + code
			}
			if running {
				if _, err := dockerCompose(s, dir, "up", "-d", "--remove-orphans"); err != nil {
					return clierr.New(clierr.Failure, fmt.Sprintf("docker compose up failed: %v", err), "The backup is "+backup+" in the volume. Fix the error, then run the command again.")
				}
			} else {
				result.Next = append(result.Next, "docker compose --project-directory "+dir+" up -d")
			}
			s.Out.Data(result, func(any) string {
				return renderInstall(result, fmt.Sprintf("Installed Checkend %s for Docker in %s.", result.Version, dir))
			})
			return nil
		},
	}
}

// dockerCompose runs `docker compose` for the compose.yaml in dir on this machine.
func dockerCompose(s *Session, dir string, args ...string) ([]byte, error) {
	return s.Host.Run(nil, "docker", append([]string{"compose", "--project-directory", dir}, args...)...)
}

// dockerRunning reports whether the checkend service runs.
func dockerRunning(s *Session, dir string) bool {
	out, err := dockerCompose(s, dir, "ps", "-q", "checkend")
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// dockerBackup writes a consistent copy of the database to file in the volume.
func dockerBackup(s *Session, dir, file string) error {
	if _, err := dockerCompose(s, dir, "exec", "-T", "checkend", "mkdir", "-p", "/data/backups"); err != nil {
		return clierr.New(clierr.Failure, fmt.Sprintf("Cannot reach the checkend service: %v", err), "Check `docker compose --project-directory "+dir+" ps`.")
	}
	if _, err := dockerCompose(s, dir, "exec", "-T", "checkend", "node", "scripts/backup.ts", file); err != nil {
		return clierr.New(clierr.Failure, fmt.Sprintf("The backup failed: %v", err), "Nothing changed. Check `docker compose --project-directory "+dir+" logs checkend`.")
	}
	return nil
}
