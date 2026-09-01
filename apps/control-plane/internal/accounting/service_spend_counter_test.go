package accounting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

// =============================================================================
// Settled spend reaches the budget gate's counter (issue #1651)
//
// finalizeLocked is the single chokepoint every settlement path routes through,
// so it is the only place a spend counter can be wired without missing an
// endpoint. These tests pin what it hands over and, just as importantly, what a
// failure of that hand-over must not do to the charge.
// =============================================================================

type spendCall struct {
	workspaceID uuid.UUID
	credits     int64
	at          time.Time
}

type spendCounterStub struct {
	calls []spendCall
	err   error
}

func (s *spendCounterStub) RecordSettledSpend(_ context.Context, workspaceID uuid.UUID, credits int64, at time.Time) error {
	s.calls = append(s.calls, spendCall{workspaceID: workspaceID, credits: credits, at: at})
	return s.err
}

func finalizeWithCounter(t *testing.T, counter SpendCounter, actualCredits int64, confirmed bool) (*ledgerStub, uuid.UUID) {
	t.Helper()
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	svc := NewService(repo, ledgerSvc, &usageStub{}).WithSpendCounter(counter)

	accountID := uuid.New()
	reservationID := uuid.New()
	repo.reservations[reservationID] = Reservation{
		ID:               reservationID,
		AccountID:        accountID,
		RequestAttemptID: uuid.New(),
		ReservationKey:   "req_spend:1",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusActive,
		ReservedCredits:  100,
	}

	reservation, err := svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservationID,
		ActualCredits:          actualCredits,
		TerminalUsageConfirmed: confirmed,
		Status:                 string(usage.AttemptStatusCompleted),
	})
	if err != nil {
		t.Fatalf("FinalizeReservation returned error: %v", err)
	}
	if reservation.Status != ReservationStatusFinalized && reservation.Status != ReservationStatusNeedsReconciliation {
		t.Fatalf("reservation left in %s", reservation.Status)
	}
	return ledgerSvc, accountID
}

// TestFinalizeReservationRecordsWhatWasChargedNotWhatWasClaimed pins the
// quantity: the counter must see the credits the ledger actually captured. The
// estimate arriving on the input can run far ahead of the hold and is clamped
// (issue #602), so recording the input would put spend on a customer's cap that
// was never billed to them.
func TestFinalizeReservationRecordsWhatWasChargedNotWhatWasClaimed(t *testing.T) {
	counter := &spendCounterStub{}
	ledgerSvc, accountID := finalizeWithCounter(t, counter, 500, false)

	if len(ledgerSvc.chargeCalls) != 1 || ledgerSvc.chargeCalls[0].credits != 100 {
		t.Fatalf("expected one clamped 100-credit charge, got %#v", ledgerSvc.chargeCalls)
	}
	if len(counter.calls) != 1 {
		t.Fatalf("expected one spend record, got %#v", counter.calls)
	}
	if counter.calls[0].credits != 100 {
		t.Fatalf("counter recorded %d credits, want the 100 that were charged", counter.calls[0].credits)
	}
	if counter.calls[0].workspaceID != accountID {
		t.Fatalf("counter recorded workspace %s, want %s", counter.calls[0].workspaceID, accountID)
	}
	if counter.calls[0].at.IsZero() {
		t.Fatal("counter received a zero settlement time; the month it lands in would be wrong")
	}
}

// TestFinalizeReservationSettlesWhenTheSpendCounterFails fixes the failure
// posture. The charge has already committed by the time the counter is called,
// so returning its error would strand the hold and leave the reservation
// unsettled: a Redis blip would become the credit leak of issue #616. The
// counter failing means a cap goes briefly unenforced, which the next
// settlement heals from the ledger; the alternative breaks billing outright.
func TestFinalizeReservationSettlesWhenTheSpendCounterFails(t *testing.T) {
	counter := &spendCounterStub{err: errors.New("redis is down")}
	ledgerSvc, _ := finalizeWithCounter(t, counter, 70, true)

	if len(ledgerSvc.chargeCalls) != 1 || ledgerSvc.chargeCalls[0].credits != 70 {
		t.Fatalf("expected the charge to post regardless, got %#v", ledgerSvc.chargeCalls)
	}
	if len(ledgerSvc.releaseCalls) != 1 || ledgerSvc.releaseCalls[0].credits != 100 {
		t.Fatalf("expected the hold to be lifted regardless, got %#v", ledgerSvc.releaseCalls)
	}
}

// TestFinalizeReservationRecordsNothingWithoutACharge keeps a zero-credit
// settlement (a failed request, a released hold) off the customer's cap.
func TestFinalizeReservationRecordsNothingWithoutACharge(t *testing.T) {
	counter := &spendCounterStub{}
	ledgerSvc, _ := finalizeWithCounter(t, counter, 0, true)

	if len(ledgerSvc.chargeCalls) != 0 {
		t.Fatalf("expected no charge, got %#v", ledgerSvc.chargeCalls)
	}
	if len(counter.calls) != 0 {
		t.Fatalf("expected no spend record, got %#v", counter.calls)
	}
}

// TestFinalizeReservationWithoutACounterStillSettles keeps the seam optional:
// a deployment with no Redis wires no counter, and settlement must not depend
// on one existing.
func TestFinalizeReservationWithoutACounterStillSettles(t *testing.T) {
	ledgerSvc, _ := finalizeWithCounter(t, nil, 70, true)

	if len(ledgerSvc.chargeCalls) != 1 || ledgerSvc.chargeCalls[0].credits != 70 {
		t.Fatalf("expected the charge to post with no counter wired, got %#v", ledgerSvc.chargeCalls)
	}
}

// TestFinalizeReservationRetryRecordsSpendOnce is the regression guard for the
// double-count the first version of this change carried, found by the adversarial
// review on PR #1677. A settlement that charges and then fails on the row write
// leaves the reservation open, and its caller retries: sessionbilling does so
// explicitly, calling FinalizeReservation a second time on a transient error.
// The retry deduplicates the charge on its idempotency key, so counting the
// spend right after the charge would have put one charge on the customer's cap
// twice and refused them early. The counter call sits after the row transition,
// where the already-settled guard short-circuits the retry.
func TestFinalizeReservationRetryRecordsSpendOnce(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	counter := &spendCounterStub{}
	svc := NewService(repo, ledgerSvc, &usageStub{}).WithSpendCounter(counter)

	accountID := uuid.New()
	reservationID := uuid.New()
	repo.reservations[reservationID] = Reservation{
		ID:               reservationID,
		AccountID:        accountID,
		RequestAttemptID: uuid.New(),
		ReservationKey:   "req_retry:1",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusActive,
		ReservedCredits:  100,
	}
	repo.finalizeErrOnce = errors.New("transient write failure")

	input := FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservationID,
		ActualCredits:          70,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	}

	if _, err := svc.FinalizeReservation(context.Background(), input); err == nil {
		t.Fatal("expected the first finalization to fail on the row write")
	}
	if _, err := svc.FinalizeReservation(context.Background(), input); err != nil {
		t.Fatalf("retry failed: %v", err)
	}

	if len(counter.calls) != 1 {
		t.Fatalf("expected one spend record across the retry, got %#v", counter.calls)
	}
	if counter.calls[0].credits != 70 {
		t.Fatalf("counter recorded %d credits, want 70", counter.calls[0].credits)
	}
}
