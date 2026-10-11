// Package cli is the command table, argument parsing, help and error handling of `ce`.
package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/docs"
	"github.com/Checkend/checkend-cli/internal/host"
	"github.com/Checkend/checkend-cli/internal/output"
	"github.com/Checkend/checkend-cli/internal/sanitize"
	"github.com/Checkend/checkend-cli/internal/version"
)

var globalOptions = map[string]Kind{
	"url":   String,
	"token": String,
	"json":  Bool,
	"plain": Bool,
	"help":  Bool,
}

// commands lists only commands whose server API exists (and so they are in --help), plus the
// hidden old names of the server commands.
func commands() []*Command {
	list := slices.Concat(authCommands(), setupCommands(), []*Command{mcpCommand(), initCommand(), nextCommand(), deployCommand(), migrateCommand()},
		issueCommands(), eventCommands(), projectCommands(), []*Command{dsnRotateCommand(), sourcemapsUploadCommand()},
		installCommands(), serverCommands(), []*Command{updateCommand()})
	for _, c := range list {
		if c.Docs == "" {
			c.Docs = commandDocs[c.Path[0]]
		}
	}
	return append(list, oldNames(list)...)
}

// Commands is the command table, for the tests.
func Commands() []*Command { return commands() }

// commandDocs is the docs section of each group of commands that has no own section.
var commandDocs = map[string]docs.Topic{
	"login": docs.Authentication, "logout": docs.Authentication, "status": docs.Authentication,
	"setup": docs.AgentSetup, "mcp": docs.MCP, "init": docs.Projects, "next": docs.Issues,
	"deploy": docs.Deploys, "migrate-from-sentry": docs.SendingErrors, "issue": docs.Issues,
	"event": docs.Issues, "project": docs.Projects, "dsn": docs.Projects, "sourcemaps": docs.SourceMaps,
	"server": docs.SelfHost, "update": docs.ConnectAgent,
}

// oldNames are the names of the server commands before `ce server`: they work for one more
// release, hidden, with a hint.
func oldNames(list []*Command) []*Command {
	var out []*Command
	for _, c := range list {
		if len(c.Path) < 2 || c.Path[0] != "server" || c.Path[1] == "status" {
			continue
		}
		old, now := *c, c
		old.Path, old.Hidden = c.Path[1:], true
		old.Run = func(s *Session) error {
			s.Out.Notice(fmt.Sprintf("`ce %s` is now `ce %s`. The old name goes away in the next release.", strings.Join(old.Path, " "), strings.Join(now.Path, " ")))
			return now.Run(s)
		}
		out = append(out, &old)
	}
	return out
}

var groups = map[string]string{
	"issue":      "List and view issues",
	"event":      "View an event",
	"project":    "Create, list and delete projects; print a DSN",
	"dsn":        "Rotate the DSN of a project",
	"sourcemaps": "Upload source maps",
	"server":     "Run your own Checkend server: install, upgrade, back up, status, uninstall",
}

// Run runs `ce argv...` and returns the exit code.
func Run(argv []string, h *host.Host) int {
	out := output.New(h, slices.Contains(argv, "--json"), slices.Contains(argv, "--plain"))
	out.Hide(h.Getenv("CHECKEND_TOKEN"))
	out.Hide(h.Getenv("CHECKEND_ADMIN_TOKEN"))
	for i, arg := range argv {
		if arg == "--token" && i+1 < len(argv) {
			out.Hide(argv[i+1])
		}
		if value, ok := strings.CutPrefix(arg, "--token="); ok {
			out.Hide(value)
		}
	}
	err := dispatch(argv, h, out)
	if err == nil {
		return clierr.OK
	}
	var exit Exit
	if errors.As(err, &exit) {
		return int(exit)
	}
	var cerr *clierr.Error
	if !errors.As(err, &cerr) {
		cerr = clierr.Fail("Unexpected error: " + err.Error())
	}
	out.Error(cerr)
	return cerr.Code
}

func isHelp(arg string) bool { return arg == "--help" || arg == "-h" }

func dispatch(argv []string, h *host.Host, out *output.Output) error {
	if len(argv) == 0 || (len(argv) == 1 && isHelp(argv[0])) || argv[0] == "help" {
		if len(argv) > 1 && argv[0] == "help" {
			return dispatch(append(slices.Clone(argv[1:]), "--help"), h, out)
		}
		out.Write(mainHelp())
		return nil
	}
	first := argv[0]
	if len(argv) == 1 && (first == "--version" || first == "-V") {
		out.Write(version.Version + "\n")
		return nil
	}
	if strings.HasPrefix(first, "-") {
		return clierr.UsageError("Give a command before the options.").WithDocs(docs.QuickStart)
	}
	all := commands()
	// The command with the longest path that starts argv: `ce server install docker`, `ce setup`.
	var command *Command
	for _, c := range all {
		if len(argv) >= len(c.Path) && slices.Equal(argv[:len(c.Path)], c.Path) && (command == nil || len(c.Path) > len(command.Path)) {
			command = c
		}
	}
	// How many words of argv name a group of commands (`ce server`, `ce server install`).
	depth := 0
	for depth < len(argv) && len(groupOf(all, argv[:depth+1])) > 0 {
		depth++
	}
	if command == nil || depth > len(command.Path) {
		if depth == 0 {
			return clierr.UsageError(fmt.Sprintf("Unknown command %q.", sanitize.OneLine(first))).WithDocs(docs.QuickStart)
		}
		prefix := argv[:depth]
		group := groupOf(all, prefix)
		topic := group[0].Docs
		if depth == len(argv) || !isHelp(argv[depth]) && strings.HasPrefix(argv[depth], "-") {
			return needsSubcommand(prefix, group).WithDocs(groupDocs(prefix, topic))
		}
		if isHelp(argv[depth]) {
			out.Write(groupHelp(prefix, group))
			return nil
		}
		return clierr.UsageError(fmt.Sprintf("Unknown command %q.", "ce "+strings.Join(prefix, " ")+" "+sanitize.OneLine(argv[depth])),
			fmt.Sprintf("Run `ce %s --help`.", strings.Join(prefix, " "))).WithDocs(groupDocs(prefix, topic))
	}
	return withDocs(runCommand(command, argv[len(command.Path):], h, out), command.Docs)
}

// groupOf are the visible commands under prefix (or the hidden ones of a hidden old name), with
// a longer path than prefix.
func groupOf(all []*Command, prefix []string) []*Command {
	var group []*Command
	for _, c := range all {
		if len(c.Path) > len(prefix) && slices.Equal(c.Path[:len(prefix)], prefix) {
			group = append(group, c)
		}
	}
	visible := slices.DeleteFunc(slices.Clone(group), func(c *Command) bool { return c.Hidden })
	if len(visible) > 0 {
		return visible
	}
	return group
}

// groupDocs is the docs topic of a group: the self-host guide for the server commands.
func groupDocs(prefix []string, fallback docs.Topic) docs.Topic {
	if prefix[0] == "server" || prefix[0] == "install" {
		return docs.SelfHost
	}
	return fallback
}

// installTargets is the help of `ce server install` without a target.
const installTargets = `Choose where to install the Checkend server:
  ce server install docker                               on this machine, with Docker
  ce server install ssh <user@host> --domain <domain>    on your Ubuntu or Debian server, over SSH
  ce server install cloudflare                           on your Cloudflare account (a Worker and D1)`

func needsSubcommand(prefix []string, group []*Command) *clierr.Error {
	if prefix[len(prefix)-1] == "install" {
		return clierr.UsageError("ce server install needs a target.", installTargets)
	}
	var lines []string
	for _, c := range group {
		lines = append(lines, "  "+strings.TrimRight(name(c)+" "+c.Usage, " "))
	}
	return clierr.UsageError(fmt.Sprintf("%q needs a subcommand.", "ce "+strings.Join(prefix, " ")),
		"Use one of:\n"+strings.Join(lines, "\n"))
}

// withDocs gives an error the docs topic of its command when it has none.
func withDocs(err error, topic docs.Topic) error {
	var cerr *clierr.Error
	if err != nil && errors.As(err, &cerr) {
		if cerr.Docs == "" {
			cerr.Docs = topic
		}
		return cerr
	}
	return err
}

func runCommand(command *Command, rest []string, h *host.Host, out *output.Output) error {
	options := map[string]Kind{}
	for k, v := range globalOptions {
		options[k] = v
	}
	for k, v := range command.Options {
		options[k] = v
	}
	p, err := parse(rest, options)
	if err != nil {
		return clierr.UsageError(sanitize.OneLine(err.Error()), helpHint(command))
	}
	if p.bools["help"] {
		out.Write(commandHelp(command))
		return nil
	}
	required := 0
	for _, name := range command.Args {
		if !strings.HasPrefix(name, "[") {
			required++
		}
	}
	if len(p.args) < required {
		return clierr.UsageError(fmt.Sprintf("Missing %s.", command.Args[len(p.args)]), helpHint(command))
	}
	variadic := len(command.Args) > 0 && strings.HasSuffix(command.Args[len(command.Args)-1], "...]")
	if len(p.args) > len(command.Args) && !variadic {
		return clierr.UsageError(fmt.Sprintf("Unexpected argument %q.", sanitize.OneLine(p.args[len(command.Args)])), helpHint(command))
	}
	return command.Run(&Session{Host: h, Out: out, Args: p.args, p: p})
}

func name(c *Command) string { return "ce " + strings.Join(c.Path, " ") }

func helpHint(c *Command) string { return fmt.Sprintf("Run `%s --help`.", name(c)) }

func mainHelp() string {
	seen := map[string]bool{}
	var labels, summaries []string
	width := 0
	for _, c := range commands() {
		if c.Hidden {
			continue
		}
		head := c.Path[0]
		if seen[head] {
			continue
		}
		seen[head] = true
		summary, label := c.Summary, head
		if len(c.Path) > 1 {
			summary, label = groups[head], head+" <action>"
		}
		labels, summaries = append(labels, label), append(summaries, summary)
		width = max(width, len(label))
	}
	var lines []string
	for i, label := range labels {
		lines = append(lines, fmt.Sprintf("  %-*s  %s", width, label, summaries[i]))
	}
	return fmt.Sprintf(`Checkend CLI %s: error tracking for coding agents.

Usage: ce <command> [options]

Commands:
%s

Options for all commands:
  --url <url>       The Checkend instance (or CHECKEND_URL)
  --token <token>   The API token (or CHECKEND_TOKEN)
  --json            Print JSON (the default when stdout is not a terminal)
  --plain           Print text, also when stdout is not a terminal
  -h, --help        Show the help
  -V, --version     Show the version

Run `+"`ce <command> --help`"+` for the options of a command.
Exit codes: 0 OK, 1 error, 2 usage, 3 not found, 4 conflict, 5 authentication,
6 waiting for approval.
`, version.Version, strings.Join(lines, "\n"))
}

func groupHelp(prefix []string, group []*Command) string {
	var lines []string
	for _, c := range group {
		lines = append(lines, fmt.Sprintf("  %s\n      %s", strings.TrimRight(name(c)+" "+c.Usage, " "), c.Summary))
	}
	title := groups[prefix[0]]
	if len(prefix) > 1 || title == "" {
		title = "ce " + strings.Join(prefix, " ")
	}
	return fmt.Sprintf("%s\n\nUsage:\n%s\n", title, strings.Join(lines, "\n"))
}

func commandHelp(c *Command) string {
	details := ""
	if len(c.Details) > 0 {
		details = "\n\nOptions:\n" + strings.Join(c.Details, "\n")
	}
	return fmt.Sprintf("%s\n\nUsage: %s%s\n", c.Summary, strings.TrimRight(name(c)+" "+c.Usage, " "), details)
}
