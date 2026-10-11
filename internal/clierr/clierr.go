// Package clierr has the exit codes and the error type of the CLI.
package clierr

import "errors"

// Exit codes. Scripts and agents depend on them: do not change a value.
const (
	OK       = 0
	Failure  = 1
	Usage    = 2
	NotFound = 3
	Conflict = 4
	Auth     = 5
	// Pending: the server accepted the request, and it waits for an approval.
	Pending = 6
)

// Error is an error with an exit code and an optional next step for the user.
type Error struct {
	Msg  string
	Code int
	Hint string
}

func (e *Error) Error() string { return e.Msg }

// New returns an error with an exit code and a hint (the hint can be empty).
func New(code int, msg, hint string) *Error { return &Error{Msg: msg, Code: code, Hint: hint} }

// Fail returns an error with exit code 1.
func Fail(msg string) *Error { return New(Failure, msg, "") }

// UsageError returns an error with exit code 2. The default hint points to the help.
func UsageError(msg string, hint ...string) *Error {
	h := "Run `ce --help` to see the commands."
	if len(hint) > 0 {
		h = hint[0]
	}
	return New(Usage, msg, h)
}

// CodeOf returns the exit code of err: its own code, or 1.
func CodeOf(err error) int {
	if err == nil {
		return OK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Failure
}
