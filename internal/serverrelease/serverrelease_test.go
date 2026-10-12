package serverrelease_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/serverrelease"
	"github.com/Checkend/checkend-cli/internal/sshsig"
	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func source(f *FakeReleases) serverrelease.Source {
	return serverrelease.Source{API: f.API, Keys: f.PublicKeys(), CLIVersion: "0.2.0"}
}

func failsWith(t *testing.T, err error, part string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), part) {
		t.Fatalf("error %v; want %q", err, part)
	}
}

func TestNoReleaseIsPublishedYet(t *testing.T) {
	f := NewFakeReleases(t)
	_, err := source(f).Latest()
	failsWith(t, err, "No Checkend server release is published yet")
	Equal(t, clierr.CodeOf(err), clierr.Failure)
	// A release without a manifest (an old tag, or a release that is not done) is not one.
	r := f.Publish("0.1.0")
	delete(r.Assets, "checkend-release.json")
	_, err = source(f).Latest()
	failsWith(t, err, "No Checkend server release is published yet")
}

func TestLatestSkipsDraftsAndPrereleases(t *testing.T) {
	f := NewFakeReleases(t)
	f.Publish("0.1.0")
	f.Publish("0.2.0").Draft = true
	f.Publish("0.3.0-rc.1").Prerelease = true
	r, err := source(f).Latest()
	if err != nil {
		t.Fatal(err)
	}
	Equal(t, r.Manifest.Version, "0.1.0")
	Equal(t, r.Manifest.Image.Ref(), "ghcr.io/checkend/checkend@"+r.Manifest.Image.Digest)
	Equal(t, r.Manifest.Worker.Bindings[1], serverrelease.Binding{Type: "r2", Name: "BLOBS", Required: false})
}

func TestGetAVersion(t *testing.T) {
	f := NewFakeReleases(t)
	f.Publish("0.1.0")
	f.Publish("0.2.0")
	for _, v := range []string{"0.1.0", "v0.1.0"} {
		r, err := source(f).Get(v)
		if err != nil {
			t.Fatal(err)
		}
		Equal(t, r.Manifest.Version, "0.1.0")
	}
	_, err := source(f).Get("9.9.9")
	failsWith(t, err, "No Checkend server release v9.9.9")
	_, err = source(f).Get("../x")
	Equal(t, clierr.CodeOf(err), clierr.Usage)
}

func TestTheSignatureIsCheckedBeforeTheManifestIsRead(t *testing.T) {
	f := NewFakeReleases(t)
	r := f.Publish("0.1.0")
	r.Assets["checkend-release.json"] = []byte(`{"format": 1, "version": "0.1.0", "evil": `)
	_, err := source(f).Latest()
	failsWith(t, err, "signature")

	r = f.Publish("0.2.0")
	r.Assets["checkend-release.json"] = append(r.Assets["checkend-release.json"], ' ')
	_, err = source(f).Latest()
	failsWith(t, err, "signature")

	r = f.Publish("0.3.0")
	delete(r.Assets, "checkend-release.json.sig")
	_, err = source(f).Latest()
	failsWith(t, err, "no signature")

	_, other, _ := ed25519.GenerateKey(rand.Reader)
	r = f.Publish("0.4.0")
	r.Assets["checkend-release.json.sig"] = sshsig.Sign(other, r.Assets["checkend-release.json"], serverrelease.Namespace)
	_, err = source(f).Latest()
	failsWith(t, err, "unknown key SHA256:")

	// A signature of a CLI release (another namespace) is not one of a server release.
	r = f.Publish("0.5.0")
	r.Assets["checkend-release.json.sig"] = sshsig.Sign(f.Key, r.Assets["checkend-release.json"], "checkend-cli-release")
	_, err = source(f).Latest()
	failsWith(t, err, "namespace")

	r = f.Publish("0.5.1")
	r.Assets["checkend-release.json.sig"] = []byte(`{"format":1,"key_id":"test-key-1","signature":"AAAA"}`)
	_, err = source(f).Latest()
	failsWith(t, err, "not an SSH signature")

	s := source(f)
	s.Keys = nil
	f.Publish("0.6.0")
	_, err = s.Latest()
	failsWith(t, err, "unknown key")
}

func TestTheManifestMustMatchTheGitHubRelease(t *testing.T) {
	f := NewFakeReleases(t)
	f.Publish("0.1.0", func(m map[string]any) { m["tag"] = "v0.0.9" })
	_, err := source(f).Latest()
	failsWith(t, err, "tag")

	f.Publish("0.2.0", func(m map[string]any) { m["commit"] = strings.Repeat("f", 40) })
	_, err = source(f).Latest()
	failsWith(t, err, "commit")
}

func TestRefusesAManifestThatThisCLICannotUse(t *testing.T) {
	for part, change := range map[string]func(m map[string]any){
		"newer ce":          func(m map[string]any) { m["format"] = 2 },
		"ce 9.0.0 or later": func(m map[string]any) { m["min_cli_version"] = "9.0.0" },
		"image digest":      func(m map[string]any) { m["image"].(map[string]any)["digest"] = "latest" },
		"image repository":  func(m map[string]any) { m["image"].(map[string]any)["repository"] = "docker.io/evil/checkend" },
		"default value": func(m map[string]any) {
			m["settings"].(map[string]any)["ADMIN_TOKEN"].(map[string]any)["default"] = "x"
		},
		"asset name":     func(m map[string]any) { m["worker"].(map[string]any)["asset"] = "../worker.tar.gz" },
		"sha256":         func(m map[string]any) { m["worker"].(map[string]any)["sha256"] = "abc" },
		"version":        func(m map[string]any) { m["version"] = "latest" },
		"main module":    func(m map[string]any) { m["worker"].(map[string]any)["main_module"] = "other.js" },
		"migration name": func(m map[string]any) { m["migrations"].(map[string]any)["latest"] = "../x.sql" },
		"proxy image": func(m map[string]any) {
			m["proxies"].(map[string]any)["caddy"] = "docker.io/evil/caddy@sha256:" + strings.Repeat("a", 64)
		},
		"proxy image ": func(m map[string]any) {
			m["proxies"].(map[string]any)["nginx"] = "docker.io/library/nginx@sha256:" + strings.Repeat("a", 64)
		},
		"compatibility date": func(m map[string]any) { m["worker"].(map[string]any)["compatibility_date"] = "soon" },
	} {
		f := NewFakeReleases(t)
		f.Publish("0.1.0", change)
		_, err := source(f).Latest()
		if err == nil || !strings.Contains(err.Error(), part) {
			t.Errorf("%s: %v", part, err)
		}
	}
}

func TestFetchChecksTheSizeAndTheChecksum(t *testing.T) {
	f := NewFakeReleases(t)
	pub := f.Publish("0.1.0")
	r, err := source(f).Latest()
	if err != nil {
		t.Fatal(err)
	}
	files, err := r.Files(r.Manifest.Migrations.Asset)
	if err != nil {
		t.Fatal(err)
	}
	Equal(t, len(files), 2)
	Equal(t, string(files["migrations/0001_init.sql"]), Migrations["migrations/0001_init.sql"])

	name := r.Manifest.Worker.Asset.Name
	changed := append([]byte(nil), pub.Assets[name]...)
	changed[len(changed)-1] ^= 0xff
	pub.Assets[name] = changed
	_, err = r.Fetch(r.Manifest.Worker.Asset)
	failsWith(t, err, "checksum")
	pub.Assets[name] = append(pub.Assets[name], 0)
	_, err = r.Fetch(r.Manifest.Worker.Asset)
	failsWith(t, err, "size")
}

func TestFilesRefusesUnsafeArchives(t *testing.T) {
	for _, name := range []string{"../evil.sql", "/etc/passwd", "migrations/../../x"} {
		f := NewFakeReleases(t)
		bad := TarGz(map[string]string{name: "x"})
		pub := f.Publish("0.1.0", func(m map[string]any) {
			mig := m["migrations"].(map[string]any)
			mig["sha256"], mig["size"] = Sum(bad), len(bad)
		})
		pub.Assets[pub.Manifest["migrations"].(map[string]any)["asset"].(string)] = bad
		r, err := source(f).Latest()
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.Files(r.Manifest.Migrations.Asset)
		failsWith(t, err, "unsafe")
	}
}

// The server release key, pinned by its fingerprint (from Simon's .pub file).
func TestTheTrustedServerKeyMatchesItsFingerprint(t *testing.T) {
	want := map[string]string{"server-2026": "SHA256:QMPYElbPG0/hSKTHjqjkeaD9ylozLFgf3BFadqoeAzw"}
	got := map[string]string{}
	for id, line := range serverrelease.TrustedKeys {
		f := strings.Fields(line)
		if len(f) != 2 || f[0] != "ssh-ed25519" {
			t.Fatalf("%s: %q is not \"ssh-ed25519 <key>\"", id, line)
		}
		blob, err := base64.StdEncoding.DecodeString(f[1])
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(blob)
		got[id] = "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	}
	if len(got) != len(want) || got["server-2026"] != want["server-2026"] {
		t.Errorf("TrustedKeys are %v, want %v", got, want)
	}
}
