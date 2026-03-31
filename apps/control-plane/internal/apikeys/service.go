package apikeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Service encapsulates all API-key lifecycle business logic.
type Service struct {
	repo Repository
}

// NewService returns a new Service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// ListKeys returns all keys for the account. Keys whose stored status is
// active but whose expires_at is in the past are reported as expired.
func (s *Service) ListKeys(ctx context.Context, accountID uuid.UUID) ([]APIKey, error) {
	keys, err := s.repo.ListKeys(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("apikeys: list: %w", err)
	}
	now := time.Now()
	for i := range keys {
		keys[i] = applyExpiry(keys[i], now)
	}
	return keys, nil
}

// CreateKey issues a new API key. The raw secret is returned once and
// must not be logged, persisted, or included in list responses.
func (s *Service) CreateKey(ctx context.Context, accountID, actorUserID uuid.UUID, input CreateKeyInput) (CreateKeyResult, error) {
	rawSecret, tokenHash, redactedSuffix, err := generateSecret()
	if err != nil {
		return CreateKeyResult{}, fmt.Errorf("apikeys: generate secret: %w", err)
	}

	keyID := uuid.New()
	key := APIKey{
		ID:              keyID,
		AccountID:       accountID,
		Nickname:        input.Nickname,
		TokenHash:       tokenHash,
		RedactedSuffix:  redactedSuffix,
		Status:          KeyStatusActive,
		ExpiresAt:       input.ExpiresAt,
		CreatedByUserID: actorUserID,
	}

	created, err := s.repo.CreateKey(ctx, key)
	if err != nil {
		return CreateKeyResult{}, fmt.Errorf("apikeys: create: %w", err)
	}

	_ = s.repo.InsertEvent(ctx, KeyEvent{
		ID:          uuid.New(),
		APIKeyID:    created.ID,
		AccountID:   accountID,
		EventType:   "created",
		ActorUserID: actorUserID,
	})

	return CreateKeyResult{
		Key:    created,
		Secret: rawSecret,
	}, nil
}

// DisableKey temporarily disables a key without revoking it.
func (s *Service) DisableKey(ctx context.Context, accountID, actorUserID, keyID uuid.UUID) (APIKey, error) {
	existing, err := s.repo.GetKey(ctx, accountID, keyID)
	if err != nil {
		return APIKey{}, fmt.Errorf("apikeys: disable: %w", err)
	}

	existing = applyExpiry(existing, time.Now())
	if existing.Status == KeyStatusRevoked {
		return APIKey{}, ErrRevoked
	}
	if existing.Status != KeyStatusActive && existing.Status != KeyStatusExpired {
		return APIKey{}, ErrNotActive
	}

	now := time.Now()
	updated, err := s.repo.UpdateKeyState(ctx, accountID, keyID, KeyStatusDisabled, &now, nil, nil)
	if err != nil {
		return APIKey{}, fmt.Errorf("apikeys: disable update: %w", err)
	}

	_ = s.repo.InsertEvent(ctx, KeyEvent{
		ID:          uuid.New(),
		APIKeyID:    keyID,
		AccountID:   accountID,
		EventType:   "disabled",
		ActorUserID: actorUserID,
	})

	return updated, nil
}

// EnableKey re-enables a previously disabled key.
func (s *Service) EnableKey(ctx context.Context, accountID, actorUserID, keyID uuid.UUID) (APIKey, error) {
	existing, err := s.repo.GetKey(ctx, accountID, keyID)
	if err != nil {
		return APIKey{}, fmt.Errorf("apikeys: enable: %w", err)
	}

	if existing.Status == KeyStatusRevoked {
		return APIKey{}, ErrRevoked
	}
	if existing.Status != KeyStatusDisabled {
		return APIKey{}, ErrDisabled
	}

	updated, err := s.repo.UpdateKeyState(ctx, accountID, keyID, KeyStatusActive, nil, nil, nil)
	if err != nil {
		return APIKey{}, fmt.Errorf("apikeys: enable update: %w", err)
	}

	_ = s.repo.InsertEvent(ctx, KeyEvent{
		ID:          uuid.New(),
		APIKeyID:    keyID,
		AccountID:   accountID,
		EventType:   "enabled",
		ActorUserID: actorUserID,
	})

	return updated, nil
}

// RevokeKey permanently revokes a key. This cannot be undone.
func (s *Service) RevokeKey(ctx context.Context, accountID, actorUserID, keyID uuid.UUID) (APIKey, error) {
	existing, err := s.repo.GetKey(ctx, accountID, keyID)
	if err != nil {
		return APIKey{}, fmt.Errorf("apikeys: revoke: %w", err)
	}

	existing = applyExpiry(existing, time.Now())
	if existing.Status == KeyStatusRevoked {
		return APIKey{}, ErrRevoked
	}

	now := time.Now()
	updated, err := s.repo.UpdateKeyState(ctx, accountID, keyID, KeyStatusRevoked, nil, &now, nil)
	if err != nil {
		return APIKey{}, fmt.Errorf("apikeys: revoke update: %w", err)
	}

	_ = s.repo.InsertEvent(ctx, KeyEvent{
		ID:          uuid.New(),
		APIKeyID:    keyID,
		AccountID:   accountID,
		EventType:   "revoked",
		ActorUserID: actorUserID,
	})

	return updated, nil
}

// RotateKey creates a brand-new replacement key and immediately revokes
// only the rotated source key. Sibling keys are unaffected.
func (s *Service) RotateKey(ctx context.Context, accountID, actorUserID, keyID uuid.UUID, nickname string, expiresAt *time.Time) (RotateKeyResult, error) {
	existing, err := s.repo.GetKey(ctx, accountID, keyID)
	if err != nil {
		return RotateKeyResult{}, fmt.Errorf("apikeys: rotate: %w", err)
	}

	existing = applyExpiry(existing, time.Now())
	if existing.Status == KeyStatusRevoked {
		return RotateKeyResult{}, ErrRevoked
	}

	rawSecret, tokenHash, redactedSuffix, err := generateSecret()
	if err != nil {
		return RotateKeyResult{}, fmt.Errorf("apikeys: rotate secret: %w", err)
	}

	newKey := APIKey{
		ID:              uuid.New(),
		AccountID:       accountID,
		Nickname:        nickname,
		TokenHash:       tokenHash,
		RedactedSuffix:  redactedSuffix,
		Status:          KeyStatusActive,
		ExpiresAt:       expiresAt,
		CreatedByUserID: actorUserID,
	}

	now := time.Now()
	old, created, err := s.repo.CreateReplacementKey(ctx, keyID, newKey, now)
	if err != nil {
		return RotateKeyResult{}, fmt.Errorf("apikeys: rotate replace: %w", err)
	}

	_ = s.repo.InsertEvent(ctx, KeyEvent{
		ID:          uuid.New(),
		APIKeyID:    keyID,
		AccountID:   accountID,
		EventType:   "rotated",
		ActorUserID: actorUserID,
		Metadata:    map[string]interface{}{"replacement_key_id": created.ID.String()},
	})

	return RotateKeyResult{
		OldKey: old,
		NewKey: created,
		Secret: rawSecret,
	}, nil
}

// --- helpers ---

// generateSecret produces a cryptographically random hk_-prefixed API secret.
// Returns the raw secret, its SHA-256 hex hash, and the last 6 characters.
func generateSecret() (rawSecret, tokenHash, redactedSuffix string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}

	encoded := base64.RawURLEncoding.EncodeToString(b)
	rawSecret = "hk_" + encoded

	h := sha256.Sum256([]byte(rawSecret))
	tokenHash = strings.ToLower(hex.EncodeToString(h[:]))

	redactedSuffix = rawSecret[len(rawSecret)-6:]

	return rawSecret, tokenHash, redactedSuffix, nil
}

// HashSecret returns the SHA-256 hex hash of a raw API secret.
func HashSecret(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return strings.ToLower(hex.EncodeToString(h[:]))
}

// applyExpiry returns the key with status set to Expired when the stored
// status is active but expires_at is in the past. No mutation to sibling keys.
func applyExpiry(k APIKey, now time.Time) APIKey {
	if k.Status == KeyStatusActive && k.ExpiresAt != nil && k.ExpiresAt.Before(now) {
		k.Status = KeyStatusExpired
	}
	return k
}
