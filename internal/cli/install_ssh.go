package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/config"
	"github.com/Checkend/checkend-cli/internal/sshhost"
)

// dockerKeyFingerprint is the fingerprint of the signing key of Docker's apt repository.
const dockerKeyFingerprint = "9DC858229FC7DD38854AE2D88D81803C0EBFCD88"

// supportedOS are the Ubuntu and Debian LTS versions that `ce install ssh` supports.
var supportedOS = map[string][]string{"ubuntu": {"22.04", "24.04", "26.04"}, "debian": {"12", "13"}}

var (
	domainPattern      = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	remoteDirPattern   = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,200}$`)
	fingerprintPattern = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
	tunnelPattern      = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{20,1000}$`)
	envValuePattern    = regexp.MustCompile(`^[^\r\n]*$`)
)

// sshServer is a server that ce reaches with the system ssh client.
type sshServer struct {
	s      *Session
	target sshhost.Target
	port   string
	known  string
}

func (r *sshServer) args() []string {
	return []string{"-p", r.port, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + r.known, "-o", "ConnectTimeout=15", "--", r.target.String(), "sh -s"}
}

// run sends a script on stdin to `sh -s` on the server; secrets go only through stdin.
func (r *sshServer) run(script string) ([]byte, error) {
	return r.s.Host.Run([]byte(script), "ssh", r.args()...)
}

// verifyHostKey decides which host key ssh may accept, and writes it to ce's known_hosts. The
// key is in the user's known_hosts, matches --host-key, or the user confirms it.
func (r *sshServer) verifyHostKey(pinned string) (string, error) {
	s := r.s
	out, err := s.Host.Run(nil, "ssh-keyscan", "-p", r.port, "-T", "10", "-t", "ed25519,ecdsa,rsa", "--", r.target.Host)
	keys := sshhost.ParseKeyscan(string(out))
	if len(keys) == 0 {
		return "", clierr.New(clierr.Failure, fmt.Sprintf("Cannot read the SSH host key of %s (port %s): %v", r.target.Host, r.port, err),
			"Check the host, the port and the firewall of the server.")
	}
	name := r.target.KnownName(r.port)
	var chosen *sshhost.Key
	var fingerprints []string
	for i, k := range keys {
		fingerprints = append(fingerprints, k.Type+" "+k.Fingerprint())
		if pinned != "" && k.Fingerprint() == pinned {
			chosen = &keys[i]
		}
	}
	if pinned != "" && chosen == nil {
		return "", clierr.New(clierr.Failure, fmt.Sprintf("The SSH host key of %s does not match %s.", r.target.Host, pinned),
			"Check the fingerprint in the console of your provider. Do not continue if it is different: someone may be in between.")
	}
	if chosen == nil {
		userKnown, _ := os.ReadFile(filepath.Join(s.Host.Home, ".ssh", "known_hosts"))
		for i, k := range keys {
			if sshhost.Known(string(userKnown), name, k) {
				chosen = &keys[i]
				break
			}
		}
	}
	if chosen == nil {
		lines := append([]string{fmt.Sprintf("The SSH host key of %s is not known yet:", r.target.Host)}, fingerprints...)
		if !s.Host.Interactive {
			return "", clierr.UsageError(strings.Join(lines, "\n  "),
				"Check the fingerprint in the console of your provider, then run again with --host-key <SHA256:…>.")
		}
		s.Out.Menu(lines)
		answer, err := s.Host.Prompt("Does the first fingerprint match the one in the console of your provider? Type yes: ", false)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(answer) != "yes" {
			return "", clierr.UsageError("You did not confirm the host key. Nothing changed.", "Run the command again with --host-key <SHA256:…>.")
		}
		chosen = &keys[0]
	}
	// ce's own known_hosts: only the confirmed key of this name.
	old, _ := os.ReadFile(r.known)
	var lines []string
	for _, line := range strings.Split(string(old), "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] != name {
			lines = append(lines, line)
		}
	}
	lines = append(lines, sshhost.Line(name, *chosen))
	if err := config.WriteFile(r.known, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	return chosen.Fingerprint(), nil
}

// probe is what the server has: its OS, Docker and the current .env.
type probe struct {
	OS, Version, Docker, Sudo string
	Env                       string
}

func (p probe) label() string {
	name := map[string]string{"ubuntu": "Ubuntu", "debian": "Debian"}[p.OS]
	if name == "" {
		name = p.OS
	}
	return name + " " + p.Version
}

const sudoLine = `SUDO=""; if [ "$(id -u)" != 0 ]; then SUDO="sudo -n"; fi` + "\n"

func probeScript(dir string) string {
	return "# ce:probe\nset -u\ndir=" + shellQuote(dir) + "\n" + sudoLine + `. /etc/os-release 2>/dev/null || true
echo "os=${ID:-unknown}"
echo "version=${VERSION_ID:-}"
if [ -n "$SUDO" ] && ! sudo -n true 2>/dev/null; then echo "sudo=no"; else echo "sudo=yes"; fi
if $SUDO docker compose version >/dev/null 2>&1; then echo "docker=yes"; else echo "docker=no"; fi
if $SUDO test -f "$dir/.env"; then echo "env=$($SUDO base64 -w0 "$dir/.env")"; else echo "env="; fi
`
}

func (r *sshServer) probe(dir string) (probe, error) {
	out, err := r.run(probeScript(dir))
	if err != nil {
		return probe{}, clierr.New(clierr.Failure, fmt.Sprintf("Cannot connect to %s with ssh: %v", r.target, err),
			fmt.Sprintf("Check that `ssh -p %s %s` works with your SSH agent (ssh-add -l).", r.port, r.target))
	}
	var p probe
	for _, line := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch k {
		case "os":
			p.OS = v
		case "version":
			p.Version = v
		case "docker":
			p.Docker = v
		case "sudo":
			p.Sudo = v
		case "env":
			data, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return p, clierr.Fail(fmt.Sprintf("Cannot read %s/.env on the server.", dir))
			}
			p.Env = string(data)
		}
	}
	if !slices.Contains(supportedOS[p.OS], p.Version) {
		return p, clierr.New(clierr.Failure, fmt.Sprintf("%s runs %s. ce install ssh supports Ubuntu or Debian LTS: Ubuntu 22.04, 24.04 and 26.04, Debian 12 and 13.", r.target, p.label()),
			"Use a server with one of these versions, or install with Docker yourself (`ce install docker` writes the files).")
	}
	if p.Sudo == "no" {
		return p, clierr.New(clierr.Failure, fmt.Sprintf("%s is not root and has no sudo without a password.", r.target.User), "Connect as root, or allow `sudo -n` for this user.")
	}
	return p, nil
}

// heredoc writes text to a file on the server through stdin; the text has no expansion.
func heredoc(file, text string) string {
	return "$SUDO tee \"" + file + "\" >/dev/null <<'CE_EOF'\n" + text + "CE_EOF\n"
}

func installScript(dir, composeText, caddyfile string, env *envFile, backup string) string {
	var b strings.Builder
	b.WriteString("# ce:install\nset -eu\ndir=" + shellQuote(dir) + "\n" + sudoLine)
	b.WriteString(`. /etc/os-release
if ! $SUDO docker compose version >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  $SUDO dpkg --configure -a
  $SUDO apt-get update -q
  $SUDO apt-get install -y -q ca-certificates curl gnupg
  key=$(mktemp)
  curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o "$key"
  fpr=$(gpg --show-keys --with-colons "$key" | awk -F: '/^fpr:/ {print $10; exit}')
  if [ "$fpr" != "` + dockerKeyFingerprint + `" ]; then
    echo "ce: the key of Docker's apt repository has fingerprint $fpr, not ` + dockerKeyFingerprint + `" >&2
    exit 21
  fi
  $SUDO install -m 0755 -d /etc/apt/keyrings
  $SUDO gpg --batch --yes --dearmor -o /etc/apt/keyrings/docker.gpg "$key"
  rm -f "$key"
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/$ID $VERSION_CODENAME stable" | $SUDO tee /etc/apt/sources.list.d/docker.list >/dev/null
  $SUDO apt-get update -q
  $SUDO apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-compose-plugin
  $SUDO systemctl enable --now docker
fi
`)
	if backup != "" {
		// An upgrade: a consistent copy of the database first, while the old version runs.
		b.WriteString(`if $SUDO test -f "$dir/compose.yaml" && [ -n "$($SUDO docker compose --project-directory "$dir" ps -q checkend 2>/dev/null)" ]; then
  $SUDO docker compose --project-directory "$dir" exec -T checkend mkdir -p /data/backups
  $SUDO docker compose --project-directory "$dir" exec -T checkend node scripts/backup.ts ` + backup + `
fi
`)
	}
	b.WriteString(`umask 077
$SUDO install -d -m 700 "$dir"
`)
	b.WriteString(heredoc("$dir/.env.new", env.String()))
	b.WriteString("$SUDO chmod 600 \"$dir/.env.new\"\n$SUDO mv \"$dir/.env.new\" \"$dir/.env\"\n")
	b.WriteString(heredoc("$dir/compose.yaml", composeText))
	if caddyfile != "" {
		b.WriteString(heredoc("$dir/Caddyfile", caddyfile))
	}
	b.WriteString(`compose() { $SUDO docker compose --project-directory "$dir" "$@"; }
compose pull -q
compose up -d --remove-orphans
for _ in $(seq 1 60); do
  if [ "$(compose ps --format '{{.Health}}' checkend 2>/dev/null)" = healthy ]; then break; fi
  sleep 2
done
health=$(compose exec -T checkend node -e "fetch('http://127.0.0.1:8787/health').then(r => r.text()).then(t => console.log(t))" 2>/dev/null || true)
echo "health=$health"
[ -n "$health" ]
`)
	return b.String()
}

// checkDomain checks that the domain points at the server, so that Caddy gets a certificate.
func checkDomain(s *Session, domain, host string) error {
	server := []string{host}
	if net.ParseIP(host) == nil {
		addrs, err := s.Host.LookupHost(host)
		if err != nil {
			return clierr.New(clierr.Failure, fmt.Sprintf("Cannot resolve %s: %v", host, err), "Check the host name of the server.")
		}
		server = addrs
	}
	addrs, err := s.Host.LookupHost(domain)
	if err == nil {
		for _, a := range addrs {
			if slices.Contains(server, a) {
				return nil
			}
		}
	}
	return clierr.New(clierr.Failure,
		fmt.Sprintf("%s does not point at the server (%s): it resolves to %s.", domain, strings.Join(server, ", "), strings.Join(addrs, ", ")),
		fmt.Sprintf("Add a DNS A (or AAAA) record %s → %s, wait for it, then run again. Or use --tunnel.", domain, server[0]))
}

func installSSHCommand() *Command {
	return &Command{
		Path:    []string{"install", "ssh"},
		Summary: "Install Checkend on your server over SSH (Ubuntu or Debian LTS; Docker; HTTPS)",
		Usage:   "<user@host> --domain <domain> [--tunnel] [--host-key <SHA256:…>] [--port <ssh port>] [--dir <dir>] [--version <v>] [--yes]",
		Args:    []string{"user@host"},
		Details: []string{
			"  --domain <domain>        The public host name of Checkend, for example errors.example.com",
			"  --tunnel                 HTTPS through a Cloudflare Tunnel (token in CHECKEND_TUNNEL_TOKEN),",
			"                           not Caddy; the domain is the public hostname of the tunnel",
			"  --host-key <SHA256:…>    The fingerprint of the SSH host key (needed with --yes for a new host)",
			"  --port <port>            The SSH port (default: 22)",
			"  --dir <dir>              The folder on the server (default: /opt/checkend)",
			"  --version <v>            A server release (default: the latest one)",
			"  --yes                    Do not ask (for agents); the plan is still printed on stderr",
			"",
			"Uses your SSH agent and verifies the host key (never skipped). Installs Docker from",
			"Docker's apt repository if it is missing, writes compose.yaml (the image pinned by its",
			"digest, a volume, restarts, a nightly backup) and .env, starts Checkend behind Caddy",
			"(the domain must point at the server; ports 80 and 443 open) or a Cloudflare Tunnel,",
			"and prints the setup link. Run it again to update: the secrets on the server stay.",
		},
		Options: map[string]Kind{"domain": String, "tunnel": Bool, "host-key": String, "port": String, "dir": String, "version": String, "yes": Bool},
		Run: func(s *Session) error {
			target, err := sshhost.ParseTarget(s.Args[0])
			if err != nil {
				return clierr.UsageError(fmt.Sprintf("%q is not a valid SSH target: %v.", s.Args[0], err), "Example: ce install ssh root@203.0.113.7 --domain errors.example.com")
			}
			domain := strings.ToLower(s.String("domain"))
			if !domainPattern.MatchString(domain) {
				return clierr.UsageError("Give the domain of Checkend with --domain, for example errors.example.com (no https://).",
					"With Caddy, it must have a DNS record that points at the server.")
			}
			port, err := portOption(s, 22)
			if err != nil {
				return err
			}
			dir := s.String("dir")
			if dir == "" {
				dir = "/opt/checkend"
			}
			if !remoteDirPattern.MatchString(dir) || path.Clean(dir) != dir || dir == "/" {
				return clierr.UsageError(fmt.Sprintf("%q is not a valid folder on the server.", dir), "Use an absolute path, for example /opt/checkend.")
			}
			pinned := s.String("host-key")
			if pinned != "" && !fingerprintPattern.MatchString(pinned) {
				return clierr.UsageError("--host-key must be a SHA256 fingerprint, for example SHA256:abc… (43 characters after SHA256:).",
					"Find it in the console of your provider, or run `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the server.")
			}
			tunnel := s.Bool("tunnel")
			tunnelToken := s.Host.Getenv("CHECKEND_TUNNEL_TOKEN")
			s.Out.Hide(tunnelToken)
			if tunnel && tunnelToken == "" && !s.Host.Interactive && s.upgrade == nil {
				return clierr.UsageError("--tunnel needs the token of the Cloudflare Tunnel in CHECKEND_TUNNEL_TOKEN.",
					"Create a tunnel in the Cloudflare dashboard (Zero Trust → Networks → Tunnels), route the domain to http://checkend:8787, and set the token.")
			}
			if tunnelToken != "" && !tunnelPattern.MatchString(tunnelToken) {
				return clierr.UsageError("CHECKEND_TUNNEL_TOKEN is not a tunnel token.", "Copy the token of the tunnel again from the Cloudflare dashboard.")
			}
			if err := needApproval(s, "ce install ssh"); err != nil {
				return err
			}
			if !tunnel {
				if err := checkDomain(s, domain, target.Host); err != nil {
					return err
				}
			}
			rel, err := pickRelease(s)
			if err != nil {
				return err
			}
			proxy, proxyName := "caddy", "caddy"
			if tunnel {
				proxy, proxyName = "tunnel", "cloudflared"
			}
			proxyImage := rel.Manifest.Proxies[proxyName]
			if proxyImage == "" {
				return clierr.New(clierr.Failure, fmt.Sprintf("Checkend %s has no pinned %s image in its release manifest.", rel.Manifest.Version, proxyName),
					"Use a newer release (--version), or install with `ce install docker` and your own proxy.")
			}
			srv := &sshServer{s: s, target: target, port: fmt.Sprint(port), known: filepath.Join(config.Dir(s.Host), "known_hosts")}
			fingerprint, err := srv.verifyHostKey(pinned)
			if err != nil {
				return err
			}
			p, err := srv.probe(dir)
			if err != nil {
				return err
			}
			env := parseEnv(p.Env)
			oldCode := env.values["SETUP_CODE"]
			for _, k := range env.keys {
				if !envValuePattern.MatchString(env.values[k]) {
					return clierr.Fail(fmt.Sprintf("%s/.env on the server has a value that ce cannot keep.", dir))
				}
			}
			code, kept := env.secrets(s.Host.Now(), s.upgrade == nil)
			url := "https://" + domain
			env.set("PUBLIC_URL", url)
			env.set("TRUSTED_PROXIES", "private")
			if tunnel {
				if tunnelToken == "" {
					tunnelToken = env.values["TUNNEL_TOKEN"]
				}
				if tunnelToken == "" {
					if tunnelToken, err = s.Host.Prompt("Token of the Cloudflare Tunnel: ", true); err != nil {
						return err
					}
					tunnelToken = strings.TrimSpace(tunnelToken)
					if !tunnelPattern.MatchString(tunnelToken) {
						return clierr.UsageError("That is not a tunnel token.", "Copy the token of the tunnel again from the Cloudflare dashboard.")
					}
				}
				env.set("TUNNEL_TOKEN", tunnelToken)
				s.Out.Hide(tunnelToken)
			}
			for _, k := range []string{"ADMIN_TOKEN", "ENCRYPTION_KEYS"} {
				s.Out.Hide(env.values[k])
			}
			composeText := compose(composeOptions{Image: rel.Manifest.Image.Ref(), Proxy: proxy, ProxyImage: proxyImage})
			caddyfile, front := "", fmt.Sprintf("Caddy for HTTPS on %s (ports 80 and 443 must be open)", domain)
			if tunnel {
				front = fmt.Sprintf("a Cloudflare Tunnel for %s (no open ports)", domain)
			} else {
				caddyfile = domain + " {\n\treverse_proxy checkend:8787\n}\n"
			}
			plan := []string{
				fmt.Sprintf("Server: %s (%s), SSH host key %s.", target, p.label(), fingerprint),
				releaseLine(s, rel),
				"Image: " + rel.Manifest.Image.Ref(),
			}
			if p.Docker != "yes" {
				plan = append(plan, "Install Docker from Docker's apt repository (its key fingerprint is checked).")
			}
			plan = append(plan,
				fmt.Sprintf("Write %s/compose.yaml (Checkend, a nightly backup, a volume, restarts, %s).", dir, front),
				secretsLine(dir+"/.env", kept, p.Env == "", oldCode != "" && env.values["SETUP_CODE"] == oldCode),
				"Start the containers and wait until Checkend is healthy.",
				fmt.Sprintf("Save the admin token for %s in the OS keychain.", url))
			backup := ""
			if s.upgrade != nil {
				backup = fmt.Sprintf("/data/backups/checkend-before-%s-%s.sqlite", rel.Manifest.Version, s.Host.Now().UTC().Format("20060102-150405"))
				plan = append(plan, "Back up the database to "+backup+" first (while the old version runs).")
			}
			if err := approve(s, plan); err != nil {
				return err
			}
			out, err := srv.run(installScript(dir, composeText, caddyfile, env, backup))
			var health struct {
				Status    string `json:"status"`
				SetupOpen *bool  `json:"setup_open"`
			}
			for _, line := range strings.Split(string(out), "\n") {
				if v, ok := strings.CutPrefix(line, "health="); ok {
					_ = json.Unmarshal([]byte(v), &health)
				}
			}
			if err != nil || health.Status == "" {
				reason := "it did not become healthy"
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					reason = err.Error()
				}
				return clierr.New(clierr.Failure, fmt.Sprintf("The install on %s did not finish: %s.", target, reason),
					fmt.Sprintf("See the logs: ssh -p %d %s 'docker compose --project-directory %s logs --tail 50'. Then run the command again.", port, target, dir))
			}
			login, err := recordInstall(s, url, config.Install{Kind: "ssh", Version: rel.Manifest.Version, Migrations: rel.Manifest.Migrations.Latest,
				Dir: dir, Target: target.String(), Port: fmt.Sprint(port), HostKey: fingerprint, Proxy: proxy}, env.values["ADMIN_TOKEN"])
			if err != nil {
				return err
			}
			result := installResult{Kind: "ssh", URL: url, Version: rel.Manifest.Version, Dir: dir, Target: target.String(), Login: login, Next: []string{}}
			if code != "" && (health.SetupOpen == nil || *health.SetupOpen) {
				result.SetupURL = url + "/setup#" + code
			} else if code != "" {
				result.Next = append(result.Next, "Setup is done: sign in at "+url)
			}
			s.Out.Data(result, func(any) string {
				return renderInstall(result, fmt.Sprintf("Installed Checkend %s on %s at %s.", result.Version, target, url))
			})
			return nil
		},
	}
}
