package images

import (
	"context"
	"log"
	"time"
)

// releaseTimeout bounds each background-context accounting call in
// settleReservation. Mirrors the inference package's
// accountingTimeout/releaseReservationBackground pattern (PR #602) so a
// finalize failure never strands the hold, even when the request context is
// already cancelled by the time settleReservation runs. A var, not a const,
// so tests can shrink it to exercise the deadline-exhaustion path in
// milliseconds instead of real seconds.
var releaseTimeout = 30 * time.Second

// settleReservation finalizes a reservation and, only when finalize itself
// fails, releases it instead, so a reservation always reaches a terminal
// state exactly once (charged or released), and a failed charge never
// leaves the hold stranded (#616). This used to be a bare
// `_ = h.accounting.FinalizeReservation(...)`, whose error was discarded: a
// failed finalize neither charged nor released the hold, stranding it
// forever.
//
// Both finalize AND its fallback release run on a fresh, bounded background
// context, never ctx (#637). By the time a request reaches this point the
// client may already have disconnected, cancelling ctx: finalizing on ctx
// then fails not because the ledger rejected the charge but because the
// underlying HTTP call aborts on the cancelled context, which used to fall
// through to the release-on-finalize-failure branch below and release a
// hold for work that was genuinely delivered and should have been charged.
// That's the exact bug PR #602 fixed on the streaming inference path
// (settleStream), reproduced here on the media path: the client
// disconnecting must not convert a chargeable request into a free one.
// ctx itself is accepted only for call-site symmetry with the rest of the
// handler and is otherwise unused -- settlement never depends on the
// request context surviving to this point.
//
// Release is attempted only when finalize errors, so a reservation that
// finalized successfully is never also released (which would refund a
// legitimate charge).
//
// Finalize and release each get their OWN context.WithTimeout call, the
// release one created only after finalize has already failed, not one
// shared context reused for both. A single shared deadline would let a
// finalize call that is merely SLOW (not instant) consume the whole
// window, so the fallback release would then run on an already-expired
// context and fail with context deadline exceeded, stranding the hold --
// exactly #616's failure mode, reintroduced through the fix meant to
// prevent it. Giving the release a fresh, full budget is what actually
// keeps the exactly-once-terminal-state invariant.
func (h *Handler) settleReservation(ctx context.Context, accountID, reservationID string, actualCredits int64, endpoint string) {
	_ = ctx // intentionally unused -- see comment above (#637)

	finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), releaseTimeout)
	err := h.accounting.FinalizeReservation(finalizeCtx, FinalizeInput{
		AccountID:     accountID,
		ReservationID: reservationID,
		ActualCredits: actualCredits,
	})
	finalizeCancel()
	if err == nil {
		return
	}
	log.Printf("images: finalize reservation failed endpoint=%s reservation_id=%s: %v", endpoint, reservationID, err)

	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer releaseCancel()
	if relErr := h.accounting.ReleaseReservation(releaseCtx, accountID, reservationID, "finalize_failed"); relErr != nil {
		log.Printf("images: release reservation after finalize failure also failed endpoint=%s reservation_id=%s: %v", endpoint, reservationID, relErr)
	}
}
