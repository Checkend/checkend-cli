// Package api is the HTTP client for the Checkend API. It maps HTTP errors to exit codes.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/host"
	"github.com/Checkend/checkend-cli/internal/sanitize"
	"github.com/Checkend/checkend-cli/internal/version"
)

const (
	timeout = 30 * time.Second
	// maxBody limits a response, so that a bad server cannot fill the memory.
	maxBody = 64 << 20
)

// Health is the body of GET /health.
type Health struct {
	Status  string `json:"status"`
	Version any    `json:"version"`
	Ingest  *struct {
		Mode any `json:"mode"`
	} `json:"ingest"`
}

// Client calls one Checkend instance.
type Client struct {
	URL   string
	token string
	http  *http.Client
}

// New returns a client. The token can be empty (only /health needs no token).
func New(h *host.Host, baseURL, token string) *Client {
	client := h.HTTP
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	c := *client
	// A redirect could send the token to another host.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{URL: baseURL, token: token, http: &c}
}

// Health calls GET /health. It needs no token. A 503 still has a health body.
func (c *Client) Health() (Health, json.RawMessage, error) {
	var health Health
	resp, err := c.send("GET", "/health", nil, "", false)
	status, raw := resp.status, resp.body
	if err == nil {
		err = json.Unmarshal(raw, &health)
	}
	if err != nil || health.Status == "" {
		if errors.As(err, new(*clierr.Error)) {
			return health, nil, err
		}
		return health, nil, clierr.New(clierr.Failure, fmt.Sprintf("The server at %s is not a Checkend instance (HTTP %d).", c.URL, status), "Check the URL.")
	}
	return health, raw, nil
}

// Get calls GET path?query. Empty query values are left out.
func (c *Client) Get(path string, query map[string]string) (json.RawMessage, error) {
	params := url.Values{}
	for k, v := range query {
		if v != "" {
			params.Set(k, v)
		}
	}
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	return c.Call("GET", path, nil, "")
}

// Post calls POST path with a JSON body.
func (c *Client) Post(path string, body any) (json.RawMessage, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.Call("POST", path, data, "application/json")
}

// Delete calls DELETE path.
func (c *Client) Delete(path string) (json.RawMessage, error) {
	return c.Call("DELETE", path, nil, "")
}

// Call sends a request with the token and returns the JSON body of a 2xx response.
func (c *Client) Call(method, path string, body []byte, contentType string) (json.RawMessage, error) {
	resp, err := c.send(method, path, body, contentType, true)
	if err != nil {
		return nil, err
	}
	if resp.status >= 200 && resp.status < 300 {
		if len(resp.body) == 0 {
			return json.RawMessage("null"), nil
		}
		return resp.body, nil
	}
	return nil, c.httpError(resp)
}

type response struct {
	status     int
	retryAfter string
	body       json.RawMessage
}

func (c *Client) send(method, path string, body []byte, contentType string, auth bool) (response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.URL+path, reader)
	if err != nil {
		return response{}, clierr.Fail(fmt.Sprintf("Cannot build the request: %v", err))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "checkend-cli/"+version.Version)
	if auth && c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		reason := err.Error()
		var uerr *url.Error
		if errors.As(err, &uerr) {
			reason = uerr.Err.Error()
		}
		return response{}, clierr.New(clierr.Failure, fmt.Sprintf("Cannot reach %s: %s", c.URL, reason), "Check the URL and your network.")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return response{}, clierr.New(clierr.Failure,
			fmt.Sprintf("%s answered with a redirect (HTTP %d). The CLI does not follow redirects.", c.URL, resp.StatusCode),
			"Use the final URL of your Checkend instance.")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return response{}, clierr.New(clierr.Failure, fmt.Sprintf("Cannot read the answer of %s: %v", c.URL, err), "Check your network.")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && !json.Valid(raw) {
		return response{}, clierr.New(clierr.Failure,
			fmt.Sprintf("The server at %s is not a Checkend instance (HTTP %d, not JSON).", c.URL, resp.StatusCode), "Check the URL.")
	}
	return response{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After"), body: raw}, nil
}

func (c *Client) httpError(resp response) error {
	var body struct {
		Error   any `json:"error"`
		Reason  any `json:"reason"`
		Details []struct {
			Path    any `json:"path"`
			Message any `json:"message"`
		} `json:"details"`
	}
	_ = json.Unmarshal(resp.body, &body)
	code, _ := body.Error.(string)
	code = sanitize.OneLine(code)
	if reason, ok := body.Reason.(string); ok && reason != "" {
		code += ": " + sanitize.OneLine(reason)
	}
	switch resp.status {
	case 400, 413, 415, 422:
		text := code
		if len(body.Details) > 0 {
			var parts []string
			for _, d := range body.Details {
				parts = append(parts, fmt.Sprintf("%v: %v", d.Path, d.Message))
			}
			text = strings.Join(parts, "; ")
		}
		return clierr.New(clierr.Usage, "Invalid request: "+sanitize.OneLine(text), "")
	case 401, 403:
		return clierr.New(clierr.Auth, fmt.Sprintf("%s rejected the token.", c.URL), "Run `ce login` to save a valid token.")
	case 404:
		return &NotFound{Code: code}
	case 409:
		return clierr.New(clierr.Conflict, "Conflict: "+code, "")
	case 429:
		after := resp.retryAfter
		if after == "" {
			after = "a few"
		}
		return clierr.New(clierr.Failure, "Too many requests.", fmt.Sprintf("Try again in %s seconds.", sanitize.OneLine(after)))
	default:
		msg := fmt.Sprintf("The server answered HTTP %d", resp.status)
		if code != "" {
			msg += ": " + code
		}
		return clierr.Fail(msg + ".")
	}
}

// NotFound is a 404. Code is the "error" of the body, for example "project_not_found".
type NotFound struct{ Code string }

func (e *NotFound) Error() string { return "Not found." }

// Unwrap gives the CLI error with exit code 3.
func (e *NotFound) Unwrap() error { return clierr.New(clierr.NotFound, "Not found.", "") }

// OrNotFound replaces a 404 with the given message and hint.
func OrNotFound(raw json.RawMessage, err error, msg, hint string) (json.RawMessage, error) {
	var nf *NotFound
	if errors.As(err, &nf) {
		return nil, clierr.New(clierr.NotFound, msg, hint)
	}
	return raw, err
}
