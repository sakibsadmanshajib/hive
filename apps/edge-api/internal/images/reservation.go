package images

import (
	"context"
	"log"
	"time"
)

// releaseTimeout bounds the background-context reservation release attempted
// when FinalizeReservation fails. Mirrors the inference package's
// accountingTimeout/releaseReservationBackground pattern (PR #602) so a
// finalize failure never strands the hold, even when the request context is
// already cancelled by the time settleReservation runs.
const releaseTimeout = 30 * time.Second

// settleReservation finalizes a reservation and, only when finalize itself
// fails, releases it instead, so a reservation always reaches a terminal
// state exactly once (charged or released), and a failed charge never
// leaves the hold stranded (#616). This used to be a bare
// `_ = h.accounting.FinalizeReservation(...)`, whose error was discarded: a
// failed finalize neither charged nor released the hold, stranding it
// forever.
//
// The release runs on a fresh, bounded background context, never ctx. By
// the time a request reaches this point the client may already have
// disconnected, cancelling ctx, which would otherwise silently swallow the
// release the same way it swallowed the original finalize: the exact bug
// PR #602 fixed on the streaming inference path, reproduced here on the
// media path.
//
// Release is attempted only when finalize errors, so a reservation that
// finalized successfully is never also released (which would refund a
// legitimate charge).
func (h *Handler) settleReservation(ctx context.Context, accountID, reservationID string, actualCredits int64, endpoint string) {
	err := h.accounting.FinalizeReservation(ctx, FinalizeInput{
		AccountID:     accountID,
		ReservationID: reservationID,
		ActualCredits: actualCredits,
	})
	if err == nil {
		return
	}
	log.Printf("images: finalize reservation failed endpoint=%s reservation_id=%s: %v", endpoint, reservationID, err)

	bgCtx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	if relErr := h.accounting.ReleaseReservation(bgCtx, accountID, reservationID, "finalize_failed"); relErr != nil {
		log.Printf("images: release reservation after finalize failure also failed endpoint=%s reservation_id=%s: %v", endpoint, reservationID, relErr)
	}
}
