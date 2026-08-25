package byok

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

// testCipher builds a Cipher from a fresh random 32-byte key.
func testCipher(t *testing.T) *Cipher {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	c, err := NewCipher(raw)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestCipherRoundTrip(t *testing.T) {
	c := testCipher(t)
	plaintext := "fake-key-abcdef0123456789"

	blob, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(blob, []byte(plaintext)) {
		t.Fatal("ciphertext contains plaintext bytes")
	}

	got, err := c.Decrypt(blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("round trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestEncryptUsesRandomNonce(t *testing.T) {
	c := testCipher(t)
	plain := "same-secret"
	a, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt a: %v", err)
	}
	b, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt b: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext produced identical blobs (nonce reuse)")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	c1 := testCipher(t)
	c2 := testCipher(t)

	blob, err := c1.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := c2.Decrypt(blob); err == nil {
		t.Fatal("decrypt with a different key must fail")
	}
}

func TestDecryptTamperedBlobFails(t *testing.T) {
	c := testCipher(t)
	blob, err := c.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	blob[len(blob)-1] ^= 0xFF
	if _, err := c.Decrypt(blob); err == nil {
		t.Fatal("decrypt of a tampered ciphertext must fail")
	}
}

func TestNilCipherFailsClosed(t *testing.T) {
	var c *Cipher
	if _, err := c.Encrypt("secret"); err == nil {
		t.Fatal("Encrypt on a nil cipher must fail closed")
	}
	if _, err := c.Decrypt([]byte{1, 2, 3}); err == nil {
		t.Fatal("Decrypt on a nil cipher must fail closed")
	}
}

func TestLoadCipherFromEnv(t *testing.T) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)

	c, err := LoadCipher(encoded)
	if err != nil {
		t.Fatalf("LoadCipher(valid): %v", err)
	}
	blob, err := c.Encrypt("roundtrip")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if got, err := c.Decrypt(blob); err != nil || got != "roundtrip" {
		t.Fatalf("LoadCipher round trip: got %q err %v", got, err)
	}
}

func TestLoadCipherRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"not base64", "!!!not-base64!!!"},
		{"too short", base64.StdEncoding.EncodeToString(make([]byte, 16))},
		{"too long", base64.StdEncoding.EncodeToString(make([]byte, 64))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadCipher(tc.in); err == nil {
				t.Fatalf("LoadCipher(%q) must reject invalid key material", tc.in)
			}
		})
	}
}

func TestMaskSecret(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"fake-key-abcdef0123456789", "6789"},
		{"short", "****"},
		{"1234", "****"},
		{"12345", "****"},
		{"1234567", "****"},
		{"12345678", "5678"},
		{"", "****"},
	}
	for _, tc := range cases {
		got := MaskSecret(tc.in)
		if got != tc.want {
			t.Errorf("MaskSecret(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMaskSecretNeverRevealsShortKey(t *testing.T) {
	for _, s := range []string{"a", "ab", "abc", "abcd", "abcde"} {
		if got := MaskSecret(s); got != "****" {
			t.Errorf("MaskSecret(%q) = %q, short keys must be fully masked", s, got)
		}
	}
}
