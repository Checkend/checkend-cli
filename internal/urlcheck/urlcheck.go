// Package urlcheck checks the URL of a Checkend instance.
package urlcheck

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Checkend/checkend-cli/internal/clierr"
)

// Normalize checks and normalizes the URL of a Checkend instance. HTTPS only, except for
// http://localhost and http://127.0.0.1, because the token goes in each request. The result
// is the origin and the path, without a trailing slash.
func Normalize(input string) (string, error) {
	invalid := clierr.UsageError(fmt.Sprintf("%q is not a valid URL.", input), "Example: https://checkend.example.com")
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
		)
	}
	if host == "" {
		return "", invalid
	}
	if u.User != nil {
		return "", clierr.UsageError("Do not put credentials in the URL.", "Use `ce login` to save the token.")
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
	return origin + strings.TrimRight(u.EscapedPath(), "/"), nil
}
