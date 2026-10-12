// The `ce` and `checkend` commands. All logic is in internal/cli; this file connects it to the
// process: terminal, environment, prompts, keychain and exit code.
package main

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Checkend/checkend-cli/internal/cli"
	"github.com/Checkend/checkend-cli/internal/credstore"
	"github.com/Checkend/checkend-cli/internal/host"
)

const maxStdin = 64 * 1024

func writer(f *os.File) host.Writer {
	w := host.Writer{Writer: f, IsTTY: term.IsTerminal(int(f.Fd())), Columns: 100}
	if cols, _, err := term.GetSize(int(f.Fd())); err == nil && cols > 0 {
		w.Columns = cols
	}
	return w
}

var stdin = bufio.NewReader(os.Stdin)

func prompt(question string, secret bool) (string, error) {
	os.Stderr.WriteString(question)
	if !secret {
		line, err := stdin.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	return readSecret()
}

// readSecret reads a line without echo. Ctrl-C gives the terminal back before it exits.
func readSecret() (string, error) {
	fd := int(os.Stdin.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return "", err
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	done := make(chan struct{})
	go func() {
		select {
		case <-interrupt:
			_ = term.Restore(fd, state)
			os.Stderr.WriteString("\n")
			os.Exit(130)
		case <-done:
		}
	}()
	secret, err := term.ReadPassword(fd)
	close(done)
	signal.Stop(interrupt)
	os.Stderr.WriteString("\n")
	return string(secret), err
}

func readStdin() (string, error) {
	data, err := io.ReadAll(io.LimitReader(stdin, maxStdin))
	return string(data), err
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// run runs a program with stdin; its stderr goes to the terminal (ssh asks there, for example).
func run(stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

func environ() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}

func main() {
	cwd, _ := os.Getwd()
	exe, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	home, _ := os.UserHomeDir()
	stdout := writer(os.Stdout)
	h := &host.Host{
		Env:         environ(),
		Cwd:         cwd,
		Home:        home,
		Stdout:      stdout,
		Stderr:      writer(os.Stderr),
		Interactive: stdout.IsTTY && term.IsTerminal(int(os.Stdin.Fd())),
		Prompt:      prompt,
		ReadStdin:   readStdin,
		Stdin:       stdin,
		Now:         time.Now,
		Keyring:     credstore.System{},
		Git:         git,
		Run:         run,
		LookupHost:  net.LookupHost,
		Executable:  exe,
		LookPath:    exec.LookPath,
	}
	os.Exit(cli.Run(os.Args[1:], h))
}
