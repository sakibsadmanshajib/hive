package db

import (
	"context"
	"fmt"

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
