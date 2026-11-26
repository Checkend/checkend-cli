package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Client talks to one Checkend server with an API token: the MCP tools at
// /mcp for stats and setup, and plain HTTP for the file export. It uses the
// same surface as an agent, so a token works in both.
type Client struct {
	URL     string // base URL, no trailing slash
	Token   string
	Version string
	HTTP    *http.Client

	sess *sdk.ClientSession
	rt   *authTransport
}

// ErrUnauthorized means the server refused the token.
var ErrUnauthorized = errors.New("the server refused the token (check it in Settings › Agent, then run: ce login)")

// ErrLocked means the org of the token is locked (cloud billing).
var ErrLocked = errors.New("the organization is locked: open Checkend in the browser to see why")

// authTransport adds the token and remembers the last error status, so a
// failed MCP connect can say why.
type authTransport struct {
	token, ua string
	base      http.RoundTripper
	mu        sync.Mutex
	status    int
}

func (t *authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	r.Header.Set("User-Agent", t.ua)
	res, err := t.base.RoundTrip(r)
	if err == nil && res.StatusCode >= 400 {
		t.mu.Lock()
		t.status = res.StatusCode
		t.mu.Unlock()
	}
	return res, err
}

func (t *authTransport) lastStatus() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (c *Client) httpClient() *http.Client {
	if c.rt == nil {
		base := http.DefaultTransport
		timeout := 60 * time.Second
		if c.HTTP != nil {
			if c.HTTP.Transport != nil {
				base = c.HTTP.Transport
			}
			if c.HTTP.Timeout != 0 {
				timeout = c.HTTP.Timeout
			}
		}
		c.rt = &authTransport{token: c.Token, ua: "ce/" + c.Version, base: base}
		c.HTTP = &http.Client{Transport: c.rt, Timeout: timeout}
	}
	return c.HTTP
}

// statusErr maps a remembered HTTP status to a clear error.
func (c *Client) statusErr(err error) error {
	if c.rt != nil {
		switch c.rt.lastStatus() {
		case http.StatusUnauthorized:
			return ErrUnauthorized
		case http.StatusPaymentRequired:
			return ErrLocked
		case http.StatusNotFound:
			return fmt.Errorf("no Checkend MCP endpoint at %s/mcp (check the URL)", c.URL)
		}
	}
	return err
}

func (c *Client) connect(ctx context.Context) error {
	if c.sess != nil {
		return nil
	}
	hc := c.httpClient()
	cl := sdk.NewClient(&sdk.Implementation{Name: "ce", Version: c.Version}, nil)
	sess, err := cl.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: c.URL + "/mcp", HTTPClient: hc,
		DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		return c.statusErr(fmt.Errorf("connect to %s: %w", c.URL, err))
	}
	c.sess = sess
	return nil
}

// Close ends the MCP session.
func (c *Client) Close() {
	if c.sess != nil {
		_ = c.sess.Close()
		c.sess = nil
	}
}

// Call runs one MCP tool and decodes its structured result into out (when
// out is not nil). raw is the result as JSON, for --json.
func (c *Client) Call(ctx context.Context, tool string, args any, out any) (raw json.RawMessage, err error) {
	if err := c.connect(ctx); err != nil {
		return nil, err
	}
	res, err := c.sess.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return nil, c.statusErr(err)
	}
	if res.IsError {
		var msg []string
		for _, ct := range res.Content {
			if t, ok := ct.(*sdk.TextContent); ok {
				msg = append(msg, t.Text)
			}
		}
		if len(msg) == 0 {
			msg = []string{tool + " failed"}
		}
		return nil, errors.New(strings.Join(msg, "; "))
	}
	raw, err = json.Marshal(res.StructuredContent)
	if err != nil {
		return nil, err
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return nil, fmt.Errorf("read the answer of %s: %w", tool, err)
		}
	}
	return raw, nil
}

// apiError is the error body of the HTTP API.
type apiError struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// Export downloads one export file (GET /api/v1/export) into w and returns the
// file name the server proposes.
func (c *Client) Export(ctx context.Context, q url.Values, w func(name string) (io.WriteCloser, error)) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/api/v1/export?"+q.Encode(), nil)
	if err != nil {
		return "", 0, err
	}
	hc := *c.httpClient()
	hc.Timeout = 0 // a large export can take minutes; ctx limits it
	res, err := hc.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", 0, httpErr(res)
	}
	name := ""
	if _, p, err := mime.ParseMediaType(res.Header.Get("Content-Disposition")); err == nil {
		name = p["filename"]
	}
	f, err := w(name)
	if err != nil {
		return "", 0, err
	}
	n, err := io.Copy(f, res.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return name, n, err
}

func httpErr(res *http.Response) error {
	switch res.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusPaymentRequired:
		return ErrLocked
	}
	var e apiError
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		if e.RequestID != "" {
			return fmt.Errorf("%s (request %s)", e.Message, e.RequestID)
		}
		return errors.New(e.Message)
	}
	return fmt.Errorf("the server answered %s", res.Status)
}
