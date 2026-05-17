package settings_test

import (
	"context"
	"os"
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
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	teardown := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.tenants WHERE slug LIKE 't%'`)
		pool.Close()
	}
	return pool, teardown
}
