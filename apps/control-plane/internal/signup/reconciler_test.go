package signup_test

// Coverage for the provisioning sweep that replaces the Supabase Database
// Webhook (D-023). The webhook was dashboard state: deleting the hosted
// project removed it with no diff and no failing test, and new identities
// silently stopped being provisioned. These tests are what makes the
// replacement's absence, and its misbehaviour, observable.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signup"
)

// mustInsertSweepUser seeds an auth.users row with explicit age and lifecycle
// columns. created_at is nullable with no default on real GoTrue (verified on
// the self-hosted deployment), so a raw insert that omits it leaves it NULL,
// which is exactly the case the sweep must refuse to guess about.
func mustInsertSweepUser(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	email string, createdAt, deletedAt, bannedUntil any,
) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO auth.users(id, email, raw_user_meta_data, created_at, deleted_at, banned_until)
		VALUES (gen_random_uuid(), $1, '{}'::jsonb, $2, $3, $4)
		RETURNING id
	`, email, createdAt, deletedAt, bannedUntil).Scan(&id)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM public.tenant_users WHERE user_id = $1`, id)
		_, _ = pool.Exec(cleanup, `DELETE FROM public.tenants WHERE personal_owner_user_id = $1`, id)
		_, _ = pool.Exec(cleanup, `DELETE FROM auth.users WHERE id = $1`, id)
	})
	return id
}

// TestReconcilerSweepProvisionsFreshIdentity is the case the whole change
// exists for: an identity that exists in auth.users with no membership, which
// nothing in the repository could provision once the dashboard webhook was
// gone. No request is made and no console page is visited; the sweep alone
// puts the row there.
func TestReconcilerSweepProvisionsFreshIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newReconcileFixture(t, ctx, pool)
	now := time.Now()
	userID := mustInsertSweepUser(t, ctx, pool, "swept-"+uuid.NewString()+"@"+fx.domain, now, nil, nil)

	rec := signup.NewReconciler(pool, fx.prov, signup.ReconcilerConfig{})

	report, err := rec.Sweep(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, report.Provisioned, 1)

	require.Equal(t, 1, countMemberships(t, ctx, pool, userID),
		"the sweep must attach the identity to the tenant that claims its domain")
	require.Equal(t, "ACTIVE", membershipStatus(t, ctx, pool, userID, fx.tenantID))

	ready, reason := rec.Ready()
	require.True(t, ready, "a sweep that worked must leave provisioning ready")
	require.Empty(t, reason)
}

// TestReconcilerSweepIgnoresIdentitiesItMustNotGuessAbout pins the blast
// radius. cmd/backfill-tenants is deliberately NOT wired into startup because
// an automatic tenancy write is one nobody reviews, so the sweep only ever
// looks at identities created inside its lookback window and never at ones
// whose age it cannot establish or whose lifecycle says they are gone.
func TestReconcilerSweepIgnoresIdentitiesItMustNotGuessAbout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newReconcileFixture(t, ctx, pool)
	old := time.Now().Add(-72 * time.Hour)
	fresh := time.Now()
	banned := time.Now().Add(time.Hour)

	tooOld := mustInsertSweepUser(t, ctx, pool, "old-"+uuid.NewString()+"@"+fx.domain, old, nil, nil)
	softDeleted := mustInsertSweepUser(t, ctx, pool, "del-"+uuid.NewString()+"@"+fx.domain, fresh, fresh, nil)
	bannedUser := mustInsertSweepUser(t, ctx, pool, "ban-"+uuid.NewString()+"@"+fx.domain, fresh, nil, banned)

	rec := signup.NewReconciler(pool, fx.prov, signup.ReconcilerConfig{})
	_, err := rec.Sweep(ctx)
	require.NoError(t, err)

	require.Equal(t, 0, countMemberships(t, ctx, pool, tooOld),
		"an identity older than the lookback window is the operator backfill's business")
	require.Equal(t, 0, countMemberships(t, ctx, pool, softDeleted),
		"a soft-deleted identity must never be provisioned")
	require.Equal(t, 0, countMemberships(t, ctx, pool, bannedUser),
		"a banned identity must never be provisioned")
	// The fixture's own user is inserted without created_at, so its age is
	// unknown. An unknown age is not a licence to write tenancy rows.
	require.Equal(t, 0, countMemberships(t, ctx, pool, fx.userID),
		"an identity with a NULL created_at must not be swept")
}

// TestReconcilerSweepIsIdempotent covers the ticker running every few minutes
// forever, and two control-plane processes sweeping the same database.
func TestReconcilerSweepIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newReconcileFixture(t, ctx, pool)
	userID := mustInsertSweepUser(t, ctx, pool, "twice-"+uuid.NewString()+"@"+fx.domain, time.Now(), nil, nil)

	rec := signup.NewReconciler(pool, fx.prov, signup.ReconcilerConfig{})
	for i := 0; i < 3; i++ {
		_, err := rec.Sweep(ctx)
		require.NoError(t, err)
	}

	require.Equal(t, 1, countMemberships(t, ctx, pool, userID))
	require.Equal(t, int32(1), fx.resolveCalls.Load(),
		"an identity that already holds a membership must short-circuit before resolution")
	require.Equal(t, 1, countTenantsForSlugPrefix(t, ctx, pool, fx.slug),
		"the sweep must not mint a second tenant")
}

// TestReconcilerBacksOffAnIdentityNoTenantClaims stops the sweep writing an
// immutable, hash-chained audit row every interval, forever, for an identity
// whose no_tenant outcome Reconcile itself documents as terminal until an
// administrator acts. This is the administered (Hive Enterprise) posture.
func TestReconcilerBacksOffAnIdentityNoTenantClaims(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	var resolveCalls int
	prov := signup.NewProvisioner(signup.WebhookDeps{
		Pool: pool,
		Resolver: signup.NewResolver(signup.ResolverDeps{
			DomainLookup: func(context.Context, string) (uuid.UUID, error) {
				resolveCalls++
				return uuid.Nil, signup.ErrNoMatch
			},
		}),
		Audit: audit.NewLogger(audit.LoggerDeps{
			Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
			WAL:  &noopWAL{},
		}),
		SelfServeTenants: false,
	})
	userID := mustInsertSweepUser(t, ctx, pool,
		"unclaimed-"+uuid.NewString()+"@unclaimed-"+uuid.NewString()[:8]+".example", time.Now(), nil, nil)

	rec := signup.NewReconciler(pool, prov, signup.ReconcilerConfig{})
	for i := 0; i < 3; i++ {
		_, err := rec.Sweep(ctx)
		require.NoError(t, err)
	}

	require.Equal(t, 0, countMemberships(t, ctx, pool, userID),
		"administered posture must not invent a tenant")
	require.Equal(t, 1, resolveCalls,
		"a terminal no_tenant determination must be attempted once per cooldown, not every sweep")
}

// TestReconcilerReadyIsFalseWhenUnwired is the guard against this change being
// silently deleted. A nil reconciler is what /health sees when nothing wired
// provisioning at all, which is the exact shape of the failure D-023 exists to
// close, one layer in.
func TestReconcilerReadyIsFalseWhenUnwired(t *testing.T) {
	var missing *signup.Reconciler
	ready, reason := missing.Ready()
	require.False(t, ready)
	require.NotEmpty(t, reason)

	ready, reason = signup.NewReconciler(nil, nil, signup.ReconcilerConfig{}).Ready()
	require.False(t, ready, "a reconciler with no pool and no provisioner is not wired")
	require.NotEmpty(t, reason)
}

// TestReconcilerReadyGoesFalseAfterRepeatedSweepFailures covers wired but
// broken, which a nil check alone cannot catch: the sweep is mounted and the
// database refuses it.
func TestReconcilerReadyGoesFalseAfterRepeatedSweepFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Port 1 is not listenable by an unprivileged process, so every query
	// fails at dial time. pgxpool connects lazily, so construction succeeds
	// and the failure lands where the sweep runs.
	dead, err := pgxpool.New(ctx, "postgres://nobody@127.0.0.1:1/nothing?sslmode=disable&connect_timeout=1")
	require.NoError(t, err)
	t.Cleanup(dead.Close)

	rec := signup.NewReconciler(dead, signup.NewProvisioner(signup.WebhookDeps{Pool: dead}),
		signup.ReconcilerConfig{})

	ready, _ := rec.Ready()
	require.True(t, ready, "one bad sweep must not flap a healthcheck red")

	for i := 0; i < 3; i++ {
		_, err := rec.Sweep(ctx)
		require.Error(t, err)
	}

	ready, reason := rec.Ready()
	require.False(t, ready, "a sweep failing every time is a provisioning outage")
	require.NotEmpty(t, reason)
	require.NotContains(t, reason, "127.0.0.1",
		"the reason reaches a public health endpoint and must not carry connection detail")
}

// TestReconcilerSweepProvisionsSelfServeIdentityWithProductionResolver is the
// production-shaped case: the resolver the server actually runs
// (signup.NewPgxResolver), self-serve posture, and an identity whose email
// domain no tenant claims. This is the shape an administrator creating a user
// through the Supabase admin API produces, which is the path the deleted
// dashboard webhook used to cover and nothing else did.
//
// It asserts on the personal tenant rather than only on the membership, because
// a membership row pointing at somebody else's tenant would satisfy a count.
func TestReconcilerSweepProvisionsSelfServeIdentityWithProductionResolver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	prov := signup.NewProvisioner(signup.WebhookDeps{
		Pool:     pool,
		Resolver: signup.NewPgxResolver(pool),
		Audit: audit.NewLogger(audit.LoggerDeps{
			Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
			WAL:  &noopWAL{},
		}),
		SelfServeTenants: true,
	})

	domain := "selfserve-" + uuid.NewString()[:8] + ".example"
	userID := mustInsertSweepUser(t, ctx, pool,
		"admin-created-"+uuid.NewString()+"@"+domain, time.Now(), nil, nil)

	report, err := signup.NewReconciler(pool, prov, signup.ReconcilerConfig{}).Sweep(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, report.Provisioned, 1)

	require.Equal(t, 1, countMemberships(t, ctx, pool, userID))
	require.Equal(t, 1, countPersonalTenants(t, ctx, pool, userID),
		"a self-serve identity no tenant claims must end up owning exactly one personal tenant")
	require.Equal(t, "ACTIVE", personalMembershipStatus(t, ctx, pool, userID))
}

func countPersonalTenants(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.tenants WHERE personal_owner_user_id = $1`, userID).Scan(&n))
	return n
}

func personalMembershipStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) string {
	t.Helper()
	var status string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT tu.status
		  FROM public.tenant_users tu
		  JOIN public.tenants t ON t.id = tu.tenant_id
		 WHERE tu.user_id = $1
		   AND t.personal_owner_user_id = $1
	`, userID).Scan(&status))
	return status
}
