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
		spend, err := s.repo.GetLifetimeSpend(ctx, key.ID)
		if err != nil {
			return nil, fmt.Errorf("apikeys: get lifetime spend: %w", err)
		}
		view := buildKeyView(key, policy, spend)
		// One more per-key read, on top of the policy and lifetime-spend reads
		// this loop already does. A key list is a page of a customer's own
		// keys, so the row count is small; batch all three into one query if
		// that ever stops being true.
		view.BudgetSpendCredits, err = s.budgetSpendCredits(ctx, key.ID, policy)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
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
	spend, err := s.repo.GetLifetimeSpend(ctx, keyID)
	if err != nil {
		return KeyView{}, fmt.Errorf("apikeys: get lifetime spend: %w", err)
	}
	view := buildKeyView(key, policy, spend)
	view.BudgetSpendCredits, err = s.budgetSpendCredits(ctx, keyID, policy)
	if err != nil {
		return KeyView{}, err
	}
	return view, nil
}

// budgetSpendCredits reports the counter the gateway enforces against, so a
// surface that draws a proportion of a cap divides the same numbers the
// refusal is made from: api_key_budget_windows consumed plus reserved, summed
// exactly as edge-api's authz.CheckAccess sums them
// (consumed + reserved + estimated > limit).
//
// Deliberately not GetLifetimeSpend. That reads api_key_usage_rollups, which
// RecordUsageFinalization writes on every settled request whether or not a cap
// exists, while the window is only ever written by ApplyReservationDelta,
// which returns early for a "none" budget kind. Nothing backfills the window
// when a cap is set later, so the two counters diverge by whatever the key
// spent before it was capped, and a console dividing the lifetime figure would
// show a red, over-cap key that the gateway is still serving (issue #1683).
//
// nil means there is nothing to enforce: no budget kind, or no limit.
func (s *Service) budgetSpendCredits(ctx context.Context, keyID uuid.UUID, policy KeyPolicy) (*int64, error) {
	if policy.BudgetKind == "" || policy.BudgetKind == "none" || policy.BudgetLimitCredits == nil {
		return nil, nil
	}
	window, err := s.repo.GetBudgetWindow(ctx, keyID, policy.BudgetKind, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("apikeys: get budget window: %w", err)
	}
	used := window.ConsumedCredits + window.ReservedCredits
	return &used, nil
}

// requireBillingTenant refuses a mint for an account edge-api is guaranteed to
// reject (issue #1330).
//
// public.tenant_billing_accounts maps an account to the tenant that bills it,
// and ResolveSnapshot carries that tenant onto the key's AuthSnapshot. When
// the mapping is absent the snapshot's TenantID is uuid.Nil, and every
// fail-closed consumer answers 403 account_not_provisioned on the first
// request (authz.AuthSnapshot.TenantUUID, PR #1240). Until this check existed
// the console still returned 201, rendered the secret in its copy-it-now
// panel, and listed the key as active, so the only signal a customer got was
// an opaque refusal from a different service naming neither cause nor remedy.
//
// This is a gate on issuing a credential, not a repair of the mapping. The
// missing row cannot simply be created here: signup.EnsureTenantBillingAccount
// deliberately refuses to guess one (both columns are UNIQUE, so a wrong
// mapping bills one tenant's usage to another account and is unreachable
// afterwards), and a user holding several workspaces has exactly one that can
// ever carry the tenant. Refusing at mint time is what turns that permanent
// state into something the customer can be told about while they still have a
// choice.
//
// The read and the insert that follows it are not one transaction, and
// deliberately so. Nothing in this repository ever deletes a
// tenant_billing_accounts row (both writers only INSERT, and the migrations
// only backfill), so the only state this window could miss is a mapping that
// appeared, which is the harmless direction. If one ever were deleted mid
// flight the resulting key would fail closed at the API boundary exactly as it
// did before this gate existed, so the worst case is the old behaviour, not a
// usable credential on an unbillable account.
func (s *Service) requireBillingTenant(ctx context.Context, accountID uuid.UUID) error {
	tenantID, err := s.repo.GetTenantIDByAccountID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("apikeys: resolve tenant for account: %w", err)
	}
	if tenantID == uuid.Nil {
		return ErrAccountNotProvisioned
	}
	return nil
}

// CreateKey issues a new API key under a freshly generated id. The raw secret
// is returned once and must not be logged, persisted, or included in list
// responses.
func (s *Service) CreateKey(ctx context.Context, accountID, actorUserID uuid.UUID, input CreateKeyInput) (CreateKeyResult, error) {
	return s.createKey(ctx, accountID, actorUserID, uuid.New(), KindUser, input)
}

// CreateAgentTaskKey mints the one short-lived credential an agent task's
// sandbox spends, on that task's own tenant billing account (issue #1507).
//
// Deliberately NOT a general "create a key with the id and kind of your
// choosing" primitive. It is the only caller that needs either, and a
// primitive that let any caller pick a key's id and kind is a sharper tool
// than this codebase has a use for. Everything the agent-task case needs is
// fixed here: the id is the task's, the kind is KindAgentTask, and the
// nickname is derived rather than passed.
//
// The key id IS the task id. That is load bearing: the task row needs no extra
// column to remember which credential its sandbox spends, revocation is a pure
// function of the task, and an operator reading public.api_keys can see which
// task a key belongs to. Uniqueness is the table's primary key, so a second
// mint for the same task fails the insert rather than quietly issuing a second
// live credential.
//
// A predictable id is safe because the id is not what authenticates. The
// secret is, and it comes from generateSecret: 32 crypto/rand bytes, unrelated
// to taskID, accountID or actorUserID, pinned by
// TestCreateKeyWithIDSecretIsRandomAndUnrelatedToTheIDs.
//
// Not reachable from the customer HTTP surface: Handler decodes a request body
// into CreateKeyInput and calls CreateKey, which generates its own id and
// leaves Kind empty (so KindUser). No request field reaches this function.
func (s *Service) CreateAgentTaskKey(ctx context.Context, accountID, actorUserID, taskID uuid.UUID, expiresAt *time.Time) (CreateKeyResult, error) {
	return s.createKey(ctx, accountID, actorUserID, taskID, KindAgentTask, CreateKeyInput{
		Nickname:  "agent task " + taskID.String(),
		ExpiresAt: expiresAt,
	})
}

func (s *Service) createKey(ctx context.Context, accountID, actorUserID, keyID uuid.UUID, kind KeyKind, input CreateKeyInput) (CreateKeyResult, error) {
	if err := s.requireBillingTenant(ctx, accountID); err != nil {
		return CreateKeyResult{}, err
	}

	rawSecret, tokenHash, redactedSuffix, err := generateSecret()
	if err != nil {
		return CreateKeyResult{}, fmt.Errorf("apikeys: generate secret: %w", err)
	}

	key := APIKey{
		Kind:            kind,
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

// RevokeAgentTaskKey revokes one agent-task credential by its primary key, and
// by nothing else (issue #1507).
//
// Why no account scope, unlike RevokeKey. The caller is agenttask's revocation
// path, and the only account id it could pass is one it would have to re-derive
// from public.tenant_billing_accounts, a second lookup that can disagree with
// the one the mint used. If it disagrees, an account-scoped revoke does not
// find the row, returns ErrNotFound, and a caller that reads that as "already
// gone" leaves a live credential on a real billing account for its full
// lifetime with nothing anywhere saying so. The scope was defence that
// introduced the hole it defended.
//
// What replaces it is stronger, not weaker. The key id is the agent task's own
// id, a primary key, so the lookup is exact and O(1); and the Kind check below
// means this function CANNOT destroy a customer's own key even if it is handed
// an id that belongs to one. RevokeKey keeps its account scope, because its
// caller is an HTTP handler acting for a viewer and there the scope is the
// authorization.
//
// ErrNotFound is returned, never swallowed. "This credential is already
// revoked" (ErrRevoked, an observed row in a known state) and "I could not find
// the thing I was told to destroy" are different states and the caller must be
// able to tell them apart.
func (s *Service) RevokeAgentTaskKey(ctx context.Context, taskID uuid.UUID) (APIKey, error) {
	existing, err := s.repo.GetKeyByID(ctx, taskID)
	if err != nil {
		return APIKey{}, err
	}
	if existing.Kind != KindAgentTask {
		// Refuses rather than proceeding: an id that resolves to a customer's
		// own key here means the caller's premise is wrong, and revoking it
		// would take away a key its owner is using.
		return APIKey{}, ErrNotAgentTaskKey
	}

	existing = applyExpiry(existing, time.Now())
	if existing.Status == KeyStatusRevoked {
		return existing, ErrRevoked
	}

	now := time.Now()
	updated, err := s.repo.UpdateKeyState(ctx, existing.AccountID, taskID, KeyStatusRevoked, nil, &now, nil)
	if err != nil {
		return APIKey{}, fmt.Errorf("apikeys: revoke agent task key: %w", err)
	}

	_ = s.repo.InsertEvent(ctx, KeyEvent{
		ID:          uuid.New(),
		APIKeyID:    taskID,
		AccountID:   existing.AccountID,
		EventType:   "revoked",
		ActorUserID: existing.CreatedByUserID,
	})

	if err := s.invalidateSnapshot(ctx, updated.TokenHash); err != nil {
		return APIKey{}, err
	}
	// updated carries last_used_at from the UPDATE's own RETURNING clause, and
	// that is deliberately left alone. An earlier revision overwrote it with the
	// value read by GetKeyByID above, which is stale: a credential can settle a
	// charge in the window between that read and this write, and the caller uses
	// this field to decide whether the task charged anything at all. Copying the
	// pre-read value back would make such a task report as a zero charge.
	return updated, nil
}

// RotateKey creates a brand-new replacement key and immediately revokes
// only the rotated source key. Sibling keys are unaffected.
func (s *Service) RotateKey(ctx context.Context, accountID, actorUserID, keyID uuid.UUID, nickname string, expiresAt *time.Time) (RotateKeyResult, error) {
	// Rotation mints a fresh secret, so it carries the same trap as creation:
	// a customer whose key is being refused clicks Rotate and walks away with
	// a second key refused identically. Checked before the source key is
	// read, so a refused rotation leaves that key exactly as it was.
	if err := s.requireBillingTenant(ctx, accountID); err != nil {
		return RotateKeyResult{}, err
	}

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

// GetLimits returns the per-key RPM/TPM and tier overrides. Caller must have
// already passed the owner gate at the HTTP layer; this method enforces only
// the account-scope gate via repository.
func (s *Service) GetLimits(ctx context.Context, accountID, keyID uuid.UUID) (KeyLimits, error) {
	limits, err := s.repo.GetLimits(ctx, accountID, keyID)
	if err != nil {
		return KeyLimits{}, fmt.Errorf("apikeys: get limits: %w", err)
	}
	return limits, nil
}

// UpdateLimits writes the per-key RPM/TPM and tier overrides. Caller must have
// already passed the owner gate at the HTTP layer. The snapshot cache is
// invalidated after a successful write so the next hot-path resolve picks up
// the new values without staleness.
func (s *Service) UpdateLimits(ctx context.Context, accountID, keyID uuid.UUID, input KeyLimitsInput) (KeyLimits, error) {
	limits, err := s.repo.UpdateLimits(ctx, accountID, keyID, input)
	if err != nil {
		return KeyLimits{}, fmt.Errorf("apikeys: update limits: %w", err)
	}
	key, err := s.repo.GetKey(ctx, accountID, keyID)
	if err == nil {
		_ = s.invalidateSnapshot(ctx, key.TokenHash)
	}
	return limits, nil
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

	// D-030: bind the key to the tenant its account bills, so edge-api can
	// enforce tenant-scoped model entitlement (routing.Service.SelectRoute)
	// and tenant-filtered model listing for API-key traffic the same way it
	// already does for JWT sessions. uuid.Nil (no mapping row yet) is not an
	// error here -- ResolveSnapshot still returns a usable snapshot; it is
	// edge-api's consumers that decide to fail closed on an unmapped tenant.
	tenantID, err := s.repo.GetTenantIDByAccountID(ctx, key.AccountID)
	if err != nil {
		return AuthSnapshot{}, fmt.Errorf("apikeys: resolve tenant for account: %w", err)
	}

	return AuthSnapshot{
		KeyID:                 key.ID,
		AccountID:             key.AccountID,
		TenantID:              tenantID,
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

func buildKeyView(key APIKey, policy KeyPolicy, spendCredits int64) KeyView {
	key = applyExpiry(key, time.Now())

	// A limit is only reported when it is a limit. UpdatePolicy's upsert
	// COALESCEs budget_limit_credits, so a nil in that column means "leave
	// unchanged", not "clear": a key switched to budget_kind "none" keeps
	// whatever ceiling it last carried in the row. Enforcement already ignores
	// it (edge-api authz.CheckAccess requires budget_kind != "none" AND a
	// non-nil limit), so reporting the stale number would put a figure on the
	// console next to a key that is not capped at all, which is the one lie a
	// spending-limit surface must never tell.
	budgetLimitCredits := policy.BudgetLimitCredits
	if policy.BudgetKind == "" || policy.BudgetKind == "none" {
		budgetLimitCredits = nil
	}

	return KeyView{
		ID:                 key.ID,
		Nickname:           key.Nickname,
		Status:             key.Status,
		RedactedSuffix:     key.RedactedSuffix,
		CreatedAt:          key.CreatedAt,
		UpdatedAt:          key.UpdatedAt,
		ExpiresAt:          key.ExpiresAt,
		LastUsedAt:         key.LastUsedAt,
		ExpirationSummary:  expirationSummary(key),
		BudgetSummary:      budgetSummary(policy),
		AllowlistSummary:   allowlistSummary(policy),
		SpendCredits:       spendCredits,
		BudgetLimitCredits: budgetLimitCredits,
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
