// Package release tests the distribution files: install.sh and the Homebrew formula script.
package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const version = "1.2.3"

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// fakeRelease builds ce and serves it as release 1.2.3, with a correct or a wrong checksum.
func fakeRelease(t *testing.T, goodChecksum bool) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "ce")
	build := exec.Command("go", "build", "-ldflags", "-X github.com/Checkend/checkend-cli/internal/version.Version="+version, "-o", bin, "./cmd/ce")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	archive := fmt.Sprintf("ce_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	f, _ := os.Create(filepath.Join(dir, archive))
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	data, _ := os.ReadFile(bin)
	_ = tw.WriteHeader(&tar.Header{Name: "ce", Mode: 0o755, Size: int64(len(data))})
	_, _ = tw.Write(data)
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
	sum, _ := os.ReadFile(filepath.Join(dir, archive))
	digest := fmt.Sprintf("%x", sha256.Sum256(sum))
	if !goodChecksum {
		digest = strings.Repeat("0", 64)
	}
	checksums := fmt.Sprintf("%s  %s\n%s  other.tar.gz\n", digest, archive, strings.Repeat("1", 64))
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/tag/v"+version, http.StatusFound)
	})
	mux.HandleFunc("/tag/", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/download/v"+version+"/"+archive, func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(dir, archive))
	})
	mux.HandleFunc("/download/v"+version+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}

func install(t *testing.T, base string, env ...string) (string, string, error) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command("sh", filepath.Join(repoRoot(t), "install.sh"))
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "CE_DOWNLOAD_BASE=" + base, "CE_INSTALL_DIR=" + dir}, env...)
	out, err := cmd.CombinedOutput()
	return dir, string(out), err
}

func TestInstallScriptChecksAndInstallsTheLatestRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux")
	}
	dir, out, err := install(t, fakeRelease(t, true))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, name := range []string{"ce", "checkend"} {
		got, err := exec.Command(filepath.Join(dir, name), "--version").Output()
		if err != nil || strings.TrimSpace(string(got)) != version {
			t.Errorf("%s --version: %q %v", name, got, err)
		}
	}
	if !strings.Contains(out, "Add "+dir+" to your PATH") {
		t.Errorf("no PATH hint:\n%s", out)
	}
}

func TestInstallScriptInstallsAGivenVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux")
	}
	dir, out, err := install(t, fakeRelease(t, true), "CE_VERSION=v"+version)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ce")); err != nil {
		t.Error("not installed")
	}
}

func TestInstallScriptRefusesAWrongChecksum(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux")
	}
	dir, out, err := install(t, fakeRelease(t, false))
	if err == nil || !strings.Contains(out, "checksum") {
		t.Fatalf("installed with a wrong checksum:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ce")); err == nil {
		t.Error("the binary was installed")
	}
}

func TestInstallScriptRefusesABadVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux")
	}
	_, out, err := install(t, "http://127.0.0.1:9", "CE_VERSION=1.0;rm -rf x")
	if err == nil || !strings.Contains(out, "not a valid version") {
		t.Fatalf("accepted a bad version:\n%s", out)
	}
}

func TestHomebrewFormula(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script is for macOS and Linux")
	}
	checksums := filepath.Join(t.TempDir(), "checksums.txt")
	var lines []string
	for i, platform := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		lines = append(lines, fmt.Sprintf("%s  ce_%s_%s.tar.gz", strings.Repeat(fmt.Sprint(i+1), 64), version, platform))
	}
	_ = os.WriteFile(checksums, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	out, err := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "homebrew-formula.sh"), version, checksums).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	formula := string(out)
	for _, want := range []string{`version "1.2.3"`, "ce_1.2.3_darwin_arm64.tar.gz", strings.Repeat("4", 64), `bin.install_symlink "ce" => "checkend"`} {
		if !strings.Contains(formula, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(formula, "@") && strings.Contains(formula, "@SHA") {
		t.Error("a placeholder is left")
	}
	if ruby, err := exec.LookPath("ruby"); err == nil {
		file := filepath.Join(t.TempDir(), "checkend.rb")
		_ = os.WriteFile(file, out, 0o644)
		if out, err := exec.Command(ruby, "-c", file).CombinedOutput(); err != nil {
			t.Errorf("ruby -c: %v\n%s", err, out)
		}
	}
	// A missing platform fails.
	_ = os.WriteFile(checksums, []byte(lines[0]+"\n"), 0o644)
	if err := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "homebrew-formula.sh"), version, checksums).Run(); err == nil {
		t.Error("made a formula without all checksums")
	}
}
