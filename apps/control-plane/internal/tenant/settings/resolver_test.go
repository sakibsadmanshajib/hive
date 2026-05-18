package settings_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
)

func TestResolver_IsEnabled_UnsetReturnsFalse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, teardown := newTestPool(t, ctx)
	defer teardown()

	r := settings.NewResolver(pool, 30*time.Second)
	tid := mustTenant(t, ctx, pool, "t1", "HIVE_CLOUD")

	require.False(t, r.IsEnabled(ctx, tid, settings.EnableCreditPool))
}

func TestResolver_IsEnabled_ReadsValue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, teardown := newTestPool(t, ctx)
	defer teardown()

	r := settings.NewResolver(pool, 30*time.Second)
	tid := mustTenant(t, ctx, pool, "t2", "HIVE_CLOUD")

	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_settings(tenant_id, key, enabled) VALUES ($1, 'ENABLE_CREDIT_POOL', true)`,
		tid)
	require.NoError(t, err)

	require.True(t, r.IsEnabled(ctx, tid, settings.EnableCreditPool))
}

func TestResolver_CacheInvalidatesOnNotify(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, teardown := newTestPool(t, ctx)
	defer teardown()

	r := settings.NewResolver(pool, 30*time.Second)
	go r.StartListener(ctx)
	time.Sleep(200 * time.Millisecond)

	tid := mustTenant(t, ctx, pool, "t3", "HIVE_CLOUD")
	require.False(t, r.IsEnabled(ctx, tid, settings.EnableRAGPersonal))

	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_settings(tenant_id, key, enabled) VALUES ($1, 'ENABLE_RAG_PERSONAL', true)`,
		tid)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return r.IsEnabled(ctx, tid, settings.EnableRAGPersonal)
	}, 3*time.Second, 50*time.Millisecond, "cache should pick up the NOTIFY within 3 s")
}

func mustTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, deployment string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO public.tenants(slug, name, deployment) VALUES ($1, $1, $2) RETURNING id`,
		slug, deployment).Scan(&id)
	require.NoError(t, err)
	return id
}
