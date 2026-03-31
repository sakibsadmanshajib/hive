package apikeys

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// KeyStatus represents the state of an API key.
type KeyStatus string

const (
	KeyStatusActive   KeyStatus = "active"
	KeyStatusDisabled KeyStatus = "disabled"
	KeyStatusRevoked  KeyStatus = "revoked"
	KeyStatusExpired  KeyStatus = "expired"
)

// APIKey is the durable API-key record. Raw secrets are never stored.
type APIKey struct {
	ID              uuid.UUID
	AccountID       uuid.UUID
	Nickname        string
	TokenHash       string
	RedactedSuffix  string
	Status          KeyStatus
	ExpiresAt       *time.Time
	LastUsedAt      *time.Time
	CreatedByUserID uuid.UUID
	DisabledAt      *time.Time
	RevokedAt       *time.Time
	ReplacedByKeyID *uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// KeyEvent is an immutable audit log entry for key lifecycle transitions.
type KeyEvent struct {
	ID          uuid.UUID
	APIKeyID    uuid.UUID
	AccountID   uuid.UUID
	EventType   string
	ActorUserID uuid.UUID
	Metadata    map[string]interface{}
	CreatedAt   time.Time
}

// CreateKeyInput is the user-supplied input when creating a new key.
type CreateKeyInput struct {
	Nickname  string
	ExpiresAt *time.Time
}

// TransitionInput is used for disable/enable/revoke operations.
type TransitionInput struct {
	AccountID   uuid.UUID
	ActorUserID uuid.UUID
	KeyID       uuid.UUID
}

// CreateKeyResult is returned when a key is created. Secret is the raw
// API secret that must be shown exactly once and never stored or logged.
type CreateKeyResult struct {
	Key    APIKey
	Secret string
}

// RotateKeyResult is returned when a key is rotated. The old key is revoked
// and a new key with a brand-new secret is returned.
type RotateKeyResult struct {
	OldKey APIKey
	NewKey APIKey
	Secret string
}

// ErrNotFound is returned when a key is not found.
var ErrNotFound = errors.New("apikeys: not found")

// ErrRevoked is returned when an operation is attempted on a revoked key.
var ErrRevoked = errors.New("apikeys: key is revoked")

// ErrDisabled is returned when an enable-only operation requires a disabled key but found a different state.
var ErrDisabled = errors.New("apikeys: key is not disabled")

// ErrNotActive is returned when an operation requires an active key.
var ErrNotActive = errors.New("apikeys: key is not active")
