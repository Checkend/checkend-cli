// Package host is everything the CLI uses from the outside world. Tests give a fake one.
package host

import (
	"io"
	"net/http"
	"time"

	"github.com/Checkend/checkend-cli/internal/credstore"
)

// Writer is stdout or stderr.
type Writer struct {
	io.Writer
	IsTTY   bool
	Columns int
}

// Host is the process: environment, files, terminal, network, clock and keychain.
type Host struct {
	Env    map[string]string
	Cwd    string
	Home   string
	Stdout Writer
	Stderr Writer
	// Interactive is true when stdin and stdout are terminals, so the CLI can ask questions.
	Interactive bool
	Prompt      func(question string, secret bool) (string, error)
	ReadStdin   func() (string, error)
	// Stdin is the input as a stream, for `ce mcp`.
	Stdin io.Reader
	HTTP  *http.Client
	// Git runs git in dir and returns its trimmed output.
	Git     func(dir string, args ...string) (string, error)
	Now     func() time.Time
	Keyring credstore.Keyring
	// ReleaseAPI and ReleaseKeys replace the source of the server releases and its trusted
	// keys (tests only; a release build leaves them empty).
	ReleaseAPI  string
	ReleaseKeys map[string]string
	// Executable is the path of the running ce, with symlinks resolved (for `ce update`).
	Executable string
	// CLIReleaseAPI replaces the GitHub API of the CLI repo (tests only).
	CLIReleaseAPI string
	// CloudflareAPI replaces https://api.cloudflare.com/client/v4 (tests only).
	CloudflareAPI string
	// Run runs a program (ssh, ssh-keyscan, docker) with stdin and returns its stdout. Tests
	// fake it.
	Run func(stdin []byte, name string, args ...string) ([]byte, error)
	// LookupHost resolves a host name to its addresses.
	LookupHost func(name string) ([]string, error)
}

// Getenv returns an environment variable, or "".
func (h *Host) Getenv(name string) string { return h.Env[name] }
