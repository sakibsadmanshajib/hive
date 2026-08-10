package db_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

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

// TestOpenWithRetry_RidesOutATransientRefusal is the guard on the behaviour
// that turned a momentary pooler spike into a failed CI job and a permanently
// degraded container: control-plane made exactly one connection attempt, about
// two seconds into a 145-second health window, and then sat out the remaining
// 143 seconds guaranteed to fail. The assertion is on elapsed time, because a
// version that gives up after one attempt returns in about a millisecond and
// would otherwise pass every check on the error value alone.
func TestOpenWithRetry_RidesOutATransientRefusal(t *testing.T) {
	const (
		budget   = 350 * time.Millisecond
		interval = 100 * time.Millisecond
	)
	start := time.Now()
	pool, err := db.OpenWithRetry(
		context.Background(),
		"postgres://u:p@127.0.0.1:1/db?connect_timeout=1",
		budget, interval,
	)
	elapsed := time.Since(start)

	if err == nil {
		pool.Close()
		t.Fatal("expected an error for an unreachable host, got nil")
	}
	if pool != nil {
		pool.Close()
		t.Fatal("expected a nil pool when every attempt failed")
	}
	// At least two waits must have happened, or nothing was retried.
	if elapsed < 2*interval {
		t.Fatalf("returned after %s; a budget of %s was not spent retrying", elapsed, budget)
	}
	// The budget is a ceiling, not a suggestion: overrunning it would push the
	// boot past the healthcheck's start_period and fail the container anyway.
	if elapsed > budget+2*time.Second {
		t.Fatalf("returned after %s, well past the %s budget", elapsed, budget)
	}
	// The operator needs to see that waiting was tried and did not help.
	if !strings.Contains(err.Error(), "attempt(s) over") {
		t.Errorf("error should report the attempts made, got: %v", err)
	}
}

// TestOpenWithRetry_CapsAnAttemptToTheRemainingBudget covers the failure a
// refused connection cannot reach: a host that accepts the TCP connection and
// then stalls in the Postgres startup exchange. Without the per-attempt clamp
// that dial blocks for the full 10s openAttemptTimeout no matter how small the
// budget was, which would push a boot past the healthcheck window the budget
// exists to fit inside.
func TestOpenWithRetry_CapsAnAttemptToTheRemainingBudget(t *testing.T) {
	// A listener that accepts and then says nothing. Connections are held open
	// (not closed) so the client blocks on the startup exchange rather than
	// seeing EOF.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				<-done
				_ = conn.Close()
			}()
		}
	}()

	const budget = 300 * time.Millisecond
	start := time.Now()
	pool, err := db.OpenWithRetry(
		context.Background(),
		"postgres://u:p@"+ln.Addr().String()+"/db",
		budget, 50*time.Millisecond,
	)
	elapsed := time.Since(start)

	if err == nil {
		pool.Close()
		t.Fatal("expected an error against a stalled server, got nil")
	}
	// The clamp floor is one second. Without the clamp this takes the full
	// ten-second attempt timeout instead.
	if elapsed > 3*time.Second {
		t.Fatalf("a stalled dial ran for %s: the attempt was not capped to the budget", elapsed)
	}
}

// TestOpenWithRetry_DoesNotRetryConfigErrors pins the other half. A missing DSN
// or a transaction-mode pooler reads exactly the same after ninety seconds of
// waiting, so retrying one only delays a fatal misconfiguration behind a long
// silence at boot.
func TestOpenWithRetry_DoesNotRetryConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
	}{
		{name: "empty DSN", dsn: ""},
		{name: "unparseable DSN", dsn: "not-a-postgres-url"},
		{
			name: "transaction-mode pooler",
			dsn:  "postgresql://u:p@aws-1-us-east-1.pooler.supabase.com:6543/postgres",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const budget = 5 * time.Second
			start := time.Now()
			pool, err := db.OpenWithRetry(context.Background(), tc.dsn, budget, time.Second)
			elapsed := time.Since(start)

			if err == nil {
				pool.Close()
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, db.ErrConfig) {
				t.Fatalf("error should be an ErrConfig so callers can stop early, got: %v", err)
			}
			if elapsed > time.Second {
				t.Fatalf("took %s: a configuration error was retried instead of returned", elapsed)
			}
		})
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

// TestSessionPoolReleasesIdleConnections is the measurement behind the two pool
// lifetime parameters the session DSN now carries.
//
// A session-mode slot is held for as long as the pgxpool connection that owns
// it exists, not for as long as it is being used, and pgxpool holds an idle
// connection for thirty minutes by default. That is what exhausts the fifteen
// shared session slots: a consumer that bursts to its cap once keeps that many
// slots for the next half hour of running no queries at all, so three idle
// consumers can hold the whole ceiling between them. Measured on 2026-08-10
// with no CI job running, six parallel session-mode connections were all
// refused with EMAXCONNSESSION.
//
// The assertion is on the count of live server backends rather than on the
// parsed config, because a config value that never reaches the reaper releases
// nothing. Timings here are the DSN's own, shortened: an explicit value in the
// DSN wins over the deployment default, which is the same override path a
// deployment under steady traffic would use to lengthen the window.
func TestSessionPoolReleasesIdleConnections(t *testing.T) {
	base := os.Getenv("HIVE_TEST_DB_URL")
	if base == "" {
		t.Skip("HIVE_TEST_DB_URL not set; this test needs a live Postgres")
	}

	const held = 4
	separator := "?"
	if strings.Contains(base, "?") {
		separator = "&"
	}
	appName := fmt.Sprintf("hive_idle_release_%d", time.Now().UnixNano())
	dsn := fmt.Sprintf(
		"%s%sapplication_name=%s&pool_max_conns=%d&pool_max_conn_idle_time=1s&pool_health_check_period=250ms",
		base, separator, appName, held,
	)

	ctx := context.Background()
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	// Count from outside the pool under test, or the counting query would
	// itself occupy one of the connections being counted.
	observer, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect observer: %v", err)
	}
	defer observer.Close(context.Background())

	backends := func() int {
		var n int
		if err := observer.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE application_name = $1`,
			appName).Scan(&n); err != nil {
			t.Fatalf("count backends: %v", err)
		}
		return n
	}

	conns := make([]*pgxpool.Conn, 0, held)
	for i := 0; i < held; i++ {
		c, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	if got := backends(); got != held {
		t.Fatalf("with %d connections checked out: want %d server backends, got %d", held, held, got)
	}
	for _, c := range conns {
		c.Release()
	}

	// Idle window plus one reaper tick, with slack for a loaded CI runner.
	deadline := time.Now().Add(15 * time.Second)
	got := held
	for time.Now().Before(deadline) {
		if got = backends(); got == 0 {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("after release: want 0 server backends once the idle window elapses, still holding %d; "+
		"the pool is squatting session slots it is not using", got)
}
