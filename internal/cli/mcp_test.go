package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/Checkend/checkend-cli/internal/testkit"
)

func lines(t *testing.T, text string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not JSON: %q", line)
		}
		out = append(out, m)
	}
	return out
}

const mcpInput = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"claude-code"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":"two","method":"tools/list"}
`

// agentSetup runs ce setup, so that the home has an agent token.
func agentSetup(t *testing.T) (*FakeAPI, *Harness) {
	api, cli, _ := setup(t)
	Code(t, cli.Run(setupArgs(api)), 0)
	api.Reset()
	return api, cli
}

func TestMCPProxiesStdioToTheServerWithTheAgentToken(t *testing.T) {
	api, cli := agentSetup(t)
	r := cli.Run([]string{"mcp", "--url", api.URL}, Options{Stdin: mcpInput})
	Code(t, r, 0)
	Equal(t, lines(t, r.Stdout), []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "result": J{"echo": "initialize"}},
		{"jsonrpc": "2.0", "id": "two", "result": J{"echo": "tools/list"}},
	})
	Equal(t, api.Paths(), []string{"POST /mcp", "POST /mcp", "POST /mcp"})
	for _, req := range api.Requests() {
		if req.Authorization != "Bearer "+api.Tokens[0] {
			t.Errorf("authorization %q", req.Authorization)
		}
		Contains(t, req.Header.Get("Accept"), "application/json")
		Contains(t, req.Header.Get("Accept"), "text/event-stream")
		Equal(t, req.Header.Get("Content-Type"), "application/json")
	}
	Equal(t, api.Requests()[0].Body, J{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": J{"clientInfo": J{"name": "claude-code"}}})
	NotContains(t, r.All, api.Tokens[0])
}

func TestMCPReadsAnEventStreamAnswer(t *testing.T) {
	api, cli := agentSetup(t)
	api.Override = func(r *Request) *Reply {
		if r.Path == "/mcp" {
			return &Reply{Status: 200, Headers: map[string]string{"Content-Type": "text/event-stream"},
				Raw: "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"}
		}
		return nil
	}
	r := cli.Run([]string{"mcp"}, Options{Stdin: `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"})
	Code(t, r, 0)
	Equal(t, lines(t, r.Stdout), []map[string]any{{"jsonrpc": "2.0", "id": 1, "result": J{}}})
}

func TestMCPAnswersAnErrorWhenTheServerRejectsTheToken(t *testing.T) {
	api, cli := agentSetup(t)
	api.Tokens = nil
	r := cli.Run([]string{"mcp"}, Options{Stdin: `{"jsonrpc":"2.0","id":7,"method":"tools/list"}` + "\n"})
	Code(t, r, 0)
	out := lines(t, r.Stdout)
	Equal(t, out[0]["id"], 7)
	Contains(t, out[0]["error"].(map[string]any)["message"].(string), "rejected the token")
	Contains(t, r.Stderr, "ce setup")
}

func TestMCPAnswersAParseError(t *testing.T) {
	api, cli := agentSetup(t)
	r := cli.Run([]string{"mcp"}, Options{Stdin: "{not json\n"})
	Code(t, r, 0)
	out := lines(t, r.Stdout)
	Equal(t, out[0]["id"], nil)
	Equal(t, out[0]["error"].(map[string]any)["code"], -32700)
	if len(api.Requests()) != 0 {
		t.Error("sent a bad line")
	}
}

func TestMCPNeverUsesTheLoginOrAnEnvironmentToken(t *testing.T) {
	api, cli := loggedIn(t)
	r := cli.Run([]string{"mcp", "--url", api.URL}, Options{Stdin: mcpInput, Env: map[string]string{"CHECKEND_TOKEN": api.Token}})
	Code(t, r, 5)
	Contains(t, r.ErrJSON(t)["hint"].(string), "ce setup")
	if r.Stdout != "" || len(api.Requests()) != 0 {
		t.Errorf("stdout %q, %d requests", r.Stdout, len(api.Requests()))
	}
	Code(t, cli.Run([]string{"mcp", "--token", api.Token}, Options{Stdin: mcpInput}), 2)
}

func TestMCPUsesTheAgentTokenOfTheLinkedApp(t *testing.T) {
	api, cli := agentSetup(t)
	other := NewHarness(t)
	_ = other
	// A second app of the same instance: the link chooses the token.
	cli.Write(".checkend.json", `{"project":"`+id(api.Projects[0])+`"}`)
	Code(t, cli.Run([]string{"setup", "--url", api.URL, "--key", api.SetupKey, "--name", "Second"}), 0)
	cli.Write(".checkend.json", `{"project":"`+id(api.Projects[0])+`"}`)
	api.Reset()
	Code(t, cli.Run([]string{"mcp", "--url", api.URL}, Options{Stdin: mcpInput}), 0)
	Equal(t, api.Last().Authorization, "Bearer "+api.Tokens[0])
}

func TestMCPUsesCheckendAgentToken(t *testing.T) {
	api, cli, _ := setup(t)
	api.Tokens = []string{"ck_test_env_agent_token_0123456789"}
	r := cli.Run([]string{"mcp", "--url", api.URL}, Options{Stdin: mcpInput, Env: map[string]string{"CHECKEND_AGENT_TOKEN": api.Tokens[0]}})
	Code(t, r, 0)
	Equal(t, api.Last().Authorization, "Bearer "+api.Tokens[0])
	NotContains(t, r.All, api.Tokens[0])
}
