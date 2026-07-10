package auth

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestSignRoundTrip generates a key in a temp home, then checks that the
// Authorization header ollama-lite produces has the exact Ollama shape
// ("<pubblob>:<base64 sig>") and that the signature verifies against the public
// key — proving cryptographic and format compatibility with ollama.com.
func TestSignRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)        // Unix
	t.Setenv("USERPROFILE", dir) // Windows (os.UserHomeDir)

	if err := EnsureKey(); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}

	// GetPublicKey must equal the on-disk .pub authorized_keys line.
	pubFile, err := os.ReadFile(filepath.Join(dir, ".ollama", "id_ed25519.pub"))
	if err != nil {
		t.Fatalf("read pub: %v", err)
	}
	wantPub := strings.TrimSpace(string(pubFile))
	gotPub, err := GetPublicKey()
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}
	if gotPub != wantPub {
		t.Fatalf("GetPublicKey = %q, want %q", gotPub, wantPub)
	}

	challenge := []byte("POST,/api/me?ts=1234567890")
	header, err := Sign(context.Background(), challenge)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	blob, sigB64, ok := strings.Cut(header, ":")
	if !ok {
		t.Fatalf("header %q is not <blob>:<sig>", header)
	}

	// The blob must be the middle field of the authorized_keys line.
	fields := strings.Fields(wantPub)
	if len(fields) < 2 || blob != fields[1] {
		t.Fatalf("header blob %q does not match public key blob %q", blob, fields[1])
	}

	// The signature must verify against the public key.
	pk, _, _, _, err := ssh.ParseAuthorizedKey(pubFile)
	if err != nil {
		t.Fatalf("parse authorized key: %v", err)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if err := pk.Verify(challenge, &ssh.Signature{Format: pk.Type(), Blob: sigBytes}); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
}

// TestStripCloudSuffixParity is covered in the server package; here we only
// ensure the shared key path is under ~/.ollama for config compatibility.
func TestKeyPathUnderOllama(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	kp, err := KeyPath()
	if err != nil {
		t.Fatalf("KeyPath: %v", err)
	}
	want := filepath.Join(dir, ".ollama", "id_ed25519")
	if kp != want {
		t.Fatalf("KeyPath = %q, want %q", kp, want)
	}
}
