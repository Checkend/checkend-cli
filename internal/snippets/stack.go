package snippets

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Stack is the language and framework of a repo, for `ce setup`.
type Stack struct {
	// Platform is the Sentry platform name, for example "node-express". "" when unknown.
	Platform string `json:"platform"`
	// Framework is for example "nextjs"; "" when none was found.
	Framework string `json:"framework"`
	// Label is for people, for example "Node / Express".
	Label string `json:"label"`
	// SDK is the Sentry SDK package to install.
	SDK string `json:"sdk"`
}

type framework struct{ dep, id, name, platform, sdk string }

// Node frameworks, the first match wins: meta-frameworks before UI libraries and servers.
var nodeFrameworks = []framework{
	{"next", "nextjs", "Next.js", "javascript-nextjs", "@sentry/nextjs"},
	{"nuxt", "nuxt", "Nuxt", "javascript-nuxt", "@sentry/nuxt"},
	{"@sveltejs/kit", "sveltekit", "SvelteKit", "javascript-sveltekit", "@sentry/sveltekit"},
	{"@remix-run/node", "remix", "Remix", "javascript-remix", "@sentry/remix"},
	{"astro", "astro", "Astro", "javascript-astro", "@sentry/astro"},
	{"@nestjs/core", "nestjs", "NestJS", "node-nestjs", "@sentry/nestjs"},
	{"express", "express", "Express", "node-express", "@sentry/node"},
	{"fastify", "fastify", "Fastify", "node-fastify", "@sentry/node"},
	{"koa", "koa", "Koa", "node-koa", "@sentry/node"},
	{"hono", "hono", "Hono", "node-hono", "@sentry/node"},
}

// Browser UI libraries.
var uiFrameworks = []framework{
	{"@angular/core", "angular", "Angular", "javascript-angular", "@sentry/angular"},
	{"react", "react", "React", "javascript-react", "@sentry/react"},
	{"vue", "vue", "Vue", "javascript-vue", "@sentry/vue"},
	{"svelte", "svelte", "Svelte", "javascript-svelte", "@sentry/svelte"},
	{"solid-js", "solid", "Solid", "javascript-solid", "@sentry/solid"},
	{"preact", "preact", "Preact", "javascript", "@sentry/browser"},
}

func read(root, name string) string {
	data, _ := os.ReadFile(filepath.Join(root, name))
	return strings.ToLower(string(data))
}

func mentions(text, name string) bool {
	return regexp.MustCompile(`(?m)(^|[^a-z0-9_-])` + regexp.QuoteMeta(name) + `($|[^a-z0-9_-])`).MatchString(text)
}

// DetectStack finds the language and the framework from the files in the repo root.
func DetectStack(root string) Stack {
	switch {
	case has(root, "Gemfile"):
		if mentions(read(root, "Gemfile"), "rails") {
			return Stack{"ruby-rails", "rails", "Ruby / Rails", "sentry-rails"}
		}
		return Stack{"ruby", "", "Ruby", "sentry-ruby"}
	case has(root, "pyproject.toml") || has(root, "requirements.txt") || has(root, "setup.py") || has(root, "Pipfile"):
		text := read(root, "pyproject.toml") + read(root, "requirements.txt") + read(root, "setup.py") + read(root, "Pipfile")
		for _, f := range [][2]string{{"django", "Django"}, {"fastapi", "FastAPI"}, {"flask", "Flask"}} {
			if mentions(text, f[0]) {
				return Stack{"python-" + f[0], f[0], "Python / " + f[1], "sentry-sdk"}
			}
		}
		return Stack{"python", "", "Python", "sentry-sdk"}
	case has(root, "go.mod"):
		text := read(root, "go.mod")
		for _, f := range [][3]string{{"github.com/gin-gonic/gin", "gin", "Gin"}, {"github.com/labstack/echo", "echo", "Echo"}, {"github.com/gofiber/fiber", "fiber", "Fiber"}} {
			if strings.Contains(text, f[0]) {
				return Stack{"go", f[1], "Go / " + f[2], "github.com/getsentry/sentry-go"}
			}
		}
		return Stack{"go", "", "Go", "github.com/getsentry/sentry-go"}
	case has(root, "composer.json"):
		text := read(root, "composer.json")
		if strings.Contains(text, `"laravel/framework"`) {
			return Stack{"php-laravel", "laravel", "PHP / Laravel", "sentry/sentry-laravel"}
		}
		if strings.Contains(text, `"symfony/framework-bundle"`) {
			return Stack{"php-symfony", "symfony", "PHP / Symfony", "sentry/sentry-symfony"}
		}
		return Stack{"php", "", "PHP", "sentry/sentry"}
	case has(root, "package.json"):
		return nodeStack(root)
	}
	if projects := dotnetProjects(root); len(projects) > 0 {
		for _, p := range projects {
			if strings.Contains(read(root, p), "microsoft.net.sdk.web") {
				return Stack{"dotnet-aspnetcore", "aspnetcore", ".NET / ASP.NET Core", "Sentry.AspNetCore"}
			}
		}
		return Stack{"dotnet", "", ".NET", "Sentry"}
	}
	return Stack{Label: "unknown"}
}

func nodeStack(root string) Stack {
	deps := PackageDeps(root)
	for _, f := range nodeFrameworks {
		if _, ok := deps[f.dep]; ok {
			return Stack{f.platform, f.id, "Node / " + f.name, f.sdk}
		}
	}
	_, vite := deps["vite"]
	suffix := ""
	if vite {
		suffix = " (Vite)"
	}
	for _, f := range uiFrameworks {
		if _, ok := deps[f.dep]; ok {
			return Stack{f.platform, f.id, "JavaScript / " + f.name + suffix, f.sdk}
		}
	}
	if vite {
		return Stack{"javascript", "vite", "JavaScript / Vite", "@sentry/browser"}
	}
	return Stack{"node", "", "Node", "@sentry/node"}
}

func dotnetProjects(root string) []string {
	var out []string
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		switch filepath.Ext(e.Name()) {
		case ".csproj", ".fsproj", ".vbproj":
			out = append(out, e.Name())
		}
	}
	return out
}
