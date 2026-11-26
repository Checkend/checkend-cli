# ce — the Checkend command line client

`ce` reads the stats of a [Checkend](https://checkend.com) server (cloud or self-hosted) and changes its
event setup, from a terminal. It uses an API token and the same surface as an agent: the MCP tools at
`/mcp` and `GET /api/v1/export`. It needs no database and no endpoint of its own.

## Install

```sh
go install github.com/checkend/checkend-cli/cmd/ce@latest
```

While this repository is private, Go needs access to it: `export GOPRIVATE=github.com/checkend/*` and a
GitHub login that can read the repository. Or build from a clone:

```sh
make build        # bin/ce
```

## Log in

```sh
ce login --url https://app.checkend.com
```

Create the token in Checkend: **Settings › Agent**. A read token is enough for stats; `rename`, `hide`,
`funnels add|rm` and `changes add` need the write scope. Without `--token`, `ce login` reads the token
from stdin, so `pbpaste | ce login` works. The login is checked before it is saved.

The login is saved in `~/.config/checkend/ce.json` (mode 0600; `$XDG_CONFIG_HOME` and `$CE_CONFIG` change
the place). `CHECKEND_URL`, `CHECKEND_TOKEN` and `CHECKEND_SITE` override it, for CI and scripts.
`ce logout` deletes the file; the token stays valid until you delete it in Settings › Agent.

## Site and period

Every stats command works on one site: `-s <domain or id>`, else the default site (`ce use <site>`,
or `CHECKEND_SITE`), else the only site of the organization.

`-p` sets the period: `realtime`, `today`, `24h`, `7d`, `28d` (default), `30d`, `90d`, `12m`.
`--from 2026-11-01 --to 2026-11-15` sets a custom range (days in the site timezone).

## Commands

| Command | What it shows or does |
|---|---|
| `ce sites` | every site: visitors in the last 24 hours, change, live, top event, name problems |
| `ce stats` | visitors, visits, pageviews, views per visit, bounce rate, visit duration, events, each with the period before and the change (bounce rate in points), as on the dashboard |
| `ce live` | visitors and their pages in the last 30 minutes |
| `ce top <dimension> [-n 10]` | top values of `source`, `source_type`, `referrer`, `utm_*`, `page`, `entry_page`, `exit_page`, `country`, `region`, `city`, `device`, `browser`, `os`, `event`, `prop:<name>` |
| `ce chart [-m metric] [-i interval]` | a metric per minute, hour, day, week or month; `-m event:Signup` for one event |
| `ce query -m … -d … -i …` | any query of `POST /api/v1/query`, as a table |
| `ce events` | custom events with counts and properties, and the name problems with the `ce rename` that fixes each |
| `ce event <name> [--prop p]` | one event by one of its properties |
| `ce rename <from> <to>` | rename an event, or merge it into an existing name (at query time; raw events are kept) |
| `ce hide <name>` | hide an event from all stats |
| `ce funnels` / `ce funnels add <name> --step … ` / `ce funnels rm <name>` | the stored funnels of the dashboard |
| `ce funnel <name>` / `ce funnel --step page:* --step event:Signup` | run a stored funnel, or steps without storing them |
| `ce changes [--all]` / `ce changes add <text> [--metric m] [--at day]` | recorded changes and their measured result |
| `ce export [--kind events\|stats] [--format csv\|parquet] [--from] [--to] [-o file]` | download raw events or daily stats (default: the last 30 days, CSV); `-o -` writes to stdout |
| `ce whoami`, `ce version`, `ce help` | |

Steps: `page:/pricing`, `page:*` (any page) or `event:Signup`; 2 to 8 steps.

Filters (`-f`, repeatable, on `stats`, `top`, `chart` and `query`): `dim=value` (is), `dim!=value` (is not),
`dim~value` (contains).

`--json` on any command prints the server's answer as JSON, for `jq` and scripts.

## Examples

```sh
ce stats -p 7d
ce top source -p 30d -f country=CA
ce top page -f source=chatgpt.com -n 20
ce chart -m event:Signup -p 90d -i week
ce query -m visitors,pageviews -d source,country -p 30d -n 50
ce funnel --step page:* --step "event:Tool Start" --step event:Signup
ce changes add "New pricing page" --metric event:Upgrade
ce export --kind events --from 2026-10-01 --to 2026-10-31 --format parquet
ce stats --json | jq .rows[0].visitors
```

Exit codes: 0 done, 1 error (the server's message is printed), 2 wrong usage.

## Development

```sh
make test         # unit tests against an in-process stub MCP server
make build        # bin/ce, version from git describe
```

The code is in `internal/cli`; `cmd/ce` is the entry point. `ce stats` uses the MCP tool `kpis`; on a
server without it, `ce stats` shows the totals with no comparison.

## License

Apache License 2.0, see [LICENSE](LICENSE). The software is provided "as is", without warranty of any
kind, and you use it at your own risk.
