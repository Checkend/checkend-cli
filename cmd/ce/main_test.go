package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ce-bin-")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "ce")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	code           int
	stdout, stderr string
}

func ce(t *testing.T, input string, args ...string) result {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command(bin, args...)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "CHECKEND_NO_KEYCHAIN=1"}
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{code, stdout.String(), stderr.String()}
}

func errorOf(t *testing.T, stderr string) map[string]string {
	t.Helper()
	var out map[string]string
	if err := json.Unmarshal([]byte(stderr), &out); err != nil {
		t.Fatalf("stderr is not JSON: %q", stderr)
	}
	return out
}

func TestTheBinaryPrintsTheVersion(t *testing.T) {
	r := ce(t, "", "--version")
	if r.code != 0 || !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(r.stdout) {
		t.Errorf("%d %q", r.code, r.stdout)
	}
}

func TestTheBinaryExits2ForAnUnknownCommand(t *testing.T) {
	r := ce(t, "", "cd", "..")
	if r.code != 2 || errorOf(t, r.stderr)["error"] != `Unknown command "cd".` || r.stdout != "" {
		t.Errorf("%d %q %q", r.code, r.stdout, r.stderr)
	}
}

func TestTheBinaryExits5WhenNobodyIsLoggedIn(t *testing.T) {
	r := ce(t, "", "issue", "list")
	if r.code != 5 || errorOf(t, r.stderr)["hint"] != "Run `ce login`." {
		t.Errorf("%d %q", r.code, r.stderr)
	}
}

func TestTheBinaryReadsTheTokenFromStdinAndReportsAnUnreachableInstance(t *testing.T) {
	r := ce(t, "secret-token\n", "login", "--url", "http://127.0.0.1:9", "--with-token")
	if r.code != 1 || !strings.Contains(errorOf(t, r.stderr)["error"], "Cannot reach http://127.0.0.1:9") || strings.Contains(r.stderr, "secret-token") {
		t.Errorf("%d %q", r.code, r.stderr)
	}
}
