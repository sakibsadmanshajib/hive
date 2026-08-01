package audio

import (
	"context"
	"errors"
	"testing"
)

// settleReservationMock is a white-box double for AccountingInterface, used
// only by this file's tests (package audio, not audio_test) so
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
	// finalizeCtxErr captures ctx.Err() as observed by FinalizeReservation,
	// so a test can prove finalize itself ran on a live context (#637) even
	// when the caller passed settleReservation an already-cancelled one. A
	// cancelled context here mimics what a real HTTP client does: it aborts
	// immediately rather than making the call, which is exactly how #637
	// converted a chargeable request into a released one.
	finalizeCtxErr error
}

func (m *settleReservationMock) CreateReservation(context.Context, ReservationInput) (string, error) {
	return "", nil
}

func (m *settleReservationMock) FinalizeReservation(ctx context.Context, _ FinalizeInput) error {
	m.finalizeCalled = true
	m.finalizeCtxErr = ctx.Err()
	if ctx.Err() != nil {
		// A real accounting HTTP client aborts immediately on an
		// already-cancelled context rather than reaching the control plane.
		return ctx.Err()
	}
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

	h.settleReservation(context.Background(), "acct-1", "res-1", 500, "/v1/audio/speech")

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

	h.settleReservation(context.Background(), "acct-1", "res-1", 500, "/v1/audio/speech")

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

	h.settleReservation(cancelledCtx, "acct-1", "res-1", 500, "/v1/audio/speech")

	if !m.releaseCalled {
		t.Fatal("expected ReleaseReservation to be called despite the cancelled request context")
	}
	if m.releaseCtxErr != nil {
		t.Fatalf("expected ReleaseReservation to receive a live (non-cancelled) context, got ctx.Err() = %v", m.releaseCtxErr)
	}
}

// TestSettleReservationFinalizeSurvivesCancelledRequestContext is #637's
// primary regression guard: a client that disconnects right before
// settlement must still be CHARGED for delivered work, not have the hold
// released. finalizeErr is nil here (finalize would succeed if it reached
// the control plane on a live context), so if settleReservation still runs
// FinalizeReservation on the caller's cancelled ctx, the mock's cancelled-
// context short-circuit turns that success into a failure and this test
// catches the resulting spurious release.
func TestSettleReservationFinalizeSurvivesCancelledRequestContext(t *testing.T) {
	m := &settleReservationMock{finalizeErr: nil}
	h := &Handler{accounting: m}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // simulate the client having already disconnected before settlement

	h.settleReservation(cancelledCtx, "acct-1", "res-1", 500, "/v1/audio/speech")

	if !m.finalizeCalled {
		t.Fatal("expected FinalizeReservation to be attempted")
	}
	if m.finalizeCtxErr != nil {
		t.Fatalf("expected FinalizeReservation to receive a live (non-cancelled) context despite the caller's ctx being cancelled, got ctx.Err() = %v", m.finalizeCtxErr)
	}
	if m.releaseCalled {
		t.Fatal("expected delivered work to be CHARGED, not released, despite the client disconnecting before settlement (#637)")
	}
}
