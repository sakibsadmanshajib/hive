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
	"github.com/redis/go-redis/v9"
)

// SnapshotCache invalidates cached auth snapshots for API keys.
type SnapshotCache interface {
	InvalidateSnapshot(ctx context.Context, tokenHash string) error
}

type redisSnapshotCache struct {
	client *redis.Client
}

// NewRedisSnapshotCache adapts a Redis client into a snapshot cache invalidator.
func NewRedisSnapshotCache(client *redis.Client) SnapshotCache {
	if client == nil {
		return nil
	}
	return &redisSnapshotCache{client: client}
}

func (c *redisSnapshotCache) InvalidateSnapshot(ctx context.Context, tokenHash string) error {
	if c == nil || c.client == nil || tokenHash == "" {
		return nil
	}
	return c.client.Del(ctx, snapshotRedisKey(tokenHash)).Err()
}

// Service encapsulates all API-key lifecycle business logic.
type Service struct {
	repo  Repository
	cache SnapshotCache
}

// NewService returns a new Service.
func NewService(repo Repository, caches ...SnapshotCache) *Service {
	var cache SnapshotCache
	if len(caches) > 0 {
		cache = caches[0]
	}
	return &Service{repo: repo, cache: cache}
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

// GetKey returns a single key for the account and exposes expired keys without
// mutating the stored durable status.
func (s *Service) GetKey(ctx context.Context, accountID, keyID uuid.UUID) (APIKey, error) {
	key, err := s.repo.GetKey(ctx, accountID, keyID)
	if err != nil {
		return APIKey{}, fmt.Errorf("apikeys: get: %w", err)
	}
	return applyExpiry(key, time.Now()), nil
}

// ListKeyViews returns customer-visible key rows with policy-backed summaries.
func (s *Service) ListKeyViews(ctx context.Context, accountID uuid.UUID) ([]KeyView, error) {
	keys, err := s.ListKeys(ctx, accountID)
	if err != nil {
		return nil, err
	}

	views := make([]KeyView, 0, len(keys))
	for _, key := range keys {
		policy, err := s.policyForKey(ctx, accountID, key.ID)
		if err != nil {
			return nil, err
		}
		views = append(views, buildKeyView(key, policy))
	}
	return views, nil
}

// GetKeyView returns a single customer-visible key row with policy summaries.
func (s *Service) GetKeyView(ctx context.Context, accountID, keyID uuid.UUID) (KeyView, error) {
	key, err := s.GetKey(ctx, accountID, keyID)
	if err != nil {
		return KeyView{}, err
	}
	policy, err := s.policyForKey(ctx, accountID, keyID)
	if err != nil {
		return KeyView{}, err
	}
	return buildKeyView(key, policy), nil
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

	if err := s.invalidateSnapshot(ctx, updated.TokenHash); err != nil {
		return APIKey{}, err
	}

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

	if err := s.invalidateSnapshot(ctx, updated.TokenHash); err != nil {
		return APIKey{}, err
	}

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

	if err := s.invalidateSnapshot(ctx, updated.TokenHash); err != nil {
		return APIKey{}, err
	}

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

	if err := s.invalidateSnapshots(ctx, old.TokenHash, created.TokenHash); err != nil {
		return RotateKeyResult{}, err
	}

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
	key, err := s.repo.GetKey(ctx, accountID, keyID)
	if err != nil {
		return KeyPolicy{}, fmt.Errorf("apikeys: update policy lookup: %w", err)
	}
	if err := s.invalidateSnapshot(ctx, key.TokenHash); err != nil {
		return KeyPolicy{}, err
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
		allowedAliases, err = s.repo.ListAllAliases(ctx)
		if err != nil {
			return AuthSnapshot{}, fmt.Errorf("apikeys: list all aliases: %w", err)
		}
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

	budgetWindow, err := s.repo.GetBudgetWindow(ctx, key.ID, policy.BudgetKind, time.Now().UTC())
	if err != nil {
		return AuthSnapshot{}, fmt.Errorf("apikeys: get budget window: %w", err)
	}
	accountRatePolicy, err := s.repo.GetAccountRatePolicy(ctx, key.AccountID)
	if err != nil {
		return AuthSnapshot{}, fmt.Errorf("apikeys: get account rate policy: %w", err)
	}
	keyRatePolicy, err := s.repo.GetKeyRatePolicy(ctx, key.ID)
	if err != nil {
		return AuthSnapshot{}, fmt.Errorf("apikeys: get key rate policy: %w", err)
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
		BudgetConsumedCredits: budgetWindow.ConsumedCredits,
		BudgetReservedCredits: budgetWindow.ReservedCredits,
		BudgetAnchorAt:        policy.BudgetAnchorAt,
		AccountRatePolicy:     &accountRatePolicy,
		KeyRatePolicy:         &keyRatePolicy,
		PolicyVersion:         policy.PolicyVersion,
	}, nil
}

func (s *Service) RefreshSnapshot(ctx context.Context, keyID uuid.UUID) error {
	key, err := s.repo.GetKeyByID(ctx, keyID)
	if err != nil {
		return fmt.Errorf("apikeys: refresh snapshot: %w", err)
	}
	return s.invalidateSnapshot(ctx, key.TokenHash)
}

// ApplyReservationDelta updates the key's budget window tracking reserved and consumed credits.
func (s *Service) ApplyReservationDelta(ctx context.Context, apiKeyID uuid.UUID, reservedDelta int64, consumedDelta int64, at time.Time) error {
	key, err := s.repo.GetKeyByID(ctx, apiKeyID)
	if err != nil {
		return fmt.Errorf("apikeys: load key for reservation delta: %w", err)
	}
	policy, err := s.policyForKey(ctx, key.AccountID, apiKeyID)
	if err != nil {
		return err
	}
	if policy.BudgetKind == "" || policy.BudgetKind == "none" {
		return nil
	}
	if err := s.repo.ApplyReservationDelta(ctx, apiKeyID, policy.BudgetKind, reservedDelta, consumedDelta, at); err != nil {
		return err
	}
	return s.invalidateSnapshot(ctx, key.TokenHash)
}

// RecordUsageFinalization records final tokens and consumes credits in the usage rollups.
func (s *Service) RecordUsageFinalization(ctx context.Context, apiKeyID uuid.UUID, modelAlias string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, consumedCredits int64, at time.Time) error {
	if err := s.repo.RecordUsageFinalization(ctx, apiKeyID, modelAlias, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, consumedCredits, at); err != nil {
		return err
	}
	return s.RefreshSnapshot(ctx, apiKeyID)
}

// MarkLastUsed updates the key's last_used_at timestamp.
func (s *Service) MarkLastUsed(ctx context.Context, apiKeyID uuid.UUID, usedAt time.Time) error {
	return s.repo.MarkLastUsed(ctx, apiKeyID, usedAt)
}

// --- helpers ---

func (s *Service) policyForKey(ctx context.Context, accountID, keyID uuid.UUID) (KeyPolicy, error) {
	policy, err := s.repo.GetPolicy(ctx, accountID, keyID)
	if err == ErrNotFound {
		return defaultPolicy(keyID), nil
	}
	if err != nil {
		return KeyPolicy{}, fmt.Errorf("apikeys: get policy: %w", err)
	}
	return policy, nil
}

func defaultPolicy(keyID uuid.UUID) KeyPolicy {
	return KeyPolicy{
		APIKeyID:          keyID,
		AllowedGroupNames: []string{"default"},
		BudgetKind:        "none",
		PolicyVersion:     1,
	}
}

func buildKeyView(key APIKey, policy KeyPolicy) KeyView {
	key = applyExpiry(key, time.Now())

	return KeyView{
		ID:                key.ID,
		Nickname:          key.Nickname,
		Status:            key.Status,
		RedactedSuffix:    key.RedactedSuffix,
		CreatedAt:         key.CreatedAt,
		UpdatedAt:         key.UpdatedAt,
		ExpiresAt:         key.ExpiresAt,
		LastUsedAt:        key.LastUsedAt,
		ExpirationSummary: expirationSummary(key),
		BudgetSummary:     budgetSummary(policy),
		AllowlistSummary:  allowlistSummary(policy),
	}
}

func expirationSummary(key APIKey) ExpirationSummary {
	if key.ExpiresAt == nil {
		return ExpirationSummary{Kind: "never", Label: "Never expires"}
	}
	if key.Status == KeyStatusExpired {
		return ExpirationSummary{Kind: "expired", Label: "Expired"}
	}
	return ExpirationSummary{
		Kind:  "scheduled",
		Label: "Expires " + key.ExpiresAt.Format(time.RFC3339),
	}
}

func budgetSummary(policy KeyPolicy) BudgetSummary {
	switch policy.BudgetKind {
	case "", "none":
		return BudgetSummary{Kind: "none", Label: "No budget cap"}
	case "lifetime":
		if policy.BudgetLimitCredits == nil {
			return BudgetSummary{Kind: "lifetime", Label: "Lifetime budget cap"}
		}
		return BudgetSummary{
			Kind:  "lifetime",
			Label: fmt.Sprintf("Lifetime budget cap: %d credits", *policy.BudgetLimitCredits),
		}
	case "monthly":
		if policy.BudgetLimitCredits == nil {
			return BudgetSummary{Kind: "monthly", Label: "Monthly budget cap"}
		}
		return BudgetSummary{
			Kind:  "monthly",
			Label: fmt.Sprintf("Monthly budget cap: %d credits", *policy.BudgetLimitCredits),
		}
	default:
		return BudgetSummary{Kind: policy.BudgetKind, Label: policy.BudgetKind}
	}
}

func allowlistSummary(policy KeyPolicy) AllowlistSummary {
	if policy.AllowAllModels {
		return AllowlistSummary{
			Mode:  "all",
			Label: "All models",
		}
	}
	if len(policy.AllowedGroupNames) == 1 &&
		policy.AllowedGroupNames[0] == "default" &&
		len(policy.AllowedAliases) == 0 &&
		len(policy.DeniedAliases) == 0 {
		return AllowlistSummary{
			Mode:       "groups",
			GroupNames: []string{"default"},
			Label:      "Default launch-safe models",
		}
	}
	if len(policy.AllowedAliases) > 0 && len(policy.AllowedGroupNames) == 0 {
		return AllowlistSummary{
			Mode:  "aliases",
			Label: "Explicit model allowlist",
		}
	}
	return AllowlistSummary{
		Mode:       "groups",
		GroupNames: append([]string(nil), policy.AllowedGroupNames...),
		Label:      "Custom model allowlist",
	}
}

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

func (s *Service) invalidateSnapshots(ctx context.Context, tokenHashes ...string) error {
	seen := make(map[string]struct{}, len(tokenHashes))
	for _, tokenHash := range tokenHashes {
		if tokenHash == "" {
			continue
		}
		if _, ok := seen[tokenHash]; ok {
			continue
		}
		seen[tokenHash] = struct{}{}
		if err := s.invalidateSnapshot(ctx, tokenHash); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) invalidateSnapshot(ctx context.Context, tokenHash string) error {
	if s.cache == nil || tokenHash == "" {
		return nil
	}
	if err := s.cache.InvalidateSnapshot(ctx, tokenHash); err != nil {
		return fmt.Errorf("apikeys: invalidate snapshot: %w", err)
	}
	return nil
}

func snapshotRedisKey(tokenHash string) string {
	return "auth:key:{" + tokenHash + "}"
}
