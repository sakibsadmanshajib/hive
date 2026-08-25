package byok

import (
	"time"

	"github.com/google/uuid"
)

// Key statuses.
const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

// Audit action names emitted by this package. Registered as security-tier
// actions in internal/audit.
const (
	AuditActionRegister = "BYOK_KEY_REGISTER"
	AuditActionRevoke   = "BYOK_KEY_REVOKE"
)

// Key is one tenant-registered provider credential row. EncryptedAPIKey holds
// AES-256-GCM ciphertext (nonce || ciphertext || tag) and must never be
// serialized into any HTTP response or log line; the JSON view builder in
// http.go deliberately omits it.
type Key struct {
	ID              uuid.UUID
	AccountID       uuid.UUID
	Label           string
	ProviderSlug    *string // non-nil: credential targets a registered custom_providers slug
	BaseURL         *string // non-nil: freeform OpenAI-compatible endpoint (mutually exclusive with ProviderSlug)
	ModelMap        map[string]string
	EncryptedAPIKey []byte
	KeyLast4        string
	Status          string
	CreatedBy       uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
	RevokedAt       *time.Time
}

// RegisterInput is the validated request to store a new credential.
type RegisterInput struct {
	Label        string
	ProviderSlug *string
	BaseURL      *string
	APIKey       string
	ModelMap     map[string]string
}
