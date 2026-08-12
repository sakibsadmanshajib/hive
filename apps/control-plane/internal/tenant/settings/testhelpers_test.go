package settings_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/testdb"
	"github.com/stretchr/testify/require"
)

func newTestPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := testdb.RequireTestDSN(t)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	teardown := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.tenants WHERE slug LIKE 't%'`)
		pool.Close()
	}
	return pool, teardown
}
