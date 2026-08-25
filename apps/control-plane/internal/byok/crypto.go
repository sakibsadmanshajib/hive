// Package byok implements tenant BYOK (bring your own key): tenants register
// their own provider credentials, Hive stores them encrypted at rest, never
// logs them, and never echoes them back beyond a last-4 mask.
package byok

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// EnvVarName names the environment variable carrying the base64-encoded
// 32-byte AES-256-GCM key that encrypts tenant provider keys at rest.
const EnvVarName = "HIVE_BYOK_ENC_KEY"

var (
	// ErrNotConfigured is returned when no encryption key is configured.
	// Every operation that would otherwise touch plaintext fails closed on
	// it: nothing is ever written or revealed unencrypted.
	ErrNotConfigured = errors.New("byok: encryption key not configured")

	// ErrInvalidKeyMaterial rejects malformed HIVE_BYOK_ENC_KEY values at
	// construction time so misconfiguration dies at boot, not on first use.
	ErrInvalidKeyMaterial = errors.New("byok: invalid key material")
)

// Cipher is an AES-256-GCM encryptor over tenant key material. A nil *Cipher
// means locked mode: Encrypt and Decrypt return ErrNotConfigured instead of
// ever falling back to plaintext.
type Cipher struct {
	gcm cipher.AEAD
}

// NewCipher validates raw key length (AES-256 needs exactly 32 bytes) and
// returns a ready Cipher.
func NewCipher(raw []byte) (*Cipher, error) {
	if len(raw) != 32 {
		return nil, fmt.Errorf("%w: need 32 bytes, got %d", ErrInvalidKeyMaterial, len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{gcm: gcm}, nil
}

// LoadCipher decodes a base64-encoded 32-byte key. Empty input returns
// ErrNotConfigured so callers can distinguish unset from malformed.
func LoadCipher(encoded string) (*Cipher, error) {
	if encoded == "" {
		return nil, ErrNotConfigured
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: not valid base64", ErrInvalidKeyMaterial)
	}
	return NewCipher(raw)
}

// Encrypt seals plaintext with a fresh random nonce; output layout is
// nonce || ciphertext || tag. Never log the return value.
func (c *Cipher) Encrypt(plaintext string) ([]byte, error) {
	if c == nil || c.gcm == nil {
		return nil, ErrNotConfigured
	}
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("byok: nonce generation failed: %w", err)
	}
	return c.gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt opens a blob produced by Encrypt. Any tampering or wrong key is an
// error; there is no best-effort path.
func (c *Cipher) Decrypt(blob []byte) (string, error) {
	if c == nil || c.gcm == nil {
		return "", ErrNotConfigured
	}
	ns := c.gcm.NonceSize()
	if len(blob) < ns+c.gcm.Overhead() {
		return "", ErrInvalidKeyMaterial
	}
	nonce, sealed := blob[:ns], blob[ns:]
	plain, err := c.gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("byok: decrypt failed: %w", err)
	}
	return string(plain), nil
}

// MaskSecret reduces a credential to a customer-safe display form. Keys of 8
// or more characters show only the final 4 characters; anything shorter is
// fully masked so not even the length band is leaked.
func MaskSecret(secret string) string {
	if len(secret) < 8 {
		return "****"
	}
	return secret[len(secret)-4:]
}
