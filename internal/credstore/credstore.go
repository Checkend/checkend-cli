// Package credstore keeps tokens in the OS keychain: the macOS Keychain, the Windows
// Credential Manager or the Secret Service on Linux.
package credstore

import (
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

// Service is the keychain service name of all Checkend tokens. The account is the instance URL.
const Service = "checkend"

// ErrNotFound is returned when the keychain has no token for the account.
var ErrNotFound = errors.New("not found in the keychain")

// Keyring is the OS keychain. Tests use Memory.
type Keyring interface {
	Get(account string) (string, error)
	Set(account, secret string) error
	Delete(account string) error
}

// System is the keychain of the operating system.
type System struct{}

func (System) Get(account string) (string, error) {
	secret, err := keyring.Get(Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return secret, err
}

func (System) Set(account, secret string) error { return keyring.Set(Service, account, secret) }

func (System) Delete(account string) error {
	err := keyring.Delete(Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// Memory is a keychain in memory, for tests. With Broken set, every call fails, as on a Linux
// server without a Secret Service.
type Memory struct {
	mu      sync.Mutex
	Secrets map[string]string
	Broken  bool
}

var errBroken = errors.New("the keychain is not available")

func (m *Memory) Get(account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Broken {
		return "", errBroken
	}
	secret, ok := m.Secrets[account]
	if !ok {
		return "", ErrNotFound
	}
	return secret, nil
}

func (m *Memory) Set(account, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Broken {
		return errBroken
	}
	if m.Secrets == nil {
		m.Secrets = map[string]string{}
	}
	m.Secrets[account] = secret
	return nil
}

func (m *Memory) Delete(account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Broken {
		return errBroken
	}
	if _, ok := m.Secrets[account]; !ok {
		return ErrNotFound
	}
	delete(m.Secrets, account)
	return nil
}
