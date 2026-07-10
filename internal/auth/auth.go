// Package auth mirrors the Ollama client's request-signing scheme so that
// ollama-lite can authenticate to ollama.com using the exact same on-disk key
// (~/.ollama/id_ed25519) that the official Ollama uses. The Authorization
// header it produces is byte-for-byte identical to Ollama's auth.Sign.
package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// connectURLFormat is the ollama.com sign-in page, matching Ollama's signinURL.
const connectURLFormat = "https://ollama.com/connect?name=%s&key=%s"

const defaultPrivateKey = "id_ed25519"

// KeyPath returns the path to the shared Ollama private key (~/.ollama/id_ed25519).
func KeyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ollama", defaultPrivateKey), nil
}

func loadPrivateKey() (ssh.Signer, error) {
	keyPath, err := KeyPath()
	if err != nil {
		return nil, err
	}

	privateKeyFile, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}

	return ssh.ParsePrivateKey(privateKeyFile)
}

// GetPublicKey returns the authorized_keys line ("ssh-ed25519 AAAA...") for the
// shared Ollama key. Matches ollama/auth.GetPublicKey.
func GetPublicKey() (string, error) {
	privateKey, err := loadPrivateKey()
	if err != nil {
		return "", err
	}

	publicKey := ssh.MarshalAuthorizedKey(privateKey.PublicKey())
	return strings.TrimSpace(string(publicKey)), nil
}

// EncodedPublicKey returns base64.RawURLEncoding of the authorized_keys line.
// This is the form used for the ollama.com connect URL and the key-deletion
// (signout) endpoint.
func EncodedPublicKey() (string, error) {
	pub, err := GetPublicKey()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(pub)), nil
}

// SigninURL returns the ollama.com connect URL used to associate this machine's
// public key with an Ollama account. Identical to Ollama's signinURL.
func SigninURL() (string, error) {
	encKey, err := EncodedPublicKey()
	if err != nil {
		return "", err
	}
	hostname, _ := os.Hostname()
	return fmt.Sprintf(connectURLFormat, url.PathEscape(hostname), encKey), nil
}

// Sign signs bts with the shared Ollama key and returns "<pubkeyblob>:<base64(sig)>",
// identical to ollama/auth.Sign. The ctx argument is accepted for signature
// parity with the upstream API but is unused.
func Sign(ctx context.Context, bts []byte) (string, error) {
	privateKey, err := loadPrivateKey()
	if err != nil {
		return "", err
	}

	// public key without the "ssh-ed25519 " type prefix
	publicKey := ssh.MarshalAuthorizedKey(privateKey.PublicKey())
	parts := bytes.Split(publicKey, []byte(" "))
	if len(parts) < 2 {
		return "", errors.New("malformed public key")
	}

	signature, err := privateKey.Sign(rand.Reader, bts)
	if err != nil {
		return "", err
	}

	// signature is <pubkey>:<signature>
	return fmt.Sprintf("%s:%s", bytes.TrimSpace(parts[1]), base64.StdEncoding.EncodeToString(signature.Blob)), nil
}

// EnsureKey generates the shared Ollama Ed25519 keypair at ~/.ollama if it does
// not already exist, using the same on-disk format the official Ollama reads
// and writes. Mirrors ollama/cmd.initializeKeypair.
func EnsureKey() error {
	keyPath, err := KeyPath()
	if err != nil {
		return err
	}
	pubKeyPath := keyPath + ".pub"

	if _, err := os.Stat(keyPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	cryptoPublicKey, cryptoPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	privateKeyBytes, err := ssh.MarshalPrivateKey(cryptoPrivateKey, "")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		return fmt.Errorf("could not create directory: %w", err)
	}

	if err := os.WriteFile(keyPath, pem.EncodeToMemory(privateKeyBytes), 0o600); err != nil {
		return err
	}

	sshPublicKey, err := ssh.NewPublicKey(cryptoPublicKey)
	if err != nil {
		return err
	}

	if err := os.WriteFile(pubKeyPath, ssh.MarshalAuthorizedKey(sshPublicKey), 0o644); err != nil {
		return err
	}

	return nil
}
