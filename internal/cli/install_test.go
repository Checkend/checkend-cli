package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func installer(t *testing.T) (*FakeReleases, *Harness) {
	f := NewFakeReleases(t)
	cli := NewHarness(t)
	cli.Releases = f
	return f, cli
}

// readEnv reads a .env file.
func readEnv(t *testing.T, file string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			env[k] = v
		}
	}
	return env
}

func noSecrets(t *testing.T, r Result, env map[string]string) {
	t.Helper()
	for _, name := range []string{"ADMIN_TOKEN", "ENCRYPTION_KEYS"} {
		if env[name] == "" {
			t.Fatalf("no %s", name)
		}
		NotContains(t, r.All, env[name])
	}
}

func TestInstallSaysWhenNoServerReleaseIsPublished(t *testing.T) {
	_, cli := installer(t)
	r := cli.Run([]string{"install", "docker", "--yes"})
	Code(t, r, 1)
	Contains(t, r.Stderr, "No Checkend server release is published yet")
	if Exists(filepath.Join(cli.Repo, "checkend")) {
		t.Error("it wrote files")
	}
}

func TestInstallNeedsYesWithoutATerminal(t *testing.T) {
	f, cli := installer(t)
	f.Publish("0.3.0")
	r := cli.Run([]string{"install", "docker"})
	Code(t, r, 2)
	Contains(t, r.Stderr, "--yes")
	Equal(t, len(f.Downloads), 0)
	for _, argv := range [][]string{{"install"}, {"install", "podman"}, {"install", "docker", "--bogus"}, {"install", "docker", "--port", "x", "--yes"}, {"install", "docker", "--port", "70000", "--yes"}} {
		Code(t, cli.Run(argv), 2)
	}
	Equal(t, len(f.Downloads), 0)
}

func TestInstallDockerWritesAComposeFileAndASecretEnvFile(t *testing.T) {
	f, cli := installer(t)
	rel := f.Publish("0.3.0")
	r := cli.Run([]string{"install", "docker", "--yes"})
	Code(t, r, 0)
	dir := filepath.Join(cli.Repo, "checkend")
	compose, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	digest := rel.Manifest["image"].(map[string]any)["digest"].(string)
	Contains(t, string(compose), "image: ghcr.io/checkend/checkend@"+digest)
	NotContains(t, string(compose), ":latest")
	Contains(t, string(compose), `"127.0.0.1:8787:8787"`)
	Contains(t, string(compose), "restart: unless-stopped")
	Contains(t, string(compose), "checkend-data:/data")
	Contains(t, string(compose), "scripts/backup.ts")
	env := readEnv(t, filepath.Join(dir, ".env"))
	Matches(t, env["ADMIN_TOKEN"], `^[0-9a-f]{64}$`)
	Matches(t, env["ENCRYPTION_KEYS"], `^k1:[A-Za-z0-9+/]{43}=$`)
	Matches(t, env["SETUP_CODE"], `^1791633600\.[A-Za-z0-9_-]{43}$`)
	Equal(t, env["PUBLIC_URL"], "http://localhost:8787")
	if info, _ := os.Stat(filepath.Join(dir, ".env")); runtime.GOOS != "windows" {
		Equal(t, info.Mode().Perm(), os.FileMode(0o600))
	}
	noSecrets(t, r, env)

	out := r.JSON(t)
	code := strings.SplitN(env["SETUP_CODE"], ".", 2)[1]
	Equal(t, out["setup_url"], "http://localhost:8787/setup#"+code)
	Equal(t, out["url"], "http://localhost:8787")
	Equal(t, out["version"], "0.3.0")
	Contains(t, strings.Join(toStrings(out["next"]), "\n"), "docker compose --project-directory "+dir+" up -d")
	// The admin token is the login of this instance, in the keychain.
	Equal(t, cli.Keyring.Secrets["http://localhost:8787"], env["ADMIN_TOKEN"])
	// Only the Docker assets: no Worker bundle.
	Equal(t, f.Downloads["checkend-worker-0.3.0.tar.gz"], 0)
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestInstallDockerAgainKeepsTheSecretsAndMakesANewSetupCode(t *testing.T) {
	f, cli := installer(t)
	f.Publish("0.3.0")
	dir := filepath.Join(cli.Repo, "checkend")
	Code(t, cli.Run([]string{"install", "docker", "--yes"}), 0)
	first := readEnv(t, filepath.Join(dir, ".env"))
	f.Publish("0.4.0")
	r := cli.Run([]string{"install", "docker", "--yes", "--plain"})
	Code(t, r, 0)
	second := readEnv(t, filepath.Join(dir, ".env"))
	Equal(t, second["ADMIN_TOKEN"], first["ADMIN_TOKEN"])
	Equal(t, second["ENCRYPTION_KEYS"], first["ENCRYPTION_KEYS"])
	if second["SETUP_CODE"] == first["SETUP_CODE"] {
		t.Error("the setup code did not change")
	}
	Contains(t, r.Stdout, "Checkend 0.4.0")
	Contains(t, r.Stdout, "/setup#")
	noSecrets(t, r, second)
	// An install record (no secrets) for upgrade, backup, status and uninstall.
	data, _ := os.ReadFile(filepath.Join(cli.Home, ".config", "checkend", "installs.json"))
	var installs map[string]map[string]any
	if err := json.Unmarshal(data, &installs); err != nil {
		t.Fatal(err)
	}
	Equal(t, installs["http://localhost:8787"]["kind"], "docker")
	Equal(t, installs["http://localhost:8787"]["version"], "0.4.0")
	Equal(t, installs["http://localhost:8787"]["dir"], dir)
	NotContains(t, string(data), second["ADMIN_TOKEN"])
}

func TestInstallDockerShowsThePlanAndAsksFirst(t *testing.T) {
	f, cli := installer(t)
	f.Publish("0.3.0")
	r := cli.Run([]string{"install", "docker", "--dir", "srv", "--port", "9000"}, Options{TTY: true, Answers: []string{"n"}})
	Code(t, r, 2)
	Contains(t, r.Stderr, "Checkend 0.3.0")
	Contains(t, r.Stderr, filepath.Join(cli.Repo, "srv", "compose.yaml"))
	Contains(t, r.Stderr, "Nothing changed")
	if Exists(filepath.Join(cli.Repo, "srv")) {
		t.Error("it wrote files after a no")
	}
	r = cli.Run([]string{"install", "docker", "--dir", "srv", "--port", "9000", "--version", "0.3.0"}, Options{TTY: true, Answers: []string{"y"}})
	Code(t, r, 0)
	Matches(t, r.Stdout, regexp.QuoteMeta("http://localhost:9000/setup#"))
	NotContains(t, r.All, readEnv(t, filepath.Join(cli.Repo, "srv", ".env"))["ADMIN_TOKEN"])
}
