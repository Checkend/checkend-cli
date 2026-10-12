// Package release tests the distribution files: install.sh (with its signature check) and the
// Homebrew formula script.
package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Checkend/checkend-cli/internal/sshsig"
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

// testKey is the TEST release key of these tests only. install.sh ships with no key; the tests
// run a copy with this key in its allowed signers.
var testKey = func() ed25519.PrivateKey {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return priv
}()

// release describes the fake release: a wrong checksum, and the kind of signature.
type release struct {
	badChecksum bool
	// sig: "" (valid), "missing", "other-key", "namespace" (a server signature), "changed"
	// (checksums.txt changed after it was signed).
	sig string
}

// fakeRelease builds ce and serves it as release 1.2.3 with checksums.txt and its signature.
func fakeRelease(t *testing.T, r release) string {
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
	if r.badChecksum {
		digest = strings.Repeat("0", 64)
	}
	checksums := fmt.Sprintf("%s  %s\n%s  other.tar.gz\n", digest, archive, strings.Repeat("1", 64))
	var sig []byte
	switch r.sig {
	case "":
		sig = sshsig.Sign(testKey, []byte(checksums), namespace)
	case "other-key":
		_, other, _ := ed25519.GenerateKey(rand.Reader)
		sig = sshsig.Sign(other, []byte(checksums), namespace)
	case "namespace":
		sig = sshsig.Sign(testKey, []byte(checksums), "checkend-server-release")
	case "changed":
		sig = sshsig.Sign(testKey, []byte(checksums), namespace)
		checksums += strings.Repeat("2", 64) + "  extra.tar.gz\n"
	}
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
	if r.sig != "missing" {
		mux.HandleFunc("/download/v"+version+"/checksums.txt.sig", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(sig)
		})
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}

const namespace = "checkend-cli-release"

var signersBlock = regexp.MustCompile(`(?s)# BEGIN ALLOWED SIGNERS\n.*# END ALLOWED SIGNERS\n`)

// testScript is a copy of install.sh that trusts the TEST key (and only it).
func testScript(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !signersBlock.Match(data) {
		t.Fatal("install.sh has no allowed signers block")
	}
	line := "test-cli-release namespaces=\"" + namespace + "\" " + sshsig.AuthorizedKey(testKey.Public().(ed25519.PublicKey)) + " TEST KEY of internal/release tests\n"
	file := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(file, signersBlock.ReplaceAll(data, []byte("# BEGIN ALLOWED SIGNERS\n"+line+"# END ALLOWED SIGNERS\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func install(t *testing.T, script, base string, env ...string) (string, string, error) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command("sh", script)
	path := os.Getenv("PATH")
	for _, e := range env {
		if p, ok := strings.CutPrefix(e, "PATH="); ok {
			path = p
		}
	}
	cmd.Env = append([]string{"PATH=" + path, "HOME=" + t.TempDir(), "CE_DOWNLOAD_BASE=" + base, "CE_INSTALL_DIR=" + dir}, env...)
	out, err := cmd.CombinedOutput()
	return dir, string(out), err
}

func unix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux")
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen")
	}
}

func notInstalled(t *testing.T, dir, out string, err error, part string) {
	t.Helper()
	if err == nil || !strings.Contains(out, part) {
		t.Errorf("want a failure with %q:\n%s", part, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ce")); err == nil {
		t.Error("the binary was installed")
	}
}

func TestInstallScriptChecksAndInstallsTheLatestRelease(t *testing.T) {
	unix(t)
	dir, out, err := install(t, testScript(t), fakeRelease(t, release{}))
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
	if !strings.Contains(out, "signed by test-cli-release") {
		t.Errorf("no signer in the output:\n%s", out)
	}
}

func TestInstallScriptInstallsAGivenVersion(t *testing.T) {
	unix(t)
	dir, out, err := install(t, testScript(t), fakeRelease(t, release{}), "CE_VERSION=v"+version)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ce")); err != nil {
		t.Error("not installed")
	}
}

func TestInstallScriptRefusesAWrongChecksum(t *testing.T) {
	unix(t)
	dir, out, err := install(t, testScript(t), fakeRelease(t, release{badChecksum: true}))
	notInstalled(t, dir, out, err, "checksum")
}

func TestInstallScriptRefusesABadSignature(t *testing.T) {
	unix(t)
	for kind, part := range map[string]string{
		"missing":   "no signature",
		"other-key": "signature",
		"namespace": "signature",
		"changed":   "signature",
	} {
		dir, out, err := install(t, testScript(t), fakeRelease(t, release{sig: kind}))
		notInstalled(t, dir, out, err, part)
		if strings.Contains(out, "Downloading ce") && !strings.Contains(out, "signature") {
			t.Errorf("%s: %s", kind, out)
		}
	}
}

// The shipped install.sh has no test key; without any key it installs nothing.
func TestInstallScriptFailsClosedWithoutAKey(t *testing.T) {
	unix(t)
	data, _ := os.ReadFile(filepath.Join(repoRoot(t), "install.sh"))
	block := signersBlock.Find(data)
	if strings.Contains(strings.ToLower(string(block)), "test") {
		t.Errorf("the shipped install.sh trusts a test key:\n%s", block)
	}
	empty := filepath.Join(t.TempDir(), "install.sh")
	_ = os.WriteFile(empty, signersBlock.ReplaceAll(data, []byte("# BEGIN ALLOWED SIGNERS\n# END ALLOWED SIGNERS\n")), 0o644)
	dir, out, err := install(t, empty, fakeRelease(t, release{}))
	notInstalled(t, dir, out, err, "trusts no release key yet")
}

// tools is a PATH with the tools that install.sh needs, plus an ssh-keygen (or none).
func tools(t *testing.T, sshKeygen string) string {
	dir := t.TempDir()
	for _, tool := range []string{"sh", "curl", "tar", "uname", "mkdir", "awk", "mktemp", "rm", "mv", "chmod", "ln", "cat", "grep", "sha256sum", "shasum", "dirname"} {
		if p, err := exec.LookPath(tool); err == nil {
			_ = os.Symlink(p, filepath.Join(dir, tool))
		}
	}
	if sshKeygen != "" {
		_ = os.WriteFile(filepath.Join(dir, "ssh-keygen"), []byte(sshKeygen), 0o755)
	}
	return dir
}

func TestInstallScriptNeedsAnSSHKeygenWithSignatures(t *testing.T) {
	unix(t)
	dir, out, err := install(t, testScript(t), fakeRelease(t, release{}), "PATH="+tools(t, ""))
	notInstalled(t, dir, out, err, "ssh-keygen")
	old := "#!/bin/sh\necho 'ssh-keygen: illegal option -- Y' >&2\necho 'usage: ssh-keygen [-q] [-b bits]' >&2\nexit 1\n"
	dir, out, err = install(t, testScript(t), fakeRelease(t, release{}), "PATH="+tools(t, old))
	notInstalled(t, dir, out, err, "too old")
}

func TestInstallScriptRefusesABadVersion(t *testing.T) {
	unix(t)
	_, out, err := install(t, testScript(t), "http://127.0.0.1:9", "CE_VERSION=1.0;rm -rf x")
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

// install.sh and the npm wrapper trust the same keys, each for the CLI namespace only.
func TestInstallScriptAndNPMTrustTheSameKeys(t *testing.T) {
	script := readLF(t, "install.sh")
	npm := readLF(t, "npm", "lib", "keys.js")
	var fromScript, fromNPM []string
	for _, line := range strings.Split(string(signersBlock.Find(script)), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if len(f) < 4 || f[1] != `namespaces="`+namespace+`"` || f[2] != "ssh-ed25519" {
			t.Errorf("install.sh: %q is not `<id> namespaces=\"%s\" ssh-ed25519 <key>`", line, namespace)
			continue
		}
		fromScript = append(fromScript, f[3])
	}
	for _, m := range regexp.MustCompile(`key:\s*"ssh-ed25519 ([A-Za-z0-9+/]+=*)"`).FindAllStringSubmatch(string(npm), -1) {
		fromNPM = append(fromNPM, m[1])
	}
	slices.Sort(fromScript)
	slices.Sort(fromNPM)
	if !slices.Equal(fromScript, fromNPM) {
		t.Errorf("install.sh trusts %v, the npm wrapper %v", fromScript, fromNPM)
	}
}

// The release keys, pinned by their fingerprints (from Simon's .pub files): a copy error in
// install.sh or npm/lib/keys.js fails here.
var releaseKeys = map[string]string{"cli-2026": "SHA256:yoR4iXfhZdJ4v5OaLXX+5/rupH9z93AhZGdas6/AP88"}

func fingerprint(t *testing.T, key string) string {
	t.Helper()
	blob, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		t.Fatalf("not base64: %q", key)
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func TestTheReleaseKeysMatchTheirFingerprints(t *testing.T) {
	script := readLF(t, "install.sh")
	got := map[string]string{}
	for _, line := range strings.Split(string(signersBlock.Find(script)), "\n") {
		if f := strings.Fields(line); len(f) >= 4 && !strings.HasPrefix(f[0], "#") {
			got[f[0]] = fingerprint(t, f[3])
		}
	}
	if !maps.Equal(got, releaseKeys) {
		t.Errorf("install.sh trusts %v, want %v", got, releaseKeys)
	}
	npm := readLF(t, "npm", "lib", "keys.js")
	got = map[string]string{}
	for _, m := range regexp.MustCompile(`id:\s*"([^"]+)",\s*key:\s*"ssh-ed25519 ([A-Za-z0-9+/]+=*)"`).FindAllStringSubmatch(string(npm), -1) {
		got[m[1]] = fingerprint(t, m[2])
	}
	if !maps.Equal(got, releaseKeys) {
		t.Errorf("npm/lib/keys.js trusts %v, want %v", got, releaseKeys)
	}
}

// readLF reads a file of the repo with LF line ends (a Windows checkout can have CRLF).
func readLF(t *testing.T, path ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{repoRoot(t)}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
}
