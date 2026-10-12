package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
)

// maxMessage limits one JSON-RPC message from the agent.
const maxMessage = 10 << 20

func mcpCommand() *Command {
	return &Command{
		Path:    []string{"mcp"},
		Summary: "Connect a coding agent over MCP (stdio; the token stays in the keychain)",
		Usage:   "[--url <url>]",
		Details: []string{
			"  --url <url>  The Checkend instance (default: the instance of `ce login` or `ce setup`)",
			"",
			"A stdio MCP server for agents: it sends each JSON-RPC message to <url>/mcp with the",
			"agent token of `ce setup` (or CHECKEND_AGENT_TOKEN), never the login of a person.",
			"Add it to an agent, for example:",
			"  claude mcp add checkend -- {ce} mcp --url <url>",
		},
		Run: func(s *Session) error {
			if s.Has("token") {
				return clierr.UsageError("ce mcp uses only the agent token of `ce setup`, not --token.", "Set CHECKEND_AGENT_TOKEN, or run `ce setup`.")
			}
			url, err := s.URL()
			if err != nil {
				return err
			}
			token, err := s.AgentToken(url)
			if err != nil {
				return err
			}
			if token == "" {
				return clierr.New(clierr.Auth, fmt.Sprintf("No agent token for %s.", url),
					fmt.Sprintf("Run `ce setup --url %s --key-stdin` in the repo of the app, and pipe the setup key.", url))
			}
			client := api.New(s.Host, url, token)
			reader := bufio.NewReaderSize(s.Host.Stdin, 64<<10)
			warned := false
			for {
				line, readErr := readMessage(reader)
				if line = bytes.TrimSpace(line); len(line) > 0 {
					warned = forward(s, client, line, warned)
				}
				if readErr == io.EOF {
					return nil
				}
				if readErr != nil {
					return readErr
				}
			}
		},
	}
}

// readMessage reads one line, at most maxMessage bytes (the rest of a longer line is dropped).
func readMessage(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) <= maxMessage {
			line = append(line, chunk...)
		}
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}

func rpcError(id json.RawMessage, code int, message string) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
	return out
}

// forward sends one message and writes the answer as one line on stdout.
func forward(s *Session, client *api.Client, line []byte, warned bool) bool {
	write := func(msg []byte) {
		var buf bytes.Buffer
		if json.Compact(&buf, msg) == nil {
			s.Out.Write(buf.String() + "\n")
		}
	}
	if !json.Valid(line) {
		write(rpcError(nil, -32700, "Parse error"))
		return warned
	}
	var head struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(line, &head)
	status, contentType, body, err := client.PostRaw("/mcp", line, "application/json, text/event-stream")
	switch {
	case err != nil:
		write(rpcError(head.ID, -32603, err.Error()))
	case status == 401 || status == 403:
		write(rpcError(head.ID, -32001, fmt.Sprintf("%s rejected the token.", client.URL)))
		if !warned {
			s.Out.Notice(fmt.Sprintf("ce mcp: %s rejected the agent token. Run `ce setup --url %s --key-stdin` again with a new setup key.", client.URL, client.URL))
			warned = true
		}
	case status < 200 || status >= 300:
		write(rpcError(head.ID, -32603, fmt.Sprintf("The server answered HTTP %d.", status)))
	case strings.HasPrefix(contentType, "text/event-stream"):
		for _, event := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n\n") {
			var data []string
			for _, l := range strings.Split(event, "\n") {
				if v, ok := strings.CutPrefix(l, "data:"); ok {
					data = append(data, strings.TrimPrefix(v, " "))
				}
			}
			if len(data) > 0 {
				write([]byte(strings.Join(data, "\n")))
			}
		}
	case len(bytes.TrimSpace(body)) > 0:
		write(body)
	}
	return warned
}
