// Package licensing is the licensing and entitlement seam (issue #304, D9).
//
// Scope, per the owner decision recorded on issue #304: a license carries
// duration (issued/expiry), a tier label, and a seat or user count -- and
// nothing else. It never gates a feature directly. Feature gates (#238,
// apps/control-plane/internal/featuregate) remain the sole mechanism for
// turning capabilities on or off, fully admin controlled, independent of
// what the license says.
//
// The license itself is an offline signed file, verified locally on a
// schedule, with no phone-home -- the same pattern NVIDIA uses for its
// Delegated License Server in on-prem AI Enterprise deployments. This fits
// the sovereignty story: a Hive Enterprise box never has to call home to
// stay licensed.
//
// Tier-based restriction of which feature-gate keys an admin may enable is
// designed but deliberately NOT enforced here (owner decision, issue #304
// comment, 2026-07-07): the license service keeps tier as a plain queryable
// attribute so that future work is additive. GateKeyPolicy below is the
// unenforced extension point for that future predicate; nothing in this
// codebase calls it yet.
package licensing

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// LicensePayload is everything a license grants: a duration (IssuedAt to
// ExpiresAt), a tier label, and a seat/user count. Deliberately minimal --
// per D9, adding a field here that any handler reads to flip a capability
// would silently reopen the license/feature-gate coupling the owner
// rejected.
type LicensePayload struct {
	Tier      string    `json:"tier"`
	Seats     int       `json:"seats"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// envelope is the on-disk license file shape: the payload plus a detached
// Ed25519 signature. Payload is carried as base64 of its exact marshaled
// bytes (the DSSE/in-toto pattern), not embedded raw JSON, so verification
// never depends on re-encoding or whitespace matching the original bytes.
type envelope struct {
	Payload   string `json:"payload"`   // base64 standard encoding of the LicensePayload JSON bytes
	Signature string `json:"signature"` // base64 standard encoding of the Ed25519 signature over those bytes
}

// Entitlement is the parsed, signature-verified, time-checked state a
// caller reads. Valid is false whenever the signature fails, the file is
// malformed, or the license has expired; Reason says which. A caller can
// still read Tier/Seats/ExpiresAt off an expired-but-validly-signed
// Entitlement -- Verify never discards data just because time ran out.
type Entitlement struct {
	Tier        string    `json:"tier"`
	Seats       int       `json:"seats"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	ValidatedAt time.Time `json:"validated_at"`
	Valid       bool      `json:"valid"`
	Reason      string    `json:"reason,omitempty"`
}

// ErrBadSignature means the Ed25519 signature did not verify against the
// given public key -- the file was tampered with, corrupted, or signed for
// a different deployment. ErrMalformed means the file isn't a valid license
// envelope at all (bad JSON, bad base64). Both are refuse-to-read errors,
// distinct from an expired-but-genuine license (which Verify returns
// successfully with Valid=false).
var (
	ErrBadSignature = errors.New("licensing: signature verification failed")
	ErrMalformed    = errors.New("licensing: malformed license file")
)

// Verify checks the Ed25519 signature over a license file's bytes and
// returns the resulting Entitlement, evaluated as of now.
func Verify(fileBytes []byte, publicKeyB64 string, now time.Time) (Entitlement, error) {
	pubKey, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil || len(pubKey) != ed25519.PublicKeySize {
		return Entitlement{}, fmt.Errorf("licensing: invalid public key: %w", err)
	}

	var env envelope
	if err := json.Unmarshal(fileBytes, &env); err != nil {
		return Entitlement{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	payloadBytes, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return Entitlement{}, fmt.Errorf("%w: payload not base64: %v", ErrMalformed, err)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return Entitlement{}, fmt.Errorf("%w: signature not base64: %v", ErrMalformed, err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pubKey), payloadBytes, sig) {
		return Entitlement{}, ErrBadSignature
	}

	var payload LicensePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return Entitlement{}, fmt.Errorf("%w: payload contents: %v", ErrMalformed, err)
	}

	e := Entitlement{
		Tier:        payload.Tier,
		Seats:       payload.Seats,
		IssuedAt:    payload.IssuedAt,
		ExpiresAt:   payload.ExpiresAt,
		ValidatedAt: now,
		Valid:       true,
	}
	if !payload.ExpiresAt.IsZero() && now.After(payload.ExpiresAt) {
		e.Valid = false
		e.Reason = "expired"
	}
	return e, nil
}

// Sign produces license file bytes for a payload and private key. It exists
// for tests and for an eventual offline license-issuing CLI; the control
// plane itself never signs a license, only verifies one.
func Sign(payload LicensePayload, priv ed25519.PrivateKey) ([]byte, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("licensing: marshal payload: %w", err)
	}
	sig := ed25519.Sign(priv, payloadBytes)
	env := envelope{
		Payload:   base64.StdEncoding.EncodeToString(payloadBytes),
		Signature: base64.StdEncoding.EncodeToString(sig),
	}
	out, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("licensing: marshal envelope: %w", err)
	}
	return out, nil
}

// GateKeyPolicy is the deferred D9 extension point: a future tier
// eligibility predicate slotted between a license lookup and a
// featuregate toggle (owner decision, issue #304 comment, 2026-07-07).
// Nothing in this codebase calls it -- wiring it into
// apps/control-plane/internal/featuregate is explicitly out of scope until
// there is real pricing-tier market signal. It lives here, typed against
// Entitlement.Tier, so that future enforcement is additive, not a
// rearchitecture.
type GateKeyPolicy interface {
	// AllowedGateKeys returns the gate keys a tier may enable, and whether
	// the tier is restricted at all. ok=false means "no restriction" (every
	// tier may enable every key) -- the only behavior wired today.
	AllowedGateKeys(tier string) (keys []string, ok bool)
}

// NoOpGateKeyPolicy is the only GateKeyPolicy implementation that exists
// today: every tier may enable every gate key. Swapping this for a real
// policy is the entire future enforcement change deferred by D9.
type NoOpGateKeyPolicy struct{}

// AllowedGateKeys always reports unrestricted (ok=false, nil keys).
func (NoOpGateKeyPolicy) AllowedGateKeys(string) ([]string, bool) { return nil, false }
