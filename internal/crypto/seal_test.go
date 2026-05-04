package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	s, err := NewSealer(newKey(t))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	cases := []string{"", "hello", strings.Repeat("x", 4096), "👁️ unicode \x00 with NUL"}
	for _, plaintext := range cases {
		sealed, err := s.Seal(plaintext)
		if err != nil {
			t.Fatalf("Seal(%q): %v", plaintext, err)
		}
		got, err := s.Open(sealed)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got != plaintext {
			t.Fatalf("round trip mismatch: got %q want %q", got, plaintext)
		}
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	s, err := NewSealer(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Seal("same")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Seal("same")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("expected distinct ciphertexts for the same plaintext")
	}
}

func TestOpenTamperedCiphertext(t *testing.T) {
	s, err := NewSealer(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Seal("payload")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatal(err)
	}
	// Flip one bit deep enough to land in the actual ciphertext, not the nonce.
	raw[len(raw)-1] ^= 0x01
	tampered := base64.StdEncoding.EncodeToString(raw)
	if _, err := s.Open(tampered); err == nil {
		t.Fatal("expected Open to fail on tampered ciphertext")
	}
}

func TestOpenWithWrongKey(t *testing.T) {
	a, err := NewSealer(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSealer(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.Seal("secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("expected Open with the wrong key to fail")
	}
}

func TestOpenRejectsTooShort(t *testing.T) {
	s, err := NewSealer(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(base64.StdEncoding.EncodeToString([]byte{1, 2, 3})); err == nil {
		t.Fatal("expected too-short ciphertext to fail")
	}
}

func TestNewSealerRejectsBadKeyLength(t *testing.T) {
	for _, n := range []int{0, 1, 16, 31, 33, 64} {
		key := make([]byte, n)
		if _, err := NewSealer(key); err == nil {
			t.Fatalf("expected error for key length %d", n)
		}
	}
}

func TestSealReturnsValidBase64(t *testing.T) {
	s, err := NewSealer(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Seal("x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base64.StdEncoding.DecodeString(sealed); err != nil {
		t.Fatalf("sealed value is not valid base64: %v", err)
	}
	// Sanity: must be longer than the nonce alone (12 bytes).
	raw, _ := base64.StdEncoding.DecodeString(sealed)
	if len(raw) <= 12 {
		t.Fatalf("ciphertext suspiciously short: %d", len(raw))
	}
	if bytes.Equal(raw[:12], make([]byte, 12)) {
		t.Fatal("nonce must not be all zeros")
	}
}
