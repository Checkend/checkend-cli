package testkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Checkend/checkend-cli/internal/cli"
	"github.com/Checkend/checkend-cli/internal/credstore"
	"github.com/Checkend/checkend-cli/internal/host"
)

// Result is the result of one CLI run.
type Result struct {
	Code   int
	Stdout string
	Stderr string
	// All is stdout and stderr together.
	All string
}

// JSON decodes stdout.
func (r Result) JSON(t testing.TB) map[string]any {
	t.Helper()
	return decode(t, r.Stdout)
}

// ErrJSON decodes stderr.
func (r Result) ErrJSON(t testing.TB) map[string]any {
	t.Helper()
	return decode(t, r.Stderr)
}

func decode(t testing.TB, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	return out
}

// Options change one run.
type Options struct {
	Env map[string]string
	TTY bool
	// Answers for prompts, in order.
	Answers []string
	Stdin   string
	Cwd     string
}

// Prompt is one question that the CLI asked.
type Prompt struct {
	Question string
	Secret   bool
}

// Harness runs the CLI in-process with a fake terminal, a temporary home, a temporary repo and
// a keychain in memory.
type Harness struct {
	t       testing.TB
	Home    string
	Repo    string
	Keyring *credstore.Memory
	Prompts []Prompt
	// Git has the output of git commands, by their arguments ("rev-parse HEAD").
	Git map[string]string
	// Releases is the fake of the server releases (nil: none is configured).
	Releases *FakeReleases
	// Exec fakes the programs that the CLI runs (ssh, docker). Calls records each call.
	Exec  func(stdin []byte, name string, args ...string) ([]byte, error)
	Calls []Call
}

// Call is one program that the CLI ran.
type Call struct {
	Name  string
	Args  []string
	Stdin string
}

// NewHarness makes a home and a repo (with .git) in a temporary directory.
func NewHarness(t testing.TB) *Harness {
	root := t.TempDir()
	h := &Harness{t: t, Home: filepath.Join(root, "home"), Repo: filepath.Join(root, "repo"), Keyring: &credstore.Memory{}}
	must(t, os.MkdirAll(h.Home, 0o755))
	must(t, os.MkdirAll(filepath.Join(h.Repo, ".git"), 0o755))
	return h
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type lockedWriter struct {
	mu  *sync.Mutex
	own *bytes.Buffer
	all *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.own.Write(p)
	return w.all.Write(p)
}

// Run runs `ce argv...`.
func (h *Harness) Run(argv []string, opts ...Options) Result {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	env := map[string]string{"HOME": h.Home, "NO_COLOR": "1"}
	for k, v := range o.Env {
		env[k] = v
	}
	cwd := o.Cwd
	if cwd == "" {
		cwd = h.Repo
	}
	var mu sync.Mutex
	var stdout, stderr, all bytes.Buffer
	answers := append([]string(nil), o.Answers...)
	hst := &host.Host{
		Env:         env,
		Cwd:         cwd,
		Home:        h.Home,
		Stdout:      host.Writer{Writer: lockedWriter{&mu, &stdout, &all}, IsTTY: o.TTY, Columns: 100},
		Stderr:      host.Writer{Writer: lockedWriter{&mu, &stderr, &all}, IsTTY: o.TTY, Columns: 100},
		Interactive: o.TTY,
		Prompt: func(question string, secret bool) (string, error) {
			h.Prompts = append(h.Prompts, Prompt{question, secret})
			if len(answers) == 0 {
				return "", fmt.Errorf("no answer for prompt: %s", question)
			}
			answer := answers[0]
			answers = answers[1:]
			return answer, nil
		},
		ReadStdin: func() (string, error) { return o.Stdin, nil },
		Stdin:     strings.NewReader(o.Stdin),
		HTTP:      nil,
		Now:       func() time.Time { return time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC) },
		Keyring:   h.Keyring,
		Git: func(dir string, args ...string) (string, error) {
			if out, ok := h.Git[strings.Join(args, " ")]; ok {
				return out, nil
			}
			return "", fmt.Errorf("fatal: not a git repository")
		},
	}
	if h.Releases != nil {
		hst.ReleaseAPI, hst.ReleaseKeys = h.Releases.API, h.Releases.PublicKeys()
	}
	hst.Run = func(stdin []byte, name string, args ...string) ([]byte, error) {
		h.Calls = append(h.Calls, Call{Name: name, Args: args, Stdin: string(stdin)})
		if h.Exec == nil {
			return nil, fmt.Errorf("%s: not found", name)
		}
		return h.Exec(stdin, name, args...)
	}
	code := cli.Run(argv, hst)
	return Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String(), All: all.String()}
}

// Write writes a file in the repo.
func (h *Harness) Write(name, content string) {
	h.t.Helper()
	path := filepath.Join(h.Repo, name)
	must(h.t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(h.t, os.WriteFile(path, []byte(content), 0o644))
}

// Exists reports whether a file exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
