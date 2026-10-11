# AGENTS.md

Instructions for coding agents (and humans) who work on the Checkend CLI. Read this file before
you change code.

The CLI (`ce` and `checkend`) is a thin client of the Checkend API
([Checkend/checkend](https://github.com/Checkend/checkend), `guides/api.md`). One static Go
binary for macOS, Linux and Windows. Standard library first; few dependencies.

## Commands

| Command | What it does |
|---|---|
| `make check` | Format check, `go vet`, staticcheck, tests and build. Run it before each commit. |
| `go test ./...` | The tests (in-process fake API). |
| `make fmt` | Format the code with `gofmt`. |
| `make e2e` | The end-to-end test against a real server (see README.md). |
| `go run ./cmd/ce <args>` | Run the CLI from source. |
| `cd npm && npm test` | The npm wrapper tests. |
| `node scripts/gen-sourcemap-fixtures.mjs <server checkout> [esbuild]` | Build the source map fixtures again with the server code. |

## Layout

```
cmd/ce/             The entry point: connects internal/cli to the process (TTY, prompts, keychain).
internal/
  cli/              Command table, argument parsing, help, error handling, the commands.
  api/              HTTP client. Maps HTTP status to exit codes. No redirects.
  config/           Config resolution, the user config file, the token storage, .checkend.json.
  credstore/        The OS keychain (and an in-memory keychain for tests).
  output/           Terminal vs JSON output, tables, colour, secret hiding.
  sanitize/         Removes terminal escapes from untrusted text; safe JSON.
  snippets/         Stack detection, SDK snippets, the Sentry scan of a repo.
  sourcemap/        The source map index (format v1), the same bytes as the server builds.
  release/          Tests of install.sh and the Homebrew formula script.
  host/             Everything from the outside world (env, files, terminal, clock). Tests fake it.
  testkit/          The fake API (fakeapi.go) and the in-process harness (harness.go).
npm/                The npm wrapper @checkend/cli (downloads and checks the binary).
packaging/          The Homebrew formula template.
install.sh          The install script (macOS and Linux).
.goreleaser.yaml    The release builds. .github/workflows/release.yml runs it on a tag.
```

## Rules

1. **Thin client.** All business logic is in the server API. One command = one API operation.
   The CLI has no rules of its own about issues, priority or grouping.
2. **Only working commands in `--help`.** Add a command when its server API exists. Do not add
   placeholders.
3. **Command pattern `ce <thing> <action>`**, plus top-level shortcuts for the fix loop
   (`next`). Names match the API and the MCP tools.
4. **Output modes.** Terminal: short tables and cards. Not a terminal or `--json`: the API JSON,
   unchanged in shape and key order. Errors on stderr; in JSON mode `{"error", "hint"}`. Each error
   gives the next step.
5. **Exit codes are a contract:** 0 OK, 1 error, 2 usage, 3 not found, 4 conflict,
   5 authentication, 6 accepted and waiting for approval. Do not change them; add new ones only.
6. **A typo never does anything.** Unknown commands, options or extra arguments exit 2 before
   any request or file write. Check input that the CLI owns (for example `--section`) before
   the request.
7. **Server text is untrusted.** Print server data only through `Output.Data()` (it cleans a copy)
   or clean it with `sanitize.Text()`. JSON output goes through `sanitize.JSON()`. Never strip the
   server's untrusted-data labels.
8. **Tokens.** Never print a token (except once at token creation, when that exists). Never write
   a token to `.checkend.json`. A saved token goes only to its own URL. Tokens go to the OS
   keychain; the config file is the fallback. Add new token sources to `Output.Hide()`.
   `ce setup` saves a separate agent token for each app (keychain account `<url>#agent:<id>`);
   `ce mcp` uses only an agent token, never the login of a person.
9. **HTTPS only**, except `http://localhost` and `http://127.0.0.1`. Do not follow redirects.
10. **Destructive commands** ask for confirmation on a terminal and need `--yes` otherwise.
11. **API changes are additive.** If the CLI needs a server change, ask the server maintainers;
    do not work around the API.
12. **No literal control characters in source.** Write them as escape sequences (`\u202e`);
    staticcheck checks this.

## How we build

1. **Test first (TDD).** Write a failing test. Run it and see it fail. Write the smallest code
   that passes. Refactor. Test through `cli.Run()` with the harness and the fake API.
2. **Hostile cases.** For each new output path, add a test with escape sequences in server text.
   For each new input path, add a usage-error test (exit 2, no request).
3. **The fake follows the server.** When the server API changes, change
   `internal/testkit/fakeapi.go` first, then run the end-to-end test against a real server.
4. **Tests never touch the real keychain.** The harness uses `credstore.Memory`.
5. **Agents do not publish.** Do not push to `main`, publish packages, or create tags or
   releases. A maintainer does that.

## Commits and pull requests

- Small commits. Each commit passes `make check`.
- Sign off every commit (Developer Certificate of Origin): `git commit -s`. CI checks it.
- Run `git config core.hooksPath .githooks` once per clone. The pre-commit hook blocks private
  planning files.
- Install [gitleaks](https://github.com/gitleaks/gitleaks) (`brew install gitleaks`): the
  pre-commit hook scans the staged changes for secrets, and CI scans the full history. A fake
  secret in a test must contain `test` or `example`.
- Security problems: see [SECURITY.md](SECURITY.md). Do not open a public issue.
