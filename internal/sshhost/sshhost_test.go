package sshhost

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"strings"
	"testing"
)

// blob is an ed25519 public key blob with a test key.
var blob = base64.StdEncoding.EncodeToString(append([]byte("\x00\x00\x00\x0bssh-ed25519\x00\x00\x00\x20"), []byte("test-key-0123456789abcdef0123456")...))

func TestParseTarget(t *testing.T) {
	for in, want := range map[string]Target{
		"root@203.0.113.7":          {User: "root", Host: "203.0.113.7"},
		"deploy@errors.example.com": {User: "deploy", Host: "errors.example.com"},
		"ubuntu@[2001:db8::1]":      {User: "ubuntu", Host: "2001:db8::1"},
		"errors.example.com":        {User: "root", Host: "errors.example.com"},
	} {
		got, err := ParseTarget(in)
		if err != nil || got != want {
			t.Errorf("ParseTarget(%q) = %+v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "-oProxyCommand=x", "root@-oProxyCommand=x", "root@host;rm", "root@ho st", "a@b@c", "root@", "Root Name@host", "root@host:22", "@host"} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("ParseTarget(%q): no error", bad)
		}
	}
}

func TestKnownHostsName(t *testing.T) {
	if got := (Target{Host: "h.example"}).KnownName("22"); got != "h.example" {
		t.Error(got)
	}
	if got := (Target{Host: "h.example"}).KnownName("2222"); got != "[h.example]:2222" {
		t.Error(got)
	}
}

func TestParseKeyscanAndFingerprint(t *testing.T) {
	keys := ParseKeyscan("# h.example:22 SSH-2.0-OpenSSH\nh.example ssh-ed25519 " + blob + "\nh.example bad-line\n")
	if len(keys) != 1 || keys[0].Type != "ssh-ed25519" {
		t.Fatalf("%+v", keys)
	}
	if fp := keys[0].Fingerprint(); !strings.HasPrefix(fp, "SHA256:") || strings.HasSuffix(fp, "=") {
		t.Errorf("fingerprint %q", fp)
	}
}

func TestKnownMatchesPlainAndHashedEntries(t *testing.T) {
	key := Key{Type: "ssh-ed25519", Blob: blob}
	salt := []byte("0123456789abcdefghij")
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte("[h.example]:2222"))
	hashed := "|1|" + base64.StdEncoding.EncodeToString(salt) + "|" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	file := strings.Join([]string{
		"# comment",
		"other.example,h.example ssh-ed25519 " + blob,
		hashed + " ssh-ed25519 " + blob,
		"@revoked h.example ssh-rsa AAAA",
	}, "\n")
	if !Known(file, "h.example", key) {
		t.Error("plain entry not found")
	}
	if !Known(file, "[h.example]:2222", key) {
		t.Error("hashed entry not found")
	}
	if Known(file, "x.example", key) {
		t.Error("wrong host matched")
	}
	if Known(file, "h.example", Key{Type: "ssh-ed25519", Blob: "AAAAother"}) {
		t.Error("wrong key matched")
	}
	if Known("@revoked h.example ssh-ed25519 "+blob, "h.example", key) {
		t.Error("a revoked key matched")
	}
}
