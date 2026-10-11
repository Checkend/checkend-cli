package testkit

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// FakeReleases is a fake of the GitHub releases API of the server repo. Each release has a
// signed checkend-release.json (format 1), the Worker bundle, the migrations and SHA256SUMS.
type FakeReleases struct {
	URL string
	// API is the repo API, for example <URL>/repos/Checkend/checkend.
	API   string
	KeyID string
	Key   ed25519.PrivateKey
	// Releases, newest first.
	Releases []*FakeRelease
	// Downloads counts the asset downloads, by asset name.
	Downloads map[string]int

	mu     sync.Mutex
	server *httptest.Server
}

// FakeRelease is one GitHub release.
type FakeRelease struct {
	Tag        string
	Commit     string
	Draft      bool
	Prerelease bool
	// Manifest is the manifest as a value; Publish signs its JSON.
	Manifest map[string]any
	// Assets are the files, by name. ManifestBytes and Signature are in Assets too.
	Assets map[string][]byte
}

// NewFakeReleases starts the fake with no release.
func NewFakeReleases(t testing.TB) *FakeReleases {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &FakeReleases{KeyID: "test-key-1", Key: private, Downloads: map[string]int{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	f.URL = f.server.URL
	f.API = f.URL + "/repos/Checkend/checkend"
	t.Cleanup(f.server.Close)
	return f
}

// PublicKeys are the trusted keys for the CLI: key ID to the raw public key.
func (f *FakeReleases) PublicKeys() map[string][]byte {
	return map[string][]byte{f.KeyID: f.Key.Public().(ed25519.PublicKey)}
}

// Migrations are the migration files of a fake release.
var Migrations = map[string]string{
	"migrations/0001_init.sql":     "CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);",
	"migrations/0002_projects.sql": "CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT NOT NULL);",
}

// TarGz makes a .tar.gz with these files.
func TarGz(files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(files[name])), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(files[name]))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// Sum is the SHA-256 of data in hex.
func Sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// Publish adds a release (newest first) with a valid manifest for this version. change can
// change the manifest before it is signed.
func (f *FakeReleases) Publish(version string, change ...func(m map[string]any)) *FakeRelease {
	commit := fmt.Sprintf("%040x", len(f.Releases)+1)
	worker := TarGz(map[string]string{"worker.js": "export default { fetch() { return new Response('v" + version + "') } };"})
	migrations := TarGz(Migrations)
	workerName := "checkend-worker-" + version + ".tar.gz"
	migrationsName := "checkend-migrations-" + version + ".tar.gz"
	manifest := map[string]any{
		"format": 1, "version": version, "tag": "v" + version, "commit": commit,
		"published_at": "2026-10-20T12:00:00Z", "min_cli_version": "0.1.0",
		"migrations": map[string]any{"asset": migrationsName, "sha256": Sum(migrations), "size": len(migrations),
			"latest": "0002_projects.sql", "count": 2},
		"worker": map[string]any{"asset": workerName, "sha256": Sum(worker), "size": len(worker),
			"main_module": "worker.js", "modules": []any{map[string]any{"name": "worker.js", "type": "esm"}},
			"compatibility_date": "2026-10-06", "compatibility_flags": []any{},
			"bindings": []any{
				map[string]any{"type": "d1", "name": "DB", "required": true},
				map[string]any{"type": "r2", "name": "BLOBS", "required": false},
			},
			"crons": []any{"*/5 * * * *"}, "observability": true},
		"image": map[string]any{"repository": "ghcr.io/checkend/checkend", "tag": version,
			"digest": "sha256:" + Sum([]byte("image "+version)), "platforms": []any{"linux/amd64", "linux/arm64"}},
		"proxies": map[string]any{
			"caddy":       "docker.io/library/caddy@sha256:" + Sum([]byte("caddy")),
			"cloudflared": "docker.io/cloudflare/cloudflared@sha256:" + Sum([]byte("cloudflared")),
		},
		"settings": map[string]any{
			"ADMIN_TOKEN":     map[string]any{"secret": true, "min_length": 32},
			"SETUP_CODE":      map[string]any{"secret": true, "format": "<unix seconds>.<code>"},
			"ENCRYPTION_KEYS": map[string]any{"secret": true},
		},
	}
	for _, c := range change {
		c(manifest)
	}
	r := &FakeRelease{Tag: "v" + version, Commit: commit, Manifest: manifest,
		Assets: map[string][]byte{workerName: worker, migrationsName: migrations}}
	f.Sign(r)
	f.mu.Lock()
	f.Releases = append([]*FakeRelease{r}, f.Releases...)
	f.mu.Unlock()
	return r
}

// Sign writes checkend-release.json and its signature from r.Manifest.
func (f *FakeReleases) Sign(r *FakeRelease) {
	data, _ := json.MarshalIndent(r.Manifest, "", "  ")
	r.Assets["checkend-release.json"] = data
	r.Assets["checkend-release.json.sig"] = f.Signature(data, f.Key, f.KeyID)
}

// Signature is a .sig file for data.
func (f *FakeReleases) Signature(data []byte, key ed25519.PrivateKey, keyID string) []byte {
	sig, _ := json.Marshal(map[string]any{"format": 1, "key_id": keyID,
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, data))})
	return sig
}

func (f *FakeReleases) release(tag string) *FakeRelease {
	for _, r := range f.Releases {
		if r.Tag == tag {
			return r
		}
	}
	return nil
}

func (f *FakeReleases) json(r *FakeRelease) map[string]any {
	var assets []any
	names := make([]string, 0, len(r.Assets))
	for name := range r.Assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		assets = append(assets, map[string]any{"name": name, "size": len(r.Assets[name]),
			"browser_download_url": f.URL + "/download/" + r.Tag + "/" + name})
	}
	return map[string]any{"tag_name": r.Tag, "target_commitish": "main", "draft": r.Draft,
		"prerelease": r.Prerelease, "assets": assets}
}

func (f *FakeReleases) serve(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	path := req.URL.Path
	switch {
	case path == "/repos/Checkend/checkend/releases":
		list := []any{}
		for _, r := range f.Releases {
			list = append(list, f.json(r))
		}
		reply(list)
	case strings.HasPrefix(path, "/repos/Checkend/checkend/releases/tags/"):
		if r := f.release(strings.TrimPrefix(path, "/repos/Checkend/checkend/releases/tags/")); r != nil {
			reply(f.json(r))
			return
		}
		http.Error(w, `{"message":"Not Found"}`, 404)
	case strings.HasPrefix(path, "/repos/Checkend/checkend/git/ref/tags/"):
		if r := f.release(strings.TrimPrefix(path, "/repos/Checkend/checkend/git/ref/tags/")); r != nil {
			reply(map[string]any{"ref": "refs/tags/" + r.Tag, "object": map[string]any{"type": "commit", "sha": r.Commit}})
			return
		}
		http.Error(w, `{"message":"Not Found"}`, 404)
	case strings.HasPrefix(path, "/download/"):
		tag, name, _ := strings.Cut(strings.TrimPrefix(path, "/download/"), "/")
		if r := f.release(tag); r != nil {
			if data, ok := r.Assets[name]; ok {
				f.Downloads[name]++
				_, _ = w.Write(data)
				return
			}
		}
		http.NotFound(w, req)
	default:
		http.NotFound(w, req)
	}
}
