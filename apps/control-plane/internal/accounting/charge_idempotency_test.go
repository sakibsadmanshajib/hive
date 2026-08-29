package accounting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

// TestChargeIdempotencyKeyDoesNotCarryItsAmount is issue #917's static
// regression guard, the charge half of what
// TestReleaseIdempotencyKeyShapeIsUnified does for the release side.
//
// It is deliberately NOT the proof that double billing is fixed: a test that
// asserts a key's format proves nothing about what the ledger does with it.
// The live test below is the proof. This one exists so a future edit that
// reintroduces the amount fails in the cheap, always-run leg rather than only
// in the database-gated one.
func TestChargeIdempotencyKeyDoesNotCarryItsAmount(t *testing.T) {
	source := readAccountingServiceSource(t)

	if strings.Contains(source, `"charge-%d"`) {
		t.Fatal(`service.go must not build a charge idempotency key that varies with the credit amount: two settlement attempts that disagree on the amount would write two keys and therefore two charges against a prepaid balance (issue #917)`)
	}
	if !strings.Contains(source, `idempotencyKey(reservation.ID, "charge")`) {
		t.Fatal(`finalizeLocked must key its charge on the reservation alone, via idempotencyKey(reservation.ID, "charge")`)
	}
}

// failFinalizeOnceRepo is the real repository with one injected fault: the
// FIRST call to FinalizeReservation fails, every later call is passed straight
// through.
//
// That models the production window issue #917 names, and it is a window in
// the code rather than an invented one: finalizeLocked posts the charge to the
// ledger BEFORE repo.FinalizeReservation updates the reservation row, so
// anything that fails in between (a lost database connection, a cancelled
// context, the process dying, the release assertion firing) leaves a committed
// charge beside a reservation row still in an open state. Production
// reservation 9d422064 is in exactly that shape: charged, with consumed_credits
// still zero.
//
// Everything else about the settlement is real: the real pgx repository, the
// real ledger service, the real usage service, the real per-account lock.
type failFinalizeOnceRepo struct {
	Repository
	failures int
}

func (r *failFinalizeOnceRepo) FinalizeReservation(ctx context.Context, accountID, reservationID uuid.UUID, consumedCredits, releasedCredits int64, terminalUsageConfirmed bool, status ReservationStatus, reason string) (Reservation, error) {
	if r.failures == 0 {
		r.failures++
		return Reservation{}, errors.New("simulated failure after the charge committed, before the reservation row was updated")
	}
	return r.Repository.FinalizeReservation(ctx, accountID, reservationID, consumedCredits, releasedCredits, terminalUsageConfirmed, status, reason)
}

// TestChargeIdempotency_DivergentSettlementRetryDoesNotDoubleBill is the test
// this change exists to satisfy.
//
// The sequence, driven for real end to end rather than reasoned about:
//
//  1. A reservation holds 1000 credits.
//  2. Settlement attempt one computes 400 credits, posts the charge, and then
//     fails before the reservation row is updated. The row stays open.
//  3. Settlement attempt two recomputes and lands on 900 credits. That is not
//     a contrived number: batchstore/worker.go recomputes actualCredits from
//     the upstream completed-request count on EVERY poll, so a batch that
//     completed more requests between two polls legitimately produces a larger
//     figure on the retry.
//
// With the amount in the key those two attempts write
// "reservation:<id>:charge-400" and "reservation:<id>:charge-900", which are
// different keys, so both insert and the customer is billed 1300 credits for
// work that cost at most 900. With the key flattened they are the same key, the
// second attempt deduplicates against the first, and the divergence is refused
// out loud instead of being written down.
//
// Asserted on the ledger's own rows and on the balance, never on either call's
// return value: the return value is exactly what looked fine while the customer
// was being billed twice.
//
// Gated on HIVE_TEST_DB_URL, and ./internal/accounting/... is in ci.yml's
// live-Postgres package list, so this runs for real in CI.
func TestChargeIdempotency_DivergentSettlementRetryDoesNotDoubleBill(t *testing.T) {
	pool := newAccountingTestPool(t)
	accountID := seedReleaseIdempotencyAccount(t, pool)
	ctx := context.Background()

	const (
		hold         = int64(1000)
		firstAmount  = int64(400)
		secondAmount = int64(900)
	)

	ledgerSvc := ledger.NewService(ledger.NewPgxRepository(pool))
	if _, err := ledgerSvc.GrantCredits(ctx, accountID, uuid.NewString(), 50000, map[string]any{"reason": "test grant"}); err != nil {
		t.Fatalf("grant credits: %v", err)
	}

	repo := &failFinalizeOnceRepo{Repository: NewPgxRepository(pool)}
	svc := NewService(repo, ledgerSvc, usage.NewService(usage.NewPgxRepository(pool)))

	reservation, err := svc.CreateReservation(ctx, CreateReservationInput{
		AccountID:        accountID,
		RequestID:        uuid.NewString(),
		AttemptNumber:    1,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "hive-fast",
		EstimatedCredits: hold,
	})
	if err != nil {
		t.Fatalf("CreateReservation: %v", err)
	}

	before, err := ledgerSvc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("balance after hold: %v", err)
	}

	// Attempt one: charges 400, then fails before the row is updated.
	if _, err := svc.FinalizeReservation(ctx, FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          firstAmount,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	}); err == nil {
		t.Fatal("expected the injected failure to surface from the first settlement attempt")
	}

	// Positive control. Without this, a fix that stopped the FIRST charge from
	// landing at all would pass every assertion below while quietly billing
	// nobody for delivered work.
	stored, err := repo.GetReservation(ctx, accountID, reservation.ID)
	if err != nil {
		t.Fatalf("re-read reservation: %v", err)
	}
	if !reservationOpen(stored.Status) {
		t.Fatalf("reservation is %s after a settlement that failed before updating the row; the retry window this test exercises does not exist", stored.Status)
	}
	if got := chargeRowCount(t, pool, accountID, reservation.ID); got != 1 {
		t.Fatalf("expected the first attempt to have committed exactly one charge, got %d", got)
	}

	// Attempt two: a legitimately recomputed, larger amount.
	_, retryErr := svc.FinalizeReservation(ctx, FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          secondAmount,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	})

	// Dumped unconditionally, so the pass and the fail both leave the ledger
	// rows themselves in the test log rather than only a verdict about them.
	logChargeRows(t, pool, accountID, reservation.ID)

	// The whole point, read off the ledger rows. Asserted BEFORE anything
	// about the returned error, because the money is the requirement and the
	// error is only how an operator hears about it.
	var charges int
	var chargeDelta int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(credits_delta), 0)
		FROM public.credit_ledger_entries
		WHERE account_id = $1 AND reservation_id = $2 AND entry_type = 'usage_charge'
	`, accountID, reservation.ID).Scan(&charges, &chargeDelta); err != nil {
		t.Fatalf("query charge rows: %v", err)
	}
	if charges != 1 {
		t.Fatalf("the customer was billed %d times for one reservation; a divergent settlement retry double billed them", charges)
	}
	if chargeDelta != -firstAmount {
		t.Fatalf("charge total is %d, want %d (the first capture only, stored negative)", chargeDelta, -firstAmount)
	}

	// Refusing the divergence out loud is the second half. A retry that
	// deduplicates to a DIFFERENT amount and then quietly marks the
	// reservation settled writes a row the ledger contradicts, and nothing
	// downstream ever notices. Typed, so the reaper and the batch worker can
	// tell a permanent divergence from an ordinary lost race.
	if retryErr == nil {
		t.Fatal("expected the divergent retry to be refused; settling it silently writes a reservation row the ledger contradicts")
	}
	var divergence *SettlementDivergenceError
	if !errors.As(retryErr, &divergence) {
		t.Fatalf("expected a *SettlementDivergenceError so alerting can key on the type, got %T: %v", retryErr, retryErr)
	}
	if divergence.Operation != "charge" {
		t.Fatalf("divergence names operation %q, want \"charge\"", divergence.Operation)
	}
	if divergence.PostedCredits != firstAmount || divergence.WantCredits != secondAmount {
		t.Fatalf("divergence carries posted=%d want=%d, expected %d and %d as positive credit magnitudes",
			divergence.PostedCredits, divergence.WantCredits, firstAmount, secondAmount)
	}

	// And read off the balance, which is the number the customer sees. Only
	// the first capture may have been taken out of it. The hold is released in
	// full by attempt one, so available returns to its pre-hold level minus
	// exactly one charge.
	after, err := ledgerSvc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("balance after retries: %v", err)
	}
	if moved := (after.AvailableCredits - hold) - before.AvailableCredits; moved != -firstAmount {
		t.Fatalf("available moved by %d net of the released hold, want -%d; the customer paid %d credits it does not owe",
			moved, firstAmount, -moved-firstAmount)
	}
}

// logChargeRows puts the actual usage_charge rows for one reservation into the
// test log. Cheap, and it means the run itself is the evidence of what the
// ledger holds before and after the fix, rather than a claim about it.
func logChargeRows(t *testing.T, pool *pgxpool.Pool, accountID, reservationID uuid.UUID) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT idempotency_key, credits_delta
		FROM public.credit_ledger_entries
		WHERE account_id = $1 AND reservation_id = $2 AND entry_type = 'usage_charge'
		ORDER BY created_at, id
	`, accountID, reservationID)
	if err != nil {
		t.Fatalf("dump charge rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var delta int64
		if err := rows.Scan(&key, &delta); err != nil {
			t.Fatalf("scan charge row: %v", err)
		}
		t.Logf("usage_charge row: idempotency_key=%s credits_delta=%d", key, delta)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate charge rows: %v", err)
	}
}

func chargeRowCount(t *testing.T, pool *pgxpool.Pool, accountID, reservationID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM public.credit_ledger_entries
		WHERE account_id = $1 AND reservation_id = $2 AND entry_type = 'usage_charge'
	`, accountID, reservationID).Scan(&n); err != nil {
		t.Fatalf("count charge rows: %v", err)
	}
	return n
}
