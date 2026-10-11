package urlcheck

import (
	"errors"
	"strings"
	"testing"

	"github.com/Checkend/checkend-cli/internal/clierr"
)

func TestAcceptsHTTPSAndRemovesTrailingSlashQueryAndHash(t *testing.T) {
	cases := map[string]string{
		"https://errors.example.com/":         "https://errors.example.com",
		"https://errors.example.com/x/?a=1#h": "https://errors.example.com/x",
		" https://Errors.Example.com:443/ ":   "https://errors.example.com",
		"http://localhost:8787":               "http://localhost:8787",
		"http://127.0.0.1:8787/":              "http://127.0.0.1:8787",
	}
	for in, want := range cases {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestAcceptsPlainHTTPOnlyForLocalhost(t *testing.T) {
	for _, in := range []string{
		"http://errors.example.com",
		"http://localhost.example.com",
		"http://127.0.0.2",
		"http://10.0.0.1",
		"http://[::1]",
	} {
		_, err := Normalize(in)
		if err == nil || !strings.Contains(err.Error(), "HTTPS") {
			t.Errorf("Normalize(%q): %v", in, err)
		}
	}
}

func TestRefusesOtherSchemesCredentialsAndBadURLs(t *testing.T) {
	for in, part := range map[string]string{
		"ftp://x.example":           "HTTPS",
		"file:///etc/passwd":        "HTTPS",
		"https://user:pw@x.example": "credentials",
		"not a url":                 "not a valid URL",
		"https://":                  "not a valid URL",
	} {
		_, err := Normalize(in)
		var cerr *clierr.Error
		if !errors.As(err, &cerr) || cerr.Code != clierr.Usage || !strings.Contains(err.Error(), part) {
			t.Errorf("Normalize(%q): %v", in, err)
		}
	}
}

func TestKeepsTheBasePathOfAHostedInstance(t *testing.T) {
	for in, want := range map[string]string{
		"https://cloud.example.com/k7q2x9":          "https://cloud.example.com/k7q2x9",
		"https://cloud.example.com/k7q2x9/":         "https://cloud.example.com/k7q2x9",
		"https://Cloud.Example.com:443/k7q2x9/a-b/": "https://cloud.example.com/k7q2x9/a-b",
	} {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestRefusesAPathThatNamesMoreThanOnePlace(t *testing.T) {
	for _, path := range []string{"/a/../b", "/./a", "//a", "/a//", "/a//b", "/a/%2e%2e", "/a%2Fb", "/a%5cb", `/a\b`} {
		_, err := Normalize("https://cloud.example.com" + path)
		var cerr *clierr.Error
		if !errors.As(err, &cerr) || cerr.Code != clierr.Usage || !strings.Contains(err.Error(), "path") {
			t.Errorf("Normalize(%q): %v", path, err)
		}
	}
}
