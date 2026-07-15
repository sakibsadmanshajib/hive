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

// TestResolver_AllEnabled_ExcludesNonClientVisibleCategories is the #293
// security-review guard: AllEnabled backs the featuregate response that reaches
// Open WebUI via GET /v1/featuregate, so it must expose only client-visible
// categories (carl, sso). Enabling one key in each sensitive category proves
// none of admin, billing, or audit_sink ever appears in the map, closing the
// information-disclosure blind spot.
func TestResolver_AllEnabled_ExcludesNonClientVisibleCategories(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, teardown := newTestPool(t, ctx)
	defer teardown()

	r := settings.NewResolver(pool, 30*time.Second)
	tid := mustTenant(t, ctx, pool, "fg-client-visible", "HIVE_CLOUD")

	// Enable one key in each sensitive category plus one client-visible key.
	for _, k := range []settings.Key{
		settings.EnableAdminConsole,    // admin
		settings.EnableStripe,          // billing
		settings.EnableAuditSinkSentry, // audit_sink
		settings.EnableRAG,             // carl (client-visible)
		settings.EnableSSOGoogle,       // sso (client-visible)
	} {
		_, err := pool.Exec(ctx,
			`INSERT INTO public.tenant_settings(tenant_id, key, enabled) VALUES ($1, $2::public.tenant_setting_key, true)`,
			tid, string(k))
		require.NoError(t, err)
	}

	gates, err := r.AllEnabled(ctx, tid)
	require.NoError(t, err)

	// Client-visible categories are returned.
	require.True(t, gates[settings.EnableRAG], "carl gate must be exposed")
	require.True(t, gates[settings.EnableSSOGoogle], "sso gate must be exposed")

	// Sensitive categories must be absent entirely, not merely false.
	for _, k := range []settings.Key{
		settings.EnableAdminConsole,
		settings.EnableMultiTenant,
		settings.EnableProviderCustom,
		settings.EnableStripe,
		settings.EnableBkash,
		settings.EnableSSLCommerz,
		settings.EnableCreditPool,
		settings.EnableAuditSinkSentry,
		settings.EnableAuditSinkELK,
	} {
		_, present := gates[k]
		require.Falsef(t, present, "non-client-visible gate %q must not be exposed", k)
	}
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
