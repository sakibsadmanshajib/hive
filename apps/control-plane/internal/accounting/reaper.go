package accounting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ReaperReleaseReason is stamped on every hold the reaper retires. It lands in
// credit_reservation_events.reason and in the release ledger entry metadata, so
// a pass is auditable long after the log line has rotated away.
const ReaperReleaseReason = "stranded_hold_reaper"

// ReaperDefaultTTL is how long a hold must sit in a non-terminal state before
// the reaper treats its request as abandoned rather than in flight.
//
// Releasing a hold for a request that is still running would revoke the
// authorization for work being performed, which then bills against nothing, so
// the TTL is set from the longest a request can legitimately still be running
// rather than from how quickly stranded credits should come back:
//
//   - every inference call, streaming included, is bounded at 120s by the
//     LiteLLM client (apps/edge-api/internal/inference/litellm_client.go);
//     http.Client.Timeout covers reading the response body, so an SSE stream
//     cannot outlive it
//   - the chat sidecar dispatch client is bounded at 5 minutes
//     (apps/edge-api/internal/chat/dispatch.go)
//   - a single control-plane accounting call is bounded at 30s
//     (accountingTimeout in apps/edge-api/internal/inference)
//
// One hour is twelve times the largest of those. Batches, the one path that can
// legitimately hold credits for a whole 24h completion window, are excluded
// structurally by ListStaleReservations rather than by inflating this value.
const ReaperDefaultTTL = time.Hour

const (
	reaperDefaultInterval   = 15 * time.Minute
	reaperDefaultBatchLimit = 200
)

// staleReservationLister is the candidate source the reaper reads. It matches
// Repository.ListStaleReservations so the production repository drops in
// directly. Declared here per Go's interface-where-used convention.
type staleReservationLister interface {
	ListStaleReservations(ctx context.Context, olderThan time.Time, limit int) ([]Reservation, error)
}

// reservationReleaser is the settlement surface the reaper drives. It matches
// Service.ReleaseReservation: releases go through the accounting service so
// they take the per-account lock, post a real reservation_release ledger entry
// under an idempotency key, write the reservation event, and unwind the API key
// reservation delta. The reaper never writes to the ledger itself.
type reservationReleaser interface {
	ReleaseReservation(ctx context.Context, input ReleaseReservationInput) (Reservation, error)
}

// ReapResult is one pass worth of work, recorded so a continuing leak shows up
// as a rising released count instead of staying silent.
type ReapResult struct {
	Scanned  int
	Released int
	Credits  int64
	Failed   int
}

// ReaperConfig controls Reaper behaviour. Zero values take defaults.
type ReaperConfig struct {
	TTL        time.Duration
	Interval   time.Duration
	BatchLimit int
	Logger     *slog.Logger
	Now        func() time.Time
}

// Reaper releases credit holds whose request never reached a terminal state.
//
// It exists because a finalize that fails loses its charge and strands the
// hold in the same step, and reserved credits are subtracted from available
// balance forever after (issue #616). Without it, an account can be refused
// service it has paid for by its own abandoned holds.
type Reaper struct {
	repo     staleReservationLister
	releaser reservationReleaser
	ttl      time.Duration
	interval time.Duration
	limit    int
	logger   *slog.Logger
	now      func() time.Time

	mu      sync.Mutex
	cancel  context.CancelFunc
	doneCh  chan struct{}
	started bool
}

// NewReaper builds a Reaper. Both dependencies must be non-nil.
func NewReaper(repo staleReservationLister, releaser reservationReleaser, cfg ReaperConfig) *Reaper {
	if repo == nil || releaser == nil {
		panic("accounting: nil reaper dependency")
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = ReaperDefaultTTL
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = reaperDefaultInterval
	}
	limit := cfg.BatchLimit
	if limit <= 0 {
		limit = reaperDefaultBatchLimit
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Reaper{
		repo:     repo,
		releaser: releaser,
		ttl:      ttl,
		interval: interval,
		limit:    limit,
		logger:   logger,
		now:      now,
	}
}

// RunOnce performs exactly one reaping pass synchronously. It is the unit of
// work the loop schedules and the unit tests exercise.
//
// Two passes may overlap without releasing a hold twice. That is enforced by
// the database, not by a check here: the release carries the fixed idempotency
// key reservation:<id>:release, and the credit_idempotency_keys primary key
// turns the second write into a no-op that returns the first entry, so the
// second pass posts no ledger row. The per-account advisory lock and the
// service backstops that refuse to release a finalized reservation and to
// finalize a released one close the remaining orderings.
func (r *Reaper) RunOnce(ctx context.Context) (ReapResult, error) {
	cutoff := r.now().Add(-r.ttl)

	stale, err := r.repo.ListStaleReservations(ctx, cutoff, r.limit)
	if err != nil {
		r.logger.WarnContext(ctx, "accounting: reaper candidate scan failed",
			"error", err, "cutoff", cutoff.Format(time.RFC3339))
		return ReapResult{}, fmt.Errorf("accounting: reaper scan: %w", err)
	}

	result := ReapResult{Scanned: len(stale)}
	for _, reservation := range stale {
		if ctx.Err() != nil {
			break
		}

		held := remainingHeldCredits(reservation)
		if _, err := r.releaser.ReleaseReservation(ctx, ReleaseReservationInput{
			AccountID:     reservation.AccountID,
			ReservationID: reservation.ID,
			Reason:        ReaperReleaseReason,
		}); err != nil {
			// A refused release is not fatal to the pass: one reservation that
			// settled underneath us must not strand the rest of the batch.
			result.Failed++
			// A divergence is different in kind from a lost race. The ledger
			// already holds a release for a different amount, so no pass can
			// ever clear this hold and an operator has to reconcile it; log it
			// at error so alerting separates it from the ordinary refusals this
			// loop expects to see.
			var divergence *SettlementDivergenceError
			if errors.As(err, &divergence) {
				r.logger.ErrorContext(ctx, "accounting: reaper found a hold whose ledger release disagrees with the row, manual reconciliation required",
					"reservation_id", reservation.ID.String(),
					"account_id", reservation.AccountID.String(),
					"posted_release_credits", divergence.PostedCredits,
					"held_credits", divergence.WantCredits)
				continue
			}
			r.logger.WarnContext(ctx, "accounting: reaper could not release a stranded hold",
				"reservation_id", reservation.ID.String(),
				"account_id", reservation.AccountID.String(),
				"error", err)
			continue
		}

		result.Released++
		// ponytail: credits are counted from the candidate snapshot, so two
		// overlapping passes can each count the same hold in their own totals.
		// The ledger cannot double count it, per the idempotency key above, and
		// the durable per-hold record is the credit_reservation_events row.
		result.Credits += held
	}

	if result.Released > 0 || result.Failed > 0 {
		r.logger.InfoContext(ctx, "accounting: reaper released stranded holds",
			"scanned", result.Scanned,
			"released", result.Released,
			"credits", result.Credits,
			"failed", result.Failed,
			"ttl", r.ttl.String(),
			"cutoff", cutoff.Format(time.RFC3339))
	}

	return result, nil
}

// Start launches the reaping loop on a background goroutine. Subsequent Start
// calls are no-ops until Stop is called. The supplied parent context controls
// loop cancellation in addition to Stop.
func (r *Reaper) Start(parent context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	doneCh := make(chan struct{})
	r.cancel = cancel
	r.doneCh = doneCh
	r.started = true

	go r.loop(ctx, doneCh)
}

// Stop signals the loop to exit and waits for the in-flight pass to finish.
// Safe to call multiple times.
func (r *Reaper) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	cancel := r.cancel
	doneCh := r.doneCh
	r.started = false
	r.cancel = nil
	r.doneCh = nil
	r.mu.Unlock()

	cancel()
	<-doneCh
}

func (r *Reaper) loop(ctx context.Context, doneCh chan<- struct{}) {
	defer close(doneCh)

	// Eager first pass. Holds already stranded before this process started are
	// the reason the reaper exists, and waiting a full interval to return them
	// keeps an account locked out of an API it has paid for.
	if _, err := r.RunOnce(ctx); err != nil {
		// already logged
		_ = err
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := r.RunOnce(ctx); err != nil {
				// already logged
				_ = err
			}
		}
	}
}
