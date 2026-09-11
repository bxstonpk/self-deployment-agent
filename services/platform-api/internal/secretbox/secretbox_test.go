package secretbox_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"

	"platform-api/internal/secretbox"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, secretbox.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func newBox(t *testing.T) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var aad = []byte("application/app-1/secret/API_KEY")

const value = "sk-live-4f9a2c1e8b7d6a5f3e2d1c0b9a8f7e6d"

func TestSealOpen_RoundTrip(t *testing.T) {
	b := newBox(t)
	sealed, err := b.Seal([]byte(value), aad)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Open(sealed, b.KeyID(), aad)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != value {
		t.Fatalf("round trip changed the value: %q", got)
	}
}

// The whole point: what reaches the database is not the value.
func TestSeal_CiphertextNeverContainsPlaintext(t *testing.T) {
	b := newBox(t)
	sealed, err := b.Seal([]byte(value), aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(value)) || bytes.Contains(sealed, []byte(value[:12])) {
		t.Fatal("sealed output contains the plaintext")
	}
}

// Equal secrets must not be detectable by comparing ciphertexts.
func TestSeal_SameValueSealsDifferentlyEachTime(t *testing.T) {
	b := newBox(t)
	first, _ := b.Seal([]byte(value), aad)
	second, _ := b.Seal([]byte(value), aad)
	if bytes.Equal(first, second) {
		t.Fatal("the same value sealed twice produced identical ciphertext")
	}
}

// FR-069 at the crypto layer: a ciphertext moved into another
// application's row does not open there.
func TestOpen_DifferentApplicationFails(t *testing.T) {
	b := newBox(t)
	sealed, _ := b.Seal([]byte(value), aad)
	_, err := b.Open(sealed, b.KeyID(), []byte("application/app-2/secret/API_KEY"))
	if !errors.Is(err, secretbox.ErrTampered) {
		t.Fatalf("expected ErrTampered for a different application, got %v", err)
	}
	_, err = b.Open(sealed, b.KeyID(), []byte("application/app-1/secret/OTHER_KEY"))
	if !errors.Is(err, secretbox.ErrTampered) {
		t.Fatalf("expected ErrTampered for a different secret name, got %v", err)
	}
}

func TestOpen_DifferentKeyIsReportedAsKeyMismatch(t *testing.T) {
	sealer, opener := newBox(t), newBox(t)
	sealed, _ := sealer.Seal([]byte(value), aad)

	_, err := opener.Open(sealed, sealer.KeyID(), aad)
	if !errors.Is(err, secretbox.ErrKeyMismatch) {
		t.Fatalf("expected ErrKeyMismatch, got %v", err)
	}
	// Even if the recorded key id were wrong, the wrong key still can't
	// open it.
	_, err = opener.Open(sealed, opener.KeyID(), aad)
	if !errors.Is(err, secretbox.ErrTampered) {
		t.Fatalf("expected ErrTampered when the key id lies, got %v", err)
	}
}

func TestOpen_TamperedCiphertextFails(t *testing.T) {
	b := newBox(t)
	sealed, _ := b.Seal([]byte(value), aad)
	for i := range sealed {
		altered := bytes.Clone(sealed)
		altered[i] ^= 0x01
		if _, err := b.Open(altered, b.KeyID(), aad); !errors.Is(err, secretbox.ErrTampered) {
			t.Fatalf("flipping byte %d was not detected: %v", i, err)
		}
	}
}

func TestOpen_TruncatedFails(t *testing.T) {
	b := newBox(t)
	sealed, _ := b.Seal([]byte(value), aad)
	for _, n := range []int{0, 5, 12, len(sealed) - 1} {
		if _, err := b.Open(sealed[:n], b.KeyID(), aad); !errors.Is(err, secretbox.ErrTampered) {
			t.Fatalf("truncating to %d bytes was not detected: %v", n, err)
		}
	}
}

func TestNew_RejectsWrongKeySize(t *testing.T) {
	for _, n := range []int{0, 16, 24, 31, 33, 64} {
		if _, err := secretbox.New(make([]byte, n)); !errors.Is(err, secretbox.ErrInvalidKey) {
			t.Fatalf("a %d-byte key was accepted: %v", n, err)
		}
	}
}

func TestFromBase64(t *testing.T) {
	key := newKey(t)
	if _, err := secretbox.FromBase64(base64.StdEncoding.EncodeToString(key) + "\n"); err != nil {
		t.Fatalf("a valid base64 key (with trailing newline) was rejected: %v", err)
	}
	// .env.example ships an empty placeholder; a person might type anything.
	for _, bad := range []string{"", "change-me", base64.StdEncoding.EncodeToString(key[:16])} {
		if _, err := secretbox.FromBase64(bad); !errors.Is(err, secretbox.ErrInvalidKey) {
			t.Fatalf("%q was accepted as a key: %v", bad, err)
		}
	}
}

func TestKeyID_StableAndDistinct(t *testing.T) {
	key := newKey(t)
	a, _ := secretbox.New(key)
	b, _ := secretbox.New(bytes.Clone(key))
	c := newBox(t)
	if a.KeyID() != b.KeyID() {
		t.Fatal("the same key produced different key ids")
	}
	if a.KeyID() == c.KeyID() {
		t.Fatal("different keys produced the same key id")
	}
	if len(a.KeyID()) != 16 {
		t.Fatalf("expected a 16-hex-char fingerprint, got %q", a.KeyID())
	}
}
