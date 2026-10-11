package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Checkend/checkend-cli/internal/sourcemap"
	. "github.com/Checkend/checkend-cli/internal/testkit"
)

const mapText = `{"version":3,"sources":["src/a.ts"],"names":[],"mappings":"AAAA"}`

func buildDir(cli *Harness) {
	cli.Write("dist/static/app.min.js.map", mapText)
	cli.Write("dist/vendor.js.map", mapText)
	cli.Write("dist/static/app.min.js", "x")
	cli.Write("dist/node_modules/lib/x.js.map", mapText)
}

func TestSourcemapsUploadSendsAPrebuiltIndexForEachMap(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	cli.Write(".checkend.json", `{"project":"app"}`)
	buildDir(cli)
	r := cli.Run([]string{"sourcemaps", "upload", "--release", "v1.2.3", "dist"}, Options{Env: env})
	Code(t, r, 0)
	want, _ := sourcemap.Build([]byte(mapText))
	var names []string
	for _, u := range api.Uploads {
		names = append(names, u.Query.Get("name"))
		Equal(t, u.Query.Get("project"), "app")
		Equal(t, u.Query.Get("release"), "v1.2.3")
		Equal(t, u.ContentType, "application/x-checkend-sourcemap-index")
		if !bytes.Equal(u.Raw, want) {
			t.Error("the body is not the index")
		}
	}
	Equal(t, names, []string{"~/static/app.min.js", "~/vendor.js"})
	data := r.JSON(t)["data"].([]any)
	Equal(t, len(data), 2)
	Equal(t, data[0].(map[string]any)["file"], filepath.Join("dist", "static", "app.min.js.map"))
}

func TestSourcemapsUploadWithAURLPrefixAndAFile(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	buildDir(cli)
	r := cli.Run([]string{"sourcemaps", "upload", "--project", "app", "--release", "1", "--url-prefix", "https://cdn.example/assets",
		filepath.Join("dist", "static", "app.min.js.map")}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	Equal(t, api.Uploads[0].Query.Get("name"), "https://cdn.example/assets/app.min.js")
	Contains(t, r.Stdout, "Uploaded 1 source map for release 1")
}

func TestSourcemapsUploadTakesManyPaths(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	for _, n := range []string{"a", "b", "c"} {
		cli.Write(n+".js.map", mapText)
	}
	Code(t, cli.Run([]string{"sourcemaps", "upload", "--project", "app", "--release", "1", "a.js.map", "b.js.map", "c.js.map"}, Options{Env: env}), 0)
	Equal(t, len(api.Uploads), 3)
}

func TestSourcemapsUploadDryRunSendsNothing(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	buildDir(cli)
	r := cli.Run([]string{"sourcemaps", "upload", "--project", "app", "--release", "1", "--dry-run", "dist"}, Options{Env: env, TTY: true})
	Code(t, r, 0)
	Contains(t, r.Stdout, "~/vendor.js")
	if len(api.Requests()) != 0 {
		t.Error("request made")
	}
}

func TestSourcemapsUploadChecksEveryMapBeforeItSendsOne(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	buildDir(cli)
	cli.Write("dist/zz.js.map", `{"version":2}`)
	r := cli.Run([]string{"sourcemaps", "upload", "--project", "app", "--release", "1", "dist"}, Options{Env: env})
	Code(t, r, 2)
	Contains(t, r.ErrJSON(t)["error"].(string), "zz.js.map")
	Contains(t, r.ErrJSON(t)["error"].(string), "version 3")
	if len(api.Uploads) != 0 {
		t.Error("uploaded before the check")
	}
}

func TestSourcemapsUploadInputErrors(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	buildDir(cli)
	_ = os.MkdirAll(filepath.Join(cli.Repo, "empty"), 0o755)
	for _, argv := range [][]string{
		{"sourcemaps", "upload", "--project", "app", "dist"},
		{"sourcemaps", "upload", "--project", "app", "--release", "1"},
		{"sourcemaps", "upload", "--project", "app", "--release", "1", "missing"},
		{"sourcemaps", "upload", "--project", "app", "--release", "1", "empty"},
		{"sourcemaps", "upload", "--release", "1", "dist"},
	} {
		r := cli.Run(argv, Options{Env: env})
		Code(t, r, 2)
	}
	if len(api.Requests()) != 0 {
		t.Error("request made")
	}
}

func TestSourcemapsUploadReportsStorageNotConfigured(t *testing.T) {
	api, cli, env := setup(t)
	api.AddProject("App")
	buildDir(cli)
	api.Override = func(r *Request) *Reply {
		if r.Path == "/v1/sourcemaps" {
			return &Reply{Status: 400, Body: J{"error": "storage_not_configured", "reason": "Bind an R2 bucket as BLOBS: Checkend stores source maps there."}}
		}
		return nil
	}
	r := cli.Run([]string{"sourcemaps", "upload", "--project", "app", "--release", "1", "dist"}, Options{Env: env})
	Code(t, r, 2)
	Contains(t, r.ErrJSON(t)["error"].(string), "R2 bucket")
}
