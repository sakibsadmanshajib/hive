package db

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// transactionPoolerPort is Supavisor's transaction-mode port. Session mode is
// served on 5432 only (Supabase deprecated session mode on 6543 in February
// 2025), so this port is unambiguous.
const transactionPoolerPort = 6543

// Open creates and validates a pgxpool connection using the provided database URL.
// Returns an error if the URL is empty or the connection cannot be established.
//
// Pool size and query-exec mode come from the DSN (`pool_max_conns`,
// `default_query_exec_mode`) rather than being set here, so a deployment can
// budget its share of the shared Supavisor session-mode pool without a rebuild.
// See scripts/derive-pooler-dsn.py for how those DSNs are produced and why the
// budget matters.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is empty; set SUPABASE_DB_URL")
	}

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Control-plane cannot run against a transaction-mode pooler, and the
	// failure would be silent rather than loud: both users of session-scoped
	// state keep working well enough to look healthy.
	//
	//   - settings.Resolver.StartListener holds one connection open for the
	//     life of the process on `LISTEN tenant_settings_changed`. Transaction
	//     mode returns the connection after each transaction, so notifications
	//     stop arriving and every instance quietly serves stale tenant settings
	//     from cache.
	//   - accounting.PgxAccountLocker takes `pg_advisory_lock`, which is
	//     session scoped, and holds it across a whole credit reservation. If the
	//     connection can be handed to another client mid-reservation then the
	//     lock serialises nothing, so two concurrent reservations against one
	//     account can both proceed.
	//
	// Startup is the only place this is cheap to notice. edge-api has neither
	// dependency and is expected to use transaction mode.
	if cfg.ConnConfig.Port == transactionPoolerPort {
		return nil, fmt.Errorf(
			"database URL targets the transaction-mode pooler (port %d); control-plane requires session mode (port 5432) for LISTEN tenant_settings_changed and for session-scoped pg_advisory_lock",
			transactionPoolerPort,
		)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create db pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}

// OpenWithRetry calls Open until it succeeds or the attempt budget runs out.
//
// The session-mode pool this connects to is small and shared: CI runs, the demo
// box and local stacks all draw from the same pool_size 15 (see issue #631), so
// a boot that lands during a burst gets EMAXCONNSESSION on the first ping. That
// is a transient condition that clears in seconds, but a single failed ping used
// to leave the process permanently without a pool, serving none of its DB-backed
// routes for the rest of its life (issue #816). Retrying rides through the burst.
//
// Giving up remains safe rather than fatal: the caller keeps a nil pool, and
// /health reports degraded so nothing routes traffic to the process.
//
// An empty URL is a configuration error, not a transient one, so it fails on the
// first attempt instead of burning the whole budget.
func OpenWithRetry(ctx context.Context, databaseURL string, attempts int, delay time.Duration) (*pgxpool.Pool, error) {
	if databaseURL == "" {
		return Open(ctx, databaseURL)
	}
	return openWithRetry(ctx, attempts, delay, func(attemptCtx context.Context) (*pgxpool.Pool, error) {
		return Open(attemptCtx, databaseURL)
	})
}

// openWithRetry holds the retry loop separately from the connection itself so
// the backoff, the budget and the context handling are testable without a
// database.
func openWithRetry(
	ctx context.Context,
	attempts int,
	delay time.Duration,
	open func(context.Context) (*pgxpool.Pool, error),
) (*pgxpool.Pool, error) {
	if attempts < 1 {
		attempts = 1
	}

	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		var pool *pgxpool.Pool
		pool, err = open(ctx)
		if err == nil {
			if attempt > 1 {
				log.Printf("database pool opened on attempt %d of %d", attempt, attempts)
			}
			return pool, nil
		}
		if attempt == attempts {
			break
		}

		log.Printf("database not ready (attempt %d of %d), retrying in %s: %v", attempt, attempts, delay, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	return nil, err
}
