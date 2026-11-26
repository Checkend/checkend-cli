package cli

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DefaultURL is the server of a new login without --url: the cloud edition.
const DefaultURL = "https://app.checkend.com"

// Config is what `ce login` stores: the server, the token and the default site.
type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Site  string `json:"site,omitempty"`
}

// configPath is $CE_CONFIG, else $XDG_CONFIG_HOME/checkend/ce.json, else
// ~/.config/checkend/ce.json (also on macOS: a CLI file belongs there).
func configPath(env func(string) string) (string, error) {
	if p := env("CE_CONFIG"); p != "" {
		return p, nil
	}
	if d := env("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "checkend", "ce.json"), nil
	}
	home := env("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	}
	return filepath.Join(home, ".config", "checkend", "ce.json"), nil
}

// loadConfig reads the file (a missing file is an empty config), then lets
// CHECKEND_URL, CHECKEND_TOKEN and CHECKEND_SITE override it.
func loadConfig(env func(string) string) (Config, string, error) {
	p, err := configPath(env)
	if err != nil {
		return Config{}, "", err
	}
	c, err := readConfigFile(p)
	if err != nil {
		return c, p, err
	}
	if v := env("CHECKEND_URL"); v != "" {
		c.URL = v
	}
	if v := env("CHECKEND_TOKEN"); v != "" {
		c.Token = v
	}
	if v := env("CHECKEND_SITE"); v != "" {
		c.Site = v
	}
	c.URL = normURL(c.URL)
	return c, p, nil
}

// readConfigFile reads the saved login only; a missing file is empty.
func readConfigFile(p string) (Config, error) {
	var c Config
	b, err := os.ReadFile(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return c, err
	default:
		if err := json.Unmarshal(b, &c); err != nil {
			return c, errors.New(p + ": " + err.Error())
		}
	}
	return c, nil
}

// saveConfig writes the file with mode 0600: it holds a token.
func saveConfig(p string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// normURL trims the URL and adds https:// when no scheme is given
// (http:// for localhost).
func normURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" || strings.Contains(u, "://") {
		return u
	}
	if strings.HasPrefix(u, "localhost") || strings.HasPrefix(u, "127.0.0.1") {
		return "http://" + u
	}
	return "https://" + u
}
