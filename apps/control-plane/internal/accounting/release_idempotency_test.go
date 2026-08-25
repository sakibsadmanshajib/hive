package accounting

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// TestReleaseIdempotencyKeyShapeIsUnified is issue #652's static regression
// guard: finalizeLocked's partial release (charge some, release the
// remainder) and releaseLocked's full release must build the release
// idempotency key the same way. A key that varies with the released amount
// cannot deduplicate two release attempts of DIFFERING amounts for the same
// reservation, which is precisely the case worth catching -- so
// service.go must not contain the old amount-varying shape anywhere.
func TestReleaseIdempotencyKeyShapeIsUnified(t *testing.T) {
	source := readAccountingServiceSource(t)

	if strings.Contains(source, `"release-%d"`) {
		t.Fatal(`service.go must not build a release idempotency key that varies with the credit amount; use the flat "release" key everywhere, matching releaseLocked`)
	}
	if matches := regexp.MustCompile(`idempotencyKey\(reservation\.ID,\s*"release"\)`).FindAllString(source, -1); len(matches) < 2 {
		t.Fatalf(`expected both finalizeLocked's partial release and releaseLocked's full release to call idempotencyKey(reservation.ID, "release"); found %d occurrences, want at least 2`, len(matches))
	}
}

func readAccountingServiceSource(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatalf("read service.go: %v", err)
	}
	return string(body)
}

// --- live ledger dedup proof ---
//
// The static test above proves the two call sites now compute the same key
// string. This proves what that buys: the underlying ledger dedup mechanism
// (credit_idempotency_keys' primary key, ON CONFLICT DO NOTHING) actually
// stops a second release for the same reservation from posting, even when
// the two attempts disagree on how many credits to release -- exactly the
// case a key that varied by amount could never catch. Gated on
// HIVE_TEST_DB_URL, same convention as internal/payments/settlement_live_db_test.go.

func newAccountingTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return dbtest.Pool(t, "HIVE_TEST_DB_URL")
}

// seedReleaseIdempotencyAccount inserts the auth.users row and the account a
// ledger entry's account_id must reference, and registers cleanup. Mirrors
// internal/payments/settlement_live_db_test.go's seedPaymentsAccount.
func seedReleaseIdempotencyAccount(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users (id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		"release-idempotency-"+uuid.NewString()+"@test.local",
	).Scan(&userID); err != nil {
		t.Skipf("seed auth.users failed (is this a migrated test DB?): %v", err)
	}

	var accountID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, 'release idempotency test', 'personal', $2) RETURNING id`,
		"release-idempotency-"+uuid.NewString(), userID,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM public.accounts WHERE id = $1`, accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM auth.users WHERE id = $1`, userID)
	})
	return accountID
}

// TestReleaseIdempotency_DifferingAmountsForSameReservationDedupeToFirst is
// (a), (b), and (c) together: two release attempts for the SAME reservation,
// using the unified key both finalizeLocked and releaseLocked now build
// (so it stands in for either code path, edge-api's normal settlement
// fallback or the #648 reaper), computing DIFFERENT credit amounts -- the
// exact case a key that varied by amount could never catch. Only the first
// amount may land: asserted on the ledger's own entries and balance, not on
// either call's return value.
func TestReleaseIdempotency_DifferingAmountsForSameReservationDedupeToFirst(t *testing.T) {
	pool := newAccountingTestPool(t)
	accountID := seedReleaseIdempotencyAccount(t, pool)
	ctx := context.Background()

	ledgerSvc := ledger.NewService(ledger.NewPgxRepository(pool))
	if _, err := ledgerSvc.GrantCredits(ctx, accountID, uuid.NewString(), 10000, map[string]any{"reason": "test grant"}); err != nil {
		t.Fatalf("grant initial credits: %v", err)
	}

	reservationID := uuid.New()
	requestID := uuid.NewString()

	// A real hold precedes any release: 1500 credits reserved, available
	// drops by 1500. Captured AFTER the hold, so "moved" below isolates what
	// the releases themselves do.
	if _, err := ledgerSvc.ReserveCredits(ctx, accountID, requestID, nil, &reservationID, "reservation:"+reservationID.String()+":reserve", 1500, map[string]any{"path": "hold"}); err != nil {
		t.Fatalf("reserve credits: %v", err)
	}
	before, err := ledgerSvc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance after hold, before release: %v", err)
	}

	svc := &Service{} // only idempotencyKey is used; no repo/lock dependency needed for it
	key := svc.idempotencyKey(reservationID, "release")

	// First attempt: the full hold released, the amount finalizeLocked's
	// (or releaseLocked's) own computation would produce for this
	// reservation.
	if _, err := ledgerSvc.ReleaseReservedCredits(ctx, accountID, requestID, nil, &reservationID, key, 1500, map[string]any{"path": "finalize-or-settlement-release"}); err != nil {
		t.Fatalf("first release: %v", err)
	}

	// Second attempt: a DIFFERENT amount, the shape of a reaper release
	// recomputing remaining held credits independently and landing on a
	// different number (state drift, a retry after a partial failure,
	// etc). Same key, so this must be a no-op against the ledger, not a
	// second entry -- exactly the case a key that varied by amount could
	// never catch.
	if _, err := ledgerSvc.ReleaseReservedCredits(ctx, accountID, requestID, nil, &reservationID, key, 1600, map[string]any{"path": "reaper-release"}); err != nil {
		t.Fatalf("second release: %v", err)
	}

	var entryCount int
	var totalDelta int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(sum(credits_delta), 0)
		 FROM public.credit_ledger_entries
		 WHERE account_id = $1 AND reservation_id = $2 AND entry_type = 'reservation_release'`,
		accountID, reservationID,
	).Scan(&entryCount, &totalDelta); err != nil {
		t.Fatalf("query ledger entries: %v", err)
	}
	if entryCount != 1 {
		t.Fatalf("expected exactly one reservation_release ledger entry, got %d", entryCount)
	}
	if totalDelta != 1500 {
		t.Fatalf("expected the ledger to reflect only the FIRST release amount (1500), got total credits_delta=%d", totalDelta)
	}

	after, err := ledgerSvc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance after releases: %v", err)
	}
	moved := after.AvailableCredits - before.AvailableCredits
	if moved != 1500 {
		t.Fatalf("expected available balance to move by exactly 1500 (the first release only, hold fully returned), moved by %d", moved)
	}
}
