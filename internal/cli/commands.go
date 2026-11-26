package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- setup ----

func (a *App) cmdLogin(ctx context.Context, args []string) error {
	fs := a.flags("login")
	u := fs.String("url", "", "server URL (default: the saved one, else "+DefaultURL+")")
	tok := fs.String("token", "", "API token (ck_…); asked when left out")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	base := normURL(*u)
	if base == "" {
		base = a.cfg.URL
	}
	if base == "" {
		base = DefaultURL
	}
	token := strings.TrimSpace(*tok)
	if token == "" {
		fmt.Fprintf(a.Stderr, "Create a token in Checkend: %s › Settings › Agent.\n", base)
		t, err := a.readLine("Token: ")
		if err != nil {
			return err
		}
		token = t
	}
	if token == "" {
		return errUsage("no token given")
	}
	a.client = &Client{URL: base, Token: token, Version: a.Version, HTTP: a.HTTP}
	sites, _, err := a.listSites(ctx)
	if err != nil {
		return err
	}
	cfg := Config{URL: base, Token: token, Site: a.site}
	if cfg.Site == "" {
		if len(sites) == 1 {
			cfg.Site = sites[0].Domain
		} else if a.cfg.URL == base && findSite(sites, a.cfg.Site) != nil {
			cfg.Site = a.cfg.Site // keep the default site of the same server
		}
	}
	if err := saveConfig(a.cfgPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Logged in to %s (%d %s).\n", base, len(sites), plural(len(sites), "site", "sites"))
	if cfg.Site != "" {
		fmt.Fprintf(a.Stdout, "Default site: %s\n", cfg.Site)
	} else if len(sites) > 1 {
		fmt.Fprintln(a.Stdout, "Set a default site with: ce use <site>")
	}
	return nil
}

func (a *App) cmdLogout(ctx context.Context, args []string) error {
	if _, err := parse(a.flags("logout"), args); err != nil {
		return err
	}
	err := os.Remove(a.cfgPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fmt.Fprintln(a.Stdout, "Logged out. The token still works until you delete it in Settings › Agent.")
	if a.Env("CHECKEND_TOKEN") != "" {
		fmt.Fprintln(a.Stdout, "Note: CHECKEND_TOKEN is still set in this shell.")
	}
	return nil
}

func (a *App) cmdWhoami(ctx context.Context, args []string) error {
	if _, err := parse(a.flags("whoami"), args); err != nil {
		return err
	}
	if a.cfg.Token == "" {
		return errors.New("not logged in: run 'ce login'")
	}
	t := newTable(a.Stdout)
	t.row("Server", a.cfg.URL)
	t.row("Token", maskToken(a.cfg.Token))
	t.row("Site", orDash(a.cfg.Site))
	t.row("Config", a.cfgPath)
	t.flush()
	return nil
}

func (a *App) cmdUse(ctx context.Context, args []string) error {
	pos, err := parse(a.flags("use"), args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errUsage("usage: ce use <site>")
	}
	sites, _, err := a.listSites(ctx)
	if err != nil {
		return err
	}
	s := findSite(sites, pos[0])
	if s == nil {
		return fmt.Errorf("no site %q (run: ce sites)", pos[0])
	}
	// The file holds the login; CHECKEND_* in the environment are not saved.
	cfg, err := readConfigFile(a.cfgPath)
	if err != nil {
		return err
	}
	if cfg.URL == "" { // a login from the environment only
		cfg.URL, cfg.Token = a.cfg.URL, a.cfg.Token
	}
	cfg.Site = s.Domain
	if p := a.cfgPath; p != "" {
		if err := saveConfig(p, cfg); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.Stdout, "Default site: %s\n", s.Domain)
	if a.Env("CHECKEND_SITE") != "" {
		fmt.Fprintln(a.Stdout, "Note: CHECKEND_SITE is set in this shell and wins over the default.")
	}
	return nil
}

func findSite(sites []siteSummary, name string) *siteSummary {
	name = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "www.")
	if name == "" {
		return nil
	}
	for i := range sites {
		if sites[i].Domain == name || strconv.FormatInt(sites[i].ID, 10) == name {
			return &sites[i]
		}
	}
	return nil
}

// ---- stats ----

// filter is one query filter.
type filter struct {
	Dim   string `json:"dim"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// parseFilter reads dim=value, dim!=value or dim~value.
func parseFilter(s string) (filter, error) {
	i := strings.IndexAny(s, "!~=")
	if i <= 0 {
		return filter{}, errUsage(fmt.Sprintf("bad filter %q: use dim=value, dim!=value or dim~value", s))
	}
	f := filter{Dim: strings.TrimSpace(s[:i])}
	switch {
	case s[i] == '!' && i+1 < len(s) && s[i+1] == '=':
		f.Op, f.Value = "is_not", s[i+2:]
	case s[i] == '~':
		f.Op, f.Value = "contains", s[i+1:]
	case s[i] == '=':
		f.Op, f.Value = "is", s[i+1:]
	default:
		return filter{}, errUsage(fmt.Sprintf("bad filter %q: use dim=value, dim!=value or dim~value", s))
	}
	return f, nil
}

// queryReq is the body of the query tool (POST /api/v1/query).
type queryReq struct {
	Site       string   `json:"site"`
	Metrics    []string `json:"metrics,omitempty"`
	Period     string   `json:"period,omitempty"`
	From       string   `json:"from,omitempty"`
	To         string   `json:"to,omitempty"`
	Dimensions []string `json:"dimensions,omitempty"`
	Filters    []filter `json:"filters,omitempty"`
	Interval   *string  `json:"interval,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

type queryResp struct {
	Rows []map[string]any `json:"rows"`
	Meta struct {
		Site     string    `json:"site"`
		From     time.Time `json:"from"`
		To       time.Time `json:"to"`
		Timezone string    `json:"timezone"`
	} `json:"meta"`
}

// statsFlags are the flags of every stats command: filters and a custom range.
type statsFlags struct {
	filters  multi
	from, to string
}

func (a *App) statsFlags(name string) (*flag.FlagSet, *statsFlags) {
	fs := a.flags(name)
	sf := &statsFlags{}
	fs.Var(&sf.filters, "f", "filter dim=value, dim!=value or dim~value (repeatable)")
	fs.Var(&sf.filters, "filter", "filter (long)")
	fs.StringVar(&sf.from, "from", "", "first day YYYY-MM-DD (custom range)")
	fs.StringVar(&sf.to, "to", "", "last day YYYY-MM-DD (custom range)")
	return fs, sf
}

// base returns a query of the site with the period, range and filters.
func (a *App) base(ctx context.Context, sf *statsFlags) (queryReq, error) {
	site, err := a.siteArg(ctx)
	if err != nil {
		return queryReq{}, err
	}
	q := queryReq{Site: site, Period: a.period}
	if sf != nil {
		if sf.from != "" || sf.to != "" {
			if sf.from == "" || sf.to == "" {
				return q, errUsage("give both --from and --to")
			}
			q.Period, q.From, q.To = "custom", sf.from, sf.to
		}
		for _, s := range sf.filters {
			f, err := parseFilter(s)
			if err != nil {
				return q, err
			}
			q.Filters = append(q.Filters, f)
		}
	}
	return q, nil
}

func (a *App) query(ctx context.Context, q queryReq) (*queryResp, []byte, error) {
	var out queryResp
	raw, err := a.client.Call(ctx, "query", q, &out)
	return &out, raw, err
}

// heading is "domain · last 28 days" (or the custom range).
func heading(q queryReq, r *queryResp) string {
	site := r.Meta.Site
	if site == "" {
		site = q.Site
	}
	p := q.Period
	if p == "" {
		p = "28d"
	}
	label := periodLabel(p)
	if p == "custom" {
		label = q.From + " to " + q.To
	}
	if len(q.Filters) > 0 {
		var fs []string
		for _, f := range q.Filters {
			op := map[string]string{"is": "=", "is_not": "!=", "contains": "~"}[f.Op]
			fs = append(fs, f.Dim+op+f.Value)
		}
		label += " · " + strings.Join(fs, ", ")
	}
	return site + " · " + label
}

func (a *App) cmdSites(ctx context.Context, args []string) error {
	if _, err := parse(a.flags("sites"), args); err != nil {
		return err
	}
	sites, raw, err := a.listSites(ctx)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	if len(sites) == 0 {
		fmt.Fprintln(a.Stdout, "No sites yet. Add one in Checkend.")
		return nil
	}
	def := a.site
	if def == "" {
		def = a.cfg.Site
	}
	t := newTable(a.Stdout, "", "site", "visitors 24h", "change", "live", "top event", "note")
	for _, s := range sites {
		mark := ""
		if findSite([]siteSummary{s}, def) != nil {
			mark = "*"
		}
		top := "-"
		if s.TopEvent != nil {
			if n, ok := s.TopEvent["name"].(string); ok {
				top = n
				if c, ok := s.TopEvent["count"].(float64); ok {
					top += " (" + num(c) + ")"
				}
			}
		}
		note := ""
		switch {
		case s.Waiting:
			note = "waiting for the first event"
		case s.ProblemLabel != nil:
			note = *s.ProblemLabel
		}
		t.row(mark, s.Domain, num(s.Visitors24h), change(s.Change), num(s.Live), top, note)
	}
	t.flush()
	return nil
}

var allMetrics = []string{"visitors", "visits", "pageviews", "bounce_rate", "visit_duration", "events"}

func (a *App) cmdStats(ctx context.Context, args []string) error {
	fs, sf := a.statsFlags("stats")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	q, err := a.base(ctx, sf)
	if err != nil {
		return err
	}
	var k kpiResp
	raw, err := a.client.Call(ctx, "kpis", map[string]any{"site": q.Site, "period": q.Period, "from": q.From, "to": q.To, "filters": q.Filters}, &k)
	if err != nil && strings.Contains(err.Error(), "unknown tool") {
		return a.statsNoCompare(ctx, q) // a server from before the kpis tool
	}
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	loc := time.UTC
	if l, err := time.LoadLocation(k.Meta.Timezone); err == nil {
		loc = l
	}
	r := &queryResp{}
	r.Meta.Site = k.Meta.Site
	fmt.Fprintln(a.Stdout, heading(q, r))
	fmt.Fprintf(a.Stdout, "compared with %s\n\n", rangeText(k.Meta.PreviousFrom, k.Meta.PreviousTo, loc))
	t := newTable(a.Stdout, "", "now", "before", "change")
	for _, m := range kpiMetrics {
		t.row(kpiLabels[m], kpiCell(m, k.Current[m]), kpiCell(m, k.Previous[m]), kpiChange(m, k.Change[m]))
	}
	t.flush()
	return nil
}

// kpiResp is the answer of the kpis tool.
type kpiResp struct {
	Current  map[string]float64  `json:"current"`
	Previous map[string]float64  `json:"previous"`
	Change   map[string]*float64 `json:"change"`
	Meta     struct {
		Site         string    `json:"site"`
		Timezone     string    `json:"timezone"`
		PreviousFrom time.Time `json:"previous_from"`
		PreviousTo   time.Time `json:"previous_to"`
	} `json:"meta"`
}

var kpiMetrics = []string{"visitors", "visits", "pageviews", "views_per_visit", "bounce_rate", "visit_duration", "events"}

var kpiLabels = map[string]string{"visitors": "Visitors", "visits": "Visits", "pageviews": "Pageviews",
	"views_per_visit": "Views per visit", "bounce_rate": "Bounce rate", "visit_duration": "Visit duration", "events": "Events"}

func kpiCell(m string, v float64) string {
	if m == "views_per_visit" {
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
	return metricCell(m, v)
}

// kpiChange is the change in percent; for the bounce rate in points.
func kpiChange(m string, c *float64) string {
	if c == nil {
		return "-"
	}
	if m == "bounce_rate" {
		s := strconv.FormatFloat(*c, 'f', 1, 64) + " pts"
		if *c > 0 {
			return "+" + s
		}
		return s
	}
	return change(c)
}

// rangeText names [from, to) in the site timezone: days for a range of a
// day or more, else times.
func rangeText(from, to time.Time, loc *time.Location) string {
	f, l := from.In(loc), to.In(loc).Add(-time.Nanosecond)
	if to.Sub(from) < 24*time.Hour {
		return f.Format("Jan 2 15:04") + " to " + to.In(loc).Format("15:04")
	}
	if f.Format("2006-01-02") == l.Format("2006-01-02") {
		return f.Format("Jan 2, 2006")
	}
	return f.Format("Jan 2, 2006") + " to " + l.Format("Jan 2, 2006")
}

// statsNoCompare is ce stats on a server without the kpis tool.
func (a *App) statsNoCompare(ctx context.Context, q queryReq) error {
	q.Metrics = allMetrics
	r, raw, err := a.query(ctx, q)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	row := map[string]any{}
	if len(r.Rows) > 0 {
		row = r.Rows[0]
	}
	fmt.Fprintln(a.Stdout, heading(q, r))
	fmt.Fprintln(a.Stdout, "(this server has no comparison yet)")
	fmt.Fprintln(a.Stdout)
	t := newTable(a.Stdout)
	for _, m := range allMetrics {
		t.row(kpiLabels[m], metricCell(m, row[m]))
		if m == "pageviews" {
			v, _ := row["visits"].(float64)
			p, _ := row["pageviews"].(float64)
			ratio := "-"
			if v > 0 {
				ratio = strconv.FormatFloat(p/v, 'f', 2, 64)
			}
			t.row(kpiLabels["views_per_visit"], ratio)
		}
	}
	t.flush()
	return nil
}

func (a *App) cmdLive(ctx context.Context, args []string) error {
	fs := a.flags("live")
	n := fs.Int("n", 10, "number of pages")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	q, err := a.base(ctx, nil)
	if err != nil {
		return err
	}
	q.Period = "realtime"
	q.Metrics = []string{"visitors", "pageviews"}
	total, raw, err := a.query(ctx, q)
	if err != nil {
		return err
	}
	q.Dimensions, q.Limit = []string{"page"}, *n
	pages, raw2, err := a.query(ctx, q)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON([]byte(`{"totals":` + string(raw) + `,"pages":` + string(raw2) + `}`))
	}
	v := 0.0
	if len(total.Rows) > 0 {
		v, _ = total.Rows[0]["visitors"].(float64)
	}
	fmt.Fprintf(a.Stdout, "%s · %s %s in the last 30 minutes\n", total.Meta.Site, num(v), plural(int(v), "visitor", "visitors"))
	if len(pages.Rows) == 0 {
		return nil
	}
	fmt.Fprintln(a.Stdout)
	t := newTable(a.Stdout, "page", "visitors", "pageviews")
	for _, r := range pages.Rows {
		t.row(clip(cell(r["page"]), 60), metricCell("visitors", r["visitors"]), metricCell("pageviews", r["pageviews"]))
	}
	t.flush()
	return nil
}

// secondMetric is the count of a breakdown: pageviews for pages, events for
// events and properties, visits otherwise (as on the dashboard).
func secondMetric(dim string) string {
	switch {
	case dim == "page" || dim == "entry_page" || dim == "exit_page":
		return "pageviews"
	case dim == "event" || strings.HasPrefix(dim, "prop:"):
		return "events"
	}
	return "visits"
}

func (a *App) cmdTop(ctx context.Context, args []string) error {
	fs, sf := a.statsFlags("top")
	n := fs.Int("n", 10, "number of rows")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errUsage("usage: ce top <dimension>: source, source_type, referrer, utm_source, utm_medium, utm_campaign, page, entry_page, exit_page, country, region, city, device, browser, os, event, prop:<name>")
	}
	dim := pos[0]
	q, err := a.base(ctx, sf)
	if err != nil {
		return err
	}
	second := secondMetric(dim)
	q.Metrics, q.Dimensions, q.Limit = []string{"visitors", second}, []string{dim}, *n
	r, raw, err := a.query(ctx, q)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	fmt.Fprintln(a.Stdout, heading(q, r))
	fmt.Fprintln(a.Stdout)
	if len(r.Rows) == 0 {
		fmt.Fprintln(a.Stdout, "No data.")
		return nil
	}
	max := 0.0
	for _, row := range r.Rows {
		if v, _ := row["visitors"].(float64); v > max {
			max = v
		}
	}
	t := newTable(a.Stdout, dim, "visitors", second, "")
	for _, row := range r.Rows {
		v, _ := row["visitors"].(float64)
		t.row(clip(cell(row[dim]), 50), num(v), metricCell(second, row[second]), bar(v, max, 20))
	}
	t.flush()
	return nil
}

// defaultInterval is the chart bucket of a period, as on the dashboard.
func defaultInterval(period string) string {
	switch period {
	case "realtime":
		return "minute"
	case "today", "24h":
		return "hour"
	case "12m":
		return "month"
	}
	return "day"
}

func (a *App) cmdChart(ctx context.Context, args []string) error {
	fs, sf := a.statsFlags("chart")
	metric := fs.String("m", "visitors", "metric: visitors, visits, pageviews, bounce_rate, visit_duration, events, event:<name>")
	interval := fs.String("i", "", "minute, hour, day, week or month (default by period)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	q, err := a.base(ctx, sf)
	if err != nil {
		return err
	}
	m := *metric
	if name, ok := strings.CutPrefix(m, "event:"); ok {
		q.Filters = append(q.Filters, filter{Dim: "event", Op: "is", Value: name})
		m = "events"
	}
	iv := *interval
	if iv == "" {
		iv = defaultInterval(q.Period)
	}
	q.Metrics, q.Interval, q.Limit = []string{m}, &iv, 1000
	r, raw, err := a.query(ctx, q)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	loc := time.UTC
	if l, err := time.LoadLocation(r.Meta.Timezone); err == nil {
		loc = l
	}
	type point struct {
		t time.Time
		v float64
	}
	var pts []point
	max := 0.0
	for _, row := range r.Rows {
		ts, _ := row["date"].(string)
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			continue
		}
		v, _ := row[m].(float64)
		pts = append(pts, point{t.In(loc), v})
		if v > max {
			max = v
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].t.Before(pts[j].t) })
	fmt.Fprintf(a.Stdout, "%s · %s per %s\n\n", heading(q, r), *metric, iv)
	if len(pts) == 0 {
		fmt.Fprintln(a.Stdout, "No data.")
		return nil
	}
	layout := map[string]string{"minute": "15:04", "hour": "Mon 15:04", "day": "Mon Jan 02", "week": "Jan 02", "month": "Jan 2006"}[iv]
	t := newTable(a.Stdout)
	for _, p := range pts {
		t.row(p.t.Format(layout), metricCell(m, p.v), bar(p.v, max, 40))
	}
	t.flush()
	return nil
}

func (a *App) cmdQuery(ctx context.Context, args []string) error {
	fs, sf := a.statsFlags("query")
	var metrics, dims multi
	fs.Var(&metrics, "m", "metrics, comma separated or repeated (default visitors)")
	fs.Var(&dims, "d", "dimensions, comma separated or repeated (at most 3)")
	interval := fs.String("i", "", "minute, hour, day, week or month: adds a date column")
	n := fs.Int("n", 0, "max rows (default 100, at most 1000)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	q, err := a.base(ctx, sf)
	if err != nil {
		return err
	}
	q.Metrics, q.Dimensions, q.Limit = list(metrics...), list(dims...), *n
	if len(q.Metrics) == 0 {
		q.Metrics = []string{"visitors"}
	}
	if *interval != "" {
		q.Interval = interval
	}
	r, raw, err := a.query(ctx, q)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	cols := append([]string{}, q.Dimensions...)
	if q.Interval != nil {
		cols = append(cols, "date")
	}
	cols = append(cols, q.Metrics...)
	if len(r.Rows) == 0 {
		fmt.Fprintln(a.Stdout, "No data.")
		return nil
	}
	t := newTable(a.Stdout, append([]string{}, cols...)...)
	for _, row := range r.Rows {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = metricCell(c, row[c])
		}
		t.row(cells...)
	}
	t.flush()
	return nil
}

// ---- events ----

type eventList struct {
	Events []struct {
		Name       string   `json:"name"`
		Count      float64  `json:"count"`
		Visitors   float64  `json:"visitors"`
		Properties []string `json:"properties"`
		New        bool     `json:"new"`
		Hidden     bool     `json:"hidden"`
		Flags      []struct {
			Text string `json:"text"`
		} `json:"flags"`
	} `json:"events"`
	Problems []struct {
		Text string `json:"text"`
		Fix  struct {
			From  string `json:"from"`
			To    string `json:"to"`
			Label string `json:"label"`
		} `json:"fix"`
	} `json:"problems"`
}

func (a *App) cmdEvents(ctx context.Context, args []string) error {
	if _, err := parse(a.flags("events"), args); err != nil {
		return err
	}
	site, err := a.siteArg(ctx)
	if err != nil {
		return err
	}
	var out eventList
	raw, err := a.client.Call(ctx, "list_events", map[string]any{"site": site, "period": a.period}, &out)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	p := a.period
	if p == "" {
		p = "28d"
	}
	fmt.Fprintf(a.Stdout, "%s · %s\n\n", site, periodLabel(p))
	if len(out.Events) == 0 {
		fmt.Fprintln(a.Stdout, "No custom events yet.")
		return nil
	}
	t := newTable(a.Stdout, "event", "count", "visitors", "properties", "note")
	for _, e := range out.Events {
		var note []string
		if e.New {
			note = append(note, "new")
		}
		if e.Hidden {
			note = append(note, "hidden")
		}
		for _, f := range e.Flags {
			note = append(note, f.Text)
		}
		t.row(e.Name, num(e.Count), num(e.Visitors), orDash(strings.Join(e.Properties, ", ")), strings.Join(note, "; "))
	}
	t.flush()
	if len(out.Problems) > 0 {
		fmt.Fprintln(a.Stdout, "\nName problems:")
		for _, pr := range out.Problems {
			fmt.Fprintf(a.Stdout, "  %s\n    fix: ce rename %s %s\n", pr.Text, shellQuote(pr.Fix.From), shellQuote(pr.Fix.To))
		}
	}
	return nil
}

func (a *App) cmdEvent(ctx context.Context, args []string) error {
	fs := a.flags("event")
	prop := fs.String("prop", "", "property name (default: the first)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errUsage("usage: ce event <name> [--prop name]")
	}
	site, err := a.siteArg(ctx)
	if err != nil {
		return err
	}
	var out struct {
		Name          string         `json:"name"`
		PropertyNames []string       `json:"property_names"`
		Prop          *string        `json:"prop"`
		Totals        map[string]any `json:"totals"`
		Rows          []struct {
			Value    string  `json:"value"`
			Count    float64 `json:"count"`
			Visitors float64 `json:"visitors"`
			Pct      float64 `json:"pct"`
		} `json:"rows"`
	}
	raw, err := a.client.Call(ctx, "event_properties", map[string]any{"site": site, "name": pos[0], "prop": *prop, "period": a.period}, &out)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	c, _ := out.Totals["count"].(float64)
	v, _ := out.Totals["visitors"].(float64)
	fmt.Fprintf(a.Stdout, "%s · %s · %s times by %s %s\n", site, out.Name, num(c), num(v), plural(int(v), "visitor", "visitors"))
	if out.Prop == nil || len(out.PropertyNames) == 0 {
		fmt.Fprintln(a.Stdout, "This event has no properties.")
		return nil
	}
	fmt.Fprintf(a.Stdout, "Properties: %s\n\n", strings.Join(out.PropertyNames, ", "))
	max := 0.0
	for _, r := range out.Rows {
		if r.Count > max {
			max = r.Count
		}
	}
	t := newTable(a.Stdout, *out.Prop, "count", "visitors", "share", "")
	for _, r := range out.Rows {
		t.row(clip(cell(r.Value), 50), num(r.Count), num(r.Visitors), pct(r.Pct), bar(r.Count, max, 20))
	}
	t.flush()
	return nil
}

func (a *App) cmdRename(ctx context.Context, args []string) error {
	pos, err := parse(a.flags("rename"), args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errUsage("usage: ce rename <from> <to> (an existing name merges the two)")
	}
	site, err := a.siteArg(ctx)
	if err != nil {
		return err
	}
	raw, err := a.client.Call(ctx, "rename_event", map[string]any{"site": site, "from": pos[0], "to": pos[1]}, nil)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	fmt.Fprintf(a.Stdout, "%s: %q is now %q. Raw events are kept.\n", site, pos[0], pos[1])
	return nil
}

func (a *App) cmdHide(ctx context.Context, args []string) error {
	pos, err := parse(a.flags("hide"), args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errUsage("usage: ce hide <event name>")
	}
	site, err := a.siteArg(ctx)
	if err != nil {
		return err
	}
	raw, err := a.client.Call(ctx, "rename_event", map[string]any{"site": site, "from": pos[0], "hidden": true}, nil)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	fmt.Fprintf(a.Stdout, "%s: %q is hidden from all stats. Raw events are kept.\n", site, pos[0])
	return nil
}

// ---- funnels ----

type funnelStep struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// parseStep reads page:/path, page:* or event:Name.
func parseStep(s string) (funnelStep, error) {
	typ, val, ok := strings.Cut(s, ":")
	if !ok || (typ != "page" && typ != "event") || val == "" {
		return funnelStep{}, errUsage(fmt.Sprintf("bad step %q: use page:/path, page:* (any page) or event:Name", s))
	}
	return funnelStep{typ, val}, nil
}

func parseSteps(in []string) ([]funnelStep, error) {
	var out []funnelStep
	for _, s := range in {
		st, err := parseStep(s)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func stepText(s funnelStep) string {
	if s.Type == "page" && s.Value == "*" {
		return "any page"
	}
	return s.Type + " " + s.Value
}

func (a *App) cmdFunnels(ctx context.Context, args []string) error {
	fs := a.flags("funnels")
	var steps multi
	fs.Var(&steps, "step", "a step: page:/path, page:* or event:Name (repeatable, with add)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	site, err := a.siteArg(ctx)
	if err != nil {
		return err
	}
	sub := ""
	if len(pos) > 0 {
		sub, pos = pos[0], pos[1:]
	}
	switch sub {
	case "", "list", "ls":
		var out struct {
			Items []struct {
				ID    int64        `json:"id"`
				Name  string       `json:"name"`
				Steps []funnelStep `json:"steps"`
			} `json:"items"`
		}
		raw, err := a.client.Call(ctx, "list_funnels", map[string]any{"site": site}, &out)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(raw)
		}
		if len(out.Items) == 0 {
			fmt.Fprintf(a.Stdout, "%s has no stored funnels. Add one with: ce funnels add <name> --step page:* --step event:Signup\n", site)
			return nil
		}
		t := newTable(a.Stdout, "id", "funnel", "steps")
		for _, f := range out.Items {
			var s []string
			for _, st := range f.Steps {
				s = append(s, stepText(st))
			}
			t.row(strconv.FormatInt(f.ID, 10), f.Name, strings.Join(s, " → "))
		}
		t.flush()
		return nil
	case "add", "create":
		if len(pos) != 1 {
			return errUsage("usage: ce funnels add <name> --step page:* --step event:Signup …")
		}
		st, err := parseSteps(steps)
		if err != nil {
			return err
		}
		if len(st) < 2 || len(st) > 8 {
			return errUsage("a funnel has 2 to 8 steps (--step)")
		}
		var out struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		raw, err := a.client.Call(ctx, "create_funnel", map[string]any{"site": site, "name": pos[0], "steps": st}, &out)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(raw)
		}
		fmt.Fprintf(a.Stdout, "%s: funnel %q stored (id %d). Run it with: ce funnel %s\n", site, out.Name, out.ID, shellQuote(out.Name))
		return nil
	case "rm", "delete":
		if len(pos) != 1 {
			return errUsage("usage: ce funnels rm <name or id>")
		}
		raw, err := a.client.Call(ctx, "delete_funnel", map[string]any{"site": site, "funnel": pos[0]}, nil)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(raw)
		}
		fmt.Fprintf(a.Stdout, "%s: funnel %q deleted.\n", site, pos[0])
		return nil
	}
	return errUsage(fmt.Sprintf("unknown funnels command %q (list, add, rm); to run a funnel: ce funnel <name>", sub))
}

func (a *App) cmdFunnel(ctx context.Context, args []string) error {
	fs := a.flags("funnel")
	var steps multi
	fs.Var(&steps, "step", "a step: page:/path, page:* or event:Name (repeatable)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	in := map[string]any{"period": a.period}
	switch {
	case len(pos) == 1 && len(steps) == 0:
		in["funnel"] = pos[0]
	case len(pos) == 0 && len(steps) >= 2:
		st, err := parseSteps(steps)
		if err != nil {
			return err
		}
		in["steps"] = st
	default:
		return errUsage("usage: ce funnel <name or id>, or ce funnel --step page:* --step event:Signup …")
	}
	site, err := a.siteArg(ctx)
	if err != nil {
		return err
	}
	in["site"] = site
	var out struct {
		Name  string `json:"name"`
		Steps []struct {
			Label            string  `json:"label"`
			Visitors         float64 `json:"visitors"`
			RateFromPrevious float64 `json:"rate_from_previous"`
			RateOverall      float64 `json:"rate_overall"`
		} `json:"steps"`
	}
	raw, err := a.client.Call(ctx, "funnel", in, &out)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	p := a.period
	if p == "" {
		p = "28d"
	}
	name := out.Name
	if name == "" {
		name = "funnel"
	}
	fmt.Fprintf(a.Stdout, "%s · %s · %s\n\n", site, name, periodLabel(p))
	max := 0.0
	if len(out.Steps) > 0 {
		max = out.Steps[0].Visitors
	}
	t := newTable(a.Stdout, "step", "visitors", "from previous", "overall", "")
	for i, s := range out.Steps {
		prev := "-"
		if i > 0 {
			prev = pct(s.RateFromPrevious)
		}
		t.row(strconv.Itoa(i+1)+". "+s.Label, num(s.Visitors), prev, pct(s.RateOverall), bar(s.Visitors, max, 20))
	}
	t.flush()
	if n := len(out.Steps); n > 1 {
		fmt.Fprintf(a.Stdout, "\nConversion: %s\n", pct(out.Steps[n-1].RateOverall))
	}
	return nil
}

// ---- changes ----

func (a *App) cmdChanges(ctx context.Context, args []string) error {
	fs := a.flags("changes")
	all := fs.Bool("all", false, "changes of all sites")
	metric := fs.String("metric", "", "metric to watch (add): visitors, pageviews, bounce_rate, … or event:<name>")
	at := fs.String("at", "", "when the change was made (add): RFC 3339 or YYYY-MM-DD; default now")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 && (pos[0] == "add" || pos[0] == "record") {
		if len(pos) < 2 {
			return errUsage("usage: ce changes add <text> [--metric m] [--at time]")
		}
		site, err := a.siteArg(ctx)
		if err != nil {
			return err
		}
		ts := *at
		if ts != "" {
			if d, err := time.ParseInLocation("2006-01-02", ts, time.Local); err == nil {
				ts = d.Format(time.RFC3339)
			}
		}
		text := strings.Join(pos[1:], " ")
		raw, err := a.client.Call(ctx, "record_change", map[string]any{"site": site, "text": text, "ts": ts, "metric": *metric}, nil)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(raw)
		}
		fmt.Fprintf(a.Stdout, "%s: change recorded. Its result shows after 3 days: ce changes\n", site)
		return nil
	}
	if len(pos) > 0 {
		return errUsage(fmt.Sprintf("unknown changes command %q (add)", pos[0]))
	}
	site := ""
	if !*all {
		if site, err = a.siteArg(ctx); err != nil {
			return err
		}
	}
	var out struct {
		Items []struct {
			Domain string    `json:"domain"`
			TS     time.Time `json:"ts"`
			Text   string    `json:"text"`
			Source string    `json:"source"`
			Agent  *string   `json:"agent"`
			Result *struct {
				Label   string `json:"label"`
				Verdict string `json:"verdict"`
			} `json:"result"`
		} `json:"items"`
	}
	raw, err := a.client.Call(ctx, "list_changes", map[string]any{"site": site}, &out)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(raw)
	}
	if len(out.Items) == 0 {
		fmt.Fprintln(a.Stdout, "No changes recorded. Record one with: ce changes add \"New CTA on /pricing\"")
		return nil
	}
	head := []string{"date", "change", "by", "result"}
	if *all {
		head = append([]string{"site"}, head...)
	}
	t := newTable(a.Stdout, head...)
	for _, c := range out.Items {
		by := c.Source
		if c.Agent != nil && *c.Agent != "" {
			by = *c.Agent
		}
		res := "waiting (3 days)"
		if c.Result != nil {
			res = c.Result.Label
		}
		row := []string{c.TS.Local().Format("2006-01-02 15:04"), clip(c.Text, 60), by, res}
		if *all {
			row = append([]string{c.Domain}, row...)
		}
		t.row(row...)
	}
	t.flush()
	return nil
}

// ---- export ----

func (a *App) cmdExport(ctx context.Context, args []string) error {
	set := a.flags("export")
	from := set.String("from", "", "first day YYYY-MM-DD (default 30 days ago)")
	to := set.String("to", "", "last day YYYY-MM-DD (default today)")
	kind := set.String("kind", "events", "events (raw events) or stats (daily stats)")
	format := set.String("format", "csv", "csv or parquet")
	outPath := set.String("o", "", "output file; - for stdout (default: the server's file name)")
	if _, err := parse(set, args); err != nil {
		return err
	}
	site, err := a.siteArg(ctx)
	if err != nil {
		return err
	}
	now := a.Now()
	if *to == "" {
		*to = now.Format("2006-01-02")
	}
	if *from == "" {
		*from = now.AddDate(0, 0, -30).Format("2006-01-02")
	}
	q := url.Values{"site": {site}, "from": {*from}, "to": {*to}, "kind": {*kind}, "format": {*format}}
	var written string
	open := func(name string) (io.WriteCloser, error) {
		p := *outPath
		if p == "-" {
			written = "stdout"
			return nopCloser{a.Stdout}, nil
		}
		if p == "" {
			p = filepath.Base(name) // never a path from the server
			if name == "" || p == "." || p == "/" {
				p = fmt.Sprintf("%s-%s-%s-%s.%s", site, *kind, *from, *to, *format)
			}
		}
		written = p
		// O_EXCL: never overwrite a file without -o naming it.
		flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if *outPath != "" {
			flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
		f, err := os.OpenFile(p, flags, 0o644)
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("%s exists: name the file with -o", p)
		}
		return f, err
	}
	_, n, err := a.client.Export(ctx, q, open)
	if err != nil {
		return err
	}
	if written != "stdout" {
		fmt.Fprintf(a.Stderr, "Wrote %s (%s bytes).\n", written, num(float64(n)))
	}
	return nil
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// ---- helpers ----

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func maskToken(t string) string {
	if len(t) <= 10 {
		return "****"
	}
	return t[:6] + "…" + t[len(t)-4:]
}

// shellQuote quotes s for a shell when it needs it.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
