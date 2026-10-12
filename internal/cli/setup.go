package cli

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/config"
	"github.com/Checkend/checkend-cli/internal/host"
	"github.com/Checkend/checkend-cli/internal/jv"
	"github.com/Checkend/checkend-cli/internal/snippets"
	"github.com/Checkend/checkend-cli/internal/urlcheck"
)

var setupKeyPattern = regexp.MustCompile(`^cks_[A-Za-z0-9_-]{20,}$`)

// setupSteps are the steps that an agent can report.
var setupSteps = []string{"sdk_installed"}

const newKeyHint = "Ask the user for a new setup key: \"Copy prompt\" in Checkend makes one (it is valid for 1 hour)."

func setupCommands() []*Command { return []*Command{setupCommand(), setupReportCommand()} }

// setupKey returns the setup key from --key-stdin (one line of stdin), --key (deprecated: it is
// in the shell history and in `ps`) or CHECKEND_SETUP_KEY, checked, and hides it. "" when none.
func setupKey(s *Session) (string, error) {
	key := s.String("key")
	if s.Bool("key-stdin") {
		if key != "" {
			return "", clierr.UsageError("Give the setup key once: --key-stdin or --key.", "Pipe it: printf '%s\n' \"$KEY\" | ce setup --url <url> --key-stdin")
		}
		input, err := s.Host.ReadStdin()
		if err != nil {
			return "", err
		}
		key, _, _ = strings.Cut(input, "\n")
		key = strings.TrimSpace(key)
		if key == "" {
			return "", clierr.UsageError("--key-stdin read no setup key from stdin.", "Pipe the key: printf '%s\n' \"$KEY\" | ce setup --url <url> --key-stdin")
		}
	} else if key != "" {
		s.Out.Notice("Warning: --key puts the setup key in your shell history and in `ps`. Pipe it to --key-stdin instead; --key goes away in the next release.")
	}
	if key == "" {
		key = s.Host.Getenv("CHECKEND_SETUP_KEY")
	}
	s.Out.Hide(key)
	if key != "" && !setupKeyPattern.MatchString(key) {
		return "", clierr.UsageError("That is not a setup key: a setup key starts with cks_.", newKeyHint)
	}
	return key, nil
}

// promptKey asks for the setup key with a hidden prompt on a terminal ("" without a terminal).
func promptKey(s *Session) (string, error) {
	if !s.Host.Interactive {
		return "", nil
	}
	key, err := s.Host.Prompt("Setup key: ", true)
	if err != nil {
		return "", err
	}
	key = strings.TrimSpace(key)
	s.Out.Hide(key)
	if key != "" && !setupKeyPattern.MatchString(key) {
		return "", clierr.UsageError("That is not a setup key: a setup key starts with cks_.", newKeyHint)
	}
	return key, nil
}

// agentName guesses the coding agent from its environment, for the token name.
func agentName(s *Session) string {
	if name := s.String("agent"); name != "" {
		return name
	}
	switch {
	case s.Host.Getenv("CLAUDECODE") != "":
		return "Claude Code"
	case s.Host.Getenv("CODEX_SANDBOX") != "" || s.Host.Getenv("CODEX_HOME") != "":
		return "Codex"
	case s.Host.Getenv("CURSOR_TRACE_ID") != "" || s.Host.Getenv("CURSOR_AGENT") != "":
		return "Cursor"
	}
	return "Coding agent"
}

type setupResult struct {
	URL     string `json:"url"`
	Project any    `json:"project"`
	Created bool   `json:"created"`
	DSN     string `json:"dsn"`
	Stack   any    `json:"stack"`
	File    string `json:"file"`
	Token   struct {
		Created bool   `json:"created"`
		Storage string `json:"storage"`
	} `json:"token"`
	TestEvent string            `json:"test_event"`
	MCP       map[string]string `json:"mcp"`
	StepsURL  string            `json:"steps_url"`
	Steps     string            `json:"steps"`
}

func mcpCommands(h *host.Host, url string) map[string]string {
	ce, path := ceCommand(h)
	return map[string]string{
		"server":      url + "/mcp",
		"claude_code": "claude mcp add checkend -- " + ce + " mcp --url " + url,
		"codex":       "codex mcp add checkend -- " + ce + " mcp --url " + url,
		"cursor":      fmt.Sprintf(`{"mcpServers":{"checkend":{"command":%q,"args":["mcp","--url",%q]}}}`, path, url),
	}
}

// ceCommand is how an agent starts this ce: "ce" when `ce` on PATH is this binary, else the
// absolute path of this binary (for the shell, and as it is).
func ceCommand(h *host.Host) (shell, path string) {
	if h.Executable == "" {
		return "ce", "ce"
	}
	self, err := filepath.EvalSymlinks(h.Executable)
	if err != nil {
		self = h.Executable
	}
	if h.LookPath != nil {
		if found, err := h.LookPath("ce"); err == nil {
			if resolved, err := filepath.EvalSymlinks(found); err == nil && resolved == self {
				return "ce", "ce"
			}
		}
	}
	return QuotePath(runtime.GOOS, h.Executable), h.Executable
}

// QuotePath quotes a program path for the shell of the system: POSIX quotes on macOS and Linux;
// on Windows, double quotes when the path has a space or a special character of cmd.
func QuotePath(goos, path string) string {
	if goos != "windows" {
		return shellQuote(path)
	}
	if strings.ContainsAny(path, " &()^%!;,=") {
		return `"` + path + `"`
	}
	return path
}

func setupCommand() *Command {
	return &Command{
		Path:    []string{"setup"},
		Summary: "Connect this repo to Checkend with a setup key (for coding agents)",
		Usage:   "--url <url> --key-stdin [--name <name>] [--platform <platform>] [--agent <name>]",
		Details: []string{
			"  --url <url>            The Checkend instance",
			"  --key-stdin            Read the setup key from stdin (one line), for example:",
			"                           printf '%s\\n' \"$KEY\" | ce setup --url <url> --key-stdin",
			"                         (or CHECKEND_SETUP_KEY; on a terminal ce asks for it, hidden)",
			"  --key <cks_…>          Deprecated: the key goes into the shell history and `ps`",
			"  --name <name>          The app name (default: the repo directory)",
			"  --platform <platform>  Override the detected stack, for example node-express",
			"  --agent <name>         The agent, for the token name (default: detected)",
			"",
			"Registers the app, writes .checkend.json, saves an agent token in the OS keychain,",
			"sends a test event, and prints the MCP commands and the next steps for the agent.",
			"The setup key and the token are never printed.",
			"",
			"Then: ce setup report sdk_installed   (after the SDK is installed)",
		},
		Options: map[string]Kind{"key": String, "key-stdin": Bool, "name": String, "platform": String, "agent": String},
		Run: func(s *Session) error {
			h := s.Host
			key, err := setupKey(s)
			if err != nil {
				return err
			}
			input := s.String("url")
			if input == "" {
				input = h.Getenv("CHECKEND_URL")
			}
			if input == "" {
				return clierr.UsageError("Give the URL of your Checkend instance.", "Run `ce setup --url <url> --key-stdin` and pipe the key (both are in the prompt).")
			}
			url, err := urlcheck.Normalize(input)
			if err != nil {
				return err
			}
			if key == "" {
				if key, err = promptKey(s); err != nil {
					return err
				}
			}
			if key == "" {
				return clierr.UsageError("Give the setup key.", "Pipe it (the key is in the prompt): printf '%s\n' \"$KEY\" | ce setup --url "+url+" --key-stdin")
			}
			root := config.FindRepoRoot(h.Cwd)
			if root == "" {
				root = h.Cwd
			}
			stack := snippets.DetectStack(root)
			platform := s.String("platform")
			if platform == "" {
				platform = stack.Platform
			}
			name := s.String("name")
			if name == "" {
				name = filepath.Base(root)
			}

			client := api.New(h, url, key)
			health, _, err := client.Health()
			if err != nil {
				return err
			}
			if health.Status != "ok" {
				return clierr.Fail(fmt.Sprintf("The Checkend instance at %s is not healthy (%s).", url, health.Status))
			}
			raw, err := client.Post("/v1/setup/apps", createBody(name, platform))
			if err != nil {
				return keyError(err)
			}
			body, _ := jv.Decode(raw)
			data := jv.Get(body, "data")
			project, ok := jv.Get(data, "project").(map[string]any)
			if !ok {
				return clierr.Fail("The server returned an app that cannot be read.")
			}
			checked, err := snippets.CheckDSN(jv.Get(data, "dsn"))
			if err != nil {
				return err
			}
			file, err := writeLink(root, project)
			if err != nil {
				return err
			}
			ref := jv.Str(project["id"])

			var r setupResult
			r.URL, r.Project, r.Created, r.DSN, r.Stack, r.File = url, project, jv.Get(data, "created") == true, checked, stack, file
			// A dedicated agent token for each app, never the login of a person: an agent with
			// an admin token over MCP would get admin power.
			saved, err := config.AgentToken(h, url, ref)
			if err != nil {
				return err
			}
			if saved != "" {
				s.Out.Hide(saved)
				r.Token.Storage = config.AgentStorage(h, url, ref)
			} else {
				raw, err := client.Post("/v1/setup/tokens", map[string]string{"name": agentName(s) + " (ce setup)", "project": ref})
				if err != nil {
					return keyError(err)
				}
				created, _ := jv.Decode(raw)
				token := jv.Str(jv.Get(created, "data", "token"))
				s.Out.Hide(token)
				if token == "" {
					return clierr.Fail("The server did not return a token.")
				}
				if r.Token.Storage, err = config.SaveAgentToken(h, url, ref, token); err != nil {
					return err
				}
				r.Token.Created = true
			}

			r.TestEvent = "sent"
			if _, err := client.Post("/v1/setup/apps/"+ref+"/test-event", map[string]any{}); err != nil {
				if clierr.CodeOf(err) != clierr.Conflict {
					return keyError(err)
				}
				r.TestEvent = "already_sent"
			}
			r.MCP = mcpCommands(h, url)
			r.StepsURL = url + "/agent/setup.md"
			if steps, err := client.Text("/agent/setup.md"); err == nil {
				r.Steps = steps
			}
			s.Out.Data(r, func(d any) string { return renderSetup(d) })
			return nil
		},
	}
}

// keyError explains a rejected setup key.
func keyError(err error) error {
	if clierr.CodeOf(err) == clierr.Auth {
		return clierr.New(clierr.Auth, "The setup key was rejected: it is old, revoked or not for this instance.", newKeyHint)
	}
	return err
}

func renderSetup(d any) string {
	project := jv.Get(d, "project")
	stack := jv.Get(d, "stack")
	lines := []string{
		fmt.Sprintf("Connected %s (%s) as app %s (%s).", jv.Str(jv.Get(project, "name")), jv.Str(jv.Get(stack, "label")),
			jv.Str(jv.Get(project, "slug")), jv.Str(jv.Get(project, "id"))),
		fmt.Sprintf("Wrote %s (the project link only, no token; safe to commit).", jv.Str(jv.Get(d, "file"))),
		"",
		"DSN: " + jv.Str(jv.Get(d, "dsn")),
	}
	if sdk := jv.Str(jv.Get(stack, "sdk")); sdk != "" {
		lines = append(lines, "Sentry SDK: "+sdk)
	}
	verb, storage := "Saved an agent token", jv.Str(jv.Get(d, "token", "storage"))
	if jv.Get(d, "token", "created") != true {
		verb = "Kept the agent token of this app"
	}
	if storage == config.StorageKeychain {
		lines = append(lines, verb+" in the OS keychain.")
	} else {
		lines = append(lines, verb+" in "+storage+" (mode 600).")
	}
	if jv.Str(jv.Get(d, "test_event")) == "sent" {
		lines = append(lines, "Sent a test event.")
	} else {
		lines = append(lines, "The test event of this app was sent before.")
	}
	mcp := jv.Get(d, "mcp")
	lines = append(lines, "",
		"Connect MCP (the token stays in the keychain):",
		"  Claude Code:  "+jv.Str(jv.Get(mcp, "claude_code")),
		"  Codex:        "+jv.Str(jv.Get(mcp, "codex")),
		"  Cursor:       add to .cursor/mcp.json: "+jv.Str(jv.Get(mcp, "cursor")),
		"")
	if steps := strings.TrimSpace(jv.Str(jv.Get(d, "steps"))); steps != "" {
		lines = append(lines, "Next steps (from "+jv.Str(jv.Get(d, "steps_url"))+"):", "", steps)
	} else {
		lines = append(lines, "Read the next steps at "+jv.Str(jv.Get(d, "steps_url"))+".")
	}
	return strings.Join(lines, "\n")
}

func setupReportCommand() *Command {
	return &Command{
		Path:    []string{"setup", "report"},
		Summary: "Report a setup step to Checkend (sdk_installed)",
		Usage:   "<step> [--key-stdin] [--project <id|slug>]",
		Details: []string{
			"  <step>               sdk_installed",
			"  --key-stdin          Read the setup key from stdin (or CHECKEND_SETUP_KEY; on a",
			"                       terminal ce asks for it). Default: the agent token of `ce setup`",
			"  --key <cks_…>        Deprecated: the key goes into the shell history and `ps`",
			"  --project <id|slug>  Default: the project in .checkend.json",
		},
		Options: map[string]Kind{"key": String, "key-stdin": Bool, "project": String},
		Args:    []string{"step"},
		Run: func(s *Session) error {
			step := s.Args[0]
			if !slices.Contains(setupSteps, step) {
				return clierr.UsageError(fmt.Sprintf("Unknown step %q.", step), "Use one of: "+strings.Join(setupSteps, ", ")+".")
			}
			key, err := setupKey(s)
			if err != nil {
				return err
			}
			ref, err := s.ProjectRef()
			if err != nil {
				return err
			}
			if ref == "" {
				return clierr.UsageError("No project given and no .checkend.json found.", "Run `ce setup` first, or give --project.")
			}
			url, err := s.URL()
			if err != nil {
				return err
			}
			token := key
			if token == "" {
				if token, err = config.AgentToken(s.Host, url, ref); err != nil {
					return err
				}
				s.Out.Hide(token)
			}
			if token == "" {
				// No agent token and no login either: ask for the setup key on a terminal.
				if login, _ := s.Token(url); login == "" {
					if key, err = promptKey(s); err != nil {
						return err
					}
					token = key
				}
			}
			var client *api.Client
			if token != "" {
				client = api.New(s.Host, url, token)
			} else if client, err = s.API(); err != nil {
				return err
			}
			path := "/v1/setup/apps/" + strings.ToLower(strings.TrimSpace(ref)) + "/report"
			if _, err := client.Post(path, map[string]string{"step": step}); err != nil {
				if clierr.CodeOf(err) == clierr.Auth {
					if key != "" {
						return keyError(err)
					}
					return clierr.New(clierr.Auth, "This token cannot report setup steps.",
						fmt.Sprintf("Run `ce setup report %s --key-stdin` and pipe the setup key from the prompt.", step))
				}
				_, err = api.OrNotFound(nil, err, fmt.Sprintf("App %q not found for this key.", ref), "Run `ce setup` first.")
				return err
			}
			s.Out.Data(map[string]any{"project": ref, "step": step, "reported": true}, func(d any) string {
				return fmt.Sprintf("Reported %s for %s.", jv.Str(jv.Get(d, "step")), jv.Str(jv.Get(d, "project")))
			})
			return nil
		},
	}
}
