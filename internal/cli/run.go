// Package cli is the command table, argument parsing, help and error handling of `ce`.
package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Checkend/checkend-cli/internal/clierr"
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

// commands lists only commands whose server API exists (and so they are in --help).
func commands() []*Command {
	return slices.Concat(authCommands(), setupCommands(), []*Command{mcpCommand(), initCommand(), nextCommand(), deployCommand(), migrateCommand()},
		issueCommands(), eventCommands(), projectCommands(), []*Command{dsnRotateCommand(), sourcemapsUploadCommand()},
		installCommands())
}

var groups = map[string]string{
	"issue":      "List and view issues",
	"event":      "View an event",
	"project":    "Create, list and delete projects; print a DSN",
	"dsn":        "Rotate the DSN of a project",
	"sourcemaps": "Upload source maps",
	"install":    "Install a released Checkend server (Docker, a server over SSH, Cloudflare)",
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
		return clierr.UsageError("Give a command before the options.")
	}
	var group []*Command
	for _, c := range commands() {
		if c.Path[0] == first {
			group = append(group, c)
		}
	}
	if len(group) == 0 {
		return clierr.UsageError(fmt.Sprintf("Unknown command %q.", sanitize.OneLine(first)))
	}
	var command *Command
	rest := argv[1:]
	for _, c := range group {
		if len(c.Path) == 1 {
			command = c
		}
	}
	// A command can also have subcommands, for example `ce setup` and `ce setup report`.
	if command != nil && len(argv) > 1 {
		for _, c := range group {
			if len(c.Path) == 2 && c.Path[1] == argv[1] {
				command, rest = c, argv[2:]
			}
		}
	}
	if command == nil {
		if len(argv) < 2 {
			return clierr.UsageError(fmt.Sprintf("%q needs a subcommand.", "ce "+first), fmt.Sprintf("Run `ce %s --help`.", first))
		}
		second := argv[1]
		if isHelp(second) {
			out.Write(groupHelp(first, group))
			return nil
		}
		for _, c := range group {
			if c.Path[1] == second {
				command = c
			}
		}
		if command == nil {
			return clierr.UsageError(fmt.Sprintf("Unknown command %q.", "ce "+first+" "+sanitize.OneLine(second)), fmt.Sprintf("Run `ce %s --help`.", first))
		}
		rest = argv[2:]
	}

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

func groupHelp(head string, group []*Command) string {
	var lines []string
	for _, c := range group {
		lines = append(lines, fmt.Sprintf("  %s\n      %s", strings.TrimRight(name(c)+" "+c.Usage, " "), c.Summary))
	}
	return fmt.Sprintf("%s\n\nUsage:\n%s\n", groups[head], strings.Join(lines, "\n"))
}

func commandHelp(c *Command) string {
	details := ""
	if len(c.Details) > 0 {
		details = "\n\nOptions:\n" + strings.Join(c.Details, "\n")
	}
	return fmt.Sprintf("%s\n\nUsage: %s%s\n", c.Summary, strings.TrimRight(name(c)+" "+c.Usage, " "), details)
}
