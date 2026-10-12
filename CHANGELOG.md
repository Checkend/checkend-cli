# Changelog

All notable changes to the Checkend CLI (`ce`) are in this file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the versions follow
[Semantic Versioning](https://semver.org/). The exit codes and the JSON output are part of the
public interface.

## [Unreleased]

The first release. Nothing is tagged or published yet. Until then, build `ce` from source:
`go install github.com/Checkend/checkend-cli/cmd/ce@latest`.

### Added

- One static binary for macOS, Linux and Windows (amd64 and arm64), written in Go. It has two
  names: `ce` and `checkend`.
- Setup commands: `ce login` (also `--with-token` for scripts), `ce logout`, `ce status` and
  `ce init`.
- Agent-first setup: `ce setup --url <url> --key <cks_…>` registers the app, writes
  `.checkend.json`, saves an agent token, sends a test event, and prints the MCP command and the
  next steps. `ce setup report sdk_installed` reports a setup step.
- `ce mcp`: a local MCP server (stdio) for Claude Code, Codex and Cursor. It sends each message to
  the instance with the agent token.
- Issues and events: `ce next`, `ce issue list` (with `--status`, `--release`, `--env`,
  `--since`, `--search`, `--project`, `--limit` and `--cursor`), `ce issue view` and
  `ce event view`.
- Projects: `ce project list`, `ce project create`, `ce project dsn` and `ce project delete`.
- Tasks for CI and agents: `ce deploy`, `ce sourcemaps upload` (the CLI builds the source map
  index, so the server does no work), `ce dsn rotate` (and `--revoke-old`) and
  `ce migrate-from-sentry`.
- Output: short tables and cards on a terminal; the API JSON when stdout is not a terminal or with
  `--json`; text everywhere with `--plain`.
- Exit codes: 0 OK, 1 error, 2 usage, 3 not found, 4 conflict, 5 authentication, 6 accepted and
  waiting for approval.
- Every command works with an instance under a base path (`--url https://host.example/<id>`):
  the requests, the DSNs, `ce setup`, `ce mcp` and `ce sourcemaps upload` keep the path.
- `ce install docker` writes `compose.yaml` and `.env` for a released, signed server version:
  the image is pinned by its digest, with a volume, restarts and a nightly backup. It shows the
  plan and asks first (`--yes` for agents), keeps the secrets when it runs again, saves the admin
  token in the keychain, and prints the setup link. It never prints a secret.
- `ce install ssh <user@host> --domain <domain>` installs Checkend on an Ubuntu or Debian LTS
  server with the SSH agent. It verifies the SSH host key (from `known_hosts`, `--host-key` or
  a confirmation; never skipped), installs Docker from Docker's apt repository (key fingerprint
  checked), and starts Checkend behind Caddy (the domain must point at the server) or a
  Cloudflare Tunnel (`--tunnel`). Secrets go to the server only through stdin.
- `ce install cloudflare` installs a signed server release on a Cloudflare account: it creates
  the D1 database, records a Time Travel bookmark before the migrations, uploads the Worker
  bundle, sets the secrets through the API, adds the cron trigger and the workers.dev URL, and
  prints the setup link. The API token comes from a link that fills in the minimal permissions,
  and stays in the keychain. It refuses a Worker or a database of the same name that is not a
  Checkend install, and a database with newer migrations. `--r2` adds the R2 bucket for blobs.
- `ce upgrade`, `ce backup`, `ce status --server` and `ce uninstall` for a server that
  `ce install` installed. An upgrade backs up first (a copy of the database, or a D1 Time Travel
  bookmark), keeps the secrets and the setup code, and refuses a downgrade unless
  `--allow-downgrade`; an older migration level is always refused. An uninstall keeps the data
  unless `--delete-data`.
- The server commands are under `ce server`: `ce server install docker|ssh|cloudflare`,
  `ce server upgrade`, `ce server backup`, `ce server status` and `ce server uninstall`. The old
  names still work in this release, hidden from the help, with a hint to the new name.
  `ce server install` without a target lists the three targets. On a hosted space (a URL with a
  base path) the server commands say that the space updates itself.
- `ce update` updates ce itself: it finds the install method (Homebrew, npm, the install script
  or go install) and runs its update after a confirmation, or prints the commands. Until a ce
  release exists, it says so and prints the `go install` command.
- `ce setup --key-stdin` and `ce setup report --key-stdin` read the setup key from stdin, so it
  stays out of the shell history and `ps`. Without a key on a terminal, they ask for it with a
  hidden prompt. `--key` still works in this release and prints a warning. The MCP commands
  that `ce setup` and `ce mcp --help` print name the absolute path of ce when `ce` on PATH is
  another binary or none.
- Every error ends with a link to the docs section that explains it; JSON errors have `"docs"`
  next to `"error"` and `"hint"`.
- Install methods for the first release: Homebrew, `install.sh` and the npm wrapper
  `@checkend/cli`. `install.sh` and the npm wrapper first check the SSH signature of
  `checksums.txt` (a Checkend release key, namespace `checkend-cli-release`), then the SHA-256 of
  the binary; without a valid signature they install nothing. Homebrew checks the SHA-256 of
  the formula.

### Security

- Tokens go to the OS keychain (macOS Keychain, Windows Credential Manager or the Secret Service).
  Without a keychain, they go to the config file with mode 600.
- `ce setup` uses a separate agent token for each app (scopes `issues:read` and
  `issues:write`). `ce mcp` uses only that token, never the login of a person, so an agent never
  gets admin power over MCP.
- The CLI never prints a setup key or a token, and never writes a token to `.checkend.json`.
- HTTPS only, except `http://localhost` and `http://127.0.0.1`. The CLI does not follow redirects.
- All server text is cleaned of terminal escape sequences and control characters before it is
  printed.
- An unknown command or option exits with code 2 and does nothing.
- `ce server install` installs only a server release whose manifest has a valid SSH signature
  (ed25519, `ssh-keygen -Y sign`, namespace `checkend-server-release`) by a trusted key. It checks
  the signature before it reads the manifest; anyone can check it by hand with
  `ssh-keygen -Y verify`.
- A saved token is for its full URL, with the base path: two instances under one host never
  share a token. A URL path with empty, `.` or `..` segments, or an encoded `/`, `\` or `.`,
  exits with code 2.

[Unreleased]: https://github.com/Checkend/checkend-cli/commits/main
