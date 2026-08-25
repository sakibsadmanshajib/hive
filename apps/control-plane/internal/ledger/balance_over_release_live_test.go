package ledger

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// Issue #918, the reader half. GetBalance used to compute
//
//	reserved = ABS(SUM(hold) + SUM(release))
//
// over the whole account. That ABS is what let a sign error render as a
// plausible number: a reservation released without ever having been held pushes
// the sum positive, and ABS presents that positive number as though credits
// were on hold. Two production accounts sit in exactly that state, and their
// customer-facing available balance is wrong in BOTH directions because the
// phantom nets against genuine in-flight holds before the ABS is taken.
//
// The bound: reserved is the sum, over reservations, of what each one still
// holds, never less than zero for any single reservation and never netted
// across them. A reservation that shows more released than held contributes
// nothing to reserved and is counted separately as an anomaly, because a
// positive net is a corruption signal, not a hold.
//
// Fixture below reproduces the production shape exactly: one settled
// reservation, one genuinely in flight, one released with no hold.
func TestGetBalanceDoesNotRenderAnOverReleaseAsAHold_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountID := seedLedgerAccount(t, pool)
	ctx := context.Background()

	svc := NewService(NewPgxRepository(pool))
	if _, err := svc.GrantCredits(ctx, accountID, uuid.NewString(), 100000, map[string]any{"reason": "test grant"}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	settled := uuid.New()
	inFlight := uuid.New()
	phantom := uuid.New()

	// Settled: held 10000, lifted in full, charged 20. Contributes nothing.
	post(t, svc, accountID, settled, "reserve", 10000, false)
	post(t, svc, accountID, settled, "release", 10000, true)
	if _, err := svc.ChargeUsage(ctx, accountID, "req-settled", nil, &settled, "reservation:"+settled.String()+":charge-20", 20, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}

	// Genuinely in flight: held 12000, not yet released.
	post(t, svc, accountID, inFlight, "reserve", 12000, false)

	// The #918 shape: released without ever having been held.
	post(t, svc, accountID, phantom, "release", 23000, true)

	balance, err := svc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}

	if balance.PostedCredits != 100000-20 {
		t.Fatalf("posted = %d, want %d", balance.PostedCredits, 100000-20)
	}
	if balance.ReservedCredits != 12000 {
		t.Fatalf("reserved = %d, want 12000: only the one hold that is genuinely outstanding. "+
			"ABS over the account nets the 23000 phantom against it and reports 11000, a hold that does not exist",
			balance.ReservedCredits)
	}
	if balance.AvailableCredits != 100000-20-12000 {
		t.Fatalf("available = %d, want %d", balance.AvailableCredits, 100000-20-12000)
	}
	if balance.OverReleasedCredits != 23000 || balance.OverReleasedReservations != 1 {
		t.Fatalf("expected the corruption reported as 23000 credits across 1 reservation, got %d across %d",
			balance.OverReleasedCredits, balance.OverReleasedReservations)
	}
}

func post(t *testing.T, svc *Service, accountID, reservationID uuid.UUID, action string, credits int64, release bool) {
	t.Helper()
	key := "reservation:" + reservationID.String() + ":" + action
	var err error
	if release {
		_, err = svc.ReleaseReservedCredits(context.Background(), accountID, "req-"+action, nil, &reservationID, key, credits, nil)
	} else {
		_, err = svc.ReserveCredits(context.Background(), accountID, "req-"+action, nil, &reservationID, key, credits, nil)
	}
	if err != nil {
		t.Fatalf("post %s: %v", action, err)
	}
}

// Same gating convention as accounting's live suites.
func newLedgerTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return dbtest.Pool(t, "HIVE_TEST_DB_URL")
}

func seedLedgerAccount(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users (id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		"ledger-balance-"+uuid.NewString()+"@test.local",
	).Scan(&userID); err != nil {
		// Fatal, not Skip. The CI leg that runs this suite bootstraps the full
		// migration chain, so a seed failure here is a schema regression, and
		// skipping would report it green: the never-runs trap this suite was
		// added to close.
		t.Fatalf("seed auth.users failed (is this a migrated test DB?): %v", err)
	}

	var accountID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, 'ledger balance test', 'personal', $2) RETURNING id`,
		"ledger-balance-"+uuid.NewString(), userID,
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
