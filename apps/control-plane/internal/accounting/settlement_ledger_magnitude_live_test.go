package accounting

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

// Proof by ledger magnitude, on a real database, through the real repositories
// and the real GetBalance SQL: one request that reserves 10000 and truly costs
// 18 must move available by 18, and must leave nothing reserved.
//
// This is the reproduction of the live measurement from the demo box console on
// 2026-08-16 (available fell 36 for 18 credits of work, reserved rose by the
// charge). On the pre-fix code it fails on the reserved assertion with exactly
// 18 credits still held, which is the defect; a test that asserted
// ReleaseReservedCredits had been called would have passed on that same code,
// because it WAS called, just for 18 credits too few.
//
// Gated on HIVE_TEST_DB_URL like release_idempotency_test.go, so CI runs it
// against the ephemeral pgvector:pg17 instance bootstrapped from
// supabase/migrations/ (.github/workflows/ci.yml).
func TestSettlementLedgerMagnitude_Live(t *testing.T) {
	pool := newAccountingTestPool(t)
	accountID := seedReleaseIdempotencyAccount(t, pool)
	ctx := context.Background()

	ledgerSvc := ledger.NewService(ledger.NewPgxRepository(pool))
	svc := NewService(
		NewPgxRepository(pool),
		ledgerSvc,
		usage.NewService(usage.NewPgxRepository(pool)),
	)

	if _, err := ledgerSvc.GrantCredits(ctx, accountID, uuid.NewString(), 300000, map[string]any{"reason": "test grant"}); err != nil {
		t.Fatalf("grant credits: %v", err)
	}

	const (
		hold   = int64(10000)
		actual = int64(18)
	)

	before, err := ledgerSvc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("balance before: %v", err)
	}

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

	// Positive control: the hold really is on the books, so the assertions
	// after settlement cannot pass on a ledger that never moved.
	during, err := ledgerSvc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("balance during: %v", err)
	}
	if during.ReservedCredits != before.ReservedCredits+hold {
		t.Fatalf("reserved after hold = %d, want %d", during.ReservedCredits, before.ReservedCredits+hold)
	}
	if during.AvailableCredits != before.AvailableCredits-hold {
		t.Fatalf("available after hold = %d, want %d", during.AvailableCredits, before.AvailableCredits-hold)
	}

	if _, err := svc.FinalizeReservation(ctx, FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          actual,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
		InputTokens:            120,
		OutputTokens:           30,
	}); err != nil {
		t.Fatalf("FinalizeReservation: %v", err)
	}

	after, err := ledgerSvc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("balance after: %v", err)
	}
	if after.ReservedCredits != before.ReservedCredits {
		t.Fatalf("reserved did not return to its pre-request value: %d, want %d (%d credits still held for a request that already settled)",
			after.ReservedCredits, before.ReservedCredits, after.ReservedCredits-before.ReservedCredits)
	}
	if moved := before.AvailableCredits - after.AvailableCredits; moved != actual {
		t.Fatalf("available moved by %d across one request costing %d; the customer is paying %d credits it does not owe",
			moved, actual, moved-actual)
	}
	if moved := before.PostedCredits - after.PostedCredits; moved != actual {
		t.Fatalf("posted moved by %d, want exactly the %d-credit charge", moved, actual)
	}

	// Same invariant read straight off the rows: the hold and its release
	// cancel, exactly one release row exists, and at most one charge.
	var holdNet, releases, charges int64
	if err := pool.QueryRow(ctx, `
		SELECT
			COALESCE(sum(credits_delta) FILTER (WHERE entry_type IN ('reservation_hold','reservation_release')), 0),
			count(*) FILTER (WHERE entry_type = 'reservation_release'),
			count(*) FILTER (WHERE entry_type = 'usage_charge')
		FROM public.credit_ledger_entries
		WHERE account_id = $1 AND reservation_id = $2
	`, accountID, reservation.ID).Scan(&holdNet, &releases, &charges); err != nil {
		t.Fatalf("query ledger rows: %v", err)
	}
	if holdNet != 0 {
		t.Fatalf("hold and release do not cancel for a settled reservation: net %d", holdNet)
	}
	if releases != 1 || charges != 1 {
		t.Fatalf("expected exactly one release and one charge row, got %d releases and %d charges", releases, charges)
	}
}
