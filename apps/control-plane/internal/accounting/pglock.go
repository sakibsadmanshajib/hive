package accounting

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxAccountLocker serializes the reservation critical section across all
// control-plane instances using a Postgres session-level advisory lock keyed on
// the account ID. This is the production locker for multi-instance deployments,
// where in-process mutexes (processAccountLocker) are insufficient.
//
// The lock is held on a single dedicated connection for the duration of fn and
// always released, so the committed reservation hold from one request is
// visible to the next waiter's balance read.
type PgxAccountLocker struct {
	pool *pgxpool.Pool
	// proc gates entry per account WITHIN this process before any pool
	// connection is checked out (see WithAccountLock for why this matters).
	proc AccountLocker
}

// NewPgxAccountLocker returns a Postgres advisory-lock-backed account locker.
func NewPgxAccountLocker(pool *pgxpool.Pool) *PgxAccountLocker {
	return &PgxAccountLocker{pool: pool, proc: NewProcessAccountLocker()}
}

func (l *PgxAccountLocker) WithAccountLock(ctx context.Context, accountID uuid.UUID, fn func(ctx context.Context) error) error {
	// Gate per account IN-PROCESS first, before touching the pool. Otherwise
	// every concurrent same-account request would check out a pool connection
	// and then block inside pg_advisory_lock; the holder's fn (which needs
	// further pool connections for its ledger/usage writes) could then be
	// starved of connections by the waiters, deadlocking the reservation path
	// until contexts time out. With the in-process gate, at most one goroutine
	// per account per instance ever holds a connection for the advisory lock,
	// so cross-process serialization costs at most one held connection per
	// running instance.
	return l.proc.WithAccountLock(ctx, accountID, func(ctx context.Context) error {
		conn, err := l.pool.Acquire(ctx)
		if err != nil {
			return fmt.Errorf("accounting: acquire advisory lock conn: %w", err)
		}
		defer conn.Release()

		key := accountID.String()
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1)::int8)`, key); err != nil {
			return fmt.Errorf("accounting: acquire account advisory lock: %w", err)
		}
		// Release the advisory lock on the same connection no matter how fn exits.
		defer func() {
			// Use a background context so unlock still runs if ctx was cancelled.
			_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext($1)::int8)`, key)
		}()

		return fn(ctx)
	})
}
