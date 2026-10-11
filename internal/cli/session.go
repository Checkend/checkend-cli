package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Checkend/checkend-cli/internal/api"
	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/config"
	"github.com/Checkend/checkend-cli/internal/host"
	"github.com/Checkend/checkend-cli/internal/jv"
	"github.com/Checkend/checkend-cli/internal/output"
)

// Command is one command, for example `ce issue list`.
type Command struct {
	// Path is for example ["issue", "list"].
	Path    []string
	Summary string
	// Usage shows the arguments and options, for example "<id> [--section <name>]".
	Usage string
	// Details are the option lines of the help.
	Details []string
	Options map[string]Kind
	// Args are the names of the positional arguments. A name in brackets is optional; a last
	// name like "[path...]" takes any number of arguments.
	Args []string
	Run  func(s *Session) error
}

// Exit ends a command with an exit code and no error message (the command printed its result).
type Exit int

func (e Exit) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// Session is one run of a command: parsed arguments, output and lazy access to the API.
type Session struct {
	Host *host.Host
	Out  *output.Output
	Args []string
	p    parsed
	link *config.Link
	read bool
	// upgrade is the install record when `ce upgrade` runs an install command.
	upgrade        *config.Install
	allowDowngrade bool
}

// String returns an option value, or "".
func (s *Session) String(name string) string { return s.p.values[name] }

// Has reports whether an option was given.
func (s *Session) Has(name string) bool {
	_, ok := s.p.values[name]
	return ok || s.p.bools[name]
}

// Bool returns a flag.
func (s *Session) Bool(name string) bool { return s.p.bools[name] }

// List returns all values of a List option.
func (s *Session) List(name string) []string { return s.p.lists[name] }

// URL returns the instance URL, or exit 5 with the next step.
func (s *Session) URL() (string, error) {
	url, err := config.ResolveURL(s.Host, s.String("url"))
	if err != nil {
		return "", err
	}
	if url == "" {
		return "", clierr.New(clierr.Auth, "Not logged in.", "Run `ce login`.")
	}
	return url, nil
}

// Token returns the token for url ("" when none) and hides it in the output. Without a login,
// it is the agent token of the linked app (from `ce setup`).
func (s *Session) Token(url string) (string, error) {
	token, err := config.ResolveToken(s.Host, s.String("token"), url)
	if token == "" && err == nil {
		if link, _ := s.Link(); link != nil {
			token, err = config.AgentToken(s.Host, url, link.Project)
		}
	}
	s.Out.Hide(token)
	return token, err
}

// AgentToken is the agent token for `ce mcp`: CHECKEND_AGENT_TOKEN, else the saved agent token
// of the linked app, else the only agent token of the instance. Never the login of a person.
func (s *Session) AgentToken(url string) (string, error) {
	token := s.Host.Getenv("CHECKEND_AGENT_TOKEN")
	if token == "" {
		ref, err := s.ProjectRef()
		if err != nil {
			return "", err
		}
		if ref != "" {
			if token, err = config.AgentToken(s.Host, url, ref); err != nil {
				return "", err
			}
		}
		if token == "" {
			if token, err = config.AgentToken(s.Host, url, ""); err != nil {
				return "", err
			}
		}
	}
	s.Out.Hide(token)
	return token, nil
}

// API returns a client with a token, or exit 5 with the next step.
func (s *Session) API() (*api.Client, error) {
	url, err := s.URL()
	if err != nil {
		return nil, err
	}
	token, err := s.Token(url)
	if err != nil {
		return nil, err
	}
	if token == "" {
		msg := "Not logged in."
		if config.HasSavedHosts(s.Host) {
			msg = fmt.Sprintf("Not logged in to %s.", url)
		}
		return nil, clierr.New(clierr.Auth, msg, fmt.Sprintf("Run `ce login --url %s`.", url))
	}
	return api.New(s.Host, url, token), nil
}

// Link returns the project link of the repo, or nil.
func (s *Session) Link() (*config.Link, error) {
	if !s.read {
		link, err := config.FindLink(s.Host)
		if err != nil {
			return nil, err
		}
		s.link, s.read = link, true
	}
	return s.link, nil
}

// ProjectRef is the project from --project or .checkend.json, or "".
func (s *Session) ProjectRef() (string, error) {
	if s.Has("project") {
		return s.String("project"), nil
	}
	link, err := s.Link()
	if err != nil || link == nil {
		return "", err
	}
	return link.Project, nil
}

// ProjectFilter is the project for a ?project= filter. The server takes the public ID or the
// slug (lowercase), so no lookup is needed.
func (s *Session) ProjectFilter() (string, error) {
	ref, err := s.ProjectRef()
	return strings.ToLower(strings.TrimSpace(ref)), err
}

// Project finds a project by public ID or slug, or exits 3.
func (s *Session) Project(c *api.Client, ref string) (map[string]any, error) {
	projects, err := Projects(c)
	if err != nil {
		return nil, err
	}
	wanted := strings.ToLower(strings.TrimSpace(ref))
	for _, p := range projects {
		if jv.Str(p["id"]) == wanted || strings.ToLower(jv.Str(p["slug"])) == wanted {
			return p, nil
		}
	}
	return nil, clierr.New(clierr.NotFound, fmt.Sprintf("Project %q not found.", ref), "Run `ce project list` to see the projects.")
}

// Projects calls GET /v1/projects.
func Projects(c *api.Client) ([]map[string]any, error) {
	raw, err := c.Get("/v1/projects", nil)
	if err != nil {
		return nil, err
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		return nil, clierr.Fail("The server returned a project list that cannot be read.")
	}
	return body.Data, nil
}

var digits = regexp.MustCompile(`^\d{1,15}$`)

// ParseID parses a positive integer ID, or exits 2.
func ParseID(value, what string) (int64, error) {
	if !digits.MatchString(value) {
		return 0, clierr.UsageError(what + " must be a number.")
	}
	return strconv.ParseInt(value, 10, 64)
}
