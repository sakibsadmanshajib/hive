package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// transactionPoolerPort is Supavisor's transaction-mode port. Session mode is
// served on 5432 only (Supabase deprecated session mode on 6543 in February
// 2025), so this port is unambiguous.
const transactionPoolerPort = 6543

// openAttemptTimeout bounds a single connection attempt so one hung dial
// cannot consume a caller's whole retry budget.
const openAttemptTimeout = 10 * time.Second

// minOpenAttemptTimeout is the floor the per-attempt clamp will not go under,
// so a caller with a very short budget still gets one honest attempt rather
// than a context that expires mid-dial and reports a timeout for a connection
// it never really tried to make.
const minOpenAttemptTimeout = time.Second

// ErrConfig marks a failure that no amount of waiting repairs: the DSN is
// missing, unparseable, or names the wrong pooler mode. A caller riding out a
// transient outage matches on it to stop immediately rather than spend its
// budget on a value that can never work.
var ErrConfig = errors.New("database configuration error")

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
		return nil, fmt.Errorf("%w: database URL is empty; set SUPABASE_DB_URL", ErrConfig)
	}

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to parse database URL: %w", ErrConfig, err)
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
			"%w: database URL targets the transaction-mode pooler (port %d); control-plane requires session mode (port 5432) for LISTEN tenant_settings_changed and for session-scoped pg_advisory_lock",
			ErrConfig, transactionPoolerPort,
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

// OpenWithRetry calls Open until it succeeds or budget elapses, waiting
// interval between attempts.
//
// The session-mode pooler control-plane must use is a shared, capped resource:
// 15 clients across CI, developer stacks and the live deployment. A boot that
// lands inside a contention spike is refused with EMAXCONNSESSION and the spike
// clears within seconds. Database readiness, however, is captured once at
// startup and never re-evaluated, so a single refused attempt leaves the
// process reporting a degraded /health for its entire life, its dependent
// containers unhealthy, and no path back short of a manual restart. Spending
// the budget here is what stops a momentary contention window from becoming a
// permanent one.
//
// Two things this deliberately does not do. It does not retry an ErrConfig,
// because a missing DSN or the wrong pooler mode will read the same way in
// ninety seconds as it does now. And it does not swallow the outcome: when the
// budget runs out the last error is returned, so a database that is genuinely
// unreachable still fails loudly rather than being retried into silence.
func OpenWithRetry(ctx context.Context, databaseURL string, budget, interval time.Duration) (*pgxpool.Pool, error) {
	deadline := time.Now().Add(budget)
	for attempt := 1; ; attempt++ {
		// Cap the attempt to what is left of the budget. A host that accepts the
		// connection and then stalls in the startup exchange would otherwise
		// block for the full openAttemptTimeout past the deadline, and the
		// budget is sized against a healthcheck window: overrunning it fails the
		// container just as surely as never retrying at all.
		attemptTimeout := openAttemptTimeout
		if remaining := time.Until(deadline); remaining < attemptTimeout {
			attemptTimeout = remaining
		}
		if attemptTimeout < minOpenAttemptTimeout {
			attemptTimeout = minOpenAttemptTimeout
		}
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		pool, err := Open(attemptCtx, databaseURL)
		cancel()
		if err == nil {
			return pool, nil
		}
		if errors.Is(err, ErrConfig) || ctx.Err() != nil {
			return nil, err
		}
		// Stop rather than sleep past the deadline for an attempt that would
		// land outside the budget the caller asked for.
		if !time.Now().Add(interval).Before(deadline) {
			return nil, fmt.Errorf("database unreachable after %d attempt(s) over %s: %w", attempt, budget, err)
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(interval):
		}
	}
}
