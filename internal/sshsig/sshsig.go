// Package sshsig verifies SSH signatures (the SSHSIG format of OpenSSH, `ssh-keygen -Y sign`)
// made with ed25519 keys. Checkend signs its release files this way, so that people can also
// check them by hand with `ssh-keygen -Y verify`. See PROTOCOL.sshsig of OpenSSH.
package sshsig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"strings"
)

const (
	magic     = "SSHSIG"
	version   = 1
	keyType   = "ssh-ed25519"
	beginLine = "-----BEGIN SSH SIGNATURE-----"
	endLine   = "-----END SSH SIGNATURE-----"
)

var errFormat = errors.New("the signature file is not an SSH signature")

// ErrUnknownKey is the error of a valid-looking signature with a key that is not trusted.
var ErrUnknownKey = errors.New("unknown key")

// AuthorizedKey is the public key as one line of authorized_keys: "ssh-ed25519 AAAA…".
func AuthorizedKey(pub ed25519.PublicKey) string {
	return keyType + " " + base64.StdEncoding.EncodeToString(keyBlob(pub))
}

// Fingerprint is the SHA256 fingerprint of a key blob, as ssh shows it.
func fingerprint(blob []byte) string {
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func keyBlob(pub ed25519.PublicKey) []byte {
	var b bytes.Buffer
	putString(&b, []byte(keyType))
	putString(&b, pub)
	return b.Bytes()
}

func putString(b *bytes.Buffer, s []byte) {
	_ = binary.Write(b, binary.BigEndian, uint32(len(s)))
	b.Write(s)
}

// reader reads the SSH wire format; any error makes every later read fail.
type reader struct {
	data []byte
	err  bool
}

func (r *reader) string() []byte {
	if r.err || len(r.data) < 4 {
		r.err = true
		return nil
	}
	n := binary.BigEndian.Uint32(r.data)
	if uint64(n) > uint64(len(r.data)-4) {
		r.err = true
		return nil
	}
	s := r.data[4 : 4+n]
	r.data = r.data[4+n:]
	return s
}

func signedData(namespace, hashAlg string, digest []byte) []byte {
	var b bytes.Buffer
	b.WriteString(magic)
	putString(&b, []byte(namespace))
	putString(&b, nil)
	putString(&b, []byte(hashAlg))
	putString(&b, digest)
	return b.Bytes()
}

// Verify checks an armored SSH signature of message for this namespace. The key in the
// signature must be one of trusted (key ID → "ssh-ed25519 AAAA…"); Verify returns its key ID.
func Verify(armored, message []byte, namespace string, trusted map[string]string) (string, error) {
	text := strings.TrimSpace(string(armored))
	body, ok := strings.CutPrefix(text, beginLine)
	if body, ok2 := strings.CutSuffix(body, endLine); ok && ok2 {
		text = body
	} else {
		return "", errFormat
	}
	blob, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
	if err != nil || len(blob) < len(magic)+4 || string(blob[:len(magic)]) != magic {
		return "", errFormat
	}
	r := &reader{data: blob[len(magic)+4:]}
	if binary.BigEndian.Uint32(blob[len(magic):]) != version {
		return "", errFormat
	}
	pubBlob := r.string()
	ns := r.string()
	_ = r.string() // reserved
	hashAlg := r.string()
	sigBlob := r.string()
	// Nothing may follow the last field, here and in the key and signature blobs.
	if r.err || len(r.data) > 0 {
		return "", errFormat
	}
	if string(ns) != namespace {
		return "", fmt.Errorf("the signature is for namespace %q, not %q", ns, namespace)
	}
	pr := &reader{data: pubBlob}
	if string(pr.string()) != keyType {
		return "", errors.New("the signature is not made with an ed25519 key")
	}
	pub := pr.string()
	if pr.err || len(pr.data) > 0 || len(pub) != ed25519.PublicKeySize {
		return "", errFormat
	}
	id := ""
	for keyID, line := range trusted {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == keyType {
			if b, err := base64.StdEncoding.DecodeString(f[1]); err == nil && bytes.Equal(b, pubBlob) {
				id = keyID
			}
		}
	}
	if id == "" {
		return "", fmt.Errorf("%w %s", ErrUnknownKey, fingerprint(pubBlob))
	}
	var h hash.Hash
	switch string(hashAlg) {
	case "sha512":
		h = sha512.New()
	case "sha256":
		h = sha256.New()
	default:
		return "", fmt.Errorf("the signature uses an unknown hash %q", hashAlg)
	}
	h.Write(message)
	sr := &reader{data: sigBlob}
	sigType, sig := sr.string(), sr.string()
	if sr.err || len(sr.data) > 0 {
		return "", errFormat
	}
	if string(sigType) != keyType || !ed25519.Verify(ed25519.PublicKey(pub), signedData(namespace, string(hashAlg), h.Sum(nil)), sig) {
		return "", errors.New("the signature is not valid for this file")
	}
	return id, nil
}

func armor(blob []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(blob)
	var b strings.Builder
	b.WriteString(beginLine + "\n")
	for len(enc) > 70 {
		b.WriteString(enc[:70] + "\n")
		enc = enc[70:]
	}
	b.WriteString(enc + "\n" + endLine + "\n")
	return []byte(b.String())
}

// Sign makes an armored SSH signature (sha512), as `ssh-keygen -Y sign` does. For tests and
// tools; the release workflow signs with ssh-keygen.
func Sign(priv ed25519.PrivateKey, message []byte, namespace string) []byte {
	digest := sha512.Sum512(message)
	sig := ed25519.Sign(priv, signedData(namespace, "sha512", digest[:]))
	var sigBlob bytes.Buffer
	putString(&sigBlob, []byte(keyType))
	putString(&sigBlob, sig)
	var b bytes.Buffer
	b.WriteString(magic)
	_ = binary.Write(&b, binary.BigEndian, uint32(version))
	putString(&b, keyBlob(priv.Public().(ed25519.PublicKey)))
	putString(&b, []byte(namespace))
	putString(&b, nil)
	putString(&b, []byte("sha512"))
	putString(&b, sigBlob.Bytes())
	return armor(b.Bytes())
}
