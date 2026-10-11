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

Requires Node.js 22.18 or later.

```sh
npm install -g @checkend/cli
ce --version
```

## Get started

1. Log in to your Checkend instance. The token is the `ADMIN_TOKEN` secret of your Checkend
   Worker. The CLI checks the URL and the token, then saves them in
   `~/.config/checkend/config.json` (file mode 600).

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
| `ce issue list [--status <s>] [--project <id\|slug>] [--limit <n>] [--cursor <c>]` | Issues, the most recent first |
| `ce issue view <id>` | One issue. `api-42` also checks that issue 42 is in project `api` |
| `ce event view <id> [--section stack\|request\|tags]` | One event |
| `ce project list` | All projects |
| `ce project create --name <name> [--platform <p>]` | Create a project and print its DSN |
| `ce project dsn [<id\|slug>]` | The DSN of a project (default: the linked project) |

`--project` accepts the public project ID (for example `4817305926`) or the project slug. More
commands come when the server API for them exists. `ce --help` shows only the commands that work.

### Options for all commands

| Option | Environment | Meaning |
|---|---|---|
| `--url <url>` | `CHECKEND_URL` | The Checkend instance |
| `--token <token>` | `CHECKEND_TOKEN` (or `CHECKEND_ADMIN_TOKEN`) | The API token |
| `--json` | | Print JSON, also on a terminal |

The CLI looks for settings in this order: flags, environment, `.checkend.json` (the project only,
found in the current directory or a parent up to the repo root), then the saved login. A saved
token goes only to the URL that it was saved for.

## Output and exit codes

- Terminal: tables and cards. Colour only on a terminal, and never when `NO_COLOR` is set.
- Not a terminal, or `--json`: the API JSON, unchanged in shape. Errors go to stderr as
  `{"error": "...", "hint": "..."}`.

| Exit code | Meaning |
|---|---|
| 0 | OK |
| 1 | Error (for example, the server cannot be reached) |
| 2 | Usage error (unknown command or option, wrong argument) |
| 3 | Not found |
| 4 | Conflict |
| 5 | Authentication (not logged in, or the token was rejected) |

## Safety

- An unknown command or option exits with code 2 and does nothing. `ce` is one key from `cd`.
- HTTPS only, except `http://localhost` and `http://127.0.0.1`. The CLI does not follow
  redirects, so the token cannot go to another host.
- The CLI never prints a token, and never writes one to `.checkend.json`.
- Error data comes from your apps and is untrusted. The CLI removes terminal escape sequences and
  control characters from all server text before it prints it. JSON output keeps the values, with
  those characters escaped.

## Develop

```sh
npm ci
npm test            # Vitest, against an in-process fake of the Checkend API
npm run lint        # Biome
npm run typecheck
npm run check       # all of the above, and the build
node src/bin.ts --help   # run from source (Node 22.18+ strips the types)
```

### End-to-end test

`test/e2e/` runs the CLI against a real Checkend server. It is skipped unless
`CHECKEND_E2E_URL` and `CHECKEND_E2E_TOKEN` are set. To run it with a local server, in a clone
of [Checkend/checkend](https://github.com/Checkend/checkend):

```sh
npx wrangler d1 migrations apply DB --local --persist-to /tmp/checkend-e2e
npx wrangler dev --port 8799 --persist-to /tmp/checkend-e2e --env-file /dev/null \
  --var ADMIN_TOKEN:e2e-token
```

Then, in this repo:

```sh
CHECKEND_E2E_URL=http://127.0.0.1:8799 CHECKEND_E2E_TOKEN=e2e-token npm run test:e2e
```

The test logs in, runs `ce init`, sends one error to the DSN as a Sentry SDK does, and reads it
back with `ce next`, `ce issue list`, `ce issue view` and `ce event view`.

## Contributing

Read [AGENTS.md](AGENTS.md) first. Sign off each commit (`git commit -s`). Install
[gitleaks](https://github.com/gitleaks/gitleaks) for the pre-commit secret scan; a fake secret in
a test must contain `test` or `example`. For security
problems, see [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE). Sentry is a trademark of Functional Software, Inc. Checkend is not affiliated
with Sentry.
