package byok

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
)

// Service-level sentinel errors.
var (
	ErrValidation = errors.New("byok: validation failed")
)

// AuditLogger is the narrow audit surface the service needs; *audit.Logger
// satisfies it. An interface keeps unit tests free of audit's concrete
// constructor dependencies (SyncWriter + WAL writer).
type AuditLogger interface {
	Log(ctx context.Context, e audit.Event) error
}

// Service owns the BYOK business rules: validate, encrypt, persist, audit.
type Service struct {
	repo   Repository
	cipher *Cipher
	audit  AuditLogger
}

// NewService wires the service. cipher may be nil (locked mode): registration
// and any future decrypt path fail closed with ErrNotConfigured, so a
// deployment without HIVE_BYOK_ENC_KEY never stores or reveals plaintext.
func NewService(repo Repository, c *Cipher, a AuditLogger) *Service {
	return &Service{repo: repo, cipher: c, audit: a}
}

// Register validates input, encrypts the credential with AES-256-GCM, and
// persists it scoped to accountID.
func (s *Service) Register(ctx context.Context, accountID, userID uuid.UUID, in RegisterInput) (Key, error) {
	// Normalize empty-string pointers to nil so "no target" is a clean
	// validation error rather than a raw FK violation at the database.
	if in.ProviderSlug != nil && *in.ProviderSlug == "" {
		in.ProviderSlug = nil
	}
	if in.BaseURL != nil && *in.BaseURL == "" {
		in.BaseURL = nil
	}
	if err := validateRegisterInput(in); err != nil {
		return Key{}, err
	}
	if s.cipher == nil {
		return Key{}, ErrNotConfigured
	}

	blob, err := s.cipher.Encrypt(in.APIKey)
	if err != nil {
		return Key{}, err
	}

	k := Key{
		AccountID:       accountID,
		Label:           strings.TrimSpace(in.Label),
		ProviderSlug:    in.ProviderSlug,
		BaseURL:         in.BaseURL,
		ModelMap:        normalizeModelMap(in.ModelMap),
		EncryptedAPIKey: blob,
		KeyLast4:        MaskSecret(in.APIKey),
		Status:          StatusActive,
		CreatedBy:       userID,
	}
	created, err := s.repo.Create(ctx, k)
	if err != nil {
		return Key{}, err
	}
	s.emitAudit(ctx, AuditActionRegister, accountID, userID, created)
	return created, nil
}

// List returns the account's own keys (masked views are built at the HTTP
// layer; the service never strips fields itself).
func (s *Service) List(ctx context.Context, accountID uuid.UUID) ([]Key, error) {
	return s.repo.ListByAccount(ctx, accountID)
}

// ListAll is the platform-admin surface: every tenant's keys, masked.
func (s *Service) ListAll(ctx context.Context) ([]Key, error) {
	return s.repo.ListAll(ctx)
}

// Get fetches one key row scoped to the account; cross-account fetches are
// indistinguishable from missing rows (ErrNotFound either way).
func (s *Service) Get(ctx context.Context, accountID, id uuid.UUID) (Key, error) {
	return s.repo.Get(ctx, accountID, id)
}

// Reveal decrypts and returns the stored credential for the given key id.
// It is the single decryption point the routing integration (follow-up
// issue) will call after its own authorization check. Fails closed when no
// encryption key is configured.
func (s *Service) Reveal(ctx context.Context, accountID, id uuid.UUID) (string, error) {
	if s.cipher == nil {
		return "", ErrNotConfigured
	}
	k, err := s.repo.Get(ctx, accountID, id)
	if err != nil {
		return "", err
	}
	if k.Status != StatusActive {
		return "", ErrNotFound
	}
	return s.cipher.Decrypt(k.EncryptedAPIKey)
}

// Revoke marks a key revoked. The WHERE clause matches active rows for this
// account only, so revoking another account's key id is a clean 404.
func (s *Service) Revoke(ctx context.Context, accountID, id, actor uuid.UUID) (Key, error) {
	k, err := s.repo.Revoke(ctx, accountID, id)
	if err != nil {
		return Key{}, err
	}
	s.emitAudit(ctx, AuditActionRevoke, accountID, actor, k)
	return k, nil
}

// emitAudit writes one security-tier audit event. Metadata carries only
// customer-safe fields: label and masked suffix, never key material. A failed
// audit write is logged loudly rather than silently discarded: the mutation is
// already committed at that point, so the operator gets a red log line to act
// on instead of an invisible loss.
func (s *Service) emitAudit(ctx context.Context, action string, accountID, actor uuid.UUID, k Key) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Log(ctx, audit.Event{
		TenantID:     accountID,
		Actor:        audit.Actor{ID: actor, Type: audit.ActorUser},
		Action:       action,
		ResourceType: "tenant_provider_key",
		ResourceID:   k.ID.String(),
		Severity:     audit.SeverityNotice,
		After: map[string]string{
			"label":     k.Label,
			"key_last4": k.KeyLast4,
		},
	}); err != nil {
		// Identifiers only. No label, no mask, no key material: this line goes
		// to the process log, which has a wider audience than the audit table.
		slog.ErrorContext(ctx, "byok audit write failed, credential mutation already committed",
			"action", action,
			"account_id", accountID.String(),
			"resource_id", k.ID.String(),
			"error", err)
	}
}

// validateRegisterInput enforces the storage-boundary rules mirrored by the
// tenant_provider_keys table CHECK constraints.
func validateRegisterInput(in RegisterInput) error {
	if label := strings.TrimSpace(in.Label); len(label) == 0 || utf8.RuneCountInString(label) > 100 {
		return fmt.Errorf("%w: label required (1 to 100 chars)", ErrValidation)
	}
	hasSlug := in.ProviderSlug != nil && *in.ProviderSlug != ""
	hasURL := in.BaseURL != nil && *in.BaseURL != ""
	if hasSlug == hasURL { // exactly one target
		return fmt.Errorf("%w: exactly one of provider_slug or base_url is required", ErrValidation)
	}
	if hasSlug && utf8.RuneCountInString(*in.ProviderSlug) > 100 {
		return fmt.Errorf("%w: provider_slug too long", ErrValidation)
	}
	if hasURL {
		u, err := url.Parse(*in.BaseURL)
		// http is allowed deliberately: self-hosted OpenAI-compatible
		// endpoints on a private network are a legitimate Enterprise posture.
		// Dial-time egress restrictions (SSRF denylist) are the routing
		// integration's job and are pinned in issue #1139.
		if err != nil || u.Scheme != "https" && u.Scheme != "http" {
			return fmt.Errorf("%w: base_url must be an absolute http(s) URL with a host", ErrValidation)
		}
		if u.Host == "" {
			return fmt.Errorf("%w: base_url must include a host", ErrValidation)
		}
		// Credentials, query strings and fragments in the URL would be stored
		// in plaintext outside encrypted_api_key and echoed by list views;
		// reject them so the credential column stays the only secret channel.
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%w: base_url must not embed credentials, a query string or a fragment", ErrValidation)
		}
	}
	// A real provider credential fits in a few hundred bytes; 4096 is a hard
	// ceiling so an oversized blob cannot be encrypted and persisted as-is.
	const maxAPIKeyLen = 4096
	if len(in.APIKey) < 8 {
		return fmt.Errorf("%w: api_key must be at least 8 characters", ErrValidation)
	}
	if len(in.APIKey) > maxAPIKeyLen {
		return fmt.Errorf("%w: api_key must be at most %d characters", ErrValidation, maxAPIKeyLen)
	}
	if len(in.ModelMap) > 50 {
		return fmt.Errorf("%w: model_map supports at most 50 entries", ErrValidation)
	}
	for req, upstream := range in.ModelMap {
		if req == "" || upstream == "" {
			return fmt.Errorf("%w: model_map entries must be non-empty", ErrValidation)
		}
	}
	return nil
}

// normalizeModelMap returns the map as-is when empty; non-nil otherwise so
// pgx serializes {} rather than SQL NULL into the jsonb column.
func normalizeModelMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
