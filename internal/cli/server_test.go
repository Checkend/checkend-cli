package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

// dockerHost fakes `docker compose` on this machine; running says whether Checkend runs.
func dockerHost(cli *Harness, running *bool) {
	cli.Exec = func(stdin []byte, name string, args ...string) ([]byte, error) {
		if name != "docker" {
			return nil, errors.New("unexpected " + name)
		}
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, " ps -q checkend"):
			if *running {
				return []byte("c0ffee\n"), nil
			}
			return nil, nil
		case strings.Contains(joined, "scripts/backup.ts"):
			if !*running {
				return nil, errors.New("service checkend is not running")
			}
			return []byte(`{"message":"checkend: backup written"}` + "\n"), nil
		}
		return nil, nil
	}
}

func dockerArgs(cli *Harness) []string {
	var out []string
	for _, c := range cli.Calls {
		if c.Name == "docker" {
			out = append(out, strings.Join(c.Args, " "))
		}
	}
	return out
}

func installedDocker(t *testing.T) (*FakeReleases, *Harness, string, *bool) {
	f, cli := installer(t)
	f.Publish("0.3.0")
	running := false
	dockerHost(cli, &running)
	Code(t, cli.Run([]string{"install", "docker", "--yes"}), 0)
	return f, cli, filepath.Join(cli.Repo, "checkend"), &running
}

func TestUpgradeDockerPinsTheNewImageAndKeepsTheSetupCode(t *testing.T) {
	f, cli, dir, running := installedDocker(t)
	before := readEnv(t, filepath.Join(dir, ".env"))
	rel := f.Publish("0.4.0")
	*running = true
	r := cli.Run([]string{"upgrade", "--yes"})
	Code(t, r, 0)
	compose, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	Contains(t, string(compose), rel.Manifest["image"].(map[string]any)["digest"].(string))
	Equal(t, readEnv(t, filepath.Join(dir, ".env")), before)
	Contains(t, r.Stderr, "Upgrade Checkend from 0.3.0 to 0.4.0")
	NotContains(t, r.All, "/setup#")
	calls := dockerArgs(cli)
	backup, up := -1, -1
	for i, c := range calls {
		if strings.Contains(c, "scripts/backup.ts /data/backups/checkend-before-0.4.0-") {
			backup = i
		}
		if strings.HasSuffix(c, "up -d --remove-orphans") {
			up = i
		}
	}
	if backup < 0 || up < 0 || backup > up {
		t.Errorf("backup %d, up %d: %v", backup, up, calls)
	}
	Equal(t, r.JSON(t)["version"], "0.4.0")

	// Again: nothing to do.
	r = cli.Run([]string{"upgrade", "--yes"})
	Code(t, r, 0)
	Contains(t, r.Stdout, "already")
}

func TestUpgradeRefusesADowngradeAndOlderMigrations(t *testing.T) {
	f, cli, _, _ := installedDocker(t)
	f.Publish("0.2.0")
	r := cli.Run([]string{"upgrade", "--yes", "--version", "0.2.0"})
	Code(t, r, 1)
	Contains(t, r.Stderr, "--allow-downgrade")
	Code(t, cli.Run([]string{"upgrade", "--yes", "--version", "0.2.0", "--allow-downgrade"}), 0)

	f.Publish("0.1.0", func(m map[string]any) {
		m["migrations"].(map[string]any)["latest"] = "0001_init.sql"
	})
	r = cli.Run([]string{"upgrade", "--yes", "--version", "0.1.0", "--allow-downgrade"})
	Code(t, r, 1)
	Contains(t, r.Stderr, "never run backwards")
}

func TestServerCommandsNeedAnInstallRecord(t *testing.T) {
	_, cli := installer(t)
	for _, argv := range [][]string{{"upgrade", "--yes"}, {"backup"}, {"uninstall", "--yes"}} {
		r := cli.Run(append(argv, "--url", "https://errors.example.com"))
		Code(t, r, 1)
		Contains(t, r.Stderr, "ce install")
	}
	r := cli.Run([]string{"status", "--server", "--url", "https://errors.example.com"})
	Code(t, r, 1)
	Code(t, cli.Run([]string{"uninstall", "--url", "https://errors.example.com"}), 2)
}

func TestBackupDocker(t *testing.T) {
	_, cli, dir, running := installedDocker(t)
	r := cli.Run([]string{"backup"})
	Code(t, r, 1)
	Contains(t, r.Stderr, "not running")
	*running = true
	out := filepath.Join(t.TempDir(), "copy.sqlite")
	r = cli.Run([]string{"backup", "--output", out})
	Code(t, r, 0)
	file := r.JSON(t)["file"].(string)
	Matches(t, file, `^/data/backups/checkend-\d{8}-\d{6}\.sqlite$`)
	Contains(t, strings.Join(dockerArgs(cli), "\n"), "--project-directory "+dir+" cp checkend:"+file+" "+out)
	Equal(t, r.JSON(t)["output"], out)
}

func TestStatusServerShowsTheInstallAndTheLatestRelease(t *testing.T) {
	f, cli := installer(t)
	f.Publish("0.3.0")
	api := NewFakeAPI(t)
	port := api.URL[strings.LastIndex(api.URL, ":")+1:]
	Code(t, cli.Run([]string{"install", "docker", "--yes", "--port", port}), 0)
	f.Publish("0.4.0")
	r := cli.Run([]string{"status", "--server", "--url", "http://localhost:" + port})
	Code(t, r, 0)
	out := r.JSON(t)
	Equal(t, out["health"].(map[string]any)["status"], "ok")
	Equal(t, out["install"].(map[string]any)["kind"], "docker")
	Equal(t, out["install"].(map[string]any)["version"], "0.3.0")
	Equal(t, out["latest"], "0.4.0")
	Equal(t, out["upgrade_available"], true)
	NotContains(t, r.All, readEnv(t, filepath.Join(cli.Repo, "checkend", ".env"))["ADMIN_TOKEN"])
}

func TestUninstallDockerKeepsTheDataUnlessAsked(t *testing.T) {
	_, cli, dir, _ := installedDocker(t)
	r := cli.Run([]string{"uninstall"}, Options{TTY: true, Answers: []string{"nope"}})
	Code(t, r, 2)
	Equal(t, len(dockerArgs(cli)), 0)
	r = cli.Run([]string{"uninstall", "--yes"})
	Code(t, r, 0)
	Equal(t, dockerArgs(cli), []string{"compose --project-directory " + dir + " down"})
	if !Exists(filepath.Join(dir, ".env")) {
		t.Error("the .env with the encryption keys is gone")
	}
	_, ok := cli.Keyring.Secrets["http://localhost:8787"]
	Equal(t, ok, false)
	Code(t, cli.Run([]string{"backup", "--url", "http://localhost:8787"}), 1)
}

func TestUninstallDockerWithItsData(t *testing.T) {
	_, cli, dir, _ := installedDocker(t)
	r := cli.Run([]string{"uninstall", "--delete-data"}, Options{TTY: true, Answers: []string{"delete http://localhost:8787"}})
	Code(t, r, 0)
	Equal(t, dockerArgs(cli), []string{"compose --project-directory " + dir + " down -v"})
	if Exists(filepath.Join(dir, ".env")) || Exists(filepath.Join(dir, "compose.yaml")) {
		t.Error("files are left")
	}
}

func TestUpgradeAndBackupSSH(t *testing.T) {
	f, cli, srv := sshServer(t)
	Code(t, cli.Run(append(sshArgs, "--yes", "--host-key", hostFingerprint())), 0)
	before := readEnv(t, writeTemp(t, srv.env))
	f.Publish("0.4.0")
	r := cli.Run([]string{"upgrade", "--yes"})
	Code(t, r, 0)
	script := srv.installed[len(srv.installed)-1]
	Contains(t, script, "scripts/backup.ts /data/backups/checkend-before-0.4.0-")
	if strings.Index(script, "scripts/backup.ts") > strings.Index(script, "compose up -d") {
		t.Error("the backup is not before the upgrade")
	}
	Equal(t, readEnv(t, writeTemp(t, srv.env)), before)
	NotContains(t, r.All, "/setup#")

	// A changed host key stops the upgrade.
	old := hostBlob
	hostBlob = strings.Replace(hostBlob, "A", "B", 1)
	defer func() { hostBlob = old }()
	f.Publish("0.5.0")
	r = cli.Run([]string{"upgrade", "--yes"})
	Code(t, r, 1)
	Contains(t, r.Stderr, "does not match")
}

func TestUpgradeCloudflareKeepsTheSetupCodeAndRecordsABookmark(t *testing.T) {
	f, cf, cli, env := cloudflare(t)
	Code(t, cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: env}), 0)
	code := cf.Scripts["checkend"].Secrets["SETUP_CODE"]
	f.Publish("0.4.0")
	r := cli.Run([]string{"upgrade", "--yes"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, cf.Scripts["checkend"].Secrets["SETUP_CODE"], code)
	Contains(t, cf.Scripts["checkend"].Modules["worker.js"], "v0.4.0")
	Equal(t, cf.DB("checkend").Bookmarks, 2)

	r = cli.Run([]string{"backup"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, cf.DB("checkend").Bookmarks, 3)
	Contains(t, r.JSON(t)["restore"].(string), "npx wrangler d1 time-travel restore checkend --bookmark=00000003")
}

func TestUninstallCloudflare(t *testing.T) {
	_, cf, cli, env := cloudflare(t)
	Code(t, cli.Run([]string{"install", "cloudflare", "--yes"}, Options{Env: env}), 0)
	r := cli.Run([]string{"uninstall", "--yes"}, Options{Env: env})
	Code(t, r, 0)
	Equal(t, len(cf.Scripts), 0)
	Equal(t, len(cf.Databases), 1)
	Contains(t, r.Stdout, "kept")
}
