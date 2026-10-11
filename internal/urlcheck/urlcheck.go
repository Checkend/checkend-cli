// Package urlcheck checks the URL of a Checkend instance.
package urlcheck

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/docs"
)

// Normalize checks and normalizes the URL of a Checkend instance. HTTPS only, except for
// http://localhost and http://127.0.0.1, because the token goes in each request. The result
// is the origin and the path, without a trailing slash.
func Normalize(input string) (string, error) {
	invalid := clierr.UsageError(fmt.Sprintf("%q is not a valid URL.", input), "Example: https://checkend.example.com").WithDocs(docs.PublicURL)
	u, err := url.Parse(strings.TrimSpace(input))
	if err != nil || u.Scheme == "" || u.Opaque != "" {
		return "", invalid
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	local := host == "localhost" || host == "127.0.0.1"
	if scheme != "https" && !(scheme == "http" && local) {
		return "", clierr.UsageError(
			fmt.Sprintf("Use HTTPS for %s://%s.", scheme, strings.ToLower(u.Host)),
			"Only http://localhost and http://127.0.0.1 can use plain HTTP.",
		).WithDocs(docs.PublicURL)
	}
	if host == "" {
		return "", invalid
	}
	if u.User != nil {
		return "", clierr.UsageError("Do not put credentials in the URL.", "Use `ce login` to save the token.").WithDocs(docs.PublicURL)
	}
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	if err := checkPath(path); err != nil {
		return "", err
	}
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	origin := scheme + "://" + strings.ToLower(u.Host)
	if port == "" {
		origin = scheme + "://" + host
		if strings.Contains(host, ":") {
			origin = scheme + "://[" + host + "]"
		}
	}
	return origin + path, nil
}

// checkPath checks the base path of a hosted instance (https://hosted.example/<id>). The
// token is saved for the full URL, so a path must name one place only: no empty, "." or ".."
// segments, and no encoded "/", "\\" or ".".
func checkPath(path string) error {
	if path == "" {
		return nil
	}
	for _, segment := range strings.Split(path, "/")[1:] {
		lower := strings.ToLower(segment)
		if segment == "" || segment == "." || segment == ".." ||
			strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(lower, "%2e") || strings.Contains(segment, "\\") {
			return clierr.UsageError(fmt.Sprintf("The URL path %q is not valid.", path),
				"Use the URL of your Checkend instance as it shows it, for example https://cloud.example.com/k7q2x9.").WithDocs(docs.PublicURL)
		}
	}
	return nil
}
