// Package config resolves the configuration, strongest first: flags, environment,
// .checkend.json (project link only), then the user config from `ce login`.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/credstore"
	"github.com/Checkend/checkend-cli/internal/host"
	"github.com/Checkend/checkend-cli/internal/urlcheck"
)

// LinkFile links a repo to a project. It has the project only, never a token.
const LinkFile = ".checkend.json"

// StorageKeychain marks a host whose token is in the OS keychain.
const StorageKeychain = "keychain"

// HostEntry is one instance in the user config. The token is in the keychain (Storage is
// "keychain"), or in the file when the keychain is not available.
type HostEntry struct {
	Token   string `json:"token,omitempty"`
	Storage string `json:"storage,omitempty"`
}

// UserConfig is ~/.config/checkend/config.json.
type UserConfig struct {
	Current string               `json:"current,omitempty"`
	Hosts   map[string]HostEntry `json:"hosts"`
}

// Dir is $XDG_CONFIG_HOME/checkend, else ~/.config/checkend.
func Dir(h *host.Host) string {
	if xdg := h.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "checkend")
	}
	return filepath.Join(h.Home, ".config", "checkend")
}

// File is the path of the user config.
func File(h *host.Host) string { return filepath.Join(Dir(h), "config.json") }

// ReadUserConfig reads the user config. A missing file is an empty config.
func ReadUserConfig(h *host.Host) (UserConfig, error) {
	cfg := UserConfig{Hosts: map[string]HostEntry{}}
	data, err := os.ReadFile(File(h))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &cfg)
	}
	if err != nil {
		return cfg, clierr.New(clierr.Failure, fmt.Sprintf("Cannot read %s.", File(h)), "Run `ce login` to write it again.")
	}
	if cfg.Hosts == nil {
		cfg.Hosts = map[string]HostEntry{}
	}
	return cfg, nil
}

// WriteUserConfig writes the user config: directory mode 700, file mode 600, atomic replace.
func WriteUserConfig(h *host.Host, cfg UserConfig) error {
	dir := Dir(h)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), File(h))
}

func useKeychain(h *host.Host) bool {
	return h.Keyring != nil && h.Getenv("CHECKEND_NO_KEYCHAIN") == ""
}

// SaveToken saves the token for url and makes url the current instance. It returns
// StorageKeychain, or the path of the file when the keychain is not available.
func SaveToken(h *host.Host, url, token string) (string, error) {
	cfg, err := ReadUserConfig(h)
	if err != nil {
		return "", err
	}
	entry := HostEntry{Token: token}
	where := File(h)
	if useKeychain(h) && h.Keyring.Set(url, token) == nil {
		entry = HostEntry{Storage: StorageKeychain}
		where = StorageKeychain
	}
	cfg.Current = url
	cfg.Hosts[url] = entry
	return where, WriteUserConfig(h, cfg)
}

// DeleteToken removes the saved token of url. It returns false when there was none.
func DeleteToken(h *host.Host, url string) (bool, error) {
	cfg, err := ReadUserConfig(h)
	if err != nil {
		return false, err
	}
	entry, ok := cfg.Hosts[url]
	if !ok {
		return false, nil
	}
	if entry.Storage == StorageKeychain && h.Keyring != nil {
		if err := h.Keyring.Delete(url); err != nil && !errors.Is(err, credstore.ErrNotFound) {
			return false, clierr.Fail(fmt.Sprintf("Cannot remove the token from the keychain: %v", err))
		}
	}
	delete(cfg.Hosts, url)
	if cfg.Current == url {
		cfg.Current = ""
	}
	return true, WriteUserConfig(h, cfg)
}

// ResolveURL is --url, then CHECKEND_URL, then the instance of `ce login`. "" when none.
func ResolveURL(h *host.Host, flag string) (string, error) {
	value := flag
	if value == "" {
		value = h.Getenv("CHECKEND_URL")
	}
	if value == "" {
		cfg, err := ReadUserConfig(h)
		if err != nil {
			return "", err
		}
		value = cfg.Current
	}
	if value == "" {
		return "", nil
	}
	return urlcheck.Normalize(value)
}

// ResolveToken is --token, then CHECKEND_TOKEN or CHECKEND_ADMIN_TOKEN, then the token saved
// for this exact URL. A saved token never goes to another instance. "" when none.
func ResolveToken(h *host.Host, flag, url string) (string, error) {
	for _, value := range []string{flag, h.Getenv("CHECKEND_TOKEN"), h.Getenv("CHECKEND_ADMIN_TOKEN")} {
		if value != "" {
			return value, nil
		}
	}
	cfg, err := ReadUserConfig(h)
	if err != nil {
		return "", err
	}
	entry := cfg.Hosts[url]
	if entry.Storage != StorageKeychain {
		return entry.Token, nil
	}
	if h.Keyring == nil {
		return "", nil
	}
	token, err := h.Keyring.Get(url)
	if errors.Is(err, credstore.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", clierr.New(clierr.Auth, fmt.Sprintf("Cannot read the token from the keychain: %v", err), fmt.Sprintf("Run `ce login --url %s`.", url))
	}
	return token, nil
}

// HasSavedHosts reports whether `ce login` saved any instance.
func HasSavedHosts(h *host.Host) bool {
	cfg, err := ReadUserConfig(h)
	return err == nil && len(cfg.Hosts) > 0
}

// Link is the project link of a repo.
type Link struct {
	File    string
	Project string
	Slug    string
}

// FindLink finds .checkend.json in the current directory or a parent, up to the git repo root.
func FindLink(h *host.Host) (*Link, error) {
	dir := h.Cwd
	for {
		file := filepath.Join(dir, LinkFile)
		if exists(file) {
			return readLink(file)
		}
		if exists(filepath.Join(dir, ".git")) {
			return nil, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

func readLink(file string) (*Link, error) {
	data, err := os.ReadFile(file)
	var value map[string]any
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		err = dec.Decode(&value)
	}
	if err != nil {
		return nil, clierr.Fail(fmt.Sprintf("Cannot read %s: it is not valid JSON.", file))
	}
	link := &Link{File: file}
	if slug, ok := value["slug"].(string); ok {
		link.Slug = slug
	}
	switch project := value["project"].(type) {
	case nil:
		return nil, nil
	case string:
		link.Project = project
	case json.Number:
		link.Project = project.String()
	default:
		return nil, clierr.Fail(fmt.Sprintf(`Cannot read %s: "project" must be a project ID or slug.`, file))
	}
	return link, nil
}

// FindRepoRoot finds the git repo root, or "" outside a repo.
func FindRepoRoot(start string) string {
	dir := start
	for {
		if exists(filepath.Join(dir, ".git")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
