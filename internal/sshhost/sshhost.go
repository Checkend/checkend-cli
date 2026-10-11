// Package sshhost checks SSH targets and host keys for `ce install ssh`. ce runs the system
// ssh client (with the SSH agent); this package decides which host key ssh may accept. A host
// key is never skipped: it must be in the user's known_hosts, match --host-key, or be
// confirmed by the user.
package sshhost

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"regexp"
	"strings"
)

// Target is user@host.
type Target struct {
	User string
	Host string
}

var (
	userPattern = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`)
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,252})$`)
)

// ParseTarget reads user@host (default user root). It refuses anything that ssh could read as
// an option or that is not a host name or an IP address.
func ParseTarget(value string) (Target, error) {
	user, host, ok := strings.Cut(value, "@")
	if !ok {
		user, host = "root", value
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
		if ip := net.ParseIP(host); ip == nil || ip.To4() != nil {
			return Target{}, errors.New("not an IPv6 address")
		}
		return checkUser(Target{User: user, Host: host})
	}
	if !hostPattern.MatchString(host) || strings.Contains(host, "..") {
		return Target{}, errors.New("not a host name or an IP address")
	}
	return checkUser(Target{User: user, Host: strings.ToLower(host)})
}

func checkUser(t Target) (Target, error) {
	if !userPattern.MatchString(t.User) {
		return Target{}, errors.New("not a user name")
	}
	return t, nil
}

// String is user@host, with brackets for IPv6.
func (t Target) String() string {
	if strings.Contains(t.Host, ":") {
		return t.User + "@[" + t.Host + "]"
	}
	return t.User + "@" + t.Host
}

// KnownName is the name of the host in known_hosts for this port.
func (t Target) KnownName(port string) string {
	if port == "" || port == "22" {
		return t.Host
	}
	return "[" + t.Host + "]:" + port
}

// Key is a host key: its type and its base64 blob.
type Key struct {
	Type string
	Blob string
}

// Fingerprint is the SHA256 fingerprint, as ssh shows it.
func (k Key) Fingerprint() string {
	raw, err := base64.StdEncoding.DecodeString(k.Blob)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

var keyTypes = map[string]bool{
	"ssh-ed25519": true, "ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true,
	"ecdsa-sha2-nistp521": true, "ssh-rsa": true,
}

func validKey(typ, blob string) bool {
	_, err := base64.StdEncoding.DecodeString(blob)
	return keyTypes[typ] && err == nil
}

// ParseKeyscan reads the output of ssh-keyscan.
func ParseKeyscan(out string) []Key {
	var keys []Key
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && !strings.HasPrefix(f[0], "#") && validKey(f[1], f[2]) {
			keys = append(keys, Key{Type: f[1], Blob: f[2]})
		}
	}
	return keys
}

// Known reports whether a known_hosts file has this key for this name (plain or hashed).
// Revoked and @cert-authority lines never match.
func Known(file, name string, key Key) bool {
	for _, line := range strings.Split(file, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || strings.HasPrefix(f[0], "#") || strings.HasPrefix(f[0], "@") {
			continue
		}
		if f[1] == key.Type && f[2] == key.Blob && hostMatches(f[0], name) {
			return true
		}
	}
	return false
}

func hostMatches(field, name string) bool {
	if rest, ok := strings.CutPrefix(field, "|1|"); ok {
		salt64, hash64, ok := strings.Cut(rest, "|")
		salt, err1 := base64.StdEncoding.DecodeString(salt64)
		hash, err2 := base64.StdEncoding.DecodeString(hash64)
		if !ok || err1 != nil || err2 != nil {
			return false
		}
		mac := hmac.New(sha1.New, salt)
		mac.Write([]byte(name))
		return hmac.Equal(mac.Sum(nil), hash)
	}
	for _, pattern := range strings.Split(field, ",") {
		if strings.EqualFold(pattern, name) {
			return true
		}
	}
	return false
}

// Line is a known_hosts line for this name and key.
func Line(name string, key Key) string { return name + " " + key.Type + " " + key.Blob }
