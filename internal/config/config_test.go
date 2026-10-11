package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Checkend/checkend-cli/internal/credstore"
	"github.com/Checkend/checkend-cli/internal/host"
)

func newHost(t *testing.T) (*host.Host, *credstore.Memory) {
	t.Helper()
	keys := &credstore.Memory{}
	home := t.TempDir()
	return &host.Host{Env: map[string]string{}, Home: home, Cwd: home, Keyring: keys}, keys
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSaveTokenUsesTheKeychainAndKeepsTheTokenOutOfTheFile(t *testing.T) {
	h, keys := newHost(t)
	where, err := SaveToken(h, "https://a.example", "ck_secret_1")
	if err != nil || where != StorageKeychain {
		t.Fatalf("SaveToken = %q, %v", where, err)
	}
	if keys.Secrets["https://a.example"] != "ck_secret_1" {
		t.Errorf("keychain: %#v", keys.Secrets)
	}
	file := filepath.Join(h.Home, ".config", "checkend", "config.json")
	text := read(t, file)
	if strings.Contains(text, "ck_secret_1") {
		t.Errorf("the token is in the file: %s", text)
	}
	cfg, _ := ReadUserConfig(h)
	if cfg.Current != "https://a.example" || cfg.Hosts["https://a.example"].Storage != StorageKeychain {
		t.Errorf("config: %#v", cfg)
	}
	token, err := ResolveToken(h, "", "https://a.example")
	if err != nil || token != "ck_secret_1" {
		t.Errorf("ResolveToken = %q, %v", token, err)
	}
}

func TestSaveTokenFallsBackToAFileWithMode600(t *testing.T) {
	h, keys := newHost(t)
	keys.Broken = true
	where, err := SaveToken(h, "https://a.example", "ck_secret_1")
	file := filepath.Join(h.Home, ".config", "checkend", "config.json")
	if err != nil || where != file {
		t.Fatalf("SaveToken = %q, %v", where, err)
	}
	if !strings.Contains(read(t, file), "ck_secret_1") {
		t.Error("the token is not in the file")
	}
	for path, mode := range map[string]os.FileMode{file: 0o600, filepath.Dir(file): 0o700} {
		if runtime.GOOS == "windows" {
			break
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != mode {
			t.Errorf("%s: mode %o", path, info.Mode().Perm())
		}
	}
	token, _ := ResolveToken(h, "", "https://a.example")
	if token != "ck_secret_1" {
		t.Errorf("ResolveToken = %q", token)
	}
}

func TestNoKeychainSettingForcesTheFile(t *testing.T) {
	h, keys := newHost(t)
	h.Env["CHECKEND_NO_KEYCHAIN"] = "1"
	if _, err := SaveToken(h, "https://a.example", "ck_secret_1"); err != nil {
		t.Fatal(err)
	}
	if len(keys.Secrets) != 0 {
		t.Errorf("keychain used: %#v", keys.Secrets)
	}
}

func TestDeleteTokenRemovesTheKeychainEntryAndTheHost(t *testing.T) {
	h, keys := newHost(t)
	_, _ = SaveToken(h, "https://a.example", "one")
	_, _ = SaveToken(h, "https://b.example", "two")
	removed, err := DeleteToken(h, "https://b.example")
	if err != nil || !removed {
		t.Fatalf("DeleteToken = %v, %v", removed, err)
	}
	if _, ok := keys.Secrets["https://b.example"]; ok {
		t.Error("the keychain entry is still there")
	}
	cfg, _ := ReadUserConfig(h)
	if cfg.Current != "" || len(cfg.Hosts) != 1 {
		t.Errorf("config: %#v", cfg)
	}
	removed, _ = DeleteToken(h, "https://b.example")
	if removed {
		t.Error("a second delete removed something")
	}
}

func TestATokenIsOnlyForItsOwnURL(t *testing.T) {
	h, _ := newHost(t)
	_, _ = SaveToken(h, "https://a.example", "one")
	token, _ := ResolveToken(h, "", "https://b.example")
	if token != "" {
		t.Errorf("token for another URL: %q", token)
	}
}

func TestResolveOrder(t *testing.T) {
	h, _ := newHost(t)
	_, _ = SaveToken(h, "https://saved.example", "saved")
	if u, _ := ResolveURL(h, ""); u != "https://saved.example" {
		t.Errorf("saved URL: %q", u)
	}
	h.Env["CHECKEND_URL"] = "https://env.example/"
	if u, _ := ResolveURL(h, ""); u != "https://env.example" {
		t.Errorf("env URL: %q", u)
	}
	if u, _ := ResolveURL(h, "https://flag.example"); u != "https://flag.example" {
		t.Errorf("flag URL: %q", u)
	}
	h.Env["CHECKEND_ADMIN_TOKEN"] = "admin"
	if tok, _ := ResolveToken(h, "", "https://saved.example"); tok != "admin" {
		t.Errorf("admin env token: %q", tok)
	}
	h.Env["CHECKEND_TOKEN"] = "env"
	if tok, _ := ResolveToken(h, "", "https://saved.example"); tok != "env" {
		t.Errorf("env token: %q", tok)
	}
	if tok, _ := ResolveToken(h, "flag", "https://saved.example"); tok != "flag" {
		t.Errorf("flag token: %q", tok)
	}
}

func TestReadsAV01ConfigWithTheTokenInTheFile(t *testing.T) {
	h, _ := newHost(t)
	dir := filepath.Join(h.Home, ".config", "checkend")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"current":"https://a.example","hosts":{"https://a.example":{"token":"old"}}}`), 0o600)
	if tok, _ := ResolveToken(h, "", "https://a.example"); tok != "old" {
		t.Errorf("token: %q", tok)
	}
}

func TestXDGConfigHome(t *testing.T) {
	h, _ := newHost(t)
	h.Env["XDG_CONFIG_HOME"] = filepath.Join(h.Home, "xdg")
	if got := Dir(h); got != filepath.Join(h.Home, "xdg", "checkend") {
		t.Errorf("Dir = %q", got)
	}
}

func TestFindLinkStopsAtTheRepoRoot(t *testing.T) {
	h, _ := newHost(t)
	repo := filepath.Join(h.Home, "repo")
	deep := filepath.Join(repo, "src", "deep")
	_ = os.MkdirAll(deep, 0o755)
	_ = os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(h.Home, LinkFile), []byte(`{"project":"outside"}`), 0o644)
	h.Cwd = deep
	if link, err := FindLink(h); err != nil || link != nil {
		t.Errorf("FindLink outside the repo: %#v %v", link, err)
	}
	_ = os.WriteFile(filepath.Join(repo, LinkFile), []byte(`{"project":4817305926}`), 0o644)
	link, err := FindLink(h)
	if err != nil || link == nil || link.Project != "4817305926" {
		t.Errorf("FindLink: %#v %v", link, err)
	}
	if root := FindRepoRoot(deep); root != repo {
		t.Errorf("FindRepoRoot = %q", root)
	}
}
