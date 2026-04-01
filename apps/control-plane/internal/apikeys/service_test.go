package apikeys

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubRepo is a test double that satisfies the Repository interface.
type stubRepo struct {
	keys     map[uuid.UUID]APIKey
	policies map[uuid.UUID]KeyPolicy
	events   []KeyEvent
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		keys:     make(map[uuid.UUID]APIKey),
		policies: make(map[uuid.UUID]KeyPolicy),
	}
}

type snapshotCacheSpy struct {
	invalidated []string
	errByHash   map[string]error
}

func newSnapshotCacheSpy() *snapshotCacheSpy {
	return &snapshotCacheSpy{
		errByHash: make(map[string]error),
	}
}

func (s *snapshotCacheSpy) InvalidateSnapshot(_ context.Context, tokenHash string) error {
	s.invalidated = append(s.invalidated, tokenHash)
	if err, ok := s.errByHash[tokenHash]; ok {
		return err
	}
	return nil
}

func (r *stubRepo) CreateKey(_ context.Context, key APIKey) (APIKey, error) {
	now := time.Now()
	key.CreatedAt = now
	key.UpdatedAt = now
	r.keys[key.ID] = key
	return key, nil
}

func (r *stubRepo) GetKey(_ context.Context, accountID, keyID uuid.UUID) (APIKey, error) {
	k, ok := r.keys[keyID]
	if !ok || k.AccountID != accountID {
		return APIKey{}, ErrNotFound
	}
	return k, nil
}

func (r *stubRepo) ListKeys(_ context.Context, accountID uuid.UUID) ([]APIKey, error) {
	var list []APIKey
	for _, k := range r.keys {
		if k.AccountID == accountID {
			list = append(list, k)
		}
	}
	return list, nil
}

func (r *stubRepo) UpdateKeyState(_ context.Context, accountID, keyID uuid.UUID, status KeyStatus, disabledAt, revokedAt *time.Time, replacedBy *uuid.UUID) (APIKey, error) {
	k, ok := r.keys[keyID]
	if !ok || k.AccountID != accountID {
		return APIKey{}, ErrNotFound
	}
	k.Status = status
	k.DisabledAt = disabledAt
	k.RevokedAt = revokedAt
	k.ReplacedByKeyID = replacedBy
	k.UpdatedAt = time.Now()
	r.keys[keyID] = k
	return k, nil
}

func (r *stubRepo) InsertEvent(_ context.Context, event KeyEvent) error {
	r.events = append(r.events, event)
	return nil
}

func (r *stubRepo) CreateReplacementKey(_ context.Context, oldKeyID uuid.UUID, newKey APIKey, rotatedAt time.Time) (APIKey, APIKey, error) {
	now := time.Now()
	newKey.CreatedAt = now
	newKey.UpdatedAt = now
	r.keys[newKey.ID] = newKey

	old := r.keys[oldKeyID]
	old.Status = KeyStatusRevoked
	old.RevokedAt = &rotatedAt
	old.ReplacedByKeyID = &newKey.ID
	old.UpdatedAt = now
	r.keys[oldKeyID] = old

	return old, newKey, nil
}

func (r *stubRepo) UpsertPolicy(_ context.Context, _, keyID uuid.UUID, input UpdatePolicyInput) (KeyPolicy, error) {
	p := KeyPolicy{
		APIKeyID:      keyID,
		BudgetKind:    "none",
		PolicyVersion: 1,
	}
	if input.AllowAllModels != nil {
		p.AllowAllModels = *input.AllowAllModels
	}
	if input.AllowedGroupNames != nil {
		p.AllowedGroupNames = input.AllowedGroupNames
	}
	if input.AllowedAliases != nil {
		p.AllowedAliases = input.AllowedAliases
	}
	if input.DeniedAliases != nil {
		p.DeniedAliases = input.DeniedAliases
	}
	if input.BudgetKind != nil {
		p.BudgetKind = *input.BudgetKind
	}
	p.BudgetLimitCredits = input.BudgetLimitCredits
	p.BudgetAnchorAt = input.BudgetAnchorAt
	if pol, exists := r.policies[keyID]; exists {
		p.PolicyVersion = pol.PolicyVersion + 1
	}
	r.policies[keyID] = p
	return p, nil
}

func (r *stubRepo) GetPolicy(_ context.Context, _, keyID uuid.UUID) (KeyPolicy, error) {
	p, ok := r.policies[keyID]
	if !ok {
		return KeyPolicy{}, ErrNotFound
	}
	return p, nil
}

func (r *stubRepo) ListGroupMembers(_ context.Context, groupNames []string) ([]string, error) {
	// For stubs, return hardcoded defaults matching the seed data.
	groups := map[string][]string{
		"default": {"hive-default", "hive-fast"},
		"premium": {"hive-auto"},
		"oss":     {},
		"closed":  {"hive-default", "hive-fast", "hive-auto"},
	}
	seen := make(map[string]bool)
	var result []string
	for _, g := range groupNames {
		for _, a := range groups[g] {
			if !seen[a] {
				seen[a] = true
				result = append(result, a)
			}
		}
	}
	return result, nil
}

func (r *stubRepo) GetByTokenHash(_ context.Context, tokenHash string) (APIKey, error) {
	for _, k := range r.keys {
		if k.TokenHash == tokenHash {
			return k, nil
		}
	}
	return APIKey{}, ErrNotFound
}

func (r *stubRepo) GetPolicyByTokenHash(_ context.Context, tokenHash string) (APIKey, KeyPolicy, error) {
	var key APIKey
	found := false
	for _, k := range r.keys {
		if k.TokenHash == tokenHash {
			key = k
			found = true
			break
		}
	}
	if !found {
		return APIKey{}, KeyPolicy{}, ErrNotFound
	}
	p, ok := r.policies[key.ID]
	if !ok {
		p = KeyPolicy{
			APIKeyID:          key.ID,
			AllowedGroupNames: []string{"default"},
			BudgetKind:        "none",
			PolicyVersion:     1,
		}
	}
	return key, p, nil
}

func (r *stubRepo) CreateDefaultPolicy(_ context.Context, keyID uuid.UUID) error {
	if _, exists := r.policies[keyID]; !exists {
		r.policies[keyID] = KeyPolicy{
			APIKeyID:          keyID,
			AllowedGroupNames: []string{"default"},
			BudgetKind:        "none",
			PolicyVersion:     1,
		}
	}
	return nil
}

func (r *stubRepo) ApplyReservationDelta(_ context.Context, apiKeyID uuid.UUID, budgetKind string, reservedDelta int64, consumedDelta int64, at time.Time) error {
	return nil
}

func (r *stubRepo) RecordUsageFinalization(_ context.Context, apiKeyID uuid.UUID, modelAlias string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, consumedCredits int64, at time.Time) error {
	return nil
}

func (r *stubRepo) MarkLastUsed(_ context.Context, apiKeyID uuid.UUID, usedAt time.Time) error {
	key := r.keys[apiKeyID]
	key.LastUsedAt = &usedAt
	r.keys[apiKeyID] = key
	return nil
}

// --- tests ---

func TestCreateKeyStoresHashAndRedactedSuffixOnly(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "test-key",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	// Secret should start with hk_
	if result.Secret[:3] != "hk_" {
		t.Fatalf("expected secret to start with hk_, got prefix %q", result.Secret[:3])
	}

	// Stored key should have hash, not raw secret
	stored := repo.keys[result.Key.ID]
	if stored.TokenHash == result.Secret {
		t.Fatal("stored token_hash must not equal the raw secret")
	}

	// Verify hash matches
	expectedHash := HashSecret(result.Secret)
	if stored.TokenHash != expectedHash {
		t.Fatalf("hash mismatch: stored=%s expected=%s", stored.TokenHash, expectedHash)
	}

	// Redacted suffix should be last 6 chars
	if stored.RedactedSuffix != result.Secret[len(result.Secret)-6:] {
		t.Fatalf("redacted suffix mismatch: %s vs %s", stored.RedactedSuffix, result.Secret[len(result.Secret)-6:])
	}
}

func TestRotateKeyCreatesReplacementAndRevokesSource(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	// Create the original key.
	createResult, _ := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "original",
	})

	// Rotate it.
	rotateResult, err := svc.RotateKey(context.Background(), accountID, actorID, createResult.Key.ID, "rotated", nil)
	if err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	// Old key must be revoked.
	if rotateResult.OldKey.Status != KeyStatusRevoked {
		t.Fatalf("expected old key status revoked, got %s", rotateResult.OldKey.Status)
	}

	// New key must be active.
	if rotateResult.NewKey.Status != KeyStatusActive {
		t.Fatalf("expected new key status active, got %s", rotateResult.NewKey.Status)
	}

	// New key must have a different secret.
	if rotateResult.Secret[:3] != "hk_" {
		t.Fatalf("new secret must start with hk_")
	}

	// Old key must point to the new key.
	if rotateResult.OldKey.ReplacedByKeyID == nil || *rotateResult.OldKey.ReplacedByKeyID != rotateResult.NewKey.ID {
		t.Fatal("old key must have replaced_by_key_id set to new key ID")
	}
}

func TestDisableAndEnableKeyTransitionsState(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	// Create a key.
	result, _ := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "toggle",
	})
	keyID := result.Key.ID

	// Disable it.
	disabled, err := svc.DisableKey(context.Background(), accountID, actorID, keyID)
	if err != nil {
		t.Fatalf("DisableKey: %v", err)
	}
	if disabled.Status != KeyStatusDisabled {
		t.Fatalf("expected disabled, got %s", disabled.Status)
	}

	// Enable it again.
	enabled, err := svc.EnableKey(context.Background(), accountID, actorID, keyID)
	if err != nil {
		t.Fatalf("EnableKey: %v", err)
	}
	if enabled.Status != KeyStatusActive {
		t.Fatalf("expected active after enable, got %s", enabled.Status)
	}
}

func TestExpiredKeyIsReportedWithoutMutatingSiblingKeys(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	// Create an expired key.
	past := time.Now().Add(-1 * time.Hour)
	expiredResult, _ := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname:  "expired",
		ExpiresAt: &past,
	})

	// Create a sibling key that does not expire.
	siblingResult, _ := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "sibling",
	})

	// List keys.
	keys, err := svc.ListKeys(context.Background(), accountID)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}

	for _, k := range keys {
		if k.ID == expiredResult.Key.ID {
			if k.Status != KeyStatusExpired {
				t.Fatalf("expected expired key status expired, got %s", k.Status)
			}
		}
		if k.ID == siblingResult.Key.ID {
			if k.Status != KeyStatusActive {
				t.Fatalf("sibling key should still be active, got %s", k.Status)
			}
		}
	}

	// Verify the stored status is still 'active' (not mutated in DB).
	stored := repo.keys[expiredResult.Key.ID]
	if stored.Status != KeyStatusActive {
		t.Fatalf("stored status should still be active, got %s", stored.Status)
	}
}

func TestRevokeKeyIsTerminal(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	result, _ := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "to-revoke",
	})

	_, err := svc.RevokeKey(context.Background(), accountID, actorID, result.Key.ID)
	if err != nil {
		t.Fatalf("first RevokeKey: %v", err)
	}

	// Second revoke should fail.
	_, err = svc.RevokeKey(context.Background(), accountID, actorID, result.Key.ID)
	if err != ErrRevoked {
		t.Fatalf("expected ErrRevoked on double revoke, got %v", err)
	}

	// Enable should fail on revoked key.
	_, err = svc.EnableKey(context.Background(), accountID, actorID, result.Key.ID)
	if err != ErrRevoked {
		t.Fatalf("expected ErrRevoked on enable-after-revoke, got %v", err)
	}
}

func TestRevokeKeyInvalidatesCachedSnapshot(t *testing.T) {
	repo := newStubRepo()
	cache := newSnapshotCacheSpy()
	svc := NewService(repo, cache)

	accountID := uuid.New()
	actorID := uuid.New()

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "to-revoke",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	_, err = svc.RevokeKey(context.Background(), accountID, actorID, result.Key.ID)
	if err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	if len(cache.invalidated) != 1 || cache.invalidated[0] != result.Key.TokenHash {
		t.Fatalf("expected revoke to invalidate %q, got %#v", result.Key.TokenHash, cache.invalidated)
	}
}

func TestRevokeKeyReturnsErrorWhenSnapshotInvalidationFails(t *testing.T) {
	repo := newStubRepo()
	cache := newSnapshotCacheSpy()
	svc := NewService(repo, cache)

	accountID := uuid.New()
	actorID := uuid.New()

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "to-revoke",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	cache.errByHash[result.Key.TokenHash] = errors.New("redis unavailable")

	_, err = svc.RevokeKey(context.Background(), accountID, actorID, result.Key.ID)
	if err == nil {
		t.Fatal("expected invalidate failure to be returned")
	}

	stored := repo.keys[result.Key.ID]
	if stored.Status != KeyStatusRevoked {
		t.Fatalf("expected durable revoke despite invalidate failure, got %s", stored.Status)
	}
}

func TestRotateKeyInvalidatesOldAndNewSnapshots(t *testing.T) {
	repo := newStubRepo()
	cache := newSnapshotCacheSpy()
	svc := NewService(repo, cache)

	accountID := uuid.New()
	actorID := uuid.New()

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "rotate-me",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	rotated, err := svc.RotateKey(context.Background(), accountID, actorID, result.Key.ID, "rotated", nil)
	if err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	if len(cache.invalidated) != 2 {
		t.Fatalf("expected old and new snapshots invalidated, got %#v", cache.invalidated)
	}
	if cache.invalidated[0] != rotated.OldKey.TokenHash {
		t.Fatalf("expected first invalidated hash %q, got %q", rotated.OldKey.TokenHash, cache.invalidated[0])
	}
	if cache.invalidated[1] != rotated.NewKey.TokenHash {
		t.Fatalf("expected second invalidated hash %q, got %q", rotated.NewKey.TokenHash, cache.invalidated[1])
	}
}

func TestUpdatePolicyInvalidatesCachedSnapshot(t *testing.T) {
	repo := newStubRepo()
	cache := newSnapshotCacheSpy()
	svc := NewService(repo, cache)

	accountID := uuid.New()
	actorID := uuid.New()

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "policy-key",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	allowAll := true
	_, err = svc.UpdatePolicy(context.Background(), accountID, actorID, result.Key.ID, UpdatePolicyInput{
		AllowAllModels: &allowAll,
	})
	if err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	if len(cache.invalidated) != 1 || cache.invalidated[0] != result.Key.TokenHash {
		t.Fatalf("expected policy update to invalidate %q, got %#v", result.Key.TokenHash, cache.invalidated)
	}
}
