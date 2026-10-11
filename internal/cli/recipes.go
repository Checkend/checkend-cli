package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The server's agent recipes (src/core/recipes.ts of Checkend/checkend) name ce commands. A test
// in CI checks every one against this command tree, so that a recipe never names a command or an
// option that the CLI does not have.

var (
	// command: "ce deploy" in the AGENT_TASKS table.
	recipeCommandField = regexp.MustCompile(`command:\s*"(ce [^"]*)"`)
	// Code fences and inline code, written with escaped backticks in a template string.
	recipeFence  = regexp.MustCompile("(?s)\\\\`\\\\`\\\\`[a-z]*\n(.*?)\\\\`\\\\`\\\\`")
	recipeInline = regexp.MustCompile("\\\\`((?:[^`\\\\]|\\\\[^`])*?)\\\\`")
	// A ce command in code: ce and its words, up to a shell operator.
	recipeCe = regexp.MustCompile(`(?:^|[\s(])(ce(?:[ \t]+[^\s|;&]+)*)`)
)

type found struct {
	at      int
	command string
}

func cesIn(text string, at int) []found {
	var out []found
	for _, m := range recipeCe.FindAllStringSubmatchIndex(text, -1) {
		words := strings.Fields(text[m[2]:m[3]])
		for i, w := range words {
			if strings.HasPrefix(w, ">") || strings.HasPrefix(w, "2>") {
				words = words[:i] // a redirect ends the command
				break
			}
		}
		if len(words) > 1 {
			out = append(out, found{at + m[2], strings.Join(words, " ")})
		}
	}
	return out
}

// recipeCommands returns the ce commands in the code of a recipe file, in file order.
func recipeCommands(source string) []string {
	var all []found
	for _, m := range recipeCommandField.FindAllStringSubmatchIndex(source, -1) {
		all = append(all, found{m[2], source[m[2]:m[3]]})
	}
	masked := []byte(source)
	for _, m := range recipeFence.FindAllStringSubmatchIndex(source, -1) {
		// In the template string, a shell line continuation is written as \\.
		body := strings.ReplaceAll(source[m[2]:m[3]], `\\`, `\`)
		body = regexp.MustCompile(`\\\n\s*`).ReplaceAllString(body, " ")
		for i, line := range strings.Split(body, "\n") {
			all = append(all, cesIn(line, m[2]+i)...)
		}
		for i := m[0]; i < m[1]; i++ {
			masked[i] = ' '
		}
	}
	for _, m := range recipeInline.FindAllStringSubmatchIndex(string(masked), -1) {
		all = append(all, cesIn(string(masked[m[2]:m[3]]), m[2])...)
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].at < all[b].at })
	out := make([]string, len(all))
	for i, f := range all {
		out[i] = f.command
	}
	return out
}

// checkRecipeCommand checks that a command line names a real command and real options.
func checkRecipeCommand(line string) error {
	words := strings.Fields(line)[1:]
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%s: "+format, append([]any{line}, args...)...)
	}
	if len(words) == 0 {
		return nil
	}
	if strings.HasPrefix(words[0], "-") {
		switch words[0] {
		case "--version", "-V", "--help", "-h":
			return nil
		}
		return fail("unknown option %s", words[0])
	}
	var single, command *Command
	subs := map[string]*Command{}
	for _, c := range commands() {
		if c.Path[0] != words[0] {
			continue
		}
		if len(c.Path) == 1 {
			single = c
		} else {
			subs[c.Path[1]] = c
		}
	}
	rest := words[1:]
	switch {
	case len(rest) > 0 && subs[rest[0]] != nil:
		command, rest = subs[rest[0]], rest[1:]
	case single != nil:
		command = single
	case len(subs) > 0 && len(rest) > 0:
		return fail("unknown command %q", words[0]+" "+rest[0])
	case len(subs) > 0:
		return fail("%q needs a subcommand", words[0])
	default:
		return fail("unknown command %q", words[0])
	}
	for _, word := range rest {
		if !strings.HasPrefix(word, "--") {
			continue
		}
		name, _, _ := strings.Cut(word[2:], "=")
		if _, ok := command.Options[name]; ok {
			continue
		}
		if _, ok := globalOptions[name]; ok {
			continue
		}
		return fail("unknown option --%s", name)
	}
	return nil
}
