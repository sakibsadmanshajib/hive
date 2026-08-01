package accounting

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

// A reservation reaches a terminal state exactly once. These tests pin both
// directions of that invariant: a settled reservation refuses a further
// release with a typed, classifiable error (issue #672), and a reservation in
// any legal open state still releases in full so the guard never strands a
// hold the way issue #616 did.

// futureReservationStatus stands in for a status added after this guard was
// written. The allowlist must refuse it rather than fall through into a
// release, which is the whole reason the guard enumerates permitted states
// instead of blocking known-bad ones.
const futureReservationStatus ReservationStatus = "awaiting_provider_invoice"

func seedStateReservation(t *testing.T, repo *repoStub, accountID uuid.UUID, status ReservationStatus, reserved, consumed, released int64) Reservation {
	t.Helper()
	reservation := Reservation{
		ID:               uuid.New(),
		AccountID:        accountID,
		RequestAttemptID: uuid.New(),
		ReservationKey:   "req_terminal_state:1",
		RequestID:        "req_terminal_state",
		AttemptNumber:    1,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "hive-fast",
		PolicyMode:       PolicyModeStrict,
		Status:           status,
		ReservedCredits:  reserved,
		ConsumedCredits:  consumed,
		ReleasedCredits:  released,
	}
	repo.reservations[reservation.ID] = reservation
	return reservation
}

// TestReleaseReservationRefusesSettledStatuses is the direct #672 guard: every
// status that represents a completed settlement, plus any status the allowlist
// does not recognize, must refuse a release. needs_reconciliation is the case
// that regressed: it is permanent in practice (issue #600 established that
// credit_reconciliation_jobs has an INSERT and no reader), so every such row
// was a standing second-release target.
func TestReleaseReservationRefusesSettledStatuses(t *testing.T) {
	for _, status := range []ReservationStatus{
		ReservationStatusFinalized,
		ReservationStatusNeedsReconciliation,
		futureReservationStatus,
	} {
		t.Run(string(status), func(t *testing.T) {
			repo := newRepoStub()
			ledgerSvc := &ledgerStub{}
			usageSvc := &usageStub{}
			svc := NewService(repo, ledgerSvc, usageSvc)

			accountID := uuid.New()
			// A settled row: 10000 reserved, 1003 charged, 8997 handed back.
			reservation := seedStateReservation(t, repo, accountID, status, 10000, 1003, 8997)

			_, err := svc.ReleaseReservation(context.Background(), ReleaseReservationInput{
				AccountID:     accountID,
				ReservationID: reservation.ID,
				Reason:        "finalize_failed",
			})

			var policyErr *PolicyError
			if !errors.As(err, &policyErr) {
				t.Fatalf("expected a *PolicyError refusing the release, got %v", err)
			}
			if policyErr.Message == "" {
				t.Fatal("expected a non-empty refusal message")
			}
			if len(ledgerSvc.releaseCalls) != 0 {
				t.Fatalf("expected no ledger movement, got %#v", ledgerSvc.releaseCalls)
			}
			if len(usageSvc.statusCalls) != 0 {
				t.Fatalf("expected no attempt status change, got %#v", usageSvc.statusCalls)
			}
			stored := repo.reservations[reservation.ID]
			if stored.Status != status {
				t.Fatalf("expected status %s to survive the refused release, got %s", status, stored.Status)
			}
			if stored.ConsumedCredits != 1003 || stored.ReleasedCredits != 8997 {
				t.Fatalf("expected the settled figures to survive, got consumed=%d released=%d", stored.ConsumedCredits, stored.ReleasedCredits)
			}
		})
	}
}

// TestReleaseAfterUnconfirmedSettlementKeepsRowBalanced drives the exact
// sequence from #672: an unconfirmed finalize commits (status
// needs_reconciliation), its HTTP response is lost, and edge-api's fallback
// release fires. The release must be refused and the row must still balance:
// reserved == consumed + released.
func TestReleaseAfterUnconfirmedSettlementKeepsRowBalanced(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	accountID := uuid.New()
	reservation := seedStateReservation(t, repo, accountID, ReservationStatusActive, 10000, 0, 0)

	settled, err := svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          1003,
		TerminalUsageConfirmed: false,
		Status:                 string(usage.AttemptStatusCompleted),
	})
	if err != nil {
		t.Fatalf("FinalizeReservation returned error: %v", err)
	}
	if settled.Status != ReservationStatusNeedsReconciliation {
		t.Fatalf("expected needs_reconciliation, got %s", settled.Status)
	}

	_, err = svc.ReleaseReservation(context.Background(), ReleaseReservationInput{
		AccountID:     accountID,
		ReservationID: reservation.ID,
		Reason:        "finalize_failed",
	})
	var policyErr *PolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("expected the second settlement to be refused with a *PolicyError, got %v", err)
	}

	stored := repo.reservations[reservation.ID]
	if stored.Status != ReservationStatusNeedsReconciliation {
		t.Fatalf("expected the reconciliation marker to survive, got %s", stored.Status)
	}
	if stored.ConsumedCredits+stored.ReleasedCredits != stored.ReservedCredits {
		t.Fatalf("row does not balance: reserved=%d consumed=%d released=%d",
			stored.ReservedCredits, stored.ConsumedCredits, stored.ReleasedCredits)
	}
	if remaining := remainingHeldCredits(stored); remaining != 0 {
		t.Fatalf("expected nothing still held, got %d", remaining)
	}
	if len(ledgerSvc.chargeCalls) != 1 || ledgerSvc.chargeCalls[0].credits != 1003 {
		t.Fatalf("expected one 1003-credit charge, got %#v", ledgerSvc.chargeCalls)
	}
	if len(ledgerSvc.releaseCalls) != 1 || ledgerSvc.releaseCalls[0].credits != 8997 {
		t.Fatalf("expected only the finalize's own 8997-credit release, got %#v", ledgerSvc.releaseCalls)
	}
	if repo.releaseEventCounts[reservation.ID] != 0 {
		t.Fatalf("expected no release event row, got %d", repo.releaseEventCounts[reservation.ID])
	}
	for _, call := range usageSvc.statusCalls {
		if call.status == usage.AttemptStatusCancelled {
			t.Fatal("a charged and delivered request must not be marked cancelled")
		}
	}
}

// TestReleaseReservationAllowedFromEveryOpenStatus is the too-strict half of
// the bound: a first release from any legal starting state must still hand the
// whole hold back. 154000 credits is the figure #616 stranded, so the guard
// being over-eager here is the failure this pins.
func TestReleaseReservationAllowedFromEveryOpenStatus(t *testing.T) {
	for _, status := range []ReservationStatus{ReservationStatusActive, ReservationStatusExpanded} {
		t.Run(string(status), func(t *testing.T) {
			repo := newRepoStub()
			ledgerSvc := &ledgerStub{}
			usageSvc := &usageStub{}
			svc := NewService(repo, ledgerSvc, usageSvc)

			accountID := uuid.New()
			reservation := seedStateReservation(t, repo, accountID, status, 154000, 0, 0)

			released, err := svc.ReleaseReservation(context.Background(), ReleaseReservationInput{
				AccountID:     accountID,
				ReservationID: reservation.ID,
				Reason:        "client_disconnect",
			})
			if err != nil {
				t.Fatalf("ReleaseReservation returned error: %v", err)
			}
			if released.Status != ReservationStatusReleased {
				t.Fatalf("expected released, got %s", released.Status)
			}
			if released.ReleasedCredits != 154000 {
				t.Fatalf("expected the full 154000 hold handed back, got %d", released.ReleasedCredits)
			}
			if len(ledgerSvc.releaseCalls) != 1 || ledgerSvc.releaseCalls[0].credits != 154000 {
				t.Fatalf("expected one 154000-credit ledger release, got %#v", ledgerSvc.releaseCalls)
			}
			wantKey := "reservation:" + reservation.ID.String() + ":release"
			if ledgerSvc.releaseCalls[0].idempotencyKey != wantKey {
				t.Fatalf("expected release key %s, got %s", wantKey, ledgerSvc.releaseCalls[0].idempotencyKey)
			}
		})
	}
}

// TestReleaseReservationReplaysAlreadyReleased keeps release idempotent for a
// caller that retries its own release: same operation, not a second
// settlement, so it returns the stored row rather than an error.
func TestReleaseReservationReplaysAlreadyReleased(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	accountID := uuid.New()
	reservation := seedStateReservation(t, repo, accountID, ReservationStatusReleased, 10000, 0, 10000)

	replayed, err := svc.ReleaseReservation(context.Background(), ReleaseReservationInput{
		AccountID:     accountID,
		ReservationID: reservation.ID,
		Reason:        "client_disconnect",
	})
	if err != nil {
		t.Fatalf("expected an idempotent replay, got error: %v", err)
	}
	if replayed.Status != ReservationStatusReleased || replayed.ReleasedCredits != 10000 {
		t.Fatalf("expected the stored released row back, got %#v", replayed)
	}
	if len(ledgerSvc.releaseCalls) != 0 {
		t.Fatalf("expected no second ledger movement, got %#v", ledgerSvc.releaseCalls)
	}
}

// TestExpandReservationRefusesSettledStatuses closes the same omission on the
// sibling caller. Expanding a settled reservation posts a fresh hold on a row
// that can no longer be released, which strands those credits outright, and
// needs_reconciliation slipped through the old two-status check here as well.
func TestExpandReservationRefusesSettledStatuses(t *testing.T) {
	for _, status := range []ReservationStatus{
		ReservationStatusFinalized,
		ReservationStatusReleased,
		ReservationStatusNeedsReconciliation,
		futureReservationStatus,
	} {
		t.Run(string(status), func(t *testing.T) {
			repo := newRepoStub()
			// A solvent account on purpose: the expansion must be refused by the
			// terminal-state guard, not incidentally by the balance policy.
			ledgerSvc := &ledgerStub{balance: ledgerBalance(500000)}
			usageSvc := &usageStub{}
			svc := NewService(repo, ledgerSvc, usageSvc)

			accountID := uuid.New()
			reservation := seedStateReservation(t, repo, accountID, status, 10000, 1003, 8997)

			_, err := svc.ExpandReservation(context.Background(), ExpandReservationInput{
				AccountID:         accountID,
				ReservationID:     reservation.ID,
				AdditionalCredits: 5000,
			})
			var policyErr *PolicyError
			if !errors.As(err, &policyErr) {
				t.Fatalf("expected a *PolicyError refusing the expansion, got %v", err)
			}
			if len(ledgerSvc.reserveCalls) != 0 {
				t.Fatalf("expected no additional hold, got %#v", ledgerSvc.reserveCalls)
			}
			if stored := repo.reservations[reservation.ID]; stored.ReservedCredits != 10000 || stored.Status != status {
				t.Fatalf("expected the settled row untouched, got reserved=%d status=%s", stored.ReservedCredits, stored.Status)
			}
		})
	}
}

// TestFinalizeReservationRefusesUnrecognizedStatus is the same allowlist on the
// finalize side: a settled row replays, an unrecognized one fails closed.
func TestFinalizeReservationRefusesUnrecognizedStatus(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	accountID := uuid.New()
	reservation := seedStateReservation(t, repo, accountID, futureReservationStatus, 10000, 0, 0)

	_, err := svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          1003,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	})
	var policyErr *PolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("expected a *PolicyError refusing the finalize, got %v", err)
	}
	if len(ledgerSvc.chargeCalls) != 0 {
		t.Fatalf("expected no charge, got %#v", ledgerSvc.chargeCalls)
	}
}
