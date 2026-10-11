package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// A piece of a server recipe file (TypeScript template text, with escaped backticks).
const sampleRecipes = "export const AGENT_TASKS = [\n" +
	"\t{ task: \"deploys\", command: \"ce deploy\" },\n" +
	"\t{ task: \"remove\", command: \"ce remove\" },\n" +
	"];\n" +
	"return `# Install ce (the Checkend CLI)\n" +
	"Run \\`ce --version\\`. If it works, \\`ce\\` is installed.\n" +
	"## 1. Run ce setup\n" +
	"\\`\\`\\`sh\n" +
	"ce setup --url ${base} \\\\\n" +
	"  --key \"$CHECKEND_SETUP_KEY\"\n" +
	"ce sourcemaps upload --release \"$RELEASE\" --bogus dist/ | tee log\n" +
	"\\`\\`\\`\n" +
	"- Claude Code: \\`claude mcp add checkend -- ce mcp --url ${base}\\`\n" +
	"- \\`ce dsn rotate --revoke-old --yes\\` and \\`ce project delete <app> --yes\\`\n" +
	"- \\`ce isue list\\`\n" +
	"`;\n"

func TestRecipeCommandsAreFoundInCodeOnly(t *testing.T) {
	got := recipeCommands(sampleRecipes)
	want := []string{
		"ce deploy",
		"ce remove",
		"ce --version",
		"ce setup --url ${base} --key \"$CHECKEND_SETUP_KEY\"",
		"ce sourcemaps upload --release \"$RELEASE\" --bogus dist/",
		"ce mcp --url ${base}",
		"ce dsn rotate --revoke-old --yes",
		"ce project delete <app> --yes",
		"ce isue list",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRecipeCommandsAreCheckedAgainstTheCommandTree(t *testing.T) {
	var problems []string
	for _, c := range recipeCommands(sampleRecipes) {
		if err := checkRecipeCommand(c); err != nil {
			problems = append(problems, err.Error())
		}
	}
	want := []string{
		`ce remove: unknown command "remove"`,
		`ce sourcemaps upload --release "$RELEASE" --bogus dist/: unknown option --bogus`,
		`ce isue list: unknown command "isue"`,
	}
	if !reflect.DeepEqual(problems, want) {
		t.Errorf("got\n%s", strings.Join(problems, "\n"))
	}
}

// TestServerRecipesNameRealCommands checks the recipes of the server (src/core/recipes.ts of
// Checkend/checkend). CI downloads the file and sets CHECKEND_RECIPES_TS.
func TestServerRecipesNameRealCommands(t *testing.T) {
	path := os.Getenv("CHECKEND_RECIPES_TS")
	if path == "" {
		t.Skip("set CHECKEND_RECIPES_TS to the path of src/core/recipes.ts")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	commands := recipeCommands(string(data))
	if len(commands) < 5 {
		t.Fatalf("found only %d ce commands: the recipe format changed, so update recipeCommands", len(commands))
	}
	var problems []string
	for _, c := range commands {
		if err := checkRecipeCommand(c); err != nil {
			problems = append(problems, "  "+err.Error())
		}
	}
	if len(problems) > 0 {
		t.Fatalf("the server recipes name %d ce command(s) or option(s) that the CLI does not have:\n%s",
			len(problems), strings.Join(problems, "\n"))
	}
	t.Logf("checked %d ce commands:\n  %s", len(commands), strings.Join(commands, "\n  "))
}
