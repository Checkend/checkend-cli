# AGENTS.md

Instructions for coding agents (and humans) who work on the Checkend CLI. Read this file before
you change code.

The CLI (`ce` and `checkend`) is a thin client of the Checkend API
([Checkend/checkend](https://github.com/Checkend/checkend), `guides/api.md`). TypeScript on
Node 22.18+, ESM, no runtime dependencies.

## Commands

| Command | What it does |
|---|---|
| `npm ci` | Install the dependencies. |
| `npm test` | Run the tests (Vitest, in-process fake API). |
| `npm run test:e2e` | The end-to-end test against a real server (see README.md). |
| `npm run lint` | Biome: format and lint checks. `npm run lint:fix` fixes what it can. |
| `npm run typecheck` | TypeScript. |
| `npm run build` | Compile `src/` to `dist/` (the published files). |
| `npm run check` | Lint, typecheck, tests and build. Run it before each commit. |

## Layout

```
src/
  bin.ts            The entry point: connects cli.ts to the process (TTY, prompts, exit code).
  cli.ts            Command table, argument parsing, help, error handling.
  command.ts        Command and Session types: options, API client, project lookup.
  commands/         One file per command group.
  api.ts            HTTP client. Maps HTTP status to exit codes. No redirects.
  config.ts         Config resolution, the user config file, .checkend.json.
  output.ts         Terminal vs JSON output, tables, colour, secret hiding.
  sanitize.ts       Removes terminal escapes from untrusted text; safe JSON.
  snippets.ts       Platform detection and SDK snippets for `ce init`.
test/
  support/          The fake API (fake-api.ts) and the in-process harness (harness.ts).
  e2e/              The end-to-end test against a real server (skipped without settings).
```

## Rules

1. **Thin client.** All business logic is in the server API. One command = one API operation.
   The CLI has no rules of its own about issues, priority or grouping.
2. **Only working commands in `--help`.** Add a command when its server API exists. Do not add
   placeholders.
3. **Command pattern `ce <thing> <action>`**, plus top-level shortcuts for the fix loop
   (`next`). Names match the API and the MCP tools.
4. **Output modes.** Terminal: short tables and cards. Not a terminal or `--json`: the API JSON,
   unchanged in shape. Errors on stderr; in JSON mode `{"error", "hint"}`. Each error gives the
   next step.
5. **Exit codes are a contract:** 0 OK, 1 error, 2 usage, 3 not found, 4 conflict,
   5 authentication. Do not change them.
6. **A typo never does anything.** Unknown commands, options or extra arguments exit 2 before
   any request or file write. Check input that the CLI owns (for example `--section`) before
   the request.
7. **Server text is untrusted.** Print server data only through `Output.data()` (it cleans a copy)
   or clean it with `cleanText()`. JSON output goes through `safeJson()`. Never strip the
   server's untrusted-data labels.
8. **Tokens.** Never print a token (except once at token creation, when that exists). Never write
   a token to `.checkend.json`. A saved token goes only to its own URL. Add new token sources to
   `Output.hide()`.
9. **HTTPS only**, except `http://localhost` and `http://127.0.0.1`. Do not follow redirects.
10. **Destructive commands** (when they exist) ask for confirmation on a terminal and need
    `--yes` otherwise.
11. **API changes are additive.** If the CLI needs a server change, ask the server maintainers;
    do not work around the API.

## How we build

1. **Test first (TDD).** Write a failing test. Run it and see it fail. Write the smallest code
   that passes. Refactor. Test through `run()` with the harness and the fake API.
2. **Hostile cases.** For each new output path, add a test with escape sequences in server text.
   For each new input path, add a usage-error test (exit 2, no request).
3. **The fake follows the server.** When the server API changes, change `test/support/fake-api.ts`
   first, then run the end-to-end test against a real server.
4. **Agents do not publish.** Do not push to `main`, publish to npm, or create releases. A
   maintainer does that.

## Commits and pull requests

- Small commits. Each commit passes `npm run check`.
- Sign off every commit (Developer Certificate of Origin): `git commit -s`. CI checks it.
- Run `git config core.hooksPath .githooks` once per clone. The pre-commit hook blocks private
  planning files.
- Install [gitleaks](https://github.com/gitleaks/gitleaks) (`brew install gitleaks`): the
  pre-commit hook scans the staged changes for secrets, and CI scans the full history. A fake
  secret in a test must contain `test` or `example`.
- Security problems: see [SECURITY.md](SECURITY.md). Do not open a public issue.
