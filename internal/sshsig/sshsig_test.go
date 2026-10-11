package sshsig

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const ns = "checkend-server-release"

func newKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, AuthorizedKey(pub)
}

func TestSignAndVerify(t *testing.T) {
	priv, pub := newKey(t)
	msg := []byte(`{"format":1}`)
	sig := Sign(priv, msg, ns)
	if !strings.HasPrefix(string(sig), "-----BEGIN SSH SIGNATURE-----\n") {
		t.Fatalf("not armored: %s", sig)
	}
	id, err := Verify(sig, msg, ns, map[string]string{"release-1": pub})
	if err != nil || id != "release-1" {
		t.Fatalf("Verify = %q, %v", id, err)
	}
}

func TestVerifyFailsClosed(t *testing.T) {
	priv, pub := newKey(t)
	other, _ := newKey(t)
	msg := []byte(`{"format":1}`)
	keys := map[string]string{"release-1": pub}
	good := Sign(priv, msg, ns)
	for name, c := range map[string]struct {
		sig  []byte
		msg  []byte
		keys map[string]string
		part string
	}{
		"changed message": {good, []byte(`{"format":2}`), keys, "not valid"},
		"other namespace": {Sign(priv, msg, "checkend-cli-release"), msg, keys, "namespace"},
		"unknown key":     {Sign(other, msg, ns), msg, keys, "unknown key SHA256:"},
		"no trusted keys": {good, msg, nil, "unknown key"},
		"not armored":     {[]byte("garbage"), msg, keys, "not an SSH signature"},
		"empty":           {nil, msg, keys, "not an SSH signature"},
		"bad base64":      {[]byte("-----BEGIN SSH SIGNATURE-----\n!!!\n-----END SSH SIGNATURE-----\n"), msg, keys, "not an SSH signature"},
		"truncated":       {armor([]byte("SSHSIG\x00\x00\x00\x01")), msg, keys, "not an SSH signature"},
		"bad trusted key": {good, msg, map[string]string{"x": "ssh-rsa AAAA"}, "unknown key"},
		"wrong magic":     {armor([]byte("SSHSIX\x00\x00\x00\x01")), msg, keys, "not an SSH signature"},
	} {
		_, err := Verify(c.sig, c.msg, ns, c.keys)
		if err == nil || !strings.Contains(err.Error(), c.part) {
			t.Errorf("%s: %v (want %q)", name, err, c.part)
		}
	}
}

// The format is the one of OpenSSH: ssh-keygen -Y verifies our signatures, and we verify its.
func TestInteroperatesWithSSHKeygen(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("no ssh-keygen")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "release")
	if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-C", "test-release", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	msg := filepath.Join(dir, "checkend-release.json")
	if err := os.WriteFile(msg, []byte(`{"format":1,"version":"0.3.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(keygen, "-Y", "sign", "-f", key, "-n", ns, msg).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	pubLine, _ := os.ReadFile(key + ".pub")
	fields := strings.Fields(string(pubLine))
	trusted := map[string]string{"release-1": fields[0] + " " + fields[1]}
	sig, _ := os.ReadFile(msg + ".sig")
	data, _ := os.ReadFile(msg)
	if id, err := Verify(sig, data, ns, trusted); err != nil || id != "release-1" {
		t.Fatalf("a signature of ssh-keygen: %q, %v", id, err)
	}

	priv, pub := newKey(t)
	ours := filepath.Join(dir, "ours.sig")
	if err := os.WriteFile(ours, Sign(priv, data, ns), 0o600); err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(dir, "allowed")
	if err := os.WriteFile(allowed, []byte("release-1 "+pub+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(keygen, "-Y", "verify", "-f", allowed, "-I", "release-1", "-n", ns, "-s", ours)
	cmd.Stdin = strings.NewReader(string(data))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen refused our signature: %v %s", err, out)
	}
}

// withExtra decodes an armored signature, changes its blob, and armors it again.
func withExtra(t *testing.T, armored []byte, change func(blob []byte) []byte) []byte {
	t.Helper()
	text := strings.TrimSpace(string(armored))
	text = strings.TrimSuffix(strings.TrimPrefix(text, beginLine), endLine)
	blob, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
	if err != nil {
		t.Fatal(err)
	}
	return armor(change(blob))
}

func TestVerifyRefusesTrailingBytes(t *testing.T) {
	priv, pub := newKey(t)
	msg := []byte(`{"format":1}`)
	keys := map[string]string{"release-1": pub}
	good := Sign(priv, msg, ns)
	// After the last field of the outer blob.
	outer := withExtra(t, good, func(b []byte) []byte { return append(b, 0) })
	// Inside the signature blob, after the signature: grow its length field and append a byte.
	inner := withExtra(t, good, func(b []byte) []byte {
		// The signature blob is the last string: its length is the 4 bytes before it.
		sigLen := binary.BigEndian.Uint32(b[len(b)-4-83 : len(b)-83])
		if sigLen != 83 {
			t.Fatalf("signature blob length %d", sigLen)
		}
		out := append([]byte(nil), b[:len(b)-4-83]...)
		out = binary.BigEndian.AppendUint32(out, 84)
		return append(append(out, b[len(b)-83:]...), 0)
	})
	for name, sig := range map[string][]byte{"outer": outer, "signature blob": inner} {
		if _, err := Verify(sig, msg, ns, keys); err == nil || !strings.Contains(err.Error(), "not an SSH signature") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Verify(good, msg, ns, keys); err != nil {
		t.Fatal(err)
	}
}
