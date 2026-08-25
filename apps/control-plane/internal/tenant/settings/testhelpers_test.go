package settings_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// newTestPool's DSN resolution (skip locally, fail in CI if unset, refuse a
// DSN without a "test" marker) is dbtest.RequireURL: teardown below performs
// a broad DELETE against public.tenants, so that marker check is a hard
// safety requirement, not a convenience.
func newTestPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := dbtest.RequireURL(t, "HIVE_TEST_DB_URL")
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	teardown := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.tenants WHERE slug LIKE 't%'`)
		pool.Close()
	}
	return pool, teardown
}
