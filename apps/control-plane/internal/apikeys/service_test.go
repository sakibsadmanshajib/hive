package apikeys

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubRepo is a test double that satisfies the Repository interface.
type stubRepo struct {
	keys              map[uuid.UUID]APIKey
	policies          map[uuid.UUID]KeyPolicy
	budgetWindows     map[string]BudgetWindow
	accountRatePolicy map[uuid.UUID]RatePolicy
	keyRatePolicy     map[uuid.UUID]RatePolicy
	keyLimits         map[uuid.UUID]KeyLimits
	tenantByAccount   map[uuid.UUID]uuid.UUID
	// unmappedAccounts opts an account out of the default-mapped behaviour in
	// GetTenantIDByAccountID below, i.e. it models the account that has no
	// public.tenant_billing_accounts row (issue #1330).
	unmappedAccounts map[uuid.UUID]bool
	events           []KeyEvent
	// lifetimeSpend mirrors what RecordUsageFinalization writes to the real
	// 'lifetime' api_key_usage_rollups window: unconditional, summed across
	// every model_alias, independent of budget_kind.
	lifetimeSpend map[uuid.UUID]int64

	// forceGetPolicyErr, when non-nil, is returned by GetPolicyByTokenHash
	// instead of the normal lookup, simulating an infrastructural failure
	// (pool checkout timeout, connection error) rather than a key verdict.
	forceGetPolicyErr error
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		keys:              make(map[uuid.UUID]APIKey),
		policies:          make(map[uuid.UUID]KeyPolicy),
		budgetWindows:     make(map[string]BudgetWindow),
		accountRatePolicy: make(map[uuid.UUID]RatePolicy),
		keyRatePolicy:     make(map[uuid.UUID]RatePolicy),
		keyLimits:         make(map[uuid.UUID]KeyLimits),
		tenantByAccount:   make(map[uuid.UUID]uuid.UUID),
		unmappedAccounts:  make(map[uuid.UUID]bool),
		lifetimeSpend:     make(map[uuid.UUID]int64),
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

func (r *stubRepo) GetKeyByID(_ context.Context, keyID uuid.UUID) (APIKey, error) {
	k, ok := r.keys[keyID]
	if !ok {
		return APIKey{}, ErrNotFound
	}
	return k, nil
}

func (r *stubRepo) GetKey(_ context.Context, accountID, keyID uuid.UUID) (APIKey, error) {
	k, ok := r.keys[keyID]
	if !ok || k.AccountID != accountID {
		return APIKey{}, ErrNotFound
	}
	return k, nil
}

// ListKeys mirrors the pgx repository's contract, kind filter included: the
// real query is `WHERE account_id = $1 AND kind = 'user'`. A stub that returned
// every kind would make the service look like it was doing the filtering, and
// the test above would then pass against a repository that had lost the WHERE
// clause entirely. What this stub cannot prove is the SQL itself; that is
// TestListKeysExcludesAgentTaskKindLive's job, against a real database.
func (r *stubRepo) ListKeys(_ context.Context, accountID uuid.UUID) ([]APIKey, error) {
	var list []APIKey
	for _, k := range r.keys {
		if k.AccountID == accountID && k.Kind == KindUser {
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

func (r *stubRepo) ListPolicies(_ context.Context, accountID uuid.UUID) ([]KeyPolicy, error) {
	var policies []KeyPolicy
	for keyID, policy := range r.policies {
		key, ok := r.keys[keyID]
		if !ok || key.AccountID != accountID {
			continue
		}
		policies = append(policies, policy)
	}
	return policies, nil
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

func (r *stubRepo) ListAllAliases(_ context.Context) ([]string, error) {
	return []string{"hive-default", "hive-fast", "hive-auto"}, nil
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
	if r.forceGetPolicyErr != nil {
		return APIKey{}, KeyPolicy{}, r.forceGetPolicyErr
	}
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

func (r *stubRepo) GetBudgetWindow(_ context.Context, apiKeyID uuid.UUID, budgetKind string, at time.Time) (BudgetWindow, error) {
	windowStart := time.Time{}
	if budgetKind == "monthly" {
		windowStart = time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	window, ok := r.budgetWindows[budgetWindowKey(apiKeyID, budgetKind, windowStart)]
	if !ok {
		return BudgetWindow{
			APIKeyID:    apiKeyID,
			WindowKind:  budgetKind,
			WindowStart: windowStart,
		}, nil
	}
	return window, nil
}

func (r *stubRepo) GetKeyRatePolicy(_ context.Context, apiKeyID uuid.UUID) (RatePolicy, error) {
	if policy, ok := r.keyRatePolicy[apiKeyID]; ok {
		return policy, nil
	}
	return defaultRatePolicy(), nil
}

func (r *stubRepo) GetAccountRatePolicy(_ context.Context, accountID uuid.UUID) (RatePolicy, error) {
	if policy, ok := r.accountRatePolicy[accountID]; ok {
		return policy, nil
	}
	return defaultRatePolicy(), nil
}

// GetTenantIDByAccountID defaults to "mapped" for any account a test has not
// said otherwise about. Minting a key now requires a billing tenant (issue
// #1330), and an account carrying one is the ordinary production state, so
// the default keeps every test that is not about provisioning free of
// fixture noise. Opt out with unmappedAccounts.
func (r *stubRepo) GetTenantIDByAccountID(_ context.Context, accountID uuid.UUID) (uuid.UUID, error) {
	if r.unmappedAccounts[accountID] {
		return uuid.Nil, nil
	}
	if tenantID, ok := r.tenantByAccount[accountID]; ok {
		return tenantID, nil
	}
	tenantID := uuid.New()
	r.tenantByAccount[accountID] = tenantID
	return tenantID, nil
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
	windowStart := time.Time{}
	if budgetKind == "monthly" {
		windowStart = time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	key := budgetWindowKey(apiKeyID, budgetKind, windowStart)
	window := r.budgetWindows[key]
	window.APIKeyID = apiKeyID
	window.WindowKind = budgetKind
	window.WindowStart = windowStart
	window.ReservedCredits += reservedDelta
	window.ConsumedCredits += consumedDelta
	window.UpdatedAt = at
	r.budgetWindows[key] = window
	return nil
}

func (r *stubRepo) RecordUsageFinalization(_ context.Context, apiKeyID uuid.UUID, modelAlias string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, consumedCredits int64, at time.Time) error {
	r.lifetimeSpend[apiKeyID] += consumedCredits
	return nil
}

func (r *stubRepo) GetLifetimeSpend(_ context.Context, apiKeyID uuid.UUID) (int64, error) {
	return r.lifetimeSpend[apiKeyID], nil
}

func (r *stubRepo) MarkLastUsed(_ context.Context, apiKeyID uuid.UUID, usedAt time.Time) error {
	key := r.keys[apiKeyID]
	key.LastUsedAt = &usedAt
	r.keys[apiKeyID] = key
	return nil
}

func (r *stubRepo) GetLimits(_ context.Context, accountID, keyID uuid.UUID) (KeyLimits, error) {
	key, ok := r.keys[keyID]
	if !ok || key.AccountID != accountID {
		return KeyLimits{}, ErrNotFound
	}
	if existing, ok := r.keyLimits[keyID]; ok {
		return existing, nil
	}
	return KeyLimits{
		APIKeyID:      keyID,
		RPM:           60,
		TPM:           120000,
		TierOverrides: map[string]TierLimit{},
	}, nil
}

func (r *stubRepo) UpdateLimits(_ context.Context, accountID, keyID uuid.UUID, input KeyLimitsInput) (KeyLimits, error) {
	if input.RPM < 0 || input.RPM > RateLimitRPMMax {
		return KeyLimits{}, ErrLimitsOutOfRange
	}
	if input.TPM < 0 || input.TPM > RateLimitTPMMax {
		return KeyLimits{}, ErrLimitsOutOfRange
	}
	for tier, lim := range input.TierOverrides {
		if !IsValidTierName(tier) {
			return KeyLimits{}, ErrLimitsOutOfRange
		}
		if lim.RPM < 0 || lim.RPM > RateLimitRPMMax {
			return KeyLimits{}, ErrLimitsOutOfRange
		}
		if lim.TPM < 0 || lim.TPM > RateLimitTPMMax {
			return KeyLimits{}, ErrLimitsOutOfRange
		}
	}
	key, ok := r.keys[keyID]
	if !ok || key.AccountID != accountID {
		return KeyLimits{}, ErrNotFound
	}
	tiers := input.TierOverrides
	if tiers == nil {
		tiers = map[string]TierLimit{}
	}
	out := KeyLimits{
		APIKeyID:      keyID,
		RPM:           input.RPM,
		TPM:           input.TPM,
		TierOverrides: tiers,
	}
	r.keyLimits[keyID] = out
	return out, nil
}

func budgetWindowKey(apiKeyID uuid.UUID, budgetKind string, windowStart time.Time) string {
	return apiKeyID.String() + "|" + budgetKind + "|" + windowStart.Format(time.RFC3339)
}

// --- tests ---

// TestCreateKeyWithIDSecretIsRandomAndUnrelatedToTheIDs guards the one thing
// that would turn the per-task agent credential (#1507) from a fix into a
// vulnerability.
//
// That feature deliberately makes the key ID predictable: it is the agent
// task's own id, so the task row needs no extra column and revocation needs no
// lookup. A predictable id is fine, because the id is not what authenticates.
// The SECRET is. If the secret were derived from the account id and the task
// id, both of which appear in logs, API responses and the task's own URL,
// anyone who could read either could mint the bearer token for a live billing
// account. This test is what stops that from ever quietly becoming true.

// TestListKeysHidesAgentTaskCredentialsButNotLookalikeUserKeys is the guard on
// the discriminator itself.
//
// The per-task agent credential (#1507) puts one api_keys row in the customer's
// account per agent task, which the customer did not create and cannot use. It
// must not appear in their API Keys list. The obvious no-migration filter is a
// nickname prefix, and this test is written so that a nickname filter FAILS it:
// the second key below is a real customer key that happens to be named
// "agent task backfill". Hiding that from its owner is a silent data-loss-shaped
// bug, and no test written only against the agent-task naming would catch it.
func TestListKeysHidesAgentTaskCredentialsButNotLookalikeUserKeys(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	accountID, actorID, taskID := uuid.New(), uuid.New(), uuid.New()

	if _, err := svc.CreateAgentTaskKey(context.Background(), accountID, actorID, taskID, nil); err != nil {
		t.Fatalf("CreateAgentTaskKey: %v", err)
	}
	lookalike, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{Nickname: "agent task backfill"})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	// The kinds are what the service actually decides, and they are the
	// discriminator the SQL filters on. Asserted first and separately from the
	// listing, so a stub that filtered wrongly could not make this pass.
	agentKey, err := svc.GetKey(context.Background(), accountID, taskID)
	if err != nil {
		t.Fatalf("GetKey on the agent-task credential: %v", err)
	}
	if agentKey.Kind != KindAgentTask {
		t.Fatalf("agent-task credential kind = %q, want %q", agentKey.Kind, KindAgentTask)
	}
	if lookalike.Key.Kind != KindUser {
		t.Fatalf("customer key named %q got kind %q, want %q: kind must come from who minted it, never from the name",
			lookalike.Key.Nickname, lookalike.Key.Kind, KindUser)
	}

	listed, err := svc.ListKeys(context.Background(), accountID)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != lookalike.Key.ID {
		var got []string
		for _, k := range listed {
			got = append(got, k.ID.String()+" kind="+string(k.Kind)+" nickname="+k.Nickname)
		}
		t.Fatalf("ListKeys returned %v; want exactly the customer's own key %s. An agent-task credential in a customer's list is a key they cannot use; a customer key missing from it is their key hidden from them.",
			got, lookalike.Key.ID)
	}
	if listed[0].Kind != KindUser {
		t.Fatalf("listed key kind = %q, want %q", listed[0].Kind, KindUser)
	}

	// Reachability by id was already proven above, and it matters: "filter it
	// out of the list" and "make it unreachable" are one careless WHERE clause
	// apart, and an unreachable credential could never be revoked.
}

func TestCreateKeyWithIDSecretIsRandomAndUnrelatedToTheIDs(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()
	firstKeyID := uuid.New()
	secondKeyID := uuid.New()

	first, err := svc.CreateAgentTaskKey(context.Background(), accountID, actorID, firstKeyID, nil)
	if err != nil {
		t.Fatalf("CreateAgentTaskKey: %v", err)
	}
	second, err := svc.CreateAgentTaskKey(context.Background(), accountID, actorID, secondKeyID, nil)
	if err != nil {
		t.Fatalf("CreateAgentTaskKey: %v", err)
	}

	// The caller-chosen id is honoured, which is the whole point of the
	// method, asserted here so the randomness checks below cannot pass by the
	// method silently ignoring the id it was given.
	if first.Key.ID != firstKeyID || second.Key.ID != secondKeyID {
		t.Fatalf("key ids = %s, %s; want %s, %s", first.Key.ID, second.Key.ID, firstKeyID, secondKeyID)
	}

	// The identifiers must not appear in the secret in any form the caller
	// could reconstruct: full uuid text, and the hex with dashes stripped.
	for _, id := range []uuid.UUID{accountID, actorID, firstKeyID} {
		text := id.String()
		stripped := strings.ReplaceAll(text, "-", "")
		if strings.Contains(first.Secret, text) || strings.Contains(first.Secret, stripped) {
			t.Fatalf("secret embeds identifier %s: a bearer token derivable from ids that appear in logs and API responses is forgeable", text)
		}
	}

	// Same account, same actor, adjacent calls: the secrets must still differ,
	// which no derivation from the account or actor could guarantee.
	if first.Secret == second.Secret {
		t.Fatal("two credentials on the same account share a secret")
	}
	if first.Key.TokenHash == second.Key.TokenHash {
		t.Fatal("two credentials on the same account share a token hash")
	}
	// Only the hash is stored. Asserted explicitly because the whole
	// per-task-credential design assumes a stolen database row cannot be
	// replayed as a bearer token.
	if strings.Contains(first.Key.TokenHash, first.Secret) {
		t.Fatal("stored token hash contains the raw secret")
	}
}

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

func TestCreateKeyCreatesDefaultPolicyRow(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "default-policy",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	policy, ok := repo.policies[result.Key.ID]
	if !ok {
		t.Fatal("expected default policy row to be created")
	}
	if policy.AllowAllModels {
		t.Fatal("default policy must not allow all models")
	}
	if len(policy.AllowedGroupNames) != 1 || policy.AllowedGroupNames[0] != "default" {
		t.Fatalf("expected default group policy, got %#v", policy.AllowedGroupNames)
	}
	if policy.BudgetKind != "none" {
		t.Fatalf("expected no budget cap by default, got %q", policy.BudgetKind)
	}
}

func TestUpdatePolicyResolvesGroupMembersAndOverrides(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "policy-overrides",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	budgetKind := "monthly"
	_, err = svc.UpdatePolicy(context.Background(), accountID, actorID, result.Key.ID, UpdatePolicyInput{
		AllowedGroupNames: []string{"default", "premium"},
		AllowedAliases:    []string{"hive-oss"},
		DeniedAliases:     []string{"hive-fast"},
		BudgetKind:        &budgetKind,
	})
	if err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	snapshot, err := svc.ResolveSnapshot(context.Background(), result.Key.TokenHash)
	if err != nil {
		t.Fatalf("ResolveSnapshot: %v", err)
	}

	expected := []string{"hive-default", "hive-auto", "hive-oss"}
	if len(snapshot.AllowedAliases) != len(expected) {
		t.Fatalf("expected %d aliases, got %#v", len(expected), snapshot.AllowedAliases)
	}
	for i := range expected {
		if snapshot.AllowedAliases[i] != expected[i] {
			t.Fatalf("expected aliases %v, got %v", expected, snapshot.AllowedAliases)
		}
	}
}

func TestResolveSnapshotReturnsAllModelsWhenAllowAllModelsIsSet(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "all-models",
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

	snapshot, err := svc.ResolveSnapshot(context.Background(), result.Key.TokenHash)
	if err != nil {
		t.Fatalf("ResolveSnapshot: %v", err)
	}

	expected := []string{"hive-default", "hive-fast", "hive-auto"}
	if len(snapshot.AllowedAliases) != len(expected) {
		t.Fatalf("expected all aliases %v, got %v", expected, snapshot.AllowedAliases)
	}
	for i := range expected {
		if snapshot.AllowedAliases[i] != expected[i] {
			t.Fatalf("expected aliases %v, got %v", expected, snapshot.AllowedAliases)
		}
	}
}

func TestResolveSnapshotReturnsExpiredWhenExpiresAtHasPassed(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()
	expiresAt := time.Now().Add(-5 * time.Minute)

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname:  "expired-snapshot",
		ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	snapshot, err := svc.ResolveSnapshot(context.Background(), result.Key.TokenHash)
	if err != nil {
		t.Fatalf("ResolveSnapshot: %v", err)
	}

	if snapshot.Status != KeyStatusExpired {
		t.Fatalf("expected expired snapshot status, got %s", snapshot.Status)
	}
}

func TestListKeyViewsExposeDefaultSummaries(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	_, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "launch-safe",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	views, err := svc.ListKeyViews(context.Background(), accountID)
	if err != nil {
		t.Fatalf("ListKeyViews: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}

	view := views[0]
	if view.ExpirationSummary.Kind != "never" || view.ExpirationSummary.Label != "Never expires" {
		t.Fatalf("unexpected expiration summary: %#v", view.ExpirationSummary)
	}
	if view.BudgetSummary.Kind != "none" || view.BudgetSummary.Label != "No budget cap" {
		t.Fatalf("unexpected budget summary: %#v", view.BudgetSummary)
	}
	if view.AllowlistSummary.Mode != "groups" || view.AllowlistSummary.Label != "Default launch-safe models" {
		t.Fatalf("unexpected allowlist summary: %#v", view.AllowlistSummary)
	}
	if len(view.AllowlistSummary.GroupNames) != 1 || view.AllowlistSummary.GroupNames[0] != "default" {
		t.Fatalf("unexpected allowlist groups: %#v", view.AllowlistSummary.GroupNames)
	}
	if view.SpendCredits != 0 {
		t.Fatalf("expected zero spend for a fresh key, got %d", view.SpendCredits)
	}
	if view.BudgetLimitCredits != nil {
		t.Fatalf("expected no budget limit for a fresh key, got %#v", *view.BudgetLimitCredits)
	}
}

// TestKeyViewReportsLifetimeSpendRegardlessOfBudgetKind pins the reason
// GetLifetimeSpend reads api_key_usage_rollups rather than
// api_key_budget_windows: ApplyReservationDelta (which is what writes
// api_key_budget_windows) returns early for a "none" budget_kind and never
// writes anything, so that table alone cannot report spend for a key that
// carries no cap. RecordUsageFinalization has no such early return.
func TestKeyViewReportsLifetimeSpendRegardlessOfBudgetKind(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	created, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "spend-key",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	keyID := created.Key.ID

	now := time.Now()
	if err := svc.RecordUsageFinalization(context.Background(), keyID, "hive-fast", 1000, 200, 0, 0, 750, now); err != nil {
		t.Fatalf("RecordUsageFinalization (first): %v", err)
	}
	if err := svc.RecordUsageFinalization(context.Background(), keyID, "hive-default", 500, 100, 0, 0, 250, now); err != nil {
		t.Fatalf("RecordUsageFinalization (second): %v", err)
	}

	view, err := svc.GetKeyView(context.Background(), accountID, keyID)
	if err != nil {
		t.Fatalf("GetKeyView: %v", err)
	}
	if view.SpendCredits != 1000 {
		t.Fatalf("expected lifetime spend to sum across model aliases (750+250=1000), got %d", view.SpendCredits)
	}
	// This key never had a budget_kind set (default "none"); spend must
	// still be reported, matching the money-path rule that display never
	// depends on enforcement being turned on.
	if view.BudgetSummary.Kind != "none" {
		t.Fatalf("expected budget_kind to stay none, got %q", view.BudgetSummary.Kind)
	}

	limit := int64(5000)
	kind := "lifetime"
	if _, err := svc.UpdatePolicy(context.Background(), accountID, actorID, keyID, UpdatePolicyInput{
		BudgetKind:         &kind,
		BudgetLimitCredits: &limit,
	}); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	view, err = svc.GetKeyView(context.Background(), accountID, keyID)
	if err != nil {
		t.Fatalf("GetKeyView after policy update: %v", err)
	}
	if view.SpendCredits != 1000 {
		t.Fatalf("expected spend to be unaffected by a later budget_kind change, got %d", view.SpendCredits)
	}
	if view.BudgetLimitCredits == nil || *view.BudgetLimitCredits != 5000 {
		t.Fatalf("expected budget limit 5000 to round-trip, got %#v", view.BudgetLimitCredits)
	}
}

func TestResolveSnapshotIncludesLiveBudgetWindow(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()
	limit := int64(1000)
	budgetKind := "monthly"

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "budgeted",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	repo.policies[result.Key.ID] = KeyPolicy{
		APIKeyID:           result.Key.ID,
		AllowedGroupNames:  []string{"default"},
		BudgetKind:         budgetKind,
		BudgetLimitCredits: &limit,
		PolicyVersion:      2,
	}
	now := time.Now().UTC()
	windowStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	repo.budgetWindows[budgetWindowKey(result.Key.ID, budgetKind, windowStart)] = BudgetWindow{
		APIKeyID:        result.Key.ID,
		WindowKind:      budgetKind,
		WindowStart:     windowStart,
		ConsumedCredits: 420,
		ReservedCredits: 80,
	}

	snapshot, err := svc.ResolveSnapshot(context.Background(), result.Key.TokenHash)
	if err != nil {
		t.Fatalf("ResolveSnapshot: %v", err)
	}
	if snapshot.BudgetConsumedCredits != 420 {
		t.Fatalf("expected consumed credits 420, got %d", snapshot.BudgetConsumedCredits)
	}
	if snapshot.BudgetReservedCredits != 80 {
		t.Fatalf("expected reserved credits 80, got %d", snapshot.BudgetReservedCredits)
	}
}

func TestResolveSnapshotReturnsSeparateAccountAndKeyRatePolicies(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "rate-policies",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	repo.accountRatePolicy[accountID] = RatePolicy{
		RateLimitRPM:          300,
		RateLimitTPM:          400000,
		RollingFiveHourLimit:  5000,
		WeeklyLimit:           10000,
		FreeTokenWeightTenths: 2,
	}
	repo.keyRatePolicy[result.Key.ID] = RatePolicy{
		RateLimitRPM:          30,
		RateLimitTPM:          90000,
		RollingFiveHourLimit:  700,
		WeeklyLimit:           1300,
		FreeTokenWeightTenths: 5,
	}

	snapshot, err := svc.ResolveSnapshot(context.Background(), result.Key.TokenHash)
	if err != nil {
		t.Fatalf("ResolveSnapshot: %v", err)
	}
	if snapshot.AccountRatePolicy == nil || snapshot.KeyRatePolicy == nil {
		t.Fatalf("expected separate account and key rate policies, got %#v %#v", snapshot.AccountRatePolicy, snapshot.KeyRatePolicy)
	}
	if snapshot.AccountRatePolicy.RateLimitRPM != 300 {
		t.Fatalf("expected account rpm 300, got %d", snapshot.AccountRatePolicy.RateLimitRPM)
	}
	if snapshot.KeyRatePolicy.RateLimitRPM != 30 {
		t.Fatalf("expected key rpm 30, got %d", snapshot.KeyRatePolicy.RateLimitRPM)
	}
	if snapshot.AccountRatePolicy.RateLimitRPM == snapshot.KeyRatePolicy.RateLimitRPM {
		t.Fatal("expected account and key rate policies to remain distinct")
	}
}

// TestResolveSnapshotIncludesMappedTenantID is the D-030 regression guard:
// ResolveSnapshot must bind the tenant that bills the key's account (via
// GetTenantIDByAccountID / public.tenant_billing_accounts) onto the
// snapshot, so edge-api can finally enforce tenant-scoped model
// entitlement for API-key traffic the same way it already does for JWT
// sessions.
func TestResolveSnapshotIncludesMappedTenantID(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()
	tenantID := uuid.New()
	repo.tenantByAccount[accountID] = tenantID

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "tenant-mapped",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	snapshot, err := svc.ResolveSnapshot(context.Background(), result.Key.TokenHash)
	if err != nil {
		t.Fatalf("ResolveSnapshot: %v", err)
	}
	if snapshot.TenantID != tenantID {
		t.Fatalf("expected tenant_id %s, got %s", tenantID, snapshot.TenantID)
	}
}

// TestResolveSnapshotReturnsNilTenantIDWhenAccountUnmapped covers the
// residual case (an account with no public.tenant_billing_accounts row,
// e.g. ambiguous multi-account tenants the backfill migration left
// unmapped): ResolveSnapshot itself still succeeds -- an unmapped tenant is
// not an error here -- it is edge-api's consumers (handleModels,
// inference.Orchestrator.selectRoute) that fail the request closed on a
// uuid.Nil TenantID.
func TestResolveSnapshotReturnsNilTenantIDWhenAccountUnmapped(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "tenant-unmapped",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	// Minting now requires a mapping (issue #1330), but resolving must still
	// tolerate its absence: every key issued before that gate existed is
	// sitting on an account that may never have had one.
	repo.unmappedAccounts[accountID] = true

	snapshot, err := svc.ResolveSnapshot(context.Background(), result.Key.TokenHash)
	if err != nil {
		t.Fatalf("ResolveSnapshot: %v", err)
	}
	if snapshot.TenantID != uuid.Nil {
		t.Fatalf("expected uuid.Nil tenant_id for an unmapped account, got %s", snapshot.TenantID)
	}
}

func TestApplyReservationDeltaUsesConfiguredMonthlyWindow(t *testing.T) {
	repo := newStubRepo()
	cache := newSnapshotCacheSpy()
	svc := NewService(repo, cache)

	accountID := uuid.New()
	actorID := uuid.New()
	limit := int64(500)

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "budget-window",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	repo.policies[result.Key.ID] = KeyPolicy{
		APIKeyID:           result.Key.ID,
		AllowedGroupNames:  []string{"default"},
		BudgetKind:         "monthly",
		BudgetLimitCredits: &limit,
		PolicyVersion:      2,
	}

	at := time.Date(2026, time.April, 15, 13, 30, 0, 0, time.UTC)
	if err := svc.ApplyReservationDelta(context.Background(), result.Key.ID, 75, 25, at); err != nil {
		t.Fatalf("ApplyReservationDelta: %v", err)
	}

	windowStart := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	window, ok := repo.budgetWindows[budgetWindowKey(result.Key.ID, "monthly", windowStart)]
	if !ok {
		t.Fatal("expected monthly budget window to be updated")
	}
	if window.ReservedCredits != 75 {
		t.Fatalf("expected reserved credits 75, got %d", window.ReservedCredits)
	}
	if window.ConsumedCredits != 25 {
		t.Fatalf("expected consumed credits 25, got %d", window.ConsumedCredits)
	}
	if len(cache.invalidated) != 1 || cache.invalidated[0] != result.Key.TokenHash {
		t.Fatalf("expected snapshot invalidation for %q, got %#v", result.Key.TokenHash, cache.invalidated)
	}
}

func TestBudgetAffectingDeltaInvalidatesSnapshot(t *testing.T) {
	repo := newStubRepo()
	cache := newSnapshotCacheSpy()
	svc := NewService(repo, cache)

	accountID := uuid.New()
	actorID := uuid.New()
	limit := int64(500)

	result, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{
		Nickname: "invalidate-budget",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	repo.policies[result.Key.ID] = KeyPolicy{
		APIKeyID:           result.Key.ID,
		AllowedGroupNames:  []string{"default"},
		BudgetKind:         "monthly",
		BudgetLimitCredits: &limit,
		PolicyVersion:      2,
	}
	cache.errByHash[result.Key.TokenHash] = errors.New("redis unavailable")

	at := time.Date(2026, time.April, 15, 9, 0, 0, 0, time.UTC)
	err = svc.ApplyReservationDelta(context.Background(), result.Key.ID, 40, 10, at)
	if err == nil {
		t.Fatal("expected snapshot invalidation failure to be returned")
	}

	windowStart := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	window, ok := repo.budgetWindows[budgetWindowKey(result.Key.ID, "monthly", windowStart)]
	if !ok {
		t.Fatal("expected durable budget window update before invalidation failure")
	}
	if window.ReservedCredits != 40 || window.ConsumedCredits != 10 {
		t.Fatalf("expected durable window update, got %+v", window)
	}
	if len(cache.invalidated) != 1 || cache.invalidated[0] != result.Key.TokenHash {
		t.Fatalf("expected invalidation attempt for %q, got %#v", result.Key.TokenHash, cache.invalidated)
	}
}

// TestKeyViewHidesALimitThatIsNotEnforced pins that a credit limit is only
// reported when it is actually enforced. UpdatePolicy upserts
// budget_limit_credits with COALESCE, so a nil on the way in means "leave
// unchanged" rather than "clear": a key moved to budget_kind "none" keeps
// whatever ceiling it last carried in the row, while edge-api authz
// CheckAccess ignores it (it requires budget_kind != "none" AND a non-nil
// limit). Reporting the stale value would show a ceiling next to a key that
// is not capped at all.
//
// Asserted against buildKeyView directly, not through svc.UpdatePolicy: the
// in-package stub repository overwrites budget_limit_credits instead of
// COALESCEing it, so a stub-driven test would report nil either way and could
// not go red for the defect this guards.
func TestKeyViewHidesALimitThatIsNotEnforced(t *testing.T) {
	limit := int64(10_000_000_000)

	for _, kind := range []string{"", "none"} {
		key := APIKey{ID: uuid.New(), Status: KeyStatusActive}
		policy := KeyPolicy{BudgetKind: kind, BudgetLimitCredits: &limit}
		view := buildKeyView(key, policy, 0)
		if view.BudgetLimitCredits != nil {
			t.Fatalf("budget_kind %q enforces nothing, so no limit may be reported, got %d", kind, *view.BudgetLimitCredits)
		}
		if view.BudgetSummary.Kind != "none" {
			t.Fatalf("budget_kind %q should summarise as none, got %q", kind, view.BudgetSummary.Kind)
		}
	}

	for _, kind := range []string{"lifetime", "monthly"} {
		key := APIKey{ID: uuid.New(), Status: KeyStatusActive}
		policy := KeyPolicy{BudgetKind: kind, BudgetLimitCredits: &limit}
		view := buildKeyView(key, policy, 0)
		if view.BudgetLimitCredits == nil || *view.BudgetLimitCredits != limit {
			t.Fatalf("an enforced %q cap must be reported unchanged, got %#v", kind, view.BudgetLimitCredits)
		}
	}
}

// TestKeyViewBudgetSpendIsTheEnforcedWindowNotLifetimeSpend pins the counter a
// percentage-of-cap surface has to divide (issue #1683).
//
// The two counters are written by different paths. RecordUsageFinalization
// writes api_key_usage_rollups on every settled request; only
// ApplyReservationDelta writes api_key_budget_windows, and it returns early
// while the budget kind is "none". Nothing backfills the window when a cap is
// set later, so a key that spends while uncapped and is capped afterwards has
// a large lifetime spend and an empty window, and edge-api's CheckAccess
// (consumed + reserved + estimated > limit) serves its next request. A view
// that reported the lifetime figure as the amount used against the cap would
// paint that key as refused while the gateway was still serving it.
func TestKeyViewBudgetSpendIsTheEnforcedWindowNotLifetimeSpend(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	ctx := context.Background()
	accountID := uuid.New()
	actorID := uuid.New()
	now := time.Now()

	created, err := svc.CreateKey(ctx, accountID, actorID, CreateKeyInput{Nickname: "capped-later"})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	keyID := created.Key.ID

	// Uncapped spend. The rollup takes it; the budget window cannot, because
	// ApplyReservationDelta returns early for a "none" budget kind.
	if err := svc.RecordUsageFinalization(ctx, keyID, "hive-fast", 1000, 200, 0, 0, 900, now); err != nil {
		t.Fatalf("RecordUsageFinalization: %v", err)
	}
	if err := svc.ApplyReservationDelta(ctx, keyID, 0, 900, now); err != nil {
		t.Fatalf("ApplyReservationDelta while uncapped: %v", err)
	}

	view, err := svc.GetKeyView(ctx, accountID, keyID)
	if err != nil {
		t.Fatalf("GetKeyView: %v", err)
	}
	if view.BudgetSpendCredits != nil {
		t.Fatalf("expected no budget spend for an uncapped key, got %d", *view.BudgetSpendCredits)
	}

	limit := int64(500)
	kind := "lifetime"
	if _, err := svc.UpdatePolicy(ctx, accountID, actorID, keyID, UpdatePolicyInput{
		BudgetKind:         &kind,
		BudgetLimitCredits: &limit,
	}); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	view, err = svc.GetKeyView(ctx, accountID, keyID)
	if err != nil {
		t.Fatalf("GetKeyView after capping: %v", err)
	}
	if view.SpendCredits != 900 {
		t.Fatalf("expected the lifetime figure to keep the pre-cap spend, got %d", view.SpendCredits)
	}
	if view.BudgetSpendCredits == nil {
		t.Fatalf("expected a budget spend figure once the key carries a cap, got nil")
	}
	// 900 against a 500 cap would read as 180 percent and refused. The window
	// is empty, and the gateway agrees: it would serve this key's next request.
	if *view.BudgetSpendCredits != 0 {
		t.Fatalf("expected the enforced window to be empty for spend that predates the cap, got %d", *view.BudgetSpendCredits)
	}

	// A request made after the cap exists reaches the window, and reserved
	// credits count towards the same total edge-api compares against.
	if err := svc.ApplyReservationDelta(ctx, keyID, 0, 200, now); err != nil {
		t.Fatalf("ApplyReservationDelta after capping: %v", err)
	}
	if err := svc.ApplyReservationDelta(ctx, keyID, 50, 0, now); err != nil {
		t.Fatalf("ApplyReservationDelta (reservation): %v", err)
	}

	view, err = svc.GetKeyView(ctx, accountID, keyID)
	if err != nil {
		t.Fatalf("GetKeyView after post-cap usage: %v", err)
	}
	if view.BudgetSpendCredits == nil || *view.BudgetSpendCredits != 250 {
		t.Fatalf("expected consumed 200 plus reserved 50, got %v", view.BudgetSpendCredits)
	}

	snapshot, err := svc.ResolveSnapshot(ctx, created.Key.TokenHash)
	if err != nil {
		t.Fatalf("ResolveSnapshot: %v", err)
	}
	if snapshot.BudgetConsumedCredits+snapshot.BudgetReservedCredits != *view.BudgetSpendCredits {
		t.Fatalf(
			"the view must report what enforcement reads: snapshot %d+%d, view %d",
			snapshot.BudgetConsumedCredits,
			snapshot.BudgetReservedCredits,
			*view.BudgetSpendCredits,
		)
	}
}
