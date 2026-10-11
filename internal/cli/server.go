package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/config"
	"github.com/Checkend/checkend-cli/internal/serverrelease"
	"github.com/Checkend/checkend-cli/internal/sshhost"
)

// The server commands work on a server that `ce install` installed from this machine: the
// install record (installs.json) names its kind and where it is.

func serverCommands() []*Command {
	return []*Command{upgradeCommand(), backupCommand(), uninstallCommand()}
}

// installed is the URL and the install record of the instance (--url, CHECKEND_URL, or the
// current one).
func installed(s *Session) (string, *config.Install, error) {
	u, err := s.URL()
	if err != nil {
		return "", nil, clierr.New(clierr.Failure, "Which server? Give --url.", "Run `ce install` first to install one.")
	}
	installs, err := config.ReadInstalls(s.Host)
	if err != nil {
		return "", nil, err
	}
	rec, ok := installs[u]
	if !ok {
		return u, nil, clierr.New(clierr.Failure, fmt.Sprintf("%s was not installed with `ce install` on this machine.", u),
			"Run `ce install docker|ssh|cloudflare` first, or give the --url of a server that it installed.")
	}
	return u, &rec, nil
}

// sshFor is the SSH connection of an install record. ce's known_hosts has the confirmed key.
func sshFor(s *Session, rec *config.Install) (*sshServer, error) {
	target, err := sshhost.ParseTarget(rec.Target)
	if err != nil {
		return nil, clierr.Fail(fmt.Sprintf("The install record has an invalid target %q.", rec.Target))
	}
	return &sshServer{s: s, target: target, port: rec.Port, known: filepath.Join(config.Dir(s.Host), "known_hosts")}, nil
}

// cfFor is a Cloudflare client for an install record.
func cfFor(s *Session) (*cfClient, error) {
	token, _, err := cloudflareToken(s, false)
	if err != nil {
		return nil, err
	}
	return newCFClient(s, token), nil
}

func backupName(s *Session) string {
	return "/data/backups/checkend-" + s.Host.Now().UTC().Format("20060102-150405") + ".sqlite"
}

func upgradeCommand() *Command {
	return &Command{
		Path:    []string{"upgrade"},
		Summary: "Upgrade a server that ce install installed to a newer signed release",
		Usage:   "[--url <url>] [--version <v>] [--allow-downgrade] [--yes]",
		Details: []string{
			"  --url <url>          The server (default: the current instance)",
			"  --version <v>        The release (default: the latest one)",
			"  --allow-downgrade    Allow an older version with the same migration level",
			"  --yes                Do not ask (for agents); the plan is still printed on stderr",
			"",
			"Backs up first (Docker and SSH: a copy of the database; Cloudflare: a D1 Time Travel",
			"bookmark), then installs the release with the same settings. The secrets and the setup",
			"code stay. Migrations only go forward: an older migration level is always refused.",
		},
		Options: map[string]Kind{"version": String, "allow-downgrade": Bool, "yes": Bool},
		Run: func(s *Session) error {
			if err := needApproval(s, "ce upgrade"); err != nil {
				return err
			}
			u, rec, err := installed(s)
			if err != nil {
				return err
			}
			parsed, _ := url.Parse(u)
			var path, argv []string
			switch rec.Kind {
			case "docker":
				path, argv = []string{"install", "docker"}, []string{"--dir", rec.Dir, "--port", parsed.Port()}
			case "ssh":
				path, argv = []string{"install", "ssh"}, []string{rec.Target, "--domain", parsed.Hostname(), "--port", rec.Port, "--dir", rec.Dir, "--host-key", rec.HostKey}
				if rec.Proxy == "tunnel" {
					argv = append(argv, "--tunnel")
				}
			case "cloudflare":
				path, argv = []string{"install", "cloudflare"}, []string{"--account", rec.Account, "--name", rec.Worker, "--database", rec.Database}
			default:
				return clierr.Fail(fmt.Sprintf("The install record of %s has an unknown kind %q.", u, rec.Kind))
			}
			if v := s.String("version"); v != "" {
				argv = append(argv, "--version", v)
			}
			if s.Bool("yes") {
				argv = append(argv, "--yes")
			}
			err = runAs(s, path, argv, rec)
			if errors.Is(err, errUpToDate) {
				s.Out.Data(map[string]any{"url": u, "version": rec.Version, "message": "Checkend " + rec.Version + " is already installed."},
					func(any) string {
						return fmt.Sprintf("Checkend %s is already installed at %s. Nothing changed.", rec.Version, u)
					})
				return nil
			}
			return err
		},
	}
}

// runAs runs an install command with these arguments, in upgrade mode.
func runAs(s *Session, path, argv []string, rec *config.Install) error {
	for _, c := range commands() {
		if strings.Join(c.Path, " ") != strings.Join(path, " ") {
			continue
		}
		options := map[string]Kind{}
		for k, v := range globalOptions {
			options[k] = v
		}
		for k, v := range c.Options {
			options[k] = v
		}
		p, err := parse(argv, options)
		if err != nil {
			return clierr.Fail(fmt.Sprintf("Cannot upgrade from the install record: %v", err))
		}
		return c.Run(&Session{Host: s.Host, Out: s.Out, Args: p.args, p: p, upgrade: rec, allowDowngrade: s.Bool("allow-downgrade")})
	}
	return clierr.Fail("Unknown install command.")
}

func backupCommand() *Command {
	return &Command{
		Path:    []string{"backup"},
		Summary: "Back up the database of a server that ce install installed",
		Usage:   "[--url <url>] [--output <file>]",
		Details: []string{
			"  --url <url>        The server (default: the current instance)",
			"  --output <file>    Also copy the backup to this file here (Docker and SSH)",
			"",
			"Docker and SSH: a consistent copy of the SQLite database in the volume",
			"(/data/backups). Cloudflare: a D1 Time Travel bookmark and the command to restore it.",
		},
		Options: map[string]Kind{"output": String},
		Run: func(s *Session) error {
			u, rec, err := installed(s)
			if err != nil {
				return err
			}
			output := s.String("output")
			if output != "" && !filepath.IsAbs(output) {
				output = filepath.Join(s.Host.Cwd, output)
			}
			result := map[string]any{"url": u, "kind": rec.Kind}
			switch rec.Kind {
			case "docker":
				if !dockerRunning(s, rec.Dir) {
					return clierr.New(clierr.Failure, "Checkend is not running, so there is nothing to back up.", "Start it: docker compose --project-directory "+rec.Dir+" up -d")
				}
				file := backupName(s)
				if err := dockerBackup(s, rec.Dir, file); err != nil {
					return err
				}
				result["file"] = file
				if output != "" {
					if _, err := dockerCompose(s, rec.Dir, "cp", "checkend:"+file, output); err != nil {
						return clierr.Fail(fmt.Sprintf("The backup is %s in the volume, but the copy failed: %v", file, err))
					}
					// docker compose cp keeps the mode of the container; the copy has all the data.
					if err := os.Chmod(output, 0o600); err != nil {
						return clierr.Fail(fmt.Sprintf("Cannot make %s private (mode 600): %v", output, err))
					}
					result["output"] = output
				}
			case "ssh":
				srv, err := sshFor(s, rec)
				if err != nil {
					return err
				}
				file := backupName(s)
				script := "# ce:backup\nset -eu\ndir=" + shellQuote(rec.Dir) + "\n" + sudoLine +
					"$SUDO docker compose --project-directory \"$dir\" exec -T checkend mkdir -p /data/backups\n" +
					"$SUDO docker compose --project-directory \"$dir\" exec -T checkend node scripts/backup.ts " + file + " >&2\n"
				if _, err := srv.run(script); err != nil {
					return clierr.New(clierr.Failure, fmt.Sprintf("The backup on %s failed: %v", rec.Target, err), "Check that Checkend runs on the server.")
				}
				result["file"] = file
				if output != "" {
					data, err := srv.run("# ce:copy\nset -eu\ndir=" + shellQuote(rec.Dir) + "\n" + sudoLine +
						"$SUDO docker compose --project-directory \"$dir\" exec -T checkend cat " + file + "\n")
					if err != nil {
						return clierr.Fail(fmt.Sprintf("The backup is %s on the server, but the copy failed: %v", file, err))
					}
					if err := config.WriteFile(output, data, 0o600); err != nil {
						return clierr.Fail(fmt.Sprintf("Cannot write %s: %v", output, err))
					}
					result["output"] = output
				}
			case "cloudflare":
				if output != "" {
					return clierr.UsageError("--output is for Docker and SSH installs.", "For a file of a D1 database: npx wrangler d1 export "+rec.Database+" --remote --output <file>")
				}
				c, err := cfFor(s)
				if err != nil {
					return err
				}
				var bookmark struct {
					Bookmark string `json:"bookmark"`
				}
				if err := c.json("GET", "/accounts/"+rec.Account+"/d1/database/"+rec.DatabaseID+"/time_travel/bookmark", nil, &bookmark); err != nil {
					return cfFail(err, "")
				}
				result["bookmark"] = bookmark.Bookmark
				result["restore"] = fmt.Sprintf("npx wrangler d1 time-travel restore %s --bookmark=%s", rec.Database, bookmark.Bookmark)
			}
			s.Out.Data(result, func(d any) string {
				m := d.(map[string]any)
				if b, ok := m["bookmark"]; ok {
					return fmt.Sprintf("D1 Time Travel bookmark: %v\nTo restore: %v", b, m["restore"])
				}
				text := fmt.Sprintf("Backed up the database to %v (in the volume).", m["file"])
				if o, ok := m["output"]; ok {
					text += fmt.Sprintf("\nCopied it to %v.", o)
				}
				return text
			})
			return nil
		},
	}
}

// serverStatus is `ce status --server`.
func serverStatus(s *Session) error {
	u, rec, err := installed(s)
	if err != nil {
		return err
	}
	result := map[string]any{"url": u, "install": rec}
	if _, raw, err := api.New(s.Host, u, "").Health(); err == nil {
		result["health"] = json.RawMessage(raw)
	} else {
		result["health"] = nil
		result["health_error"] = err.Error()
	}
	if latest, err := releaseSource(s).Latest(); err == nil {
		result["latest"] = latest.Manifest.Version
		result["upgrade_available"] = serverrelease.Compare(latest.Manifest.Version, rec.Version) > 0
	} else {
		result["latest_error"] = err.Error()
	}
	s.Out.Data(result, func(d any) string {
		m := d.(map[string]any)
		inst := m["install"].(map[string]any)
		lines := []string{fmt.Sprintf("%s: Checkend %v (%v), installed %v.", u, inst["version"], inst["kind"], inst["installed_at"])}
		if h, ok := m["health"].(map[string]any); ok {
			lines = append(lines, fmt.Sprintf("Health: %v.", h["status"]))
		} else {
			lines = append(lines, fmt.Sprintf("Health: not reachable (%v).", m["health_error"]))
		}
		if m["upgrade_available"] == true {
			lines = append(lines, fmt.Sprintf("Checkend %v is available: run `ce upgrade`.", m["latest"]))
		} else if l, ok := m["latest"]; ok {
			lines = append(lines, fmt.Sprintf("The latest release is %v.", l))
		}
		return strings.Join(lines, "\n")
	})
	return nil
}

func uninstallCommand() *Command {
	return &Command{
		Path:    []string{"uninstall"},
		Summary: "Remove a server that ce install installed (the data stays unless --delete-data)",
		Usage:   "[--url <url>] [--delete-data] [--yes]",
		Details: []string{
			"  --url <url>       The server (default: the current instance)",
			"  --delete-data     Also delete the data: the volume and the files (Docker, SSH), or",
			"                    the D1 database (Cloudflare). This cannot be undone.",
			"  --yes             Do not ask (for agents)",
		},
		Options: map[string]Kind{"delete-data": Bool, "yes": Bool},
		Run: func(s *Session) error {
			if err := needYes(s, "ce uninstall"); err != nil {
				return err
			}
			u, rec, err := installed(s)
			if err != nil {
				return err
			}
			deleteData := s.Bool("delete-data")
			warning := []string{fmt.Sprintf("This stops and removes Checkend at %s (%s). The data stays.", u, rec.Kind)}
			question, want := fmt.Sprintf("Type %s to confirm: ", u), u
			if deleteData {
				warning = []string{fmt.Sprintf("This removes Checkend at %s (%s) AND DELETES ALL ITS DATA. It cannot be undone.", u, rec.Kind)}
				question, want = fmt.Sprintf("Type delete %s to confirm: ", u), "delete "+u
			}
			if err := confirm(s, warning, question, want); err != nil {
				return err
			}
			kept := ""
			switch rec.Kind {
			case "docker":
				args := []string{"down"}
				if deleteData {
					args = append(args, "-v")
				}
				if _, err := dockerCompose(s, rec.Dir, args...); err != nil {
					return clierr.Fail(fmt.Sprintf("docker compose down failed: %v", err))
				}
				if deleteData {
					for _, f := range []string{"compose.yaml", ".env"} {
						if err := os.Remove(filepath.Join(rec.Dir, f)); err != nil && !os.IsNotExist(err) {
							return clierr.Fail(fmt.Sprintf("Cannot remove %s: %v", f, err))
						}
					}
				} else {
					kept = fmt.Sprintf("The data is kept in the Docker volume, with %s/.env (it has the encryption keys).", rec.Dir)
				}
			case "ssh":
				srv, err := sshFor(s, rec)
				if err != nil {
					return err
				}
				script := "# ce:uninstall\nset -eu\ndir=" + shellQuote(rec.Dir) + "\n" + sudoLine
				if deleteData {
					script += "$SUDO docker compose --project-directory \"$dir\" down -v\n$SUDO rm -rf \"$dir\"\n"
				} else {
					script += "$SUDO docker compose --project-directory \"$dir\" down\n"
					kept = fmt.Sprintf("The data is kept on %s: the Docker volume, and %s/.env (it has the encryption keys).", rec.Target, rec.Dir)
				}
				if _, err := srv.run(script); err != nil {
					return clierr.Fail(fmt.Sprintf("The uninstall on %s failed: %v", rec.Target, err))
				}
			case "cloudflare":
				c, err := cfFor(s)
				if err != nil {
					return err
				}
				if err := c.json("DELETE", "/accounts/"+rec.Account+"/workers/scripts/"+rec.Worker+"?force=true", nil, nil); err != nil && !notFound(err) {
					return cfFail(err, "")
				}
				if deleteData {
					if err := c.json("DELETE", "/accounts/"+rec.Account+"/d1/database/"+rec.DatabaseID, nil, nil); err != nil && !notFound(err) {
						return cfFail(err, "")
					}
				} else {
					kept = fmt.Sprintf("The D1 database %s is kept (and an R2 bucket, if any).", rec.Database)
				}
			}
			if err := config.SaveInstall(s.Host, u, nil); err != nil {
				return err
			}
			if _, err := config.DeleteToken(s.Host, u); err != nil {
				return err
			}
			data := "deleted"
			if kept != "" {
				data = "kept"
			}
			s.Out.Data(map[string]any{"url": u, "kind": rec.Kind, "removed": true, "data": data, "note": kept},
				func(any) string { return strings.TrimSpace(fmt.Sprintf("Removed Checkend at %s.\n%s", u, kept)) })
			return nil
		},
	}
}
