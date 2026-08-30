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

// KeyKind is what minted a key and who it is for. It is the structural
// discriminator the customer's key list filters on, deliberately not the
// nickname: a display string is not an identity, and a customer who names
// their own key "agent task backfill" must not have it hidden from them.
type KeyKind string

const (
	// KindUser is a customer-created key. The only kind ListKeys returns.
	KindUser KeyKind = "user"
	// KindAgentTask is one short-lived credential minted per agent task so the
	// sandbox's inference settles against the tenant that submitted it
	// (issue #1507). Its id is that task's own id. Never listed to a customer,
	// who did not create it and cannot use it.
	KindAgentTask KeyKind = "agent_task"
)

// APIKey is the durable API-key record. Raw secrets are never stored.
type APIKey struct {
	ID              uuid.UUID
	AccountID       uuid.UUID
	Kind            KeyKind
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

// MaxNicknameLen bounds the API key nickname, counted in characters rather
// than bytes so a Bangla name gets the same allowance as an English one.
//
// Issue #1400: the field was unbounded at every layer, and one 5000-character
// nickname made the console key table 50,000 pixels wide, pushing the Revoke
// control of every key in the workspace out of reach. Nothing in the product
// could shorten a stored value afterwards, so repairing it took a direct
// database write. 100 matches the bound the BYOK label already uses.
const MaxNicknameLen = 100

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

// ErrNotAgentTaskKey is returned when RevokeAgentTaskKey is handed an id that
// resolves to a key of some other kind. Never silently ignored: it means the
// caller's premise about what that id identifies is wrong.
var ErrNotAgentTaskKey = errors.New("apikeys: key is not an agent task credential")

// ErrRevoked is returned when an operation is attempted on a revoked key.
var ErrRevoked = errors.New("apikeys: key is revoked")

// ErrDisabled is returned when an enable-only operation requires a disabled key but found a different state.
var ErrDisabled = errors.New("apikeys: key is not disabled")

// ErrNotActive is returned when an operation requires an active key.
var ErrNotActive = errors.New("apikeys: key is not active")

// ErrAccountNotProvisioned is returned when a mint is attempted for an account
// that has no public.tenant_billing_accounts row. edge-api fails such a key
// closed on its very first request (authz.AuthSnapshot.TenantUUID, PR #1240),
// so the key is dead the moment it is issued.
var ErrAccountNotProvisioned = errors.New("apikeys: account has no billing tenant")

// KeyPolicy holds the durable per-key policy configuration.
type KeyPolicy struct {
	APIKeyID           uuid.UUID
	AllowAllModels     bool
	AllowedGroupNames  []string
	AllowedAliases     []string
	DeniedAliases      []string
	BudgetKind         string
	BudgetLimitCredits *int64
	BudgetAnchorAt     *time.Time
	PolicyVersion      int64
	UpdatedAt          time.Time
}

// BudgetPolicy encapsulates budget-related policy data.
type BudgetPolicy struct {
	Kind         string
	LimitCredits *int64
	AnchorAt     *time.Time
}

// RatePolicy is the projected edge-facing rate-limit configuration for one scope.
type RatePolicy struct {
	RateLimitRPM          int   `json:"rate_limit_rpm"`
	RateLimitTPM          int   `json:"rate_limit_tpm"`
	RollingFiveHourLimit  int64 `json:"rolling_five_hour_limit"`
	WeeklyLimit           int64 `json:"weekly_limit"`
	FreeTokenWeightTenths int   `json:"free_token_weight_tenths"`
	// TierOverrides carries per-tier RPM/TPM overrides that take precedence over
	// env-driven tier defaults at hot-path enforcement. Tiers absent from this
	// map fall through to env defaults. Tiers present override env defaults.
	TierOverrides map[string]TierLimit `json:"tier_overrides,omitempty"`
}

// TierLimit is a per-tier override pair. Either field at zero means
// "no override for that dimension; use env default".
type TierLimit struct {
	RPM int `json:"rpm"`
	TPM int `json:"tpm"`
}

// KeyLimitsInput carries the user-supplied per-key limits update.
type KeyLimitsInput struct {
	RPM           int                  `json:"rpm"`
	TPM           int                  `json:"tpm"`
	TierOverrides map[string]TierLimit `json:"tier_overrides"`
}

// KeyLimits is the read model for a key's rate-limit configuration.
type KeyLimits struct {
	APIKeyID      uuid.UUID            `json:"api_key_id"`
	RPM           int                  `json:"rpm"`
	TPM           int                  `json:"tpm"`
	TierOverrides map[string]TierLimit `json:"tier_overrides"`
}

// Range bounds enforced at the application layer to keep the DB CHECK
// surface small and to avoid drift between SQL constraints and Go validation.
const (
	RateLimitRPMMax = 100000
	RateLimitTPMMax = 10000000
)

// ErrLimitsOutOfRange is returned when an UpdateLimits call carries an
// RPM/TPM value outside the allowed bounds.
var ErrLimitsOutOfRange = errors.New("apikeys: rate-limit value out of range")

// IsValidTierName returns true if the tier name is one of the four known
// hot-path tiers. Phase 12 ships with a fixed enumeration; Phase 20 may
// extend the resolver but the enumeration stays stable.
func IsValidTierName(name string) bool {
	switch name {
	case "guest", "unverified", "verified", "credited":
		return true
	}
	return false
}

// ExpirationSummary is the customer-visible expiration projection for a key.
type ExpirationSummary struct {
	Kind  string
	Label string
}

// BudgetSummary is the customer-visible budget projection for a key.
type BudgetSummary struct {
	Kind  string
	Label string
}

// AllowlistSummary is the customer-visible model access projection for a key.
type AllowlistSummary struct {
	Mode       string
	GroupNames []string
	Label      string
}

// KeyView is the customer-visible representation of an API key plus summaries.
type KeyView struct {
	ID                uuid.UUID
	Nickname          string
	Status            KeyStatus
	RedactedSuffix    string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ExpiresAt         *time.Time
	LastUsedAt        *time.Time
	ExpirationSummary ExpirationSummary
	BudgetSummary     BudgetSummary
	AllowlistSummary  AllowlistSummary
	// SpendCredits is the key's lifetime consumed credits, read unconditionally
	// off api_key_usage_rollups (Repository.GetLifetimeSpend) regardless of
	// whether a budget cap is configured. It is a raw integer for the wire;
	// web-console renders it through lib/format/model-pricing.ts, never as a
	// bare number.
	SpendCredits int64
	// BudgetLimitCredits mirrors KeyPolicy.BudgetLimitCredits (nil = no cap).
	// BudgetSummary.Label already renders a human sentence for the kind, but
	// carried no machine-readable limit for a UI to reformat or edit against.
	BudgetLimitCredits *int64
}

// AuthSnapshot is the control-plane-owned, Redis-projected authorization
// snapshot consumed by the edge for hot-path enforcement.
type AuthSnapshot struct {
	KeyID     uuid.UUID `json:"key_id"`
	AccountID uuid.UUID `json:"account_id"`
	// TenantID is resolved server-side from AccountID via
	// public.tenant_billing_accounts (Service.ResolveSnapshot), never from
	// client input. uuid.Nil means the account has no billing-account
	// mapping yet; edge-api's consumers fail closed on that rather than
	// falling back to unfiltered/unentitled access (D-030).
	TenantID              uuid.UUID   `json:"tenant_id"`
	Status                KeyStatus   `json:"status"`
	ExpiresAt             *time.Time  `json:"expires_at,omitempty"`
	AllowAllModels        bool        `json:"allow_all_models"`
	AllowedAliases        []string    `json:"allowed_aliases"`
	BudgetKind            string      `json:"budget_kind"`
	BudgetLimitCredits    *int64      `json:"budget_limit_credits,omitempty"`
	BudgetConsumedCredits int64       `json:"budget_consumed_credits"`
	BudgetReservedCredits int64       `json:"budget_reserved_credits"`
	BudgetAnchorAt        *time.Time  `json:"budget_anchor_at,omitempty"`
	AccountRatePolicy     *RatePolicy `json:"account_rate_policy,omitempty"`
	KeyRatePolicy         *RatePolicy `json:"key_rate_policy,omitempty"`
	PolicyVersion         int64       `json:"policy_version"`
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

// UsageRollupWindow tracks per-key usage aggregations over time windows.
type UsageRollupWindow struct {
	APIKeyID         uuid.UUID
	ModelAlias       string
	WindowKind       string // 'lifetime' or 'monthly'
	WindowStart      time.Time
	RequestCount     int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ConsumedCredits  int64
	LastSeenAt       time.Time
}

// BudgetWindow tracks per-key financial states (consumed/reserved credits) over time windows.
type BudgetWindow struct {
	APIKeyID        uuid.UUID
	WindowKind      string // 'lifetime' or 'monthly'
	WindowStart     time.Time
	WindowEnd       *time.Time
	ConsumedCredits int64
	ReservedCredits int64
	UpdatedAt       time.Time
}
