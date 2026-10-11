package testkit

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Code checks the exit code and shows stderr when it is wrong.
func Code(t testing.TB, r Result, want int) {
	t.Helper()
	if r.Code != want {
		t.Fatalf("exit code %d, want %d\nstdout: %s\nstderr: %s", r.Code, want, r.Stdout, r.Stderr)
	}
}

// Contains checks that text contains part.
func Contains(t testing.TB, text, part string) {
	t.Helper()
	if !strings.Contains(text, part) {
		t.Errorf("missing %q in:\n%s", part, text)
	}
}

// NotContains checks that text does not contain part.
func NotContains(t testing.TB, text, part string) {
	t.Helper()
	if part != "" && strings.Contains(text, part) {
		t.Errorf("unexpected %q in:\n%s", part, text)
	}
}

// Matches checks text against a regular expression.
func Matches(t testing.TB, text, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("no match for /%s/ in:\n%s", pattern, text)
	}
}

// NotMatches checks that text does not match a regular expression.
func NotMatches(t testing.TB, text, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("unexpected match for /%s/ in:\n%q", pattern, text)
	}
}

// Equal compares two values as JSON (so 1 and 1.0 are equal).
func Equal(t testing.TB, got, want any) {
	t.Helper()
	if g, w := Remarshal(got), Remarshal(want); !reflect.DeepEqual(g, w) {
		t.Errorf("got  %#v\nwant %#v", g, w)
	}
}
