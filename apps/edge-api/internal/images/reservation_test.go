package images

import (
	"context"
	"errors"
	"testing"
)

// settleReservationMock is a white-box double for AccountingInterface, used
// only by this file's tests (package images, not images_test) so
// settleReservation (an unexported method) can be exercised directly
// without racing real HTTP dispatch to force a finalize failure.
type settleReservationMock struct {
	finalizeErr error

	finalizeCalled bool
	releaseCalled  bool
	// releaseCtxErr captures ctx.Err() as observed by ReleaseReservation, so
	// tests can prove the release ran on a live context even when the caller
	// passed settleReservation an already-cancelled one.
	releaseCtxErr error
}

func (m *settleReservationMock) CreateReservation(context.Context, ReservationInput) (string, error) {
	return "", nil
}

func (m *settleReservationMock) FinalizeReservation(context.Context, FinalizeInput) error {
	m.finalizeCalled = true
	return m.finalizeErr
}

func (m *settleReservationMock) ReleaseReservation(ctx context.Context, _, _, _ string) error {
	m.releaseCalled = true
	m.releaseCtxErr = ctx.Err()
	return nil
}

// TestSettleReservationReleasesOnFinalizeFailure is the #616 regression guard:
// a failed finalize must release the hold rather than stranding it.
func TestSettleReservationReleasesOnFinalizeFailure(t *testing.T) {
	m := &settleReservationMock{finalizeErr: errors.New("control-plane unreachable")}
	h := &Handler{accounting: m}

	h.settleReservation(context.Background(), "acct-1", "res-1", 5000, "/v1/images/generations")

	if !m.finalizeCalled {
		t.Fatal("expected FinalizeReservation to be attempted")
	}
	if !m.releaseCalled {
		t.Fatal("expected ReleaseReservation to be called after finalize failure: the hold would otherwise strand")
	}
}

// TestSettleReservationNeverReleasesAfterSuccessfulFinalize is the
// exactly-once-terminal-state guard: a reservation that finalized (charged)
// successfully must never also be released, which would refund a
// legitimate charge.
func TestSettleReservationNeverReleasesAfterSuccessfulFinalize(t *testing.T) {
	m := &settleReservationMock{finalizeErr: nil}
	h := &Handler{accounting: m}

	h.settleReservation(context.Background(), "acct-1", "res-1", 5000, "/v1/images/generations")

	if !m.finalizeCalled {
		t.Fatal("expected FinalizeReservation to be attempted")
	}
	if m.releaseCalled {
		t.Fatal("expected ReleaseReservation NOT to be called after a successful finalize: would double-settle the reservation")
	}
}

// TestSettleReservationReleaseSurvivesCancelledRequestContext is the #602
// trap reproduced on the media path: by the time settleReservation runs the
// client may already have disconnected, cancelling the request context. The
// release must run on a fresh background context, not the cancelled one, or
// it silently fails the same way the original finalize call used to.
func TestSettleReservationReleaseSurvivesCancelledRequestContext(t *testing.T) {
	m := &settleReservationMock{finalizeErr: errors.New("upstream write failed: client disconnected")}
	h := &Handler{accounting: m}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // simulate the client having already disconnected

	h.settleReservation(cancelledCtx, "acct-1", "res-1", 5000, "/v1/images/generations")

	if !m.releaseCalled {
		t.Fatal("expected ReleaseReservation to be called despite the cancelled request context")
	}
	if m.releaseCtxErr != nil {
		t.Fatalf("expected ReleaseReservation to receive a live (non-cancelled) context, got ctx.Err() = %v", m.releaseCtxErr)
	}
}
