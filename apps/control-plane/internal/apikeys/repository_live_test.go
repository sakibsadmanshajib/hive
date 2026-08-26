package apikeys

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// Behavioral coverage for the apikeys repository against real Postgres.
// Before this file, every test in this package ran against fakes, so when the
// package's SQL broke it broke in production only: #1173 (input/output tokens
// written as zeros into api_key_usage_rollups) and #1204 (cache_read_tokens /
// cache_write_tokens written as zeros) were each shipped with a fake-backed
// regression test that could not have caught the incident it was written for,
// because a stub RecordUsageFinalization records nothing about what real SQL
// does with an ON CONFLICT clause.
//
// This suite pins the two surfaces that have already broken once, plus the
// token-hash authentication path they hang off:
//
//  1. Token-hash authentication: GetByTokenHash / GetPolicyByTokenHash /
//     ResolveSnapshot against the live schema, including the disabled /
//     revoked / expired rejection statuses consumers enforce, and the
//     replaced-by-key rotation chain.
//  2. The api_key_usage_rollups upsert: insert-then-update accumulation for
//     monthly AND lifetime windows, input/output token accumulation (#1173's
//     contract), cache-token columns carrying real values end to end (#1204's
//     contract), and last_seen_at never moving backwards.
//
// Gating follows packages/dbtest: skips locally without HIVE_TEST_DB_URL,
// fails in CI if the var is missing while CI is set (a wiring defect), and
// refuses any database whose name lacks "test".

func newAPIKeysTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return dbtest.Pool(t, "HIVE_TEST_DB_URL")
}

// seedAPIKeysAccount inserts an auth.users row and the public.accounts row it
// owns, registering cleanup that cascades api_keys, policies, rate policies,
// budget windows and usage rollups (all of them FK-chain back to accounts).
func seedAPIKeysAccount(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users (id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		"apikeys-repo-"+uuid.NewString()+"@test.local",
	).Scan(&userID); err != nil {
		t.Fatalf("seed auth.users: %v", err)
	}

	var accountID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, 'apikeys repo test', 'personal', $2) RETURNING id`,
		"apikeys-repo-"+uuid.NewString(), userID,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM public.accounts WHERE id = $1`, accountID)
		_, _ = pool.Exec(c, `DELETE FROM auth.users WHERE id = $1`, userID)
	})
	return accountID
}

// seedAPIKeysRow creates a key through the repository under test so the
// insert path itself is exercised, not bypassed with hand-written SQL.
func seedAPIKeysRow(t *testing.T, ctx context.Context, repo Repository, accountID uuid.UUID, expiresAt *time.Time) APIKey {
	t.Helper()
	key, err := repo.CreateKey(ctx, APIKey{
		ID:              uuid.New(),
		AccountID:       accountID,
		Nickname:        "live-test-" + uuid.NewString()[:8],
		TokenHash:       "hash-" + uuid.NewString(),
		RedactedSuffix:  "abcd",
		Status:          KeyStatusActive,
		ExpiresAt:       expiresAt,
		CreatedByUserID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	return key
}

func rollupRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, keyID uuid.UUID, windowKind string, windowStart time.Time) (requests, inTok, outTok, readTok, writeTok, credits int64, lastSeen time.Time) {
	t.Helper()
	err := pool.QueryRow(ctx, `
		SELECT request_count, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		       consumed_credits, last_seen_at
		FROM public.api_key_usage_rollups
		WHERE api_key_id = $1 AND model_alias = $2 AND window_kind = $3 AND window_start = $4
	`, keyID, "gpt-live-test", windowKind, windowStart).Scan(
		&requests, &inTok, &outTok, &readTok, &writeTok, &credits, &lastSeen)
	if err != nil {
		t.Fatalf("read %s rollup row: %v", windowKind, err)
	}
	return
}

// -----------------------------------------------------------------------------
// Token-hash authentication path.
// -----------------------------------------------------------------------------

func TestGetByTokenHash_ResolvesKeyAndRejectsUnknownHash(t *testing.T) {
	pool := newAPIKeysTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedAPIKeysAccount(t, pool)

	key := seedAPIKeysRow(t, ctx, repo, accountID, nil)

	got, err := repo.GetByTokenHash(ctx, key.TokenHash)
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if got.ID != key.ID || got.AccountID != accountID {
		t.Fatalf("expected key %s/%s by hash, got %s/%s", key.ID, accountID, got.ID, got.AccountID)
	}
	if got.Status != KeyStatusActive || got.TokenHash != key.TokenHash {
		t.Fatalf("expected active key with preserved hash, got status=%q hash=%q", got.Status, got.TokenHash)
	}

	if _, err := repo.GetByTokenHash(ctx, "no-such-hash-"+uuid.NewString()); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for unknown hash, got %v", err)
	}
}

func TestGetPolicyByTokenHash_DefaultPolicyFallbackAndSeed(t *testing.T) {
	pool := newAPIKeysTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedAPIKeysAccount(t, pool)
	key := seedAPIKeysRow(t, ctx, repo, accountID, nil)

	// No policy row yet: the hot path must fall back to the default policy
	// rather than erroring, because a key created without an explicit policy
	// is still a working key.
	gotKey, policy, err := repo.GetPolicyByTokenHash(ctx, key.TokenHash)
	if err != nil {
		t.Fatalf("GetPolicyByTokenHash before policy row: %v", err)
	}
	if gotKey.ID != key.ID {
		t.Fatalf("expected key %s, got %s", key.ID, gotKey.ID)
	}
	if len(policy.AllowedGroupNames) != 1 || policy.AllowedGroupNames[0] != "default" {
		t.Fatalf("expected default group fallback [\"default\"], got %#v", policy.AllowedGroupNames)
	}
	if policy.BudgetKind != "" && policy.BudgetKind != "none" {
		t.Fatalf("expected empty or none budget kind on fallback, got %q", policy.BudgetKind)
	}

	// Seeding the default policy row is idempotent and yields a persisted
	// row for the same key.
	for i := 0; i < 2; i++ {
		if err := repo.CreateDefaultPolicy(ctx, key.ID); err != nil {
			t.Fatalf("CreateDefaultPolicy call %d: %v", i+1, err)
		}
	}
	_, _, err = repo.GetPolicyByTokenHash(ctx, key.TokenHash)
	if err != nil {
		t.Fatalf("GetPolicyByTokenHash after seeding default policy: %v", err)
	}
}

// ResolveSnapshot is the endpoint edge-api hits to turn a token hash into the
// enforcement snapshot. The SQL layer never errors on a non-active key; it
// returns the stored state and the consumer rejects on Status. These subtests
// pin that each lifecycle state reaches the snapshot correctly, because a
// wrong column or a dropped WHERE clause silently authenticates revoked keys
// forever.
func TestResolveSnapshot_LifecycleStatesReachSnapshot(t *testing.T) {
	pool := newAPIKeysTestPool(t)
	repo := NewPgxRepository(pool)
	svc := NewService(repo)
	ctx := context.Background()
	accountID := seedAPIKeysAccount(t, pool)

	t.Run("active resolves with unmapped tenant as Nil not error", func(t *testing.T) {
		key := seedAPIKeysRow(t, ctx, repo, accountID, nil)
		snap, err := svc.ResolveSnapshot(ctx, key.TokenHash)
		if err != nil {
			t.Fatalf("ResolveSnapshot: %v", err)
		}
		if snap.KeyID != key.ID || snap.AccountID != accountID {
			t.Fatalf("expected snapshot for key %s, got key=%s account=%s", key.ID, snap.KeyID, snap.AccountID)
		}
		if snap.Status != KeyStatusActive {
			t.Fatalf("expected active snapshot, got %q", snap.Status)
		}
		// No tenant_billing_accounts row exists for this fixture account;
		// D-030 makes that an expected non-error state surfaced as uuid.Nil
		// for the consumer to fail closed on.
		if snap.TenantID != uuid.Nil {
			t.Fatalf("expected unmapped account to surface TenantID=Nil, got %s", snap.TenantID)
		}
	})

	t.Run("disabled key reports disabled status", func(t *testing.T) {
		key := seedAPIKeysRow(t, ctx, repo, accountID, nil)
		now := time.Now().UTC()
		disabled, err := repo.UpdateKeyState(ctx, accountID, key.ID, KeyStatusDisabled, &now, nil, nil)
		if err != nil {
			t.Fatalf("disable key: %v", err)
		}
		snap, err := svc.ResolveSnapshot(ctx, key.TokenHash)
		if err != nil {
			t.Fatalf("ResolveSnapshot: %v", err)
		}
		if snap.Status != disabled.Status || snap.Status != KeyStatusDisabled {
			t.Fatalf("expected snapshot to carry disabled so consumers reject, got %q", snap.Status)
		}
	})

	t.Run("revoked key reports revoked status", func(t *testing.T) {
		key := seedAPIKeysRow(t, ctx, repo, accountID, nil)
		now := time.Now().UTC()
		revoked, err := repo.UpdateKeyState(ctx, accountID, key.ID, KeyStatusRevoked, nil, &now, nil)
		if err != nil {
			t.Fatalf("revoke key: %v", err)
		}
		if revoked.RevokedAt == nil {
			t.Fatal("expected revoked_at persisted on the key row")
		}
		snap, err := svc.ResolveSnapshot(ctx, key.TokenHash)
		if err != nil {
			t.Fatalf("ResolveSnapshot: %v", err)
		}
		if snap.Status != KeyStatusRevoked {
			t.Fatalf("expected snapshot to carry revoked so consumers reject, got %q", snap.Status)
		}
	})

	t.Run("expired key reports expired via applyExpiry", func(t *testing.T) {
		past := time.Now().UTC().Add(-24 * time.Hour)
		key := seedAPIKeysRow(t, ctx, repo, accountID, &past)
		snap, err := svc.ResolveSnapshot(ctx, key.TokenHash)
		if err != nil {
			t.Fatalf("ResolveSnapshot: %v", err)
		}
		if snap.Status != KeyStatusExpired {
			t.Fatalf("expected expired key (expires_at in past, stored active) to surface as expired for consumers, got %q", snap.Status)
		}
	})

	t.Run("rotation chains old hash to revoked with replaced_by link", func(t *testing.T) {
		old := seedAPIKeysRow(t, ctx, repo, accountID, nil)
		replacement := APIKey{
			ID:              uuid.New(),
			AccountID:       accountID,
			Nickname:        "live-test-rot-" + uuid.NewString()[:8],
			TokenHash:       "hash-" + uuid.NewString(),
			RedactedSuffix:  "wxyz",
			Status:          KeyStatusActive,
			CreatedByUserID: uuid.New(),
		}
		rotatedAt := time.Now().UTC()
		gotOld, gotNew, err := repo.CreateReplacementKey(ctx, old.ID, replacement, rotatedAt)
		if err != nil {
			t.Fatalf("CreateReplacementKey: %v", err)
		}
		if gotNew.ID != replacement.ID || gotOld.ID != old.ID {
			t.Fatalf("expected rotation to return old=%s new=%s, got old=%s new=%s",
				old.ID, replacement.ID, gotOld.ID, gotNew.ID)
		}
		if gotOld.ReplacedByKeyID == nil || *gotOld.ReplacedByKeyID != replacement.ID {
			t.Fatalf("expected old key linked to replacement %s, got %v", replacement.ID, gotOld.ReplacedByKeyID)
		}
		if gotOld.Status != KeyStatusRevoked {
			t.Fatalf("expected old key revoked by rotation, got %q", gotOld.Status)
		}

		oldSnap, err := svc.ResolveSnapshot(ctx, old.TokenHash)
		if err != nil {
			t.Fatalf("ResolveSnapshot(old): %v", err)
		}
		if oldSnap.Status != KeyStatusRevoked {
			t.Fatalf("expected rotated-out hash to resolve as revoked so consumers reject it, got %q", oldSnap.Status)
		}
		newSnap, err := svc.ResolveSnapshot(ctx, replacement.TokenHash)
		if err != nil {
			t.Fatalf("ResolveSnapshot(new): %v", err)
		}
		if newSnap.Status != KeyStatusActive || newSnap.KeyID != replacement.ID {
			t.Fatalf("expected replacement hash to resolve active for key %s, got %q for %s",
				replacement.ID, newSnap.Status, newSnap.KeyID)
		}
	})
}

// -----------------------------------------------------------------------------
// api_key_usage_rollups upsert (#1173 accumulation, #1204 cache tokens).
// -----------------------------------------------------------------------------

// TestRecordUsageFinalization_InsertThenAccumulate drives the exact upsert
// both incidents lived in: first call inserts the monthly AND lifetime rows,
// later calls must accumulate onto them. #1173's contract is input/output
// accumulation; #1204's contract is cache_read/cache_write carrying real
// values through the same path instead of zeros.
func TestRecordUsageFinalization_InsertThenAccumulate(t *testing.T) {
	pool := newAPIKeysTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedAPIKeysAccount(t, pool)
	key := seedAPIKeysRow(t, ctx, repo, accountID, nil)

	monthlyStart := func(at time.Time) time.Time {
		return time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC).Truncate(time.Microsecond)
	}

	first := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	second := first.Add(2 * time.Hour)

	if err := repo.RecordUsageFinalization(ctx, key.ID, "gpt-live-test", 100, 50, 30, 20, 7, first); err != nil {
		t.Fatalf("RecordUsageFinalization #1: %v", err)
	}
	if err := repo.RecordUsageFinalization(ctx, key.ID, "gpt-live-test", 40, 25, 10, 5, 3, second); err != nil {
		t.Fatalf("RecordUsageFinalization #2: %v", err)
	}

	// An out-of-order older event must not drag last_seen_at backwards; the
	// upsert takes GREATEST(existing, incoming).
	if err := repo.RecordUsageFinalization(ctx, key.ID, "gpt-live-test", 1, 1, 1, 1, 1, first.Add(-time.Hour)); err != nil {
		t.Fatalf("RecordUsageFinalization out-of-order: %v", err)
	}

	for _, kind := range []string{"monthly", "lifetime"} {
		// The SQL writes two rows per call: a monthly bucket at the month
		// start and a lifetime bucket at the zero timestamp.
		wantStart := monthlyStart(first)
		if kind == "lifetime" {
			wantStart = time.Time{}
		}
		requests, inTok, outTok, readTok, writeTok, credits, lastSeen := rollupRow(t, ctx, pool, key.ID, kind, wantStart)
		wantLastSeen := second.Truncate(time.Microsecond)
		if requests != 3 {
			t.Fatalf("%s: expected request_count 3 after three calls, got %d", kind, requests)
		}
		if inTok != 141 || outTok != 76 {
			t.Fatalf("%s: expected input 100+40+1=141 / output 50+25+1=76 accumulated, got input=%d output=%d", kind, inTok, outTok)
		}
		if readTok != 41 || writeTok != 26 {
			t.Fatalf("%s: expected cache tokens carried end to end (read 30+10+1=41, write 20+5+1=26), got read=%d write=%d (#1204 regression)", kind, readTok, writeTok)
		}
		if credits != 11 {
			t.Fatalf("%s: expected consumed_credits 7+3+1=11, got %d", kind, credits)
		}
		if !lastSeen.Equal(wantLastSeen) {
			t.Fatalf("%s: expected last_seen_at pinned at latest call (%s), got %s: GREATEST guard failed", kind, wantLastSeen, lastSeen)
		}
	}

	// Lifetime window_start is the zero timestamp by contract; if the SQL
	// ever writes the month start into the lifetime row the two windows
	// collapse into one bucket.
	lifetimeStart := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	var start time.Time
	if err := pool.QueryRow(ctx, `
		SELECT window_start FROM public.api_key_usage_rollups
		WHERE api_key_id = $1 AND model_alias = $2 AND window_kind = 'lifetime'
	`, key.ID, "gpt-live-test").Scan(&start); err != nil {
		t.Fatalf("read lifetime window_start: %v", err)
	}
	if !start.Equal(lifetimeStart.Truncate(time.Microsecond)) {
		t.Fatalf("expected lifetime window_start zero-time, got %s", start)
	}
}

// TestApplyReservationDelta_InsertThenAccumulate pins the budget-window upsert
// that feeds ResolveSnapshot's BudgetConsumed/BudgetReserved fields: insert on
// first touch per (key, kind, month), additive update afterwards.
func TestApplyReservationDelta_InsertThenAccumulate(t *testing.T) {
	pool := newAPIKeysTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedAPIKeysAccount(t, pool)
	key := seedAPIKeysRow(t, ctx, repo, accountID, nil)

	at := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	if err := repo.ApplyReservationDelta(ctx, key.ID, "monthly", 500, 200, at); err != nil {
		t.Fatalf("ApplyReservationDelta #1: %v", err)
	}
	if err := repo.ApplyReservationDelta(ctx, key.ID, "monthly", 300, 100, at.Add(time.Hour)); err != nil {
		t.Fatalf("ApplyReservationDelta #2: %v", err)
	}

	var reserved, consumed int64
	if err := pool.QueryRow(ctx, `
		SELECT reserved_credits, consumed_credits FROM public.api_key_budget_windows
		WHERE api_key_id = $1 AND window_kind = 'monthly' AND window_start = $2
	`, key.ID, start).Scan(&reserved, &consumed); err != nil {
		t.Fatalf("read budget window: %v", err)
	}
	if reserved != 800 || consumed != 300 {
		t.Fatalf("expected reservation 500+300=800 and consumption 200+100=300 accumulated, got reserved=%d consumed=%d", reserved, consumed)
	}
}
