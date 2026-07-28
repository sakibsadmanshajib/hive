package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/db"
)

// TestOpen_EmptyURL verifies that an empty database URL is rejected immediately
// with a descriptive error before any network call is attempted.
func TestOpen_EmptyURL(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, "")
	if err == nil {
		pool.Close()
		t.Fatal("expected error for empty database URL, got nil")
	}
	if !strings.Contains(err.Error(), "database URL is empty") {
		t.Fatalf("expected 'database URL is empty' in error, got: %v", err)
	}
}

// TestOpen_InvalidURL verifies that a syntactically invalid DSN returns a
// wrapped error and does not return a non-nil pool.
func TestOpen_InvalidURL(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, "not-a-postgres-url")
	if err == nil {
		pool.Close()
		t.Fatal("expected error for invalid DSN, got nil")
	}
	if pool != nil {
		pool.Close()
		t.Fatal("expected nil pool on error")
	}
}

// TestOpen_UnreachableHost verifies that a well-formed DSN pointing at an
// unreachable host returns a non-nil error with pool closed (fail-closed
// semantics: we do not hand back a pool that cannot be pinged).
func TestOpen_UnreachableHost(t *testing.T) {
	ctx := context.Background()
	// Use a valid DSN format but an address that will always be refused.
	pool, err := db.Open(ctx, "postgres://user:pass@127.0.0.1:1/testdb?connect_timeout=1")
	if err == nil {
		pool.Close()
		t.Fatal("expected error for unreachable host, got nil")
	}
	if pool != nil {
		pool.Close()
		t.Fatal("expected nil pool when ping fails")
	}
}

// TestOpen_RefusesTransactionModePooler pins the one mode control-plane cannot
// run in. It holds a permanent connection on LISTEN tenant_settings_changed and
// takes a session-scoped pg_advisory_lock across a whole credit reservation;
// under transaction mode both keep looking healthy while silently doing nothing,
// so refusing at startup is the only cheap signal. The DSN below is unreachable,
// which is the point: the refusal must happen before any network call.
func TestOpen_RefusesTransactionModePooler(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, "postgresql://u:p@aws-1-us-east-1.pooler.supabase.com:6543/postgres")
	if err == nil {
		pool.Close()
		t.Fatal("expected Open to refuse a transaction-mode DSN, got nil")
	}
	if pool != nil {
		pool.Close()
		t.Fatal("expected nil pool when the pooler mode is wrong")
	}
	// The message has to name the cause, or the next person sees only a
	// connection error and starts debugging the network.
	for _, want := range []string{"transaction-mode", "session mode", "LISTEN", "pg_advisory_lock"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got: %v", want, err)
		}
	}
}

// TestPoolerDSNCarriesBudgetAndExecMode asserts pgx honours the two DSN
// parameters the pooler budget is expressed with. They live in the DSN rather
// than in code so a deployment can resize without a rebuild, which only works
// while pgx keeps parsing them. If a pgx upgrade dropped either one, both Go
// services would silently revert to pgxpool's default of max(4, NumCPU)
// connections each and re-exhaust the 15-client session-mode pooler.
func TestPoolerDSNCarriesBudgetAndExecMode(t *testing.T) {
	sessionDSN := "postgresql://u:p@aws-1-us-east-1.pooler.supabase.com:5432/postgres?pool_max_conns=6"
	cfg, err := pgxpool.ParseConfig(sessionDSN)
	if err != nil {
		t.Fatalf("parse session DSN: %v", err)
	}
	if cfg.MaxConns != 6 {
		t.Errorf("session MaxConns: want 6 got %d", cfg.MaxConns)
	}
	if cfg.ConnConfig.DefaultQueryExecMode != pgx.QueryExecModeCacheStatement {
		t.Errorf("session exec mode: want the pgx default of cache statement, got %v",
			cfg.ConnConfig.DefaultQueryExecMode)
	}

	transactionDSN := "postgresql://u:p@aws-1-us-east-1.pooler.supabase.com:6543/postgres?pool_max_conns=8&default_query_exec_mode=exec"
	cfg, err = pgxpool.ParseConfig(transactionDSN)
	if err != nil {
		t.Fatalf("parse transaction DSN: %v", err)
	}
	if cfg.MaxConns != 8 {
		t.Errorf("transaction MaxConns: want 8 got %d", cfg.MaxConns)
	}
	// Transaction mode cannot carry a prepared statement across the connection
	// it was prepared on, so pgx has to stop caching them.
	if cfg.ConnConfig.DefaultQueryExecMode != pgx.QueryExecModeExec {
		t.Errorf("transaction exec mode: want exec got %v", cfg.ConnConfig.DefaultQueryExecMode)
	}
	// Neither may survive as a server runtime parameter, or every connection
	// would fail with "unrecognized configuration parameter".
	for _, unwanted := range []string{"pool_max_conns", "default_query_exec_mode"} {
		if _, ok := cfg.ConnConfig.RuntimeParams[unwanted]; ok {
			t.Errorf("%q leaked into server runtime params", unwanted)
		}
	}
}

// TestOpen_ErrorWrapping verifies that errors returned from Open contain
// contextual wrappers (i.e. the caller can identify the failure stage).
func TestOpen_ErrorWrapping(t *testing.T) {
	cases := []struct {
		name    string
		dsn     string
		wantMsg string
	}{
		{
			name:    "empty URL",
			dsn:     "",
			wantMsg: "database URL is empty",
		},
		{
			name:    "unreachable host",
			dsn:     "postgres://u:p@127.0.0.1:1/db?connect_timeout=1",
			wantMsg: "failed to",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pool, err := db.Open(ctx, tc.dsn)
			if err == nil {
				pool.Close()
				t.Fatalf("expected error for DSN %q, got nil", tc.dsn)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}
