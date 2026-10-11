package cli_test

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

var hostBlob = base64.StdEncoding.EncodeToString([]byte("\x00\x00\x00\x0bssh-ed25519\x00\x00\x00\x20test-host-key-0123456789abcdef01"))

func hostFingerprint() string {
	raw, _ := base64.StdEncoding.DecodeString(hostBlob)
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// fakeServer is an Ubuntu server over SSH. env is its /opt/checkend/.env.
type fakeServer struct {
	os, version, env string
	installed        []string
	healthy          bool
}

func sshServer(t *testing.T) (*FakeReleases, *Harness, *fakeServer) {
	f, cli := installer(t)
	f.Publish("0.3.0")
	srv := &fakeServer{os: "ubuntu", version: "24.04", healthy: true}
	cli.DNS = map[string][]string{"errors.example.com": {"203.0.113.7"}}
	cli.Exec = func(stdin []byte, name string, args ...string) ([]byte, error) {
		script := string(stdin)
		switch {
		case name == "ssh-keyscan":
			return []byte("203.0.113.7 ssh-ed25519 " + hostBlob + "\n"), nil
		case name == "ssh" && strings.Contains(script, "# ce:probe"):
			return []byte("os=" + srv.os + "\nversion=" + srv.version + "\ndocker=no\nenv=" + base64.StdEncoding.EncodeToString([]byte(srv.env)) + "\n"), nil
		case name == "ssh" && strings.Contains(script, "# ce:install"):
			srv.installed = append(srv.installed, script)
			start := strings.Index(script, "$SUDO tee \"$dir/.env.new\" >/dev/null <<'CE_EOF'\n")
			if start >= 0 {
				rest := script[start+len("$SUDO tee \"$dir/.env.new\" >/dev/null <<'CE_EOF'\n"):]
				srv.env = rest[:strings.Index(rest, "CE_EOF\n")]
			}
			if !srv.healthy {
				return []byte("health=\n"), errors.New("exit status 1")
			}
			return []byte(`health={"status":"ok","setup_open":true}` + "\n"), nil
		}
		return nil, errors.New("unexpected " + name)
	}
	return f, cli, srv
}

var sshArgs = []string{"install", "ssh", "root@203.0.113.7", "--domain", "errors.example.com"}

func sshCalls(cli *Harness) []Call {
	var out []Call
	for _, c := range cli.Calls {
		if c.Name == "ssh" {
			out = append(out, c)
		}
	}
	return out
}

func TestInstallSSHChecksItsInputBeforeAnyCall(t *testing.T) {
	_, cli, _ := sshServer(t)
	for _, argv := range [][]string{
		{"install", "ssh", "--yes", "--domain", "errors.example.com"},
		{"install", "ssh", "-oProxyCommand=x", "--yes", "--domain", "errors.example.com"},
		{"install", "ssh", "root@host;rm", "--yes", "--domain", "errors.example.com"},
		{"install", "ssh", "root@203.0.113.7", "--yes"},
		{"install", "ssh", "root@203.0.113.7", "--yes", "--domain", "https://errors.example.com"},
		{"install", "ssh", "root@203.0.113.7", "--yes", "--domain", "errors.example.com", "--dir", "/opt/../etc"},
		{"install", "ssh", "root@203.0.113.7", "--yes", "--domain", "errors.example.com", "--host-key", "MD5:aa"},
		{"install", "ssh", "root@203.0.113.7", "--domain", "errors.example.com"},
	} {
		Code(t, cli.Run(argv), 2)
	}
	Equal(t, len(cli.Calls), 0)
}

func TestInstallSSHNeverSkipsTheHostKey(t *testing.T) {
	_, cli, _ := sshServer(t)
	r := cli.Run(append(sshArgs, "--yes"))
	Code(t, r, 2)
	Contains(t, r.Stderr, hostFingerprint())
	Contains(t, r.Stderr, "--host-key")
	Equal(t, len(sshCalls(cli)), 0)

	r = cli.Run(append(sshArgs, "--yes", "--host-key", "SHA256:"+strings.Repeat("A", 43)))
	Code(t, r, 1)
	Contains(t, r.Stderr, "does not match")
	Equal(t, len(sshCalls(cli)), 0)
}

func TestInstallSSHInstallsWithAPinnedImageAndCaddy(t *testing.T) {
	f, cli, srv := sshServer(t)
	r := cli.Run(append(sshArgs, "--yes", "--host-key", hostFingerprint()))
	Code(t, r, 0)
	calls := sshCalls(cli)
	Equal(t, len(calls), 2)
	known := filepath.Join(cli.Home, ".config", "checkend", "known_hosts")
	data, _ := os.ReadFile(known)
	Equal(t, string(data), "203.0.113.7 ssh-ed25519 "+hostBlob+"\n")
	for _, c := range calls {
		args := strings.Join(c.Args, " ")
		Contains(t, args, "StrictHostKeyChecking=yes")
		Contains(t, args, "BatchMode=yes")
		Contains(t, args, "UserKnownHostsFile="+known)
		NotContains(t, args, "StrictHostKeyChecking=no")
		Equal(t, c.Args[len(c.Args)-3:], []string{"--", "root@203.0.113.7", "sh -s"})
	}
	script := srv.installed[0]
	image := f.Releases[0].Manifest["image"].(map[string]any)["digest"].(string)
	Contains(t, script, "image: ghcr.io/checkend/checkend@"+image)
	Contains(t, script, "image: docker.io/library/caddy@sha256:")
	Contains(t, script, "errors.example.com {\n\treverse_proxy checkend:8787\n}")
	Contains(t, script, "9DC858229FC7DD38854AE2D88D81803C0EBFCD88")
	NotContains(t, script, "get.docker.com")
	env := readEnv(t, writeTemp(t, srv.env))
	Equal(t, env["PUBLIC_URL"], "https://errors.example.com")
	Equal(t, env["TRUSTED_PROXIES"], "private")
	noSecrets(t, r, env)
	for _, c := range cli.Calls {
		NotContains(t, strings.Join(c.Args, " "), env["ADMIN_TOKEN"])
	}
	out := r.JSON(t)
	Equal(t, out["url"], "https://errors.example.com")
	Equal(t, out["setup_url"], "https://errors.example.com/setup#"+strings.SplitN(env["SETUP_CODE"], ".", 2)[1])
	Equal(t, cli.Keyring.Secrets["https://errors.example.com"], env["ADMIN_TOKEN"])
}

func writeTemp(t *testing.T, text string) string {
	file := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestInstallSSHAcceptsAHostKeyFromKnownHosts(t *testing.T) {
	_, cli, srv := sshServer(t)
	if err := os.MkdirAll(filepath.Join(cli.Home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cli.Home, ".ssh", "known_hosts"), []byte("203.0.113.7 ssh-ed25519 "+hostBlob+"\n"), 0o600)
	Code(t, cli.Run(append(sshArgs, "--yes")), 0)
	first := readEnv(t, writeTemp(t, srv.env))
	// Again: the secrets on the server stay, and so does the setup code (still valid).
	Code(t, cli.Run(append(sshArgs, "--yes")), 0)
	second := readEnv(t, writeTemp(t, srv.env))
	Equal(t, second["ADMIN_TOKEN"], first["ADMIN_TOKEN"])
	Equal(t, second["ENCRYPTION_KEYS"], first["ENCRYPTION_KEYS"])
	Equal(t, second["SETUP_CODE"], first["SETUP_CODE"])
}

func TestInstallSSHAsksForTheHostKeyAndThePlanOnATerminal(t *testing.T) {
	_, cli, _ := sshServer(t)
	r := cli.Run(sshArgs, Options{TTY: true, Answers: []string{"yes", "y"}})
	Code(t, r, 0)
	Contains(t, cli.Prompts[0].Question, "yes")
	Contains(t, r.Stderr, hostFingerprint())
	Contains(t, r.Stderr, "Ubuntu 24.04")
	Contains(t, r.Stderr, "Install Docker")

	_, cli, _ = sshServer(t)
	r = cli.Run(sshArgs, Options{TTY: true, Answers: []string{"no"}})
	Code(t, r, 2)
	Equal(t, len(sshCalls(cli)), 0)
}

func TestInstallSSHRefusesAnOSThatItDoesNotSupport(t *testing.T) {
	_, cli, srv := sshServer(t)
	srv.os, srv.version = "centos", "9"
	r := cli.Run(append(sshArgs, "--yes", "--host-key", hostFingerprint()))
	Code(t, r, 1)
	Contains(t, r.Stderr, "Ubuntu or Debian")
	Equal(t, len(srv.installed), 0)
	srv.os, srv.version = "ubuntu", "23.10"
	Code(t, cli.Run(append(sshArgs, "--yes", "--host-key", hostFingerprint())), 1)
	Equal(t, len(srv.installed), 0)
}

func TestInstallSSHChecksThatTheDomainPointsAtTheServer(t *testing.T) {
	_, cli, srv := sshServer(t)
	cli.DNS["errors.example.com"] = []string{"198.51.100.1"}
	r := cli.Run(append(sshArgs, "--yes", "--host-key", hostFingerprint()))
	Code(t, r, 1)
	Contains(t, r.Stderr, "errors.example.com")
	Contains(t, r.Stderr, "203.0.113.7")
	Equal(t, len(srv.installed), 0)
	Equal(t, len(sshCalls(cli)), 0)
}

func TestInstallSSHWithACloudflareTunnel(t *testing.T) {
	_, cli, srv := sshServer(t)
	delete(cli.DNS, "errors.example.com")
	const tunnel = "test-tunnel-token-0123456789abcdef"
	r := cli.Run(append(sshArgs, "--yes", "--host-key", hostFingerprint(), "--tunnel"), Options{Env: map[string]string{"CHECKEND_TUNNEL_TOKEN": tunnel}})
	Code(t, r, 0)
	script := srv.installed[0]
	Contains(t, script, "image: docker.io/cloudflare/cloudflared@sha256:")
	NotContains(t, script, "caddy")
	NotContains(t, r.All, tunnel)
	Equal(t, readEnv(t, writeTemp(t, srv.env))["TUNNEL_TOKEN"], tunnel)
	for _, c := range cli.Calls {
		if slices.ContainsFunc(c.Args, func(a string) bool { return strings.Contains(a, tunnel) }) {
			t.Error("the tunnel token is on a command line")
		}
	}
	// Without the token: exit 2 before any call.
	cli.Calls = nil
	srv.env = ""
	Code(t, cli.Run(append(sshArgs, "--yes", "--host-key", hostFingerprint(), "--tunnel")), 2)
	Equal(t, len(cli.Calls), 0)
}

func TestInstallSSHReportsAServerThatDoesNotStart(t *testing.T) {
	_, cli, srv := sshServer(t)
	srv.healthy = false
	r := cli.Run(append(sshArgs, "--yes", "--host-key", hostFingerprint()))
	Code(t, r, 1)
	Contains(t, r.Stderr, "docker compose")
	NotContains(t, r.Stdout, "/setup#")
}
