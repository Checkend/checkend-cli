// Package serverrelease finds, verifies and downloads a released Checkend server: a GitHub
// release of Checkend/checkend with a signed manifest (checkend-release.json, format 1), the
// Worker bundle, the migrations and the digest of the Docker image. `ce install` installs only
// a release that this package verified.
//
// The order of the checks: the signature of the exact manifest bytes (with a trusted key),
// then the manifest, then its tag and commit against the GitHub release, then each asset by
// its size and SHA-256 when it is downloaded.
package serverrelease

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/docs"
	"github.com/Checkend/checkend-cli/internal/version"
)

// DefaultAPI is the GitHub API of the server repo.
const DefaultAPI = "https://api.github.com/repos/Checkend/checkend"

// ImageRepository is the only image repository that a manifest can name.
const ImageRepository = "ghcr.io/checkend/checkend"

// TrustedKeys are the public keys that sign the server releases: key ID to the raw ed25519
// public key. A new key is added here before its first release; an old key stays until no
// supported release uses it.
var TrustedKeys = map[string][]byte{}

const (
	manifestName = "checkend-release.json"
	signatureExt = ".sig"
	maxManifest  = 1 << 20
	maxSignature = 4 << 10
	maxAsset     = 200 << 20
	maxFile      = 50 << 20
	timeout      = 2 * time.Minute
)

// NoRelease is the message when no server release has a manifest yet.
const NoRelease = "No Checkend server release is published yet."

var (
	versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?$`)
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	shaPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	digestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	assetPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	migrationName  = regexp.MustCompile(`^\d{4}_[a-z0-9_]+\.sql$`)
	bindingName    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	datePattern    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// Source is where the releases come from.
type Source struct {
	// API is the GitHub API of the repo (DefaultAPI).
	API string
	// Keys are the trusted keys (TrustedKeys).
	Keys map[string][]byte
	// CLIVersion is the version of this CLI, for min_cli_version.
	CLIVersion string
	HTTP       *http.Client
}

// Default is the source of a release build.
func Default(client *http.Client) Source {
	return Source{API: DefaultAPI, Keys: TrustedKeys, CLIVersion: version.Version, HTTP: client}
}

// Asset is a file of the release, checked by its size and SHA-256.
type Asset struct {
	Name   string `json:"asset"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Module is one module of the Worker bundle.
type Module struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Binding is a binding of the Worker. Optional bindings can be offered by the installer.
type Binding struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

// Setting is a setting of the server. Secrets never have a default value.
type Setting struct {
	Secret    bool   `json:"secret"`
	MinLength int    `json:"min_length,omitempty"`
	Format    string `json:"format,omitempty"`
}

// Manifest is checkend-release.json, format 1.
type Manifest struct {
	Format        int    `json:"format"`
	Version       string `json:"version"`
	Tag           string `json:"tag"`
	Commit        string `json:"commit"`
	PublishedAt   string `json:"published_at"`
	MinCLIVersion string `json:"min_cli_version"`
	Migrations    struct {
		Asset
		Latest string `json:"latest"`
		Count  int    `json:"count"`
	} `json:"migrations"`
	Worker struct {
		Asset
		MainModule         string    `json:"main_module"`
		Modules            []Module  `json:"modules"`
		CompatibilityDate  string    `json:"compatibility_date"`
		CompatibilityFlags []string  `json:"compatibility_flags"`
		Bindings           []Binding `json:"bindings"`
		Crons              []string  `json:"crons"`
		Observability      bool      `json:"observability"`
	} `json:"worker"`
	Image    Image              `json:"image"`
	Settings map[string]Setting `json:"settings"`
	// Proxies are the pinned images of the HTTPS proxies of `ce install ssh` (optional):
	// "caddy" and "cloudflared".
	Proxies map[string]string `json:"proxies,omitempty"`
}

// ProxyRepositories are the only repositories that a proxy image can have.
var ProxyRepositories = map[string]string{
	"caddy":       "docker.io/library/caddy",
	"cloudflared": "docker.io/cloudflare/cloudflared",
}

// Image is the Docker image of the release.
type Image struct {
	Repository string   `json:"repository"`
	Tag        string   `json:"tag"`
	Digest     string   `json:"digest"`
	Platforms  []string `json:"platforms"`
}

// Ref is the image pinned by its digest, for example ghcr.io/checkend/checkend@sha256:….
func (i Image) Ref() string { return i.Repository + "@" + i.Digest }

// Release is a verified release.
type Release struct {
	Manifest Manifest
	// Signer is the ID of the key that signed the manifest.
	Signer string
	src    Source
	assets map[string]ghAsset
}

type ghAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	Tag        string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

func (r ghRelease) has(name string) bool {
	return slices.ContainsFunc(r.Assets, func(a ghAsset) bool { return a.Name == name })
}

func fail(format string, args ...any) error {
	return clierr.New(clierr.Failure, fmt.Sprintf(format, args...),
		"Do not install this release. Report it: https://github.com/Checkend/checkend/security")
}

// Latest is the newest release that is not a draft or a prerelease and has a manifest.
func (s Source) Latest() (*Release, error) {
	var list []ghRelease
	if err := s.getJSON(s.API+"/releases?per_page=30", &list); err != nil {
		return nil, err
	}
	for _, r := range list {
		if !r.Draft && !r.Prerelease && r.has(manifestName) {
			return s.verify(r)
		}
	}
	return nil, clierr.New(clierr.Failure, NoRelease, "Watch https://github.com/Checkend/checkend/releases for the first release.").WithDocs(docs.Upgrade)
}

// Get is the release of a version ("0.3.0" or "v0.3.0").
func (s Source) Get(v string) (*Release, error) {
	v = strings.TrimPrefix(v, "v")
	if !versionPattern.MatchString(v) {
		return nil, clierr.UsageError(fmt.Sprintf("%q is not a version.", v), "Example: --version 0.3.0")
	}
	var r ghRelease
	err := s.getJSON(s.API+"/releases/tags/v"+v, &r)
	if errors.Is(err, errNotFound) || err == nil && !r.has(manifestName) {
		return nil, clierr.New(clierr.Failure, fmt.Sprintf("No Checkend server release v%s with a manifest.", v),
			"See https://github.com/Checkend/checkend/releases for the versions.")
	}
	if err != nil {
		return nil, err
	}
	return s.verify(r)
}

func (s Source) verify(r ghRelease) (*Release, error) {
	rel := &Release{src: s, assets: map[string]ghAsset{}}
	for _, a := range r.Assets {
		rel.assets[a.Name] = a
	}
	sigAsset, ok := rel.assets[manifestName+signatureExt]
	if !ok {
		return nil, fail("Release %s has no signature for its manifest.", r.Tag)
	}
	sig, err := s.download(sigAsset.URL, maxSignature)
	if err != nil {
		return nil, err
	}
	data, err := s.download(rel.assets[manifestName].URL, maxManifest)
	if err != nil {
		return nil, err
	}
	if rel.Signer, err = s.checkSignature(data, sig); err != nil {
		return nil, err
	}
	// Only now, with a valid signature, is the manifest read.
	if err := json.Unmarshal(data, &rel.Manifest); err != nil {
		var cerr *clierr.Error
		if errors.As(err, &cerr) {
			return nil, err
		}
		return nil, fail("The manifest of %s cannot be read.", r.Tag)
	}
	if err := s.check(&rel.Manifest); err != nil {
		return nil, err
	}
	m := &rel.Manifest
	if m.Tag != r.Tag || m.Tag != "v"+m.Version {
		return nil, fail("The manifest names tag %q, but the GitHub release is %q.", m.Tag, r.Tag)
	}
	commit, err := s.tagCommit(r.Tag)
	if err != nil {
		return nil, err
	}
	if commit != m.Commit {
		return nil, fail("The manifest names commit %s, but tag %s is commit %s.", m.Commit, r.Tag, commit)
	}
	return rel, nil
}

func (s Source) checkSignature(data, sigFile []byte) (string, error) {
	var sig struct {
		Format    int    `json:"format"`
		KeyID     string `json:"key_id"`
		Signature string `json:"signature"`
	}
	if json.Unmarshal(sigFile, &sig) != nil || sig.Format != 1 {
		return "", fail("The signature file of the manifest cannot be read.")
	}
	key, ok := s.Keys[sig.KeyID]
	if !ok || len(key) != ed25519.PublicKeySize {
		return "", clierr.New(clierr.Failure,
			fmt.Sprintf("The manifest is signed with an unknown key %q.", sig.KeyID),
			"Update ce (a newer ce may trust a newer key), then run the command again.")
	}
	raw, err := base64.StdEncoding.DecodeString(sig.Signature)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(key), data, raw) {
		return "", fail("The signature of the manifest is not valid.")
	}
	return sig.KeyID, nil
}

func (s Source) check(m *Manifest) error {
	if m.Format != 1 {
		return clierr.New(clierr.Failure, fmt.Sprintf("The release manifest has format %d: it needs a newer ce.", m.Format), "Update ce, then run the command again.")
	}
	if !versionPattern.MatchString(m.Version) {
		return fail("The manifest has no valid version.")
	}
	if Compare(m.MinCLIVersion, s.CLIVersion) > 0 {
		return clierr.New(clierr.Failure, fmt.Sprintf("Checkend %s needs ce %s or later (this is %s).", m.Version, m.MinCLIVersion, s.CLIVersion), "Update ce, then run the command again.")
	}
	if !commitPattern.MatchString(m.Commit) {
		return fail("The manifest has no valid commit.")
	}
	for _, a := range []Asset{m.Migrations.Asset, m.Worker.Asset} {
		if !assetPattern.MatchString(a.Name) || strings.HasPrefix(a.Name, manifestName) {
			return fail("The manifest has an invalid asset name %q.", a.Name)
		}
		if !shaPattern.MatchString(a.SHA256) || a.Size <= 0 || a.Size > maxAsset {
			return fail("The manifest has an invalid sha256 or size for %s.", a.Name)
		}
	}
	if !migrationName.MatchString(m.Migrations.Latest) || m.Migrations.Count < 1 {
		return fail("The manifest has an invalid latest migration name %q.", m.Migrations.Latest)
	}
	w := m.Worker
	if !slices.ContainsFunc(w.Modules, func(mod Module) bool { return mod.Name == w.MainModule && mod.Type == "esm" }) {
		return fail("The manifest names a main module %q that is not an ES module of the bundle.", w.MainModule)
	}
	for _, mod := range w.Modules {
		if !assetPattern.MatchString(path.Base(mod.Name)) || path.Clean(mod.Name) != mod.Name || strings.HasPrefix(mod.Name, "/") || strings.HasPrefix(mod.Name, "..") {
			return fail("The manifest has an invalid module name %q.", mod.Name)
		}
	}
	if !datePattern.MatchString(w.CompatibilityDate) {
		return fail("The manifest has an invalid compatibility date.")
	}
	for _, b := range w.Bindings {
		if !bindingName.MatchString(b.Name) {
			return fail("The manifest has an invalid binding name %q.", b.Name)
		}
	}
	if !slices.Contains(w.Bindings, Binding{Type: "d1", Name: "DB", Required: true}) {
		return fail("The manifest has no D1 binding DB.")
	}
	if m.Image.Repository != ImageRepository {
		return fail("The manifest names image repository %q, not %s.", m.Image.Repository, ImageRepository)
	}
	if !digestPattern.MatchString(m.Image.Digest) {
		return fail("The manifest has no valid image digest.")
	}
	for name, ref := range m.Proxies {
		repo, digest, ok := strings.Cut(ref, "@")
		if want, known := ProxyRepositories[name]; !known || !ok || repo != want || !digestPattern.MatchString(digest) {
			return fail("The manifest has an invalid proxy image %q.", name)
		}
	}
	return nil
}

// UnmarshalJSON refuses a setting with a default value: the manifest never has one for a secret.
func (s *Setting) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if _, ok := raw["default"]; ok {
		return fail("A setting of the manifest has a default value; secrets never do.")
	}
	type plain Setting
	return json.Unmarshal(data, (*plain)(s))
}

// tagCommit is the commit of a tag (an annotated tag points to a tag object first).
func (s Source) tagCommit(tag string) (string, error) {
	var ref struct {
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := s.getJSON(s.API+"/git/ref/tags/"+url.PathEscape(tag), &ref); err != nil {
		return "", err
	}
	for i := 0; ref.Object.Type == "tag" && i < 3; i++ {
		if err := s.getJSON(s.API+"/git/tags/"+url.PathEscape(ref.Object.SHA), &ref); err != nil {
			return "", err
		}
	}
	return ref.Object.SHA, nil
}

// Fetch downloads an asset and checks its size and SHA-256.
func (r *Release) Fetch(a Asset) ([]byte, error) {
	gh, ok := r.assets[a.Name]
	if !ok {
		return nil, fail("Release %s has no asset %s.", r.Manifest.Tag, a.Name)
	}
	data, err := r.src.download(gh.URL, a.Size+1)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != a.Size {
		return nil, fail("%s has the wrong size (%d bytes, the manifest says %d).", a.Name, len(data), a.Size)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != a.SHA256 {
		return nil, fail("%s does not match its checksum in the signed manifest.", a.Name)
	}
	return data, nil
}

// Files downloads a .tar.gz asset, checks it, and returns its regular files by name.
func (r *Release) Files(a Asset) (map[string][]byte, error) {
	data, err := r.Fetch(a)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fail("%s is not a .tar.gz file.", a.Name)
	}
	files := map[string][]byte{}
	tr := tar.NewReader(gz)
	total := int64(0)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, fail("%s cannot be read: %v", a.Name, err)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		name := h.Name
		if h.Typeflag != tar.TypeReg || name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
			return nil, fail("%s has an unsafe entry %q.", a.Name, name)
		}
		total += h.Size
		if h.Size > maxFile || total > maxAsset {
			return nil, fail("%s is too large.", a.Name)
		}
		content, err := io.ReadAll(io.LimitReader(tr, h.Size))
		if err != nil {
			return nil, fail("%s cannot be read: %v", a.Name, err)
		}
		files[name] = content
	}
}

var errNotFound = errors.New("not found")

func (s Source) client() *http.Client {
	c := http.Client{Timeout: timeout}
	if s.HTTP != nil {
		c = *s.HTTP
		if c.Timeout == 0 {
			c.Timeout = timeout
		}
	}
	// The downloads of GitHub go through a redirect; each hop must stay on HTTPS. The content is
	// checked against the signed manifest anyway.
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		return checkURL(req.URL)
	}
	return &c
}

func checkURL(u *url.URL) error {
	host := u.Hostname()
	if u.Scheme == "https" || u.Scheme == "http" && (host == "127.0.0.1" || host == "localhost") {
		return nil
	}
	return fmt.Errorf("refusing %s: HTTPS only", u.Redacted())
}

func (s Source) get(raw string, limit int64) ([]byte, error) {
	u, err := url.Parse(raw)
	if err == nil {
		err = checkURL(u)
	}
	if err != nil {
		return nil, clierr.Fail(fmt.Sprintf("Cannot download the release: %v", err))
	}
	req, _ := http.NewRequest("GET", raw, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "checkend-cli/"+s.CLIVersion)
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, clierr.New(clierr.Failure, fmt.Sprintf("Cannot reach %s: %v", u.Host, err), "Check your network.")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, errNotFound
	}
	if resp.StatusCode != 200 {
		hint := "Try again later."
		if resp.StatusCode == 403 || resp.StatusCode == 429 {
			hint = "GitHub limits requests without a token. Try again in an hour."
		}
		return nil, clierr.New(clierr.Failure, fmt.Sprintf("%s answered HTTP %d.", u.Host, resp.StatusCode), hint)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, clierr.Fail(fmt.Sprintf("Cannot download from %s: %v", u.Host, err))
	}
	if int64(len(data)) > limit {
		return nil, fail("A release file from %s is too large.", u.Host)
	}
	return data, nil
}

func (s Source) download(raw string, limit int64) ([]byte, error) {
	data, err := s.get(raw, limit)
	if errors.Is(err, errNotFound) {
		return nil, fail("A release file is missing.")
	}
	return data, err
}

func (s Source) getJSON(raw string, v any) error {
	data, err := s.get(raw, maxManifest*4)
	if err != nil {
		return err
	}
	if json.Unmarshal(data, v) != nil {
		return clierr.Fail("GitHub returned an answer that cannot be read.")
	}
	return nil
}

// Compare compares two versions (1.2.3, with an optional -prerelease): -1, 0 or 1. A version
// that is not valid is the lowest.
func Compare(a, b string) int {
	pa, pb := versionPattern.FindStringSubmatch(strings.TrimPrefix(a, "v")), versionPattern.FindStringSubmatch(strings.TrimPrefix(b, "v"))
	switch {
	case pa == nil && pb == nil:
		return 0
	case pa == nil:
		return -1
	case pb == nil:
		return 1
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa[4] == pb[4]:
		return 0
	case pa[4] == "":
		return 1
	case pb[4] == "":
		return -1
	}
	return strings.Compare(pa[4], pb[4])
}
