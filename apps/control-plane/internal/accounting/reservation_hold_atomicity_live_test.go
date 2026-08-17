package accounting

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

// Issue #918. A reservation row and its ledger hold were written in two
// separate transactions, so a hold that failed to post left a row claiming
// credits the ledger never held. The stranded-hold reaper later released that
// row, posting a reservation_release with no matching reservation_hold, which
// credits an account for credits that were never taken from it. Ten such
// reservations exist in production, 25000 credits, every one of them created
// before this change.
//
// The bound, as magnitudes rather than call counts: across any attempt to
// create a reservation, successful or not, the number of reservation rows and
// the number of hold entries move together. Either both exist or neither does.
//
// The failure is injected at the database, not through a stub, because the
// guarantee under test IS a transaction boundary and a fake cannot have one.
// The injected shape is real: a claimed idempotency key whose ledger entry is
// missing, which is the poisoned-key state issue #663 documented, and which
// makes the hold write fail after the reservation row has been inserted, at
// exactly the point the old sequence committed the row anyway.
func TestReservationRowAndHoldAreWrittenAtomically_Live(t *testing.T) {
	pool := newAccountingTestPool(t)
	accountID := seedReleaseIdempotencyAccount(t, pool)
	ctx := context.Background()

	repo := NewPgxRepository(pool)
	usageSvc := usage.NewService(usage.NewPgxRepository(pool))

	attempt, err := usageSvc.StartAttempt(ctx, usage.StartAttemptInput{
		AccountID:     accountID,
		RequestID:     uuid.NewString(),
		AttemptNumber: 1,
		Endpoint:      "/v1/audio/transcriptions",
		ModelAlias:    "hive-stt",
		Status:        usage.AttemptStatusAccepted,
	})
	if err != nil {
		t.Fatalf("start attempt: %v", err)
	}

	reservationID := uuid.New()
	holdKey := "reservation:" + reservationID.String() + ":reserve"

	// Claim the hold's idempotency key without its ledger entry. PostEntryTx
	// then takes the duplicate branch, finds no entry, and fails.
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.credit_idempotency_keys (account_id, operation_type, idempotency_key)
		 VALUES ($1, 'reservation_hold', $2)`, accountID, holdKey); err != nil {
		t.Fatalf("poison idempotency key: %v", err)
	}

	_, err = repo.CreateReservation(ctx, Reservation{
		ID:               reservationID,
		AccountID:        accountID,
		RequestAttemptID: attempt.ID,
		ReservationKey:   buildReservationKey(accountID, attempt.RequestID, 1),
		RequestID:        attempt.RequestID,
		AttemptNumber:    1,
		Endpoint:         "/v1/audio/transcriptions",
		ModelAlias:       "hive-stt",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusActive,
		ReservedCredits:  500,
	}, "reserved", ReservationHold{IdempotencyKey: holdKey, Credits: 500})
	if err == nil {
		t.Fatal("expected CreateReservation to fail when the hold cannot be posted")
	}

	rows, holds := countRowsAndHolds(t, pool, accountID)
	if rows != holds {
		t.Fatalf("reservation rows (%d) and hold entries (%d) diverged: a row with no hold is what the reaper later releases against nothing", rows, holds)
	}
	if rows != 0 {
		t.Fatalf("expected the failed create to leave nothing behind, got %d reservation rows", rows)
	}
	// The events table must not carry an orphan either: all ten production rows
	// have a 'reserved' event, which is how they looked legitimate.
	var events int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.credit_reservation_events WHERE reservation_id = $1`,
		reservationID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 0 {
		t.Fatalf("expected no reservation events from a rolled-back create, got %d", events)
	}

	// Positive control: the same repository, with a key nobody has claimed,
	// writes both. Without this the assertions above would pass on a repository
	// that never writes anything at all.
	controlID := uuid.New()
	if _, err := repo.CreateReservation(ctx, Reservation{
		ID:               controlID,
		AccountID:        accountID,
		RequestAttemptID: attempt.ID,
		ReservationKey:   buildReservationKey(accountID, attempt.RequestID, 2),
		RequestID:        attempt.RequestID,
		AttemptNumber:    2,
		Endpoint:         "/v1/audio/transcriptions",
		ModelAlias:       "hive-stt",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusActive,
		ReservedCredits:  500,
	}, "reserved", ReservationHold{
		IdempotencyKey: "reservation:" + controlID.String() + ":reserve",
		Credits:        500,
	}); err != nil {
		t.Fatalf("control CreateReservation: %v", err)
	}

	rows, holds = countRowsAndHolds(t, pool, accountID)
	if rows != 1 || holds != 1 {
		t.Fatalf("control: expected one row and one hold, got %d rows and %d holds", rows, holds)
	}

	// And the hold is the real thing, in the right direction and amount: a
	// reservation whose hold went in with the wrong sign would still count as
	// one row and one entry above.
	balance, err := ledger.NewService(ledger.NewPgxRepository(pool)).GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if balance.ReservedCredits != 500 {
		t.Fatalf("reserved = %d, want the 500 the control reservation holds", balance.ReservedCredits)
	}
	if balance.OverReleasedReservations != 0 {
		t.Fatalf("expected no over-released reservations, got %d", balance.OverReleasedReservations)
	}
}

func countRowsAndHolds(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID) (int64, int64) {
	t.Helper()
	var rows, holds int64
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM public.credit_reservations WHERE account_id = $1),
			(SELECT count(*) FROM public.credit_ledger_entries
			 WHERE account_id = $1 AND entry_type = 'reservation_hold')
	`, accountID).Scan(&rows, &holds); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return rows, holds
}
