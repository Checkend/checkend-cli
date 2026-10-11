package snippets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectStack(t *testing.T) {
	cases := []struct {
		files               map[string]string
		platform, framework string
		label, sdk          string
	}{
		{map[string]string{"package.json": `{"dependencies":{"next":"15","react":"19"}}`}, "javascript-nextjs", "nextjs", "Node / Next.js", "@sentry/nextjs"},
		{map[string]string{"package.json": `{"devDependencies":{"vite":"6"},"dependencies":{"react":"19"}}`}, "javascript-react", "react", "JavaScript / React (Vite)", "@sentry/react"},
		{map[string]string{"package.json": `{"devDependencies":{"vite":"6"}}`}, "javascript", "vite", "JavaScript / Vite", "@sentry/browser"},
		{map[string]string{"package.json": `{"dependencies":{"express":"5"}}`}, "node-express", "express", "Node / Express", "@sentry/node"},
		{map[string]string{"package.json": `{"dependencies":{"@nestjs/core":"11"}}`}, "node-nestjs", "nestjs", "Node / NestJS", "@sentry/nestjs"},
		{map[string]string{"package.json": `{}`}, "node", "", "Node", "@sentry/node"},
		{map[string]string{"requirements.txt": "Django==5.1\n"}, "python-django", "django", "Python / Django", "sentry-sdk"},
		{map[string]string{"pyproject.toml": "[project]\ndependencies = [\"fastapi\"]\n"}, "python-fastapi", "fastapi", "Python / FastAPI", "sentry-sdk"},
		{map[string]string{"Pipfile": "[packages]\nflask = \"*\"\n"}, "python-flask", "flask", "Python / Flask", "sentry-sdk"},
		{map[string]string{"Gemfile": "gem \"rails\", \"~> 8.0\"\n", "package.json": "{}"}, "ruby-rails", "rails", "Ruby / Rails", "sentry-rails"},
		{map[string]string{"Gemfile": "gem \"sinatra\"\n"}, "ruby", "", "Ruby", "sentry-ruby"},
		{map[string]string{"composer.json": `{"require":{"laravel/framework":"^11"}}`}, "php-laravel", "laravel", "PHP / Laravel", "sentry/sentry-laravel"},
		{map[string]string{"composer.json": `{"require":{"symfony/framework-bundle":"7"}}`}, "php-symfony", "symfony", "PHP / Symfony", "sentry/sentry-symfony"},
		{map[string]string{"go.mod": "module x\n\nrequire github.com/gin-gonic/gin v1.10.0\n"}, "go", "gin", "Go / Gin", "github.com/getsentry/sentry-go"},
		{map[string]string{"Shop.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"></Project>`}, "dotnet-aspnetcore", "aspnetcore", ".NET / ASP.NET Core", "Sentry.AspNetCore"},
		{map[string]string{"Tool.csproj": `<Project Sdk="Microsoft.NET.Sdk"></Project>`}, "dotnet", "", ".NET", "Sentry"},
		{map[string]string{"README.md": "x"}, "", "", "unknown", ""},
	}
	for _, c := range cases {
		root := t.TempDir()
		for name, content := range c.files {
			if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got := DetectStack(root)
		if got.Platform != c.platform || got.Framework != c.framework || got.Label != c.label || got.SDK != c.sdk {
			t.Errorf("%v: got %+v", c.files, got)
		}
	}
}
