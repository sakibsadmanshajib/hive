// Package dbtest is the one shared gate for every control-plane/edge-api
// test suite that needs a real Postgres. Before this package existed, each
// suite hand-rolled its own "skip if the DSN env var is unset" helper, and
// every one of those copies skipped unconditionally, so a suite guarding
// the highest-stakes paths in the product (double-spend prevention,
// membership RLS, settlement idempotency) could go months without ever
// actually running in CI while still reporting a green check, following the
// same failure shape as issues #655, #701, and #708.
//
// A skipped test and a passing test are indistinguishable inside a green
// check, so RequireURL treats a missing DSN differently depending on where
// it runs: locally it skips (a laptop with no test Postgres is a normal
// day), but in CI (CI env var set, and not a plain `-short` compile pass)
// a missing DSN is a wiring defect and fails the test instead.
package dbtest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RequireURL resolves envVar (e.g. "HIVE_TEST_DB_URL") to a test database
// DSN.
//
// Local dev, DSN unset: skip.
// CI (the CI env var is set) with a real test run (not `go test -short`),
// DSN unset: fail the test. `-short` compile-and-unit-test passes run
// before any live Postgres is bootstrapped and are expected to skip;
// testing.Short() is what tells that leg apart from a live-suite step that
// simply forgot to wire the DSN.
//
// The DSN's parsed database name (not the raw DSN string) must contain
// "test" (case-insensitive): a blunt guard against a misconfigured env var
// pointing a suite that deletes rows at a staging or production database.
// Checking the whole DSN string instead of the parsed name would let a
// production DSN through by accident whenever some unrelated parameter
// happened to contain "test" (e.g. application_name=test-runner-3), and
// would just as wrongly refuse a legitimately-named test database whose
// host or user happens not to say "test" anywhere.
func RequireURL(t *testing.T, envVar string) string {
	t.Helper()
	dsn := os.Getenv(envVar)
	if dsn == "" {
		if os.Getenv("CI") != "" && !testing.Short() {
			t.Fatalf("%s not set in CI: this suite must not silently skip (issue #708)", envVar)
		}
		t.Skipf("%s not set; skipping (set CI=true, unset -short, to make this fail instead of skip)", envVar)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("dbtest: %s is not a valid Postgres DSN: %v", envVar, err)
	}
	if !strings.Contains(strings.ToLower(cfg.ConnConfig.Database), "test") {
		t.Fatalf("refusing to run: %s database name %q must contain 'test'", envVar, cfg.ConnConfig.Database)
	}
	return dsn
}

// Pool resolves envVar via RequireURL, opens a pgxpool against it, verifies
// connectivity with a ping, and registers pool.Close via t.Cleanup.
func Pool(t *testing.T, envVar string) *pgxpool.Pool {
	t.Helper()
	dsn := RequireURL(t, envVar)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("dbtest: connect %s: %v", envVar, err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("dbtest: ping %s: %v", envVar, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// PoolWithConfig is like Pool but lets the caller tune the pgxpool.Config
// (e.g. MaxConns) before connecting, for suites that must open the pool
// under a restricted role (marketplace's newRLSTestPool sets MaxConns=1 then
// issues `SET ROLE hive_app` on the single connection).
func PoolWithConfig(t *testing.T, envVar string, tune func(*pgxpool.Config)) *pgxpool.Pool {
	t.Helper()
	dsn := RequireURL(t, envVar)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("dbtest: parse %s: %v", envVar, err)
	}
	if tune != nil {
		tune(cfg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("dbtest: connect %s: %v", envVar, err)
	}
	t.Cleanup(pool.Close)
	return pool
}
