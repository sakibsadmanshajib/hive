package accounting

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

// The bound this file defends, stated as magnitudes rather than as call counts:
// for a request that reserves H credits and truly costs C, settlement must move
// the account's available balance by exactly C, and must return reserved to
// zero. Not C plus the leftover hold, and not H minus anything.
//
// Measured on the demo box on 2026-08-16, one chat completion against a 10000
// hold costing 18 credits: posted fell 18 (correct) while reserved ROSE 18, so
// available fell 36 for 18 credits of work. Across the database that was 143642
// credits held against 1825 reservations that had already settled, every one of
// them holding exactly its own consumed_credits, growing with every request.
//
// The cause is arithmetic on the success path, not an error path: finalize
// released hold minus actual and nothing ever lifted the captured remainder, so
// GetBalance's ABS(SUM(hold) + SUM(release)) kept counting it forever.
//
// These cases assert against the fake ledger's own balance rather than against
// call counts, so a regression shows up as a wrong number of credits. The fake
// keeps reserved as a signed held minus released, where the production SQL
// takes ABS of the same sum; the two agree for every case here, because none
// releases more than it held, and TestSettlementLedgerMagnitude_Live covers the
// real query on a real database anyway.
func TestSettlementReturnsReservedToZero(t *testing.T) {
	const grant = 300000

	cases := []struct {
		name      string
		hold      int64
		actual    int64
		confirmed bool
		// wantCharge is what the account must actually pay, after
		// finalizeLocked's clamp on unconfirmed estimates.
		wantCharge int64
	}{
		{
			// The demo box case, scaled: a flat 10000 hold, a tiny real cost.
			name:       "actual far below the hold",
			hold:       10000,
			actual:     18,
			confirmed:  true,
			wantCharge: 18,
		},
		{
			// A completed request that consumed nothing still has to give the
			// whole hold back.
			name:       "zero actual",
			hold:       10000,
			actual:     0,
			confirmed:  true,
			wantCharge: 0,
		},
		{
			// The clamp path (issue #602): an unconfirmed estimate above the
			// hold is charged at the hold. Before the fix this released
			// nothing at all, stranding the entire 10000.
			name:       "unconfirmed estimate clamped to the hold",
			hold:       10000,
			actual:     2_600_000,
			confirmed:  false,
			wantCharge: 10000,
		},
		{
			// Confirmed usage above the flat hold is billed in full, and the
			// hold is still an authorization that must be lifted in full.
			name:       "confirmed usage above the hold",
			hold:       10000,
			actual:     15000,
			confirmed:  true,
			wantCharge: 15000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepoStub()
			ledgerSvc := newReaperLedger(grant)
			// The hold lands with the row (issue #918), so the fake repository
			// posts it to the fake ledger the way production does.
			repo.ledger = ledgerSvc
			svc := NewService(repo, ledgerSvc, concurrentUsage{})
			ctx := context.Background()
			accountID := uuid.New()

			reservation, err := svc.CreateReservation(ctx, CreateReservationInput{
				AccountID:        accountID,
				RequestID:        uuid.NewString(),
				AttemptNumber:    1,
				Endpoint:         "/v1/chat/completions",
				ModelAlias:       "hive-fast",
				EstimatedCredits: tc.hold,
			})
			if err != nil {
				t.Fatalf("CreateReservation: %v", err)
			}

			held, err := ledgerSvc.GetBalance(ctx, accountID)
			if err != nil {
				t.Fatalf("GetBalance after hold: %v", err)
			}
			// Positive control: without a real hold in the ledger, "reserved
			// returns to zero" below would pass on a ledger that never moved.
			if held.ReservedCredits != tc.hold {
				t.Fatalf("fixture wrong: reserved after hold = %d, want %d", held.ReservedCredits, tc.hold)
			}

			if _, err := svc.FinalizeReservation(ctx, FinalizeReservationInput{
				AccountID:              accountID,
				ReservationID:          reservation.ID,
				ActualCredits:          tc.actual,
				TerminalUsageConfirmed: tc.confirmed,
				Status:                 string(usage.AttemptStatusCompleted),
			}); err != nil {
				t.Fatalf("FinalizeReservation: %v", err)
			}

			after, err := ledgerSvc.GetBalance(ctx, accountID)
			if err != nil {
				t.Fatalf("GetBalance after settlement: %v", err)
			}
			if after.ReservedCredits != 0 {
				t.Fatalf("reserved did not return to zero after settlement: %d credits still held", after.ReservedCredits)
			}
			if after.PostedCredits != grant-tc.wantCharge {
				t.Fatalf("posted = %d, want %d (grant minus the charge)", after.PostedCredits, grant-tc.wantCharge)
			}
			if moved := held.AvailableCredits + tc.hold - after.AvailableCredits; moved != tc.wantCharge {
				t.Fatalf("available moved by %d across the request, want exactly the %d-credit charge", moved, tc.wantCharge)
			}

			// Exactly once, per D-034: one lift of the authorization, at most
			// one capture, whichever path got there.
			if got := ledgerSvc.entryCount(ledger.EntryTypeReservationRelease); got != 1 {
				t.Fatalf("expected exactly one reservation_release entry, got %d", got)
			}
			wantCharges := 1
			if tc.wantCharge == 0 {
				wantCharges = 0
			}
			if got := ledgerSvc.entryCount(ledger.EntryTypeUsageCharge); got != wantCharges {
				t.Fatalf("expected %d usage_charge entries, got %d", wantCharges, got)
			}
		})
	}
}

// A settlement whose release deduplicates against an EARLIER, SMALLER release
// must fail rather than mark the reservation settled. PostEntry hands back the
// first entry for a repeated (account, type, key), so without the check that
// call returns quietly with the hold only partly lifted, the row goes terminal,
// and the remainder is stranded past the reaper, which only scans non-terminal
// holds. That is issue #616 reintroduced silently, on the very rows this branch
// exists to fix. The sequence is reachable across a deploy: the old code posted
// a partial release and then failed before updating the row.
func TestSettlementRefusesToSettleAgainstAPartialRelease(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := newReaperLedger(300000)
	repo.ledger = ledgerSvc
	svc := NewService(repo, ledgerSvc, concurrentUsage{})
	ctx := context.Background()
	accountID := uuid.New()

	reservation, err := svc.CreateReservation(ctx, CreateReservationInput{
		AccountID:        accountID,
		RequestID:        uuid.NewString(),
		AttemptNumber:    1,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "hive-fast",
		EstimatedCredits: 10000,
	})
	if err != nil {
		t.Fatalf("CreateReservation: %v", err)
	}

	// What the pre-fix code left behind: a release for the unused remainder
	// only, under the same flat key this settlement will use.
	if _, err := ledgerSvc.ReleaseReservedCredits(ctx, accountID, reservation.RequestID, nil, &reservation.ID,
		svc.idempotencyKey(reservation.ID, "release"), 9982, map[string]any{"path": "pre-fix partial release"}); err != nil {
		t.Fatalf("seed partial release: %v", err)
	}

	_, err = svc.FinalizeReservation(ctx, FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          18,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	})
	if err == nil {
		t.Fatal("expected settlement to refuse a hold that is only partly lifted, got no error")
	}
	// Typed, because this one cannot self-heal and the reaper logs it at error
	// for alerting rather than as one more ordinary refusal.
	var divergence *SettlementDivergenceError
	if !errors.As(err, &divergence) {
		t.Fatalf("expected a *SettlementDivergenceError, got %T: %v", err, err)
	}
	if divergence.PostedCredits != 9982 || divergence.WantCredits != 10000 {
		t.Fatalf("error carries posted=%d want=%d, expected 9982 and 10000", divergence.PostedCredits, divergence.WantCredits)
	}

	stored := repo.reservations[reservation.ID]
	if !reservationOpen(stored.Status) {
		t.Fatalf("reservation was marked %s with 18 credits still held; the reaper can no longer reach it", stored.Status)
	}
	if balance, err := ledgerSvc.GetBalance(ctx, accountID); err != nil {
		t.Fatalf("GetBalance: %v", err)
	} else if balance.ReservedCredits != 18 {
		t.Fatalf("expected the 18 unlifted credits to remain visibly reserved, got %d", balance.ReservedCredits)
	}
}
