package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/jv"
	"github.com/Checkend/checkend-cli/internal/sourcemap"
)

// maxIndexBytes is the upload limit of the server.
const maxIndexBytes = 30 << 20

type mapFile struct {
	file  string // as the user sees it
	name  string // the minified file, for the server
	index []byte
}

// findMaps returns the .map files of a path (a file, or a directory without node_modules).
func findMaps(cwd, arg, prefix string) ([]mapFile, error) {
	path := arg
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, arg)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, clierr.UsageError(fmt.Sprintf("Cannot read %s.", arg), "Give the build directory or the .map files.")
	}
	name := func(rel string) string {
		return prefix + strings.TrimSuffix(filepath.ToSlash(rel), ".map")
	}
	if !info.IsDir() {
		return []mapFile{{file: arg, name: name(filepath.Base(path))}}, nil
	}
	var out []mapFile
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".map") {
			rel, _ := filepath.Rel(path, p)
			out = append(out, mapFile{file: filepath.Join(arg, rel), name: name(rel)})
		}
		return nil
	})
	if err != nil {
		return nil, clierr.Fail(fmt.Sprintf("Cannot read %s: %v", arg, err))
	}
	if len(out) == 0 {
		return nil, clierr.UsageError(fmt.Sprintf("No .map files in %s.", arg), "Make the build write source maps, then upload them.")
	}
	return out, nil
}

func sourcemapsUploadCommand() *Command {
	return &Command{
		Path:    []string{"sourcemaps", "upload"},
		Summary: "Upload the source maps of a build (in CI), so that briefs show the original code",
		Usage:   "--release <v> [--url-prefix <p>] [--project <id|slug>] [--dry-run] <dir|file.map>...",
		Details: []string{
			"  --release <version>   The release of the SDK (the same value as in Sentry.init)",
			"  --url-prefix <p>      Where the browser loads the files: ~/ (default, any host),",
			"                        /assets/ or https://cdn.example/assets/",
			"  --project <id|slug>   Default: the project in .checkend.json",
			"  --dry-run             Check the maps and print the names; upload nothing",
			"",
			"The CLI builds the index of each map, so the server does no work. A map for",
			"dist/static/app.js.map is the file ~/static/app.js. Needs the releases:write scope.",
		},
		Options: map[string]Kind{"release": String, "url-prefix": String, "project": String, "dry-run": Bool},
		Args:    []string{"[path...]"},
		Run: func(s *Session) error {
			release := s.String("release")
			if release == "" {
				return clierr.UsageError("Give the release with --release.", "Use the same release as the SDK, for example --release \"$GIT_SHA\".")
			}
			paths := s.Args
			if len(paths) == 0 {
				return clierr.UsageError("Give the build directory or the .map files.", "Example: ce sourcemaps upload --release \"$GIT_SHA\" dist/")
			}
			ref, err := s.ProjectRef()
			if err != nil {
				return err
			}
			if ref == "" && !s.Bool("dry-run") {
				return clierr.UsageError("No project given and no .checkend.json found.", "Run `ce init` to link this repo, or give --project.")
			}
			prefix := s.String("url-prefix")
			if prefix == "" {
				prefix = "~/"
			}
			if !strings.HasSuffix(prefix, "/") {
				prefix += "/"
			}
			var files []mapFile
			for _, arg := range paths {
				found, err := findMaps(s.Host.Cwd, arg, prefix)
				if err != nil {
					return err
				}
				files = append(files, found...)
			}
			// Check every map before the first upload.
			for i := range files {
				f := &files[i]
				path := f.file
				if !filepath.IsAbs(path) {
					path = filepath.Join(s.Host.Cwd, path)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return clierr.Fail(fmt.Sprintf("Cannot read %s: %v", f.file, err))
				}
				if f.index, err = sourcemap.Build(data); err != nil {
					var invalid *sourcemap.InvalidError
					if errors.As(err, &invalid) {
						return clierr.UsageError(fmt.Sprintf("%s is not a valid source map: %s", f.file, invalid.Msg), "Check the build that writes it.")
					}
					return err
				}
				if len(f.index) > maxIndexBytes {
					return clierr.UsageError(fmt.Sprintf("The index of %s is larger than 30 MB.", f.file), "Split the bundle into smaller files.")
				}
			}
			type uploaded struct {
				File       string `json:"file"`
				Name       string `json:"name"`
				IndexBytes int    `json:"index_bytes"`
				Sourcemap  any    `json:"sourcemap"`
			}
			result := []uploaded{}
			if s.Bool("dry-run") {
				for _, f := range files {
					result = append(result, uploaded{f.file, f.name, len(f.index), nil})
				}
				s.Out.Data(map[string]any{"data": result, "dry_run": true}, func(d any) string {
					var rows [][]any
					for _, u := range jv.List(jv.Get(d, "data")) {
						rows = append(rows, []any{jv.Get(u, "file"), jv.Get(u, "name")})
					}
					return s.Out.Table([]string{"FILE", "NAME"}, rows) + fmt.Sprintf("\n\nChecked %d source map(s). Nothing was uploaded (--dry-run).", len(rows))
				})
				return nil
			}
			client, err := s.API()
			if err != nil {
				return err
			}
			project := strings.ToLower(strings.TrimSpace(ref))
			for _, f := range files {
				query := url.Values{"project": {project}, "release": {release}, "name": {f.name}}
				raw, err := client.Call("POST", "/v1/sourcemaps?"+query.Encode(), f.index, sourcemap.ContentType)
				if raw, err = projectNotFound(raw, err, ref); err != nil {
					return err
				}
				body, _ := jv.Decode(raw)
				result = append(result, uploaded{f.file, f.name, len(f.index), jv.Get(body, "data")})
			}
			s.Out.Data(map[string]any{"data": result}, func(d any) string {
				var rows [][]any
				for _, u := range jv.List(jv.Get(d, "data")) {
					rows = append(rows, []any{jv.Get(u, "file"), jv.Get(u, "name")})
				}
				noun := "source maps"
				if len(rows) == 1 {
					noun = "source map"
				}
				return fmt.Sprintf("Uploaded %d %s for release %s.\n\n", len(rows), noun, release) + s.Out.Table([]string{"FILE", "NAME"}, rows)
			})
			return nil
		},
	}
}
