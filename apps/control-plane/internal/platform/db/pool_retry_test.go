package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The failure this exists for is transient: the shared session-mode pool
// refuses new clients for a few seconds under load, and the boot that lands in
// that window used to give up permanently (issue #816). A later attempt must be
// able to succeed.
func TestOpenWithRetrySucceedsAfterTransientFailures(t *testing.T) {
	calls := 0
	open := func(context.Context) (*pgxpool.Pool, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("FATAL: (EMAXCONNSESSION) max clients reached in session mode")
		}
		return nil, nil
	}

	if _, err := openWithRetry(context.Background(), 6, time.Millisecond, open); err != nil {
		t.Fatalf("openWithRetry after transient failures = %v, want nil", err)
	}
	if calls != 3 {
		t.Fatalf("open called %d times, want 3", calls)
	}
}

// Exhausting the budget must surface the last error rather than a nil pool with
// a nil error, which the caller would treat as a working database.
func TestOpenWithRetryGivesUpAndReturnsLastError(t *testing.T) {
	calls := 0
	last := errors.New("last failure")
	open := func(context.Context) (*pgxpool.Pool, error) {
		calls++
		return nil, last
	}

	_, err := openWithRetry(context.Background(), 4, time.Millisecond, open)
	if !errors.Is(err, last) {
		t.Fatalf("openWithRetry error = %v, want %v", err, last)
	}
	if calls != 4 {
		t.Fatalf("open called %d times, want 4", calls)
	}
}

// A success on the first attempt must not pay any delay, so a healthy boot is
// not slowed down by the retry budget existing.
func TestOpenWithRetryDoesNotDelayOnFirstAttemptSuccess(t *testing.T) {
	calls := 0
	open := func(context.Context) (*pgxpool.Pool, error) {
		calls++
		return nil, nil
	}

	start := time.Now()
	if _, err := openWithRetry(context.Background(), 6, time.Hour, open); err != nil {
		t.Fatalf("openWithRetry = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("first-attempt success waited %s, want no delay", elapsed)
	}
	if calls != 1 {
		t.Fatalf("open called %d times, want 1", calls)
	}
}

// Shutdown during the backoff must abandon the retries instead of holding the
// process for the rest of the budget.
func TestOpenWithRetryStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	open := func(context.Context) (*pgxpool.Pool, error) {
		calls++
		cancel()
		return nil, errors.New("boom")
	}

	_, err := openWithRetry(ctx, 6, time.Hour, open)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("openWithRetry error = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("open called %d times after cancellation, want 1", calls)
	}
}

// An empty URL is a misconfiguration, not a transient fault, so it must fail
// immediately rather than burn the retry budget on a result that cannot change.
func TestOpenWithRetryDoesNotRetryEmptyURL(t *testing.T) {
	start := time.Now()
	_, err := OpenWithRetry(context.Background(), "", 6, time.Hour)
	if err == nil {
		t.Fatal("OpenWithRetry with an empty URL = nil error, want an error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("empty URL waited %s, want an immediate failure", elapsed)
	}
}
