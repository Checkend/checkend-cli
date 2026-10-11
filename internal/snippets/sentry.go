package snippets

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SentryRef is a place in the repo that names a Sentry DSN. Host is the host of a DSN that is
// written in the file; the DSN itself is never kept (it has the key).
type SentryRef struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Variable string `json:"variable,omitempty"`
	Host     string `json:"host,omitempty"`
}

var (
	dsnPattern      = regexp.MustCompile(`https?://[0-9a-fA-F]{16,}(?::[0-9a-fA-F]+)?@([A-Za-z0-9.-]+)(?::\d+)?/[\w/-]*\d`)
	variablePattern = regexp.MustCompile(`\b[A-Z0-9_]*SENTRY_DSN\b`)
	skipDirs        = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true,
		".next": true, "target": true, "venv": true, ".venv": true, "__pycache__": true, "coverage": true, "bin": true, "obj": true}
	scanExts = map[string]bool{".env": true, ".rb": true, ".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true,
		".tsx": true, ".jsx": true, ".vue": true, ".svelte": true, ".php": true, ".go": true, ".cs": true, ".json": true,
		".yml": true, ".yaml": true, ".toml": true, ".ini": true, ".properties": true, ".config": true, ".exs": true,
		".ex": true, ".java": true, ".kt": true, ".erb": true, ".example": true, ".sample": true, ".local": true}
)

const (
	maxScanFile = 1 << 20
	maxRefs     = 200
)

// ScanSentry finds the Sentry SDKs and the DSN references of a repo.
func ScanSentry(root string) (sdks []string, refs []SentryRef) {
	for dep := range PackageDeps(root) {
		if strings.HasPrefix(dep, "@sentry/") {
			sdks = append(sdks, dep)
		}
	}
	for _, f := range []struct{ file, pattern string }{
		{"Gemfile", `["'](sentry-[a-z-]+)["']`},
		{"requirements.txt", `(?m)^(sentry-sdk)\b`},
		{"pyproject.toml", `["'](sentry-sdk)\b`},
		{"composer.json", `"(sentry/[a-z-]+)"`},
		{"go.mod", `(github\.com/getsentry/sentry-go)`},
	} {
		for _, m := range regexp.MustCompile(f.pattern).FindAllStringSubmatch(read(root, f.file), -1) {
			sdks = append(sdks, m[1])
		}
	}
	for _, p := range dotnetProjects(root) {
		for _, m := range regexp.MustCompile(`include="(sentry[a-z.]*)"`).FindAllStringSubmatch(read(root, p), -1) {
			sdks = append(sdks, m[1])
		}
	}
	sort.Strings(sdks)
	sdks = compact(sdks)

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || len(refs) >= maxRefs {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !scanExts[filepath.Ext(name)] && !strings.HasPrefix(name, ".env") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxScanFile {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 64<<10), maxScanFile)
		for line := 1; scanner.Scan() && len(refs) < maxRefs; line++ {
			text := scanner.Text()
			ref := SentryRef{File: filepath.ToSlash(rel), Line: line, Variable: variablePattern.FindString(text)}
			if m := dsnPattern.FindStringSubmatch(text); m != nil {
				ref.Host = strings.ToLower(m[1])
			}
			if ref.Variable != "" || ref.Host != "" {
				refs = append(refs, ref)
			}
		}
		return nil
	})
	return sdks, refs
}

func compact(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
