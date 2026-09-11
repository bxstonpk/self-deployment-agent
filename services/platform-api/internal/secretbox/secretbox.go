// Package secretbox is the encryption underneath Module O (Secret
// Management, docs/02_Functional_Requirements.md FR-066): every stored
// secret — an employee's API key, or a database password the platform
// generated for Module N — is sealed with AES-256-GCM before it reaches the
// database, and opened only in memory at the moment a container is started
// with it (FR-067).
//
// This is a stand-in, the same way internal/runtimeengine stands in for
// K3s. DEC-006 (docs/17_Decision_Log.md) — which secret-management backend
// the platform uses — is still Open, and choosing between Vault, a cloud
// secrets manager and Kubernetes-native secrets is not this package's call.
// What it does guarantee is the property SEC-SECRET-1 and FR-066 require
// whatever the backend turns out to be: read access to the platform's
// database alone no longer yields a single plaintext secret. The key lives
// only in the platform-api process's environment (PLATFORM_SECRET_KEY),
// never in the database it protects.
//
// What it does NOT solve, stated so nobody mistakes it for a KMS:
//   - The key is one static value from an environment variable. There is no
//     key rotation and no re-encryption tool; changing the key makes every
//     stored secret unreadable — which fails closed (see Open), but is still
//     an outage for every application that has one.
//   - Anyone who can read platform-api's environment (`docker inspect` on
//     its container, for instance) can read the key, and with it every
//     secret. Taking that off the host is exactly what a real backend chosen
//     under DEC-006 would do.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeySize is AES-256's key length.
const KeySize = 32

// keyIDDomain separates the key fingerprint from any other hash of the same
// key someone might one day compute.
const keyIDDomain = "platform-api/secretbox/key-id/v1\x00"

var (
	ErrInvalidKey = errors.New("secret key must be exactly 32 bytes, base64-encoded (for example the output of `openssl rand -base64 32`)")
	// ErrKeyMismatch means the value was sealed under a different key than
	// the one the platform is running with — almost always a changed
	// PLATFORM_SECRET_KEY. Reported as such, rather than as corruption, so
	// the fix is obvious.
	ErrKeyMismatch = errors.New("secret was sealed under a different platform key")
	// ErrTampered covers everything else that fails authentication: altered
	// bytes, truncation, or a ciphertext that belongs to a different
	// application or name (see the associated data in Seal/Open).
	ErrTampered = errors.New("secret failed authentication: it was altered, or it belongs to a different application or name")
)

type Box struct {
	aead  cipher.AEAD
	keyID string
}

func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w (got %d bytes)", ErrInvalidKey, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init GCM: %w", err)
	}
	// A fingerprint, not the key: 8 bytes of a domain-separated SHA-256 of
	// 32 random bytes reveals nothing usable, and lets every stored row say
	// which key sealed it.
	fingerprint := sha256.Sum256(append([]byte(keyIDDomain), key...))
	return &Box{aead: aead, keyID: hex.EncodeToString(fingerprint[:8])}, nil
}

func FromBase64(encoded string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("%w (not valid base64: %v)", ErrInvalidKey, err)
	}
	return New(key)
}

// KeyID identifies the key this Box seals with. Safe to log and to store.
func (b *Box) KeyID() string {
	return b.keyID
}

// Seal encrypts plaintext and returns nonce || ciphertext. aad is bound
// into the authentication tag without being stored: Open fails unless it is
// given the same aad, which is how a ciphertext is tied to the one
// application and name it was sealed for.
//
// Nonces are random (96 bits). That is safe for far more values than this
// platform will ever hold under one key.
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize(), b.aead.NonceSize()+len(plaintext)+b.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

// Open decrypts a value Seal produced. keyID is the one recorded alongside
// the ciphertext; a mismatch is reported as ErrKeyMismatch before any
// decryption is attempted.
func (b *Box) Open(sealed []byte, keyID string, aad []byte) ([]byte, error) {
	if keyID != b.keyID {
		return nil, fmt.Errorf("%w (sealed under key %s, platform is running with key %s)", ErrKeyMismatch, keyID, b.keyID)
	}
	nonceSize := b.aead.NonceSize()
	if len(sealed) < nonceSize+b.aead.Overhead() {
		return nil, ErrTampered
	}
	plaintext, err := b.aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], aad)
	if err != nil {
		return nil, ErrTampered
	}
	return plaintext, nil
}
