package settings_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func newTestPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	// Hard safety check: teardown performs a broad DELETE against
	// public.tenants. Require the DSN's database name to contain a
	// "test" marker so a misconfigured env var pointing at staging or
	// production cannot wipe real tenant rows.
	if !strings.Contains(strings.ToLower(dsn), "test") {
		t.Fatalf("refusing to run: HIVE_TEST_DB_URL must point at a test database (DSN missing 'test' marker)")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	teardown := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.tenants WHERE slug LIKE 't%'`)
		pool.Close()
	}
	return pool, teardown
}
