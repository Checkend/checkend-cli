// Package cli is `ce`, the command line client of Checkend. It reads stats
// and changes the setup of a remote Checkend server (cloud or self-hosted)
// with an API token, through the MCP tools (/mcp) and /api/v1/export: the
// same surface an agent uses, so no endpoint exists only for the CLI.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"
)

// App holds what one run of ce needs. Main builds it from the process; tests
// build their own.
type App struct {
	Version string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Env     func(string) string
	HTTP    *http.Client // nil: a default client
	Now     func() time.Time

	cfg     Config
	cfgPath string
	client  *Client

	// common flags of every command
	site   string
	period string
	json   bool
}

// Main runs ce with the process arguments and returns the exit code.
func Main(version string) int {
	a := &App{Version: version, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Env: os.Getenv}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return a.Run(ctx, os.Args[1:])
}

const usage = `ce - the Checkend command line client

Usage: ce <command> [arguments] [flags]

Setup:
  login [--url URL] [--token ck_…]   save the server and an API token (Settings › Agent)
  logout                             forget the token
  whoami                             show the server, the token and the default site
  use <site>                         set the default site

Stats (flags: -s site, -p period):
  sites                              all sites: visitors in the last 24 hours, live
  stats                              visitors, visits, pageviews, bounce rate, duration, events
  live                               visitors in the last 30 minutes and their pages
  top <dimension> [-n 10]            top values: source, page, country, browser, event, …
  chart [-m metric] [-i interval]    a metric over time
  query -m metrics [-d dims] [-f filter]… [-i interval] [-n limit]
                                     any query, as in POST /api/v1/query

Events, funnels, changes:
  events                             custom events, counts and name problems
  event <name> [--prop name]         one event by one of its properties
  rename <from> <to>                 rename or merge an event name (write token)
  hide <name>                        hide an event from all stats (write token)
  funnels                            stored funnels
  funnels add <name> --step page:/ --step event:Signup …   (write token)
  funnels rm <name or id>            (write token)
  funnel <name or id>                run a stored funnel
  funnel --step page:* --step event:Signup …              run steps without storing them
  changes [--all]                    recorded changes and their measured result
  changes add <text> [--metric m] [--at time]               (write token)

Data:
  export [--from day] [--to day] [--kind events|stats] [--format csv|parquet] [-o file]

Other:
  version, help

Common flags:
  -s, --site      site domain or id (default: ce use, $CHECKEND_SITE, or the only site)
  -p, --period    realtime, today, 24h, 7d, 28d, 30d, 90d, 12m (default 28d)
      --from, --to  YYYY-MM-DD: a custom range (stats commands)
      --json      print the raw JSON answer

Filters (-f, repeatable): dim=value (is), dim!=value (is not), dim~value (contains).
  Example: ce top page -f source=Google -f country!=US

Environment: CHECKEND_URL, CHECKEND_TOKEN, CHECKEND_SITE override the saved login;
CE_CONFIG sets the config file (default ~/.config/checkend/ce.json).
`

// errUsage prints the usage of a command and exits with code 2.
type errUsage string

func (e errUsage) Error() string { return string(e) }

// Run runs one command and returns the exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if a.Env == nil {
		a.Env = func(string) string { return "" }
	}
	if a.Now == nil {
		a.Now = time.Now
	}
	err := a.run(ctx, args)
	if a.client != nil {
		a.client.Close()
	}
	if err == nil {
		return 0
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	var u errUsage
	if errors.As(err, &u) {
		fmt.Fprintf(a.Stderr, "ce: %s\nRun 'ce help' for the commands.\n", u)
		return 2
	}
	fmt.Fprintf(a.Stderr, "ce: %v\n", err)
	return 1
}

type command struct {
	run  func(a *App, ctx context.Context, args []string) error
	auth bool // needs a login
}

var commands map[string]command

func init() {
	commands = map[string]command{
		"login":   {(*App).cmdLogin, false},
		"logout":  {(*App).cmdLogout, false},
		"whoami":  {(*App).cmdWhoami, false},
		"use":     {(*App).cmdUse, true},
		"sites":   {(*App).cmdSites, true},
		"stats":   {(*App).cmdStats, true},
		"live":    {(*App).cmdLive, true},
		"top":     {(*App).cmdTop, true},
		"chart":   {(*App).cmdChart, true},
		"query":   {(*App).cmdQuery, true},
		"events":  {(*App).cmdEvents, true},
		"event":   {(*App).cmdEvent, true},
		"rename":  {(*App).cmdRename, true},
		"hide":    {(*App).cmdHide, true},
		"funnels": {(*App).cmdFunnels, true},
		"funnel":  {(*App).cmdFunnel, true},
		"changes": {(*App).cmdChanges, true},
		"export":  {(*App).cmdExport, true},
	}
}

func (a *App) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(a.Stdout, usage)
		return nil
	}
	name, rest := args[0], args[1:]
	switch name {
	case "help", "-h", "--help":
		fmt.Fprint(a.Stdout, usage)
		return nil
	case "version", "--version":
		fmt.Fprintf(a.Stdout, "ce %s\n", a.Version)
		return nil
	}
	cmd, ok := commands[name]
	if !ok {
		return errUsage(fmt.Sprintf("unknown command %q", name))
	}
	cfg, p, err := loadConfig(a.Env)
	if err != nil {
		return err
	}
	a.cfg, a.cfgPath = cfg, p
	if cmd.auth {
		if a.cfg.URL == "" || a.cfg.Token == "" {
			return errors.New("not logged in: run 'ce login' (or set CHECKEND_URL and CHECKEND_TOKEN)")
		}
		a.client = &Client{URL: a.cfg.URL, Token: a.cfg.Token, Version: a.Version, HTTP: a.HTTP}
	}
	return cmd.run(a, ctx, rest)
}

// flags returns a flag set with the common flags.
func (a *App) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("ce "+name, flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	fs.StringVar(&a.site, "site", "", "site domain or id")
	fs.StringVar(&a.site, "s", "", "site (short)")
	fs.StringVar(&a.period, "period", "", "period")
	fs.StringVar(&a.period, "p", "", "period (short)")
	fs.BoolVar(&a.json, "json", false, "print the raw JSON answer")
	fs.Usage = func() { fmt.Fprintf(a.Stderr, "Usage: ce %s … (see 'ce help')\n", name) }
	return fs
}

// parse parses flags and positional arguments in any order.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, errUsage(err.Error())
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// list splits comma separated values of repeatable flags.
func list(vals ...string) []string {
	var out []string
	for _, v := range vals {
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// siteSummary is one site of list_sites.
type siteSummary struct {
	ID           int64          `json:"id"`
	Domain       string         `json:"domain"`
	Timezone     string         `json:"timezone"`
	Visitors24h  float64        `json:"visitors_24h"`
	Change       *float64       `json:"visitors_change"`
	Live         float64        `json:"live"`
	TopEvent     map[string]any `json:"top_event"`
	ProblemLabel *string        `json:"name_problems"`
	Waiting      bool           `json:"waiting_for_first_event"`
}

func (a *App) listSites(ctx context.Context) ([]siteSummary, json.RawMessage, error) {
	var out struct {
		Items []siteSummary `json:"items"`
	}
	raw, err := a.client.Call(ctx, "list_sites", map[string]any{}, &out)
	return out.Items, raw, err
}

// siteArg returns the site of the command: --site, then CHECKEND_SITE or
// `ce use`, then the only site of the org.
func (a *App) siteArg(ctx context.Context) (string, error) {
	if a.site != "" {
		return a.site, nil
	}
	if a.cfg.Site != "" {
		return a.cfg.Site, nil
	}
	sites, _, err := a.listSites(ctx)
	if err != nil {
		return "", err
	}
	switch len(sites) {
	case 0:
		return "", errors.New("this organization has no site yet: add one in Checkend")
	case 1:
		return sites[0].Domain, nil
	}
	names := make([]string, len(sites))
	for i, s := range sites {
		names[i] = s.Domain
	}
	sort.Strings(names)
	return "", errUsage("which site? use -s <site> or 'ce use <site>' (sites: " + strings.Join(names, ", ") + ")")
}

// printJSON writes a raw answer, indented.
func (a *App) printJSON(raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	enc := json.NewEncoder(a.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// readLine reads one line from stdin (for the token prompt).
func (a *App) readLine(prompt string) (string, error) {
	if f, ok := a.Stdin.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprint(a.Stderr, prompt)
		}
	}
	s, err := bufio.NewReader(a.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(s), nil
}
