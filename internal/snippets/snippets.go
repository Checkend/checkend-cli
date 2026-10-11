// Package snippets detects the platform of a repo and makes the SDK setup snippet that
// `ce init` prints. Checkend accepts events from any Sentry SDK.
package snippets

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	"github.com/Checkend/checkend-cli/internal/clierr"
)

var frontend = []string{"react", "react-dom", "vue", "svelte", "@angular/core", "preact", "solid-js"}
var server = []string{"express", "fastify", "koa", "hono", "@nestjs/core", "next", "nuxt", "@remix-run/node"}

func has(root, file string) bool {
	_, err := os.Stat(filepath.Join(root, file))
	return err == nil
}

// PackageDeps returns the dependencies and devDependencies of package.json in root.
func PackageDeps(root string) map[string]any {
	deps := map[string]any{}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return deps
	}
	var pkg struct {
		Dependencies    map[string]any `json:"dependencies"`
		DevDependencies map[string]any `json:"devDependencies"`
	}
	// An unreadable package.json is still a JavaScript project.
	_ = json.Unmarshal(data, &pkg)
	for k, v := range pkg.Dependencies {
		deps[k] = v
	}
	for k, v := range pkg.DevDependencies {
		deps[k] = v
	}
	return deps
}

func anyOf(deps map[string]any, names []string) bool {
	for _, n := range names {
		if _, ok := deps[n]; ok {
			return true
		}
	}
	return false
}

// DetectPlatform guesses the platform from the files in the repo root. "" when unknown.
func DetectPlatform(root string) string {
	switch {
	case has(root, "Gemfile"):
		return "ruby"
	case has(root, "pyproject.toml") || has(root, "requirements.txt") || has(root, "setup.py"):
		return "python"
	case has(root, "go.mod"):
		return "go"
	case has(root, "composer.json"):
		return "php"
	case has(root, "package.json"):
		deps := PackageDeps(root)
		if anyOf(deps, frontend) && !anyOf(deps, server) {
			return "javascript"
		}
		return "node"
	}
	return ""
}

var plainURL = regexp.MustCompile("^https?://[^\\s\"'`\\\\<>]+$")

// CheckDSN checks a DSN from the server. A DSN goes into code that people paste, so it must
// be a plain URL: no quotes, spaces or backslashes.
func CheckDSN(dsn any) (string, error) {
	s, ok := dsn.(string)
	if ok && plainURL.MatchString(s) {
		if _, err := url.Parse(s); err == nil {
			return s, nil
		}
	}
	return "", clierr.Fail("The server returned a DSN that is not a valid URL.")
}

// Snippet is the SDK setup code for a platform.
func Snippet(platform, dsn string) string {
	switch platform {
	case "node", "javascript":
		pkg := "@sentry/node"
		if platform == "javascript" {
			pkg = "@sentry/browser"
		}
		return fmt.Sprintf("// npm install %s\nimport * as Sentry from \"%s\";\n\nSentry.init({ dsn: \"%s\" });", pkg, pkg, dsn)
	case "python":
		return fmt.Sprintf("# pip install sentry-sdk\nimport sentry_sdk\n\nsentry_sdk.init(dsn=\"%s\")", dsn)
	case "ruby":
		return fmt.Sprintf("# Gemfile: gem \"sentry-ruby\"\nSentry.init do |config|\n  config.dsn = \"%s\"\nend", dsn)
	case "go":
		return fmt.Sprintf("// go get github.com/getsentry/sentry-go\nsentry.Init(sentry.ClientOptions{Dsn: \"%s\"})", dsn)
	case "php":
		return fmt.Sprintf("// composer require sentry/sentry\n\\Sentry\\init(['dsn' => '%s']);", dsn)
	}
	return fmt.Sprintf("// Use the Sentry SDK for your platform with this DSN:\nSentry.init({ dsn: \"%s\" });", dsn)
}
