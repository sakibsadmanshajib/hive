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

// KeyPolicy holds the durable per-key policy configuration.
type KeyPolicy struct {
	APIKeyID          uuid.UUID
	AllowAllModels    bool
	AllowedGroupNames []string
	AllowedAliases    []string
	DeniedAliases     []string
	BudgetKind        string
	BudgetLimitCredits *int64
	BudgetAnchorAt    *time.Time
	PolicyVersion     int64
	UpdatedAt         time.Time
}

// BudgetPolicy encapsulates budget-related policy data.
type BudgetPolicy struct {
	Kind         string
	LimitCredits *int64
	AnchorAt     *time.Time
}

// AuthSnapshot is the control-plane-owned, Redis-projected authorization
// snapshot consumed by the edge for hot-path enforcement.
type AuthSnapshot struct {
	KeyID                uuid.UUID  `json:"key_id"`
	AccountID            uuid.UUID  `json:"account_id"`
	Status               KeyStatus  `json:"status"`
	ExpiresAt            *time.Time `json:"expires_at,omitempty"`
	AllowAllModels       bool       `json:"allow_all_models"`
	AllowedAliases       []string   `json:"allowed_aliases"`
	BudgetKind           string     `json:"budget_kind"`
	BudgetLimitCredits   *int64     `json:"budget_limit_credits,omitempty"`
	BudgetConsumedCredits int64     `json:"budget_consumed_credits"`
	BudgetReservedCredits int64     `json:"budget_reserved_credits"`
	BudgetAnchorAt       *time.Time `json:"budget_anchor_at,omitempty"`
	PolicyVersion        int64      `json:"policy_version"`
}

// UpdatePolicyInput is the user-supplied input for per-key policy updates.
type UpdatePolicyInput struct {
	ExpiresAt          *time.Time
	AllowAllModels     *bool
	AllowedGroupNames  []string
	AllowedAliases     []string
	DeniedAliases      []string
	BudgetKind         *string
	BudgetLimitCredits *int64
	BudgetAnchorAt     *time.Time
}

// ResolveSnapshotResult wraps the auth snapshot returned by the resolve action.
type ResolveSnapshotResult struct {
	Snapshot AuthSnapshot
}

