package cli_test

import (
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func TestSynonymsWorkSilently(t *testing.T) {
	api, cli, env := setup(t)
	app := api.AddProject("App")
	issue := api.AddIssue(J{"project_id": app["id"]})
	for _, argv := range [][]string{
		{"issues", "view", jsonString(issue["id"])},
		{"issue", "show", jsonString(issue["id"])},
		{"issues", "show", jsonString(issue["id"])},
		{"projects", "list"},
		{"issues", "list"},
	} {
		r := cli.Run(argv, Options{Env: env})
		Code(t, r, 0)
		if r.Stderr != "" {
			t.Errorf("%v: stderr %q", argv, r.Stderr)
		}
	}
	// A synonym is only a command name: `show` as an argument stays an argument.
	r := cli.Run([]string{"issue", "view", "show"}, Options{Env: env})
	Code(t, r, 2)
	help := commandsSection(cli.Run([]string{"--help"}).Stdout)
	for _, synonym := range []string{"issues", "projects", "show"} {
		NotMatches(t, help, `(?m)^\s+`+synonym+`\b`)
	}
	NotContains(t, cli.Run([]string{"issue", "--help"}).Stdout, "show")
}

func TestUnknownCommandsSuggestTheNearestOne(t *testing.T) {
	api, cli, env := setup(t)
	for argv, want := range map[string]string{
		"issue sho 3":    "Did you mean `ce issue view`?",
		"isue list":      "Did you mean `ce issue`?",
		"projet list":    "Did you mean `ce project`?",
		"issue lst":      "Did you mean `ce issue list`?",
		"server upgarde": "Did you mean `ce server upgrade`?",
		"tokn create":    "Did you mean `ce token`?",
	} {
		r := cli.Run(strings.Fields(argv), Options{Env: env})
		Code(t, r, 2)
		Contains(t, r.ErrJSON(t)["hint"].(string), want)
	}
	r := cli.Run([]string{"frobnicate"}, Options{Env: env})
	Code(t, r, 2)
	NotContains(t, r.ErrJSON(t)["hint"].(string), "Did you mean")
	Equal(t, len(api.Requests()), 0)
}
