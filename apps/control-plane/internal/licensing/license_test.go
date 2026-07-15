package licensing_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/licensing"
)

func TestVerify_ValidSignatureAndNotExpired(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	payload := licensing.LicensePayload{
		Tier:      "enterprise",
		Seats:     50,
		IssuedAt:  now.AddDate(0, -1, 0),
		ExpiresAt: now.AddDate(1, 0, 0),
	}
	fileBytes, err := licensing.Sign(payload, priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	e, err := licensing.Verify(fileBytes, base64.StdEncoding.EncodeToString(pub), now)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !e.Valid {
		t.Fatalf("expected valid entitlement, got invalid reason=%q", e.Reason)
	}
	if e.Tier != "enterprise" || e.Seats != 50 {
		t.Fatalf("unexpected entitlement: %+v", e)
	}
}

func TestVerify_ExpiredLicenseStillParsesButInvalid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	payload := licensing.LicensePayload{
		Tier:      "starter",
		Seats:     5,
		IssuedAt:  now.AddDate(-2, 0, 0),
		ExpiresAt: now.AddDate(-1, 0, 0), // expired one year ago
	}
	fileBytes, err := licensing.Sign(payload, priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	e, err := licensing.Verify(fileBytes, base64.StdEncoding.EncodeToString(pub), now)
	if err != nil {
		t.Fatalf("verify should not error on expiry, got: %v", err)
	}
	if e.Valid {
		t.Fatalf("expected expired entitlement to be invalid")
	}
	if e.Reason != "expired" {
		t.Fatalf("expected reason=expired, got %q", e.Reason)
	}
	// Duration and tier stay readable even though the license is expired.
	if e.Tier != "starter" || e.Seats != 5 {
		t.Fatalf("expired entitlement lost its data: %+v", e)
	}
}

func TestVerify_TamperedPayloadFailsSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Now()
	payload := licensing.LicensePayload{Tier: "enterprise", Seats: 10, IssuedAt: now, ExpiresAt: now.AddDate(1, 0, 0)}
	fileBytes, err := licensing.Sign(payload, priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	var env map[string]string
	if err := json.Unmarshal(fileBytes, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	rawPayload, err := base64.StdEncoding.DecodeString(env["payload"])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var tampered licensing.LicensePayload
	if err := json.Unmarshal(rawPayload, &tampered); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	tampered.Seats = 999999
	tamperedBytes, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("marshal tampered payload: %v", err)
	}
	env["payload"] = base64.StdEncoding.EncodeToString(tamperedBytes)
	tamperedFile, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal tampered envelope: %v", err)
	}

	_, err = licensing.Verify(tamperedFile, base64.StdEncoding.EncodeToString(pub), now)
	if err == nil {
		t.Fatalf("expected signature verification to fail on tampered payload")
	}
	if !errors.Is(err, licensing.ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature, got %v", err)
	}
}

func TestVerify_WrongPublicKeyFailsSignature(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	otherPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}
	now := time.Now()
	payload := licensing.LicensePayload{Tier: "enterprise", Seats: 10, IssuedAt: now, ExpiresAt: now.AddDate(1, 0, 0)}
	fileBytes, err := licensing.Sign(payload, priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	_, err = licensing.Verify(fileBytes, base64.StdEncoding.EncodeToString(otherPub), now)
	if !errors.Is(err, licensing.ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature for wrong public key, got %v", err)
	}
}

func TestVerify_MalformedFileReturnsError(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	_, err = licensing.Verify([]byte("not json at all"), base64.StdEncoding.EncodeToString(pub), time.Now())
	if !errors.Is(err, licensing.ErrMalformed) {
		t.Fatalf("expected ErrMalformed, got %v", err)
	}
}

func TestVerify_InvalidPublicKeyEncodingReturnsError(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Now()
	payload := licensing.LicensePayload{Tier: "enterprise", Seats: 1, IssuedAt: now, ExpiresAt: now.AddDate(1, 0, 0)}
	fileBytes, err := licensing.Sign(payload, priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := licensing.Verify(fileBytes, "not-base64!!", now); err == nil {
		t.Fatalf("expected error for malformed public key encoding")
	}
}
