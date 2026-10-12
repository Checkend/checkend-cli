# Checkend CLI

The command-line interface for [Checkend](https://github.com/Checkend/checkend), open-source
error tracking for coding agents. One command, two names: `ce` (short) and `checkend`.

```sh
ce next            # the issue to work on next, with the next commands
ce issue view 42   # one issue
ce event view 7    # its stack, request and tags
```

On a terminal, the CLI prints short tables and cards. When stdout is not a terminal (an agent,
a script, a pipe), or with `--json`, it prints the API JSON.

## Install

`ce` is one binary for macOS, Linux and Windows. From the first release, install it with one
of these:

```sh
brew install checkend/tap/checkend
curl -fsSL https://raw.githubusercontent.com/Checkend/checkend-cli/main/install.sh | sh
npx @checkend/cli --version
```

`install.sh` and the npm package first check the SSH signature of `checksums.txt` (made with a
Checkend release key, namespace `checkend-cli-release`), then the SHA-256 of the binary. Without
a valid signature they install nothing. `install.sh` needs `ssh-keygen` from OpenSSH 8.1 or later
(macOS and current Linux have it). See [SECURITY.md](SECURITY.md) to check a download by hand.
Until the first release, build it from source (Go 1.26 or later):

```sh
go install github.com/Checkend/checkend-cli/cmd/ce@latest
ce --version
```

## Set up with a coding agent

In Checkend, "Copy prompt" gives a prompt for Claude Code, Codex or Cursor with a setup key
(`cks_…`, valid for 1 hour). The agent pipes the key to `ce setup`, so that it stays out of the
shell history and `ps`:

```sh
printf '%s\n' "$CHECKEND_SETUP_KEY" | ce setup --url https://checkend.example.com --key-stdin
```

On a terminal without a key, `ce setup` asks for it with a hidden prompt. `--key cks_…` still
works in this release, with a warning.

`ce setup` registers the app, writes `.checkend.json`, saves an agent token for the app in the OS
keychain, sends a test event, and prints the MCP command and the code changes for the agent. The
agent token (scopes `issues:read` and `issues:write`) is separate from your own login, which
stays unchanged. The setup key and the token are never printed. The agent connects MCP with
`ce mcp`, which uses only the agent token, never your login, and keeps it in the keychain:

```sh
claude mcp add checkend -- ce mcp --url https://checkend.example.com
```

## Get started

1. Log in to your Checkend instance with an API token (or the `ADMIN_TOKEN` secret of your
   Checkend instance). The CLI checks the URL and the token. It saves the token in the OS
   keychain (macOS Keychain, Windows Credential Manager or the Secret Service on Linux), and the
   URL in `~/.config/checkend/config.json`. Without a keychain, the token goes to that file
   (mode 600), and the CLI tells you.

   ```sh
   ce login
   ```

   In CI or a script, pipe the token on stdin:

   ```sh
   echo "$CHECKEND_TOKEN" | ce login --url https://checkend.example.com --with-token
   ```

2. In the repo of your app, link a project (or create one):

   ```sh
   ce init
   ```

   `ce init` detects the platform, prints the DSN and the SDK setup code, and writes
   `.checkend.json`. This file has the project link only, never a token: you can commit it.
   Checkend is Sentry SDK compatible, so you use a Sentry SDK with the Checkend DSN.

3. Check the setup:

   ```sh
   ce status
   ```

## Commands

| Command | What it does |
|---|---|
| `ce login [--url <url>] [--with-token]` | Check and save the URL and token |
| `ce logout` | Remove the saved token of the current instance |
| `ce status` | Instance, version, health, ingest mode, linked project |
| `ce init [--name <name> \| --project <id\|slug>] [--platform <p>]` | Link the repo to a project (or create one); print the DSN and SDK snippet |
| `ce next [--project <id\|slug>]` | The most recent unresolved issue, with the next commands |
| `ce issue list [--status <s>] [--release <v>] [--env <name>] [--since <t>] [--search <text>] [--project <id\|slug>] [--limit <n>] [--cursor <c>]` | Issues, the most recent first. `--since` takes `30m`, `24h`, `7d` or an ISO time |
| `ce issue view <id>` | One issue. `api-42` also checks that issue 42 is in project `api` |
| `ce event view <id> [--section stack\|request\|tags]` | One event |
| `ce project list` | All projects |
| `ce project create --name <name> [--platform <p>]` | Create a project and print its DSN |
| `ce project dsn [<id\|slug>]` | The DSN of a project (default: the linked project) |
| `ce project delete <id\|slug> [--yes]` | Delete a project and its data. Asks for the name on a terminal; `--yes` in CI |
| `ce setup --url <url> --key-stdin` | Connect the repo with a setup key from stdin (for coding agents; see above) |
| `ce setup report sdk_installed` | Report a setup step |
| `ce mcp [--url <url>]` | A stdio MCP server for agents, with the agent token of `ce setup` (or `CHECKEND_AGENT_TOKEN`) |
| `ce deploy [--release <v>] [--environment <name>] [--commit <sha>] [--commits-since <sha>] [--dry-run]` | Report a deploy from CI (default commit: git HEAD); `--dry-run` prints the request and sends nothing |
| `ce sourcemaps upload --release <v> [--url-prefix <p>] <dir\|file.map>...` | Build the index of each source map locally and upload it |
| `ce dsn rotate [--revoke-old [--yes]]` | Make a new DSN key; then revoke the old keys |
| `ce migrate-from-sentry` | Find the Sentry setup of the repo and get a Checkend DSN for it |
| `ce server install docker [--dir <dir>] [--port <p>]` | Write `compose.yaml` and `.env` for a signed server release on this machine |
| `ce server install ssh <user@host> --domain <d> [--tunnel] [--host-key <SHA256:…>]` | Install on an Ubuntu or Debian LTS server over SSH, behind Caddy or a Cloudflare Tunnel |
| `ce server install cloudflare [--account <id>] [--name <worker>] [--r2]` | Install on a Cloudflare account: a Worker and D1 |
| `ce server upgrade [--version <v>] [--allow-downgrade]` | Back up, then upgrade a server that `ce server install` installed |
| `ce server backup [--output <file>]` | Back up its database (Cloudflare: a D1 Time Travel bookmark) |
| `ce server status` | Its version, health, and whether a newer release exists |
| `ce server uninstall [--delete-data]` | Remove it; the data stays unless `--delete-data` |
| `ce token create --scope <scope>... [--project <app>] [--name <n>] [--show]` | Create an API token; only the token goes to stdout (pipe it to `gh secret set`) |
| `ce token list` / `ce token revoke <id> [--yes]` | List the tokens (never the tokens themselves); revoke one |
| `ce update [--yes]` | Update ce itself (Homebrew, npm, the install script or go install) |

`ce server` is for a Checkend server that you run yourself; a hosted space updates itself. The
old names (`ce install`, `ce upgrade`, `ce backup`, `ce uninstall`, `ce status --server`) still
work in this release and print the new name. The install commands install only a signed server release, show the plan and ask first
(`--yes` for agents), can run again safely, never print a secret, and print the setup link.

`--project` accepts the public project ID (for example `4817305926`) or the project slug. More
commands come when the server API for them exists. `ce --help` shows only the commands that work.

### Errors

Each error says what happened, gives the next step, and ends with a link to the section of the
docs that explains it (`Docs: https://checkend.com/docs/…`). In JSON mode the error is
`{"error", "hint", "docs"}` on stderr.

### Options for all commands

| Option | Environment | Meaning |
|---|---|---|
| `--url <url>` | `CHECKEND_URL` | The Checkend instance; it can have a base path (`https://host.example/k7q2x9`) |
| `--token <token>` | `CHECKEND_TOKEN` (or `CHECKEND_ADMIN_TOKEN`) | The API token |
| `--json` | | Print JSON, also on a terminal |
| `--plain` | | Print text, also when stdout is not a terminal (`--json` wins) |

The CLI looks for settings in this order: flags, environment, `.checkend.json` (the project only,
found in the current directory or a parent up to the repo root), then the saved login. A saved
token goes only to the URL that it was saved for. With a base path, that is the full URL: two
instances under one host never share a token.

## Output and exit codes

- Terminal: tables and cards. Colour only on a terminal, and never when `NO_COLOR` is set.
- Not a terminal, or `--json`: the API JSON, unchanged in shape. `--plain` prints text instead. Errors go to stderr as
  `{"error": "...", "hint": "..."}`.

| Exit code | Meaning |
|---|---|
| 0 | OK |
| 1 | Error (for example, the server cannot be reached) |
| 2 | Usage error (unknown command or option, wrong argument) |
| 3 | Not found |
| 4 | Conflict |
| 5 | Authentication (not logged in, or the token was rejected) |
| 6 | Accepted, waiting for approval (for example `ce project delete` with `REQUIRE_APPROVAL`) |

## Safety

- An unknown command or option exits with code 2 and does nothing. `ce` is one key from `cd`.
- HTTPS only, except `http://localhost` and `http://127.0.0.1`. The CLI does not follow
  redirects, so the token cannot go to another host.
- The CLI never prints a token, and never writes one to `.checkend.json`. Set
  `CHECKEND_NO_KEYCHAIN=1` to keep the token in the config file instead of the keychain.
- Error data comes from your apps and is untrusted. The CLI removes terminal escape sequences and
  control characters from all server text before it prints it. JSON output keeps the values, with
  those characters escaped.

## Develop

```sh
make check          # gofmt, go vet, staticcheck, tests and build
go test ./...       # the tests, against an in-process fake of the Checkend API
go run ./cmd/ce --help
```

### End-to-end test

`internal/cli/e2e_test.go` runs the CLI against a real Checkend server. It is skipped unless
`CHECKEND_E2E_URL` and `CHECKEND_E2E_TOKEN` are set. To run it with a local server, in a clone
of [Checkend/checkend](https://github.com/Checkend/checkend):

```sh
npx wrangler d1 migrations apply DB --local --persist-to /tmp/checkend-e2e
npx wrangler dev --port 8799 --persist-to /tmp/checkend-e2e --env-file /dev/null \
  --var ADMIN_TOKEN:e2e-test-admin-token-0123456789abcdef
```

Then, in this repo:

```sh
CHECKEND_E2E_URL=http://127.0.0.1:8799 CHECKEND_E2E_TOKEN=e2e-test-admin-token-0123456789abcdef make e2e
```

The server needs an `ADMIN_TOKEN` of at least 32 characters. The test logs in, runs `ce init`, sends one error to the DSN as a Sentry SDK does, and reads it
back with `ce next`, `ce issue list`, `ce issue view` and `ce event view`.

A hosted Checkend lives under a base path (`https://host/<id>`). To test that, add
`CHECKEND_E2E_BASE_PATH=/k7q2x9`: the test then reaches the server through a front stub that
serves it only under that path, and it fails on any request outside the path.

## Contributing

Read [AGENTS.md](AGENTS.md) first. Sign off each commit (`git commit -s`). Install
[gitleaks](https://github.com/gitleaks/gitleaks) for the pre-commit secret scan; a fake secret in
a test must contain `test` or `example`. For security
problems, see [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE). Sentry is a trademark of Functional Software, Inc. Checkend is not affiliated
with Sentry.
