package batchstore

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounting"
)

// Issue #917, item 4: this poll handler recomputes actualCredits from the
// upstream completed-request count on every poll, so it is the caller most
// likely to arrive at a settlement with a number the last attempt did not use.
// Once the ledger has refused one of those as divergent, no later poll can
// clear it, so a plain error return would re-enqueue the task to fail
// identically until the retry budget drains.
//
// Both settlement calls in settleTerminalReservation are covered, because a
// divergence can be raised by either half: the charge assertion added for this
// issue and the release assertion from issue #616.
func TestSettleTerminalReservation_ParksOnSettlementDivergence(t *testing.T) {
	accountID := uuid.New()
	reservationID := uuid.New()
	divergence := &accounting.SettlementDivergenceError{
		ReservationID: reservationID,
		Operation:     "charge",
		PostedCredits: 400,
		WantCredits:   900,
	}

	tests := []struct {
		name          string
		actualCredits int64
		settler       *fakeAccountingSettler
	}{
		{
			name:          "divergent charge on the finalize path",
			actualCredits: 900,
			settler:       &fakeAccountingSettler{finalizeErr: divergence},
		},
		{
			name:          "divergent release on the zero-credit path",
			actualCredits: 0,
			settler:       &fakeAccountingSettler{releaseErr: divergence},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &BatchWorker{accounting: tc.settler}
			err := w.settleTerminalReservation(context.Background(), BatchPollPayload{
				AccountID:     accountID.String(),
				ReservationID: reservationID.String(),
				ActualCredits: tc.actualCredits,
				Endpoint:      "/v1/chat/completions",
			}, nil, map[string]interface{}{}, "completed", map[string]interface{}{})

			if err == nil {
				t.Fatal("expected the divergence to surface as an error")
			}
			if !errors.Is(err, asynq.SkipRetry) {
				t.Fatalf("expected the task to be parked with asynq.SkipRetry so it stops re-polling into a failure no poll can clear, got %v", err)
			}
			// The typed cause must survive the wrapping, so anything reading
			// the archived task still learns what diverged and by how much.
			var got *accounting.SettlementDivergenceError
			if !errors.As(err, &got) {
				t.Fatalf("expected the *SettlementDivergenceError to remain in the chain, got %v", err)
			}
			if got.PostedCredits != 400 || got.WantCredits != 900 {
				t.Fatalf("divergence detail lost in wrapping: posted=%d want=%d", got.PostedCredits, got.WantCredits)
			}
		})
	}
}

// The counterpart, and the reason this is not simply "park on every settlement
// error": an ordinary settlement failure is usually transient (a dropped
// connection, a lock contention, a restart) and must keep its retry.
func TestSettleTerminalReservation_OrdinaryFailureStillRetries(t *testing.T) {
	settler := &fakeAccountingSettler{finalizeErr: errors.New("connection reset by peer")}
	w := &BatchWorker{accounting: settler}

	err := w.settleTerminalReservation(context.Background(), BatchPollPayload{
		AccountID:     uuid.NewString(),
		ReservationID: uuid.NewString(),
		ActualCredits: 900,
		Endpoint:      "/v1/chat/completions",
	}, nil, map[string]interface{}{}, "completed", map[string]interface{}{})

	if err == nil {
		t.Fatal("expected the settlement failure to surface as an error")
	}
	if errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("a transient settlement failure must keep its retry, got a parked task: %v", err)
	}
}
