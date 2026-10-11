package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Checkend/checkend-cli/internal/clierr"
	"github.com/Checkend/checkend-cli/internal/host"
)

// Install is a server that `ce install` installed, by its URL. It has no secrets: they are on
// the server, in its .env file, or in the keychain.
type Install struct {
	// Kind is "docker", "ssh" or "cloudflare".
	Kind        string `json:"kind"`
	Version     string `json:"version"`
	Migrations  string `json:"migrations"`
	InstalledAt string `json:"installed_at"`
	UpgradedAt  string `json:"upgraded_at,omitempty"`
	// Dir is the folder of compose.yaml (docker: on this machine; ssh: on the server).
	Dir string `json:"dir,omitempty"`
	// Target is user@host for ssh, and Port its SSH port.
	Target string `json:"target,omitempty"`
	Port   string `json:"port,omitempty"`
	// HostKey is the SHA256 fingerprint of the SSH host key that the user confirmed.
	HostKey string `json:"host_key,omitempty"`
	// Account, Worker, Database and DatabaseID are the Cloudflare resources.
	Account    string `json:"account,omitempty"`
	Worker     string `json:"worker,omitempty"`
	Database   string `json:"database,omitempty"`
	DatabaseID string `json:"database_id,omitempty"`
}

// InstallsFile is the path of the install records.
func InstallsFile(h *host.Host) string { return filepath.Join(Dir(h), "installs.json") }

// ReadInstalls reads the install records. A missing file is none.
func ReadInstalls(h *host.Host) (map[string]Install, error) {
	installs := map[string]Install{}
	data, err := os.ReadFile(InstallsFile(h))
	if errors.Is(err, os.ErrNotExist) {
		return installs, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &installs)
	}
	if err != nil {
		return nil, clierr.Fail(fmt.Sprintf("Cannot read %s.", InstallsFile(h)))
	}
	return installs, nil
}

// SaveInstall writes the record of url (nil removes it), mode 600.
func SaveInstall(h *host.Host, url string, install *Install) error {
	installs, err := ReadInstalls(h)
	if err != nil {
		return err
	}
	if install == nil {
		delete(installs, url)
	} else {
		installs[url] = *install
	}
	data, err := json.MarshalIndent(installs, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(InstallsFile(h), append(data, '\n'), 0o600)
}

// WriteFile replaces a file atomically with this mode; the folder is made with mode 700.
func WriteFile(file string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".ce-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), file)
}
