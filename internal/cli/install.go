package cli

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
	return []*Command{installDockerCommand()}
}

// releaseSource is the source of the server releases (tests replace it through the host).
func releaseSource(s *Session) serverrelease.Source {
	src := serverrelease.Default(s.Host.HTTP)
	if s.Host.ReleaseAPI != "" {
		src.API, src.Keys = s.Host.ReleaseAPI, s.Host.ReleaseKeys
	}
	return src
}

// pickRelease is the release of --version, else the latest one.
func pickRelease(s *Session) (*serverrelease.Release, error) {
	if v := s.String("version"); v != "" {
		return releaseSource(s).Get(v)
	}
	return releaseSource(s).Latest()
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

func releaseLine(r *serverrelease.Release) string {
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

// secrets fills ADMIN_TOKEN and ENCRYPTION_KEYS when they are missing, and always writes a new
// setup code. It returns the setup code for the link and the keys that it kept.
func (e *envFile) secrets(now time.Time) (code string, kept []string) {
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
	stored, code := newSetupCode(now)
	e.set("SETUP_CODE", stored)
	return code, kept
}

func secretsLine(file string, kept []string, isNew bool) string {
	if !isNew && len(kept) == 2 {
		return fmt.Sprintf("Write %s (mode 600): keep ADMIN_TOKEN and ENCRYPTION_KEYS; write a new setup code.", file)
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
	return config.SaveToken(s.Host, url, adminToken)
}

type installResult struct {
	Kind     string   `json:"kind"`
	URL      string   `json:"url"`
	Version  string   `json:"version"`
	Dir      string   `json:"dir,omitempty"`
	Target   string   `json:"target,omitempty"`
	Files    []string `json:"files,omitempty"`
	Login    string   `json:"login"`
	SetupURL string   `json:"setup_url,omitempty"`
	Next     []string `json:"next"`
}

func renderInstall(r installResult, title string) string {
	lines := []string{title}
	if len(r.Files) > 0 {
		lines = append(lines, "Wrote "+strings.Join(r.Files, ", ")+" (.env has the secrets; mode 600).")
	}
	where := "the OS keychain"
	if r.Login != config.StorageKeychain {
		where = r.Login + " (mode 600)"
	}
	lines = append(lines, fmt.Sprintf("Saved the admin token for %s in %s.", r.URL, where), "")
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
			code, kept := env.secrets(s.Host.Now())
			env.set("PUBLIC_URL", url)
			s.Out.Hide(env.values["ADMIN_TOKEN"])
			s.Out.Hide(env.values["ENCRYPTION_KEYS"])
			if err := approve(s, []string{
				releaseLine(rel),
				"Image: " + rel.Manifest.Image.Ref(),
				fmt.Sprintf("Write %s (Checkend on 127.0.0.1:%d, a nightly backup, a volume, restarts).", composePath, port),
				secretsLine(envPath, kept, readErr != nil),
				fmt.Sprintf("Save the admin token for %s in the OS keychain.", url),
				"Start nothing: you run `docker compose up -d` after this.",
			}); err != nil {
				return err
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
				Files: []string{composePath, envPath}, Login: login, SetupURL: url + "/setup#" + code,
				Next: []string{"docker compose --project-directory " + dir + " up -d"}}
			s.Out.Data(result, func(any) string {
				return renderInstall(result, fmt.Sprintf("Installed Checkend %s for Docker in %s.", result.Version, dir))
			})
			return nil
		},
	}
}
