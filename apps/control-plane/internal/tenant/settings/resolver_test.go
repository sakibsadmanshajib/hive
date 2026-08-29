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

// TestResolver_ClientVisibleEnabled_ExcludesSensitiveCategories is the #293
// security-review guard: ClientVisibleEnabled backs the featuregate response
// that reaches Open WebUI via GET /v1/featuregate, so it must expose only
// client-visible categories (agents, sso). Enabling one key in each sensitive
// category proves neither admin nor billing ever appears in the map, closing
// the information-disclosure blind spot. The audit_sink category used to be
// the third example here; its keys were retired in issue #755 and its gates
// no longer exist to enable.
func TestResolver_ClientVisibleEnabled_ExcludesSensitiveCategories(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, teardown := newTestPool(t, ctx)
	defer teardown()

	r := settings.NewResolver(pool, 30*time.Second)
	tid := mustTenant(t, ctx, pool, "fg-client-visible", "HIVE_CLOUD")

	// Enable one key in each sensitive category plus one client-visible key.
	for _, k := range []settings.Key{
		settings.EnableAdminConsole, // admin
		settings.EnableStripe,       // billing
		settings.EnableRAG,          // agents (client-visible)
		settings.EnableSSOGoogle,    // sso (client-visible)
	} {
		_, err := pool.Exec(ctx,
			`INSERT INTO public.tenant_settings(tenant_id, key, enabled) VALUES ($1, $2::public.tenant_setting_key, true)`,
			tid, string(k))
		require.NoError(t, err)
	}

	gates, err := r.ClientVisibleEnabled(ctx, tid)
	require.NoError(t, err)

	// Client-visible categories are returned.
	require.True(t, gates[settings.EnableRAG], "agents gate must be exposed")
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
	} {
		_, present := gates[k]
		require.Falsef(t, present, "non-client-visible gate %q must not be exposed", k)
	}
}

// TestResolver_AllEnabled_ReturnsFullSet proves AllEnabled stays unfiltered so
// internal callers (the #323 admin console toggle UI) see every gate,
// including admin and billing, not just the client-visible subset.
func TestResolver_AllEnabled_ReturnsFullSet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, teardown := newTestPool(t, ctx)
	defer teardown()

	r := settings.NewResolver(pool, 30*time.Second)
	tid := mustTenant(t, ctx, pool, "fg-full-set", "HIVE_CLOUD")

	for _, k := range []settings.Key{
		settings.EnableAdminConsole, // admin
		settings.EnableStripe,       // billing
		settings.EnableRAG,          // agents
	} {
		_, err := pool.Exec(ctx,
			`INSERT INTO public.tenant_settings(tenant_id, key, enabled) VALUES ($1, $2::public.tenant_setting_key, true)`,
			tid, string(k))
		require.NoError(t, err)
	}

	gates, err := r.AllEnabled(ctx, tid)
	require.NoError(t, err)

	// Every category is present in the full set, including sensitive ones.
	for _, k := range []settings.Key{
		settings.EnableAdminConsole,
		settings.EnableStripe,
		settings.EnableRAG,
	} {
		require.Truef(t, gates[k], "AllEnabled must expose %q to internal callers", k)
	}
}

// TestResolver_GateDefaults_UnsetKeyReadsDeclaredDefault is the #1107 guard:
// a workspace with no explicit tenant_settings row for a key whose registry
// row declares default_enabled = true must resolve that gate as enabled. This
// is what makes Cowork launchable on every workspace by default instead of
// only on tenants hand-seeded by scripts/seed-demo-owner.py, and it must hold
// through BOTH read surfaces (AllEnabled backs the admin console toggle UI,
// ClientVisibleEnabled backs the featuregate endpoint edge-api gates /v1/agent
// routes on).
func TestResolver_GateDefaults_UnsetKeyReadsDeclaredDefault(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, teardown := newTestPool(t, ctx)
	defer teardown()

	r := settings.NewResolver(pool, 30*time.Second)
	tid := mustTenant(t, ctx, pool, "t-fg-default-on", "HIVE_CLOUD")

	clientVisible, err := r.ClientVisibleEnabled(ctx, tid)
	require.NoError(t, err)
	require.Truef(t, clientVisible[settings.EnableCowork],
		"ENABLE_COWORK has default_enabled = true and no explicit row, so ClientVisibleEnabled must report it enabled")
	require.Falsef(t, clientVisible[settings.EnableRAG],
		"keys without a declared default keep opt-in behavior: ENABLE_RAG unset must stay false")

	all, err := r.AllEnabled(ctx, tid)
	require.NoError(t, err)
	require.Truef(t, all[settings.EnableCowork],
		"AllEnabled must apply the declared default so the admin toggle UI shows the real state")
}

// TestResolver_GateDefaults_ExplicitRowOverridesDefault proves the default is
// only a fallback: an admin who turns the gate off per workspace writes an
// enabled=false row through Resolver.Set, and that row must win over the
// declared default on every read surface.
func TestResolver_GateDefaults_ExplicitRowOverridesDefault(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, teardown := newTestPool(t, ctx)
	defer teardown()

	r := settings.NewResolver(pool, 30*time.Second)
	tid := mustTenant(t, ctx, pool, "t-fg-default-off", "HIVE_CLOUD")

	require.NoError(t, r.Set(ctx, tid, settings.EnableCowork, false, uuid.Nil))

	clientVisible, err := r.ClientVisibleEnabled(ctx, tid)
	require.NoError(t, err)
	require.Falsef(t, clientVisible[settings.EnableCowork],
		"an explicit enabled=false tenant_settings row must override the declared default")

	all, err := r.AllEnabled(ctx, tid)
	require.NoError(t, err)
	require.Falsef(t, all[settings.EnableCowork],
		"an explicit enabled=false tenant_settings row must override the declared default in the full set too")
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
