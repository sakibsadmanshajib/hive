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

	// Create default policy row for the new key.
	_ = s.repo.CreateDefaultPolicy(ctx, created.ID)

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

	// Create default policy for the new key.
	_ = s.repo.CreateDefaultPolicy(ctx, created.ID)

	return RotateKeyResult{
		OldKey: old,
		NewKey: created,
		Secret: rawSecret,
	}, nil
}

// UpdatePolicy updates the per-key policy configuration.
func (s *Service) UpdatePolicy(ctx context.Context, accountID, actorUserID, keyID uuid.UUID, input UpdatePolicyInput) (KeyPolicy, error) {
	policy, err := s.repo.UpsertPolicy(ctx, accountID, keyID, input)
	if err != nil {
		return KeyPolicy{}, fmt.Errorf("apikeys: update policy: %w", err)
	}
	return policy, nil
}

// ResolveSnapshot builds an AuthSnapshot from the key and policy data.
// This is called by the internal resolver endpoint for edge hot-path enforcement.
func (s *Service) ResolveSnapshot(ctx context.Context, tokenHash string) (AuthSnapshot, error) {
	key, policy, err := s.repo.GetPolicyByTokenHash(ctx, tokenHash)
	if err != nil {
		return AuthSnapshot{}, fmt.Errorf("apikeys: resolve snapshot: %w", err)
	}

	key = applyExpiry(key, time.Now())

	// Build allowed aliases from group members + explicit allowed - denied.
	var allowedAliases []string
	if policy.AllowAllModels {
		// All models mode — edge will not filter by alias.
		allowedAliases = []string{"*"}
	} else {
		// Resolve group members.
		if len(policy.AllowedGroupNames) > 0 {
			groupAliases, err := s.repo.ListGroupMembers(ctx, policy.AllowedGroupNames)
			if err != nil {
				return AuthSnapshot{}, fmt.Errorf("apikeys: resolve group members: %w", err)
			}
			allowedAliases = append(allowedAliases, groupAliases...)
		}
		// Add explicit allowed aliases.
		allowedAliases = append(allowedAliases, policy.AllowedAliases...)
		// Remove denied aliases.
		if len(policy.DeniedAliases) > 0 {
			denied := make(map[string]bool, len(policy.DeniedAliases))
			for _, d := range policy.DeniedAliases {
				denied[d] = true
			}
			var filtered []string
			for _, a := range allowedAliases {
				if !denied[a] {
					filtered = append(filtered, a)
				}
			}
			allowedAliases = filtered
		}
		// Deduplicate.
		allowedAliases = dedup(allowedAliases)
	}

	return AuthSnapshot{
		KeyID:                 key.ID,
		AccountID:             key.AccountID,
		Status:                key.Status,
		ExpiresAt:             key.ExpiresAt,
		AllowAllModels:        policy.AllowAllModels,
		AllowedAliases:        allowedAliases,
		BudgetKind:            policy.BudgetKind,
		BudgetLimitCredits:    policy.BudgetLimitCredits,
		BudgetConsumedCredits: 0, // populated in Plan 05-03
		BudgetReservedCredits: 0, // populated in Plan 05-03
		BudgetAnchorAt:        policy.BudgetAnchorAt,
		PolicyVersion:         policy.PolicyVersion,
	}, nil
}

// RefreshSnapshot is a placeholder for Plan 05-03 where the snapshot
// will be written/refreshed in Redis after policy or budget changes.
func (s *Service) RefreshSnapshot(ctx context.Context, keyID uuid.UUID) error {
	// No-op until Plan 05-02 Task 2 adds Redis projection.
	return nil
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

// dedup returns unique strings from the input slice preserving order.
func dedup(input []string) []string {
	seen := make(map[string]bool, len(input))
	var result []string
	for _, s := range input {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

