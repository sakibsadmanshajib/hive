package signup_test

// Coverage for the provisioning sweep that replaces the Supabase Database
// Webhook (D-023). The webhook was dashboard state: deleting the hosted
// project removed it with no diff and no failing test, and new identities
// silently stopped being provisioned. These tests are what makes the
// replacement's absence, and its misbehaviour, observable.

import (
	"context"
	"errors"
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

// TestReconcilerCountsRepeatedSweepFailures covers wired but broken, which the
// nil check alone cannot catch: the sweep is mounted and the database refuses
// it. This state is exported for the telemetry gauge rather than folded into
// readiness, because taking the container out of service over a provisioning
// fault would convert a signup outage into a billing one (review finding,
// CodeRabbit on PR 993).
func TestReconcilerCountsRepeatedSweepFailures(t *testing.T) {
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

	require.Equal(t, 0, rec.ConsecutiveFailures())

	for i := 0; i < 3; i++ {
		_, err := rec.Sweep(ctx)
		require.Error(t, err)
	}

	require.Equal(t, 3, rec.ConsecutiveFailures(),
		"a sweep failing every time has to be countable by something")

	// Wiring is unaffected, which is the whole point of the split: the process
	// keeps serving every other route while this is reported elsewhere.
	ready, reason := rec.Ready()
	require.True(t, ready)
	require.Empty(t, reason)
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

// TestReconcilerCountsAnUnfinishedSweepAsFailure closes the hole that a bounded
// sweep would otherwise open. A pass that runs out of time has not done its
// work, so if it returned quietly the health endpoint would keep reporting ready
// while nobody was being provisioned, which is the same invisible failure the
// sweep exists to remove.
func TestReconcilerCountsAnUnfinishedSweepAsFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })
	// The cancellation is driven from inside the provisioning attempt, through
	// the disposable-domain hook Reconcile already calls per identity, so the
	// second loop iteration is the one that observes the dead context. Two
	// candidates are seeded precisely so there is a second iteration.
	var abandon context.CancelFunc
	stopOnFirstIdentity := func(string) (bool, error) {
		abandon()
		return false, nil
	}
	prov := signup.NewProvisioner(signup.WebhookDeps{
		Pool:            pool,
		Resolver:        signup.NewPgxResolver(pool),
		Audit:           newTestAuditLogger(pool),
		DisposableCheck: stopOnFirstIdentity,
	})
	domain := uuid.NewString()[:8] + "-abandoned.example"
	mustInsertSweepUser(t, ctx, pool, "one-"+uuid.NewString()+"@"+domain, time.Now(), nil, nil)
	mustInsertSweepUser(t, ctx, pool, "two-"+uuid.NewString()+"@"+domain, time.Now(), nil, nil)
	rec := signup.NewReconciler(pool, prov, signup.ReconcilerConfig{})
	for i := 0; i < 3; i++ {
		sweepCtx, stop := context.WithCancel(ctx)
		abandon = stop
		_, err := rec.Sweep(sweepCtx)
		require.Error(t, err)
		stop()
	}

	require.Equal(t, 3, rec.ConsecutiveFailures(),
		"an unfinished pass must count against the sweep, not pass as a quiet one")
}

// newTestAuditLogger builds the audit logger these suites share.
func newTestAuditLogger(pool *pgxpool.Pool) *audit.Logger {
	cfg := audit.WriterConfig{DeploySHA: "s", Env: "test"}
	deps := audit.LoggerDeps{Sync: audit.NewSyncWriter(pool, cfg), WAL: &noopWAL{}}
	return audit.NewLogger(deps)
}

// TestReconcilerSweepTakesTheOldestCandidatesFirst pins the ordering, which is
// what stops a set of identities the sweep cannot clear from refilling every
// batch while the identities behind them age out of the lookback window
// unattempted (review finding, Greptile on PR 993). The batch limit is applied by
// the database, so the ordering decides who a pass can act on at all.
func TestReconcilerSweepTakesTheOldestCandidatesFirst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newReconcileFixture(t, ctx, pool)
	older := mustInsertSweepUser(t, ctx, pool, "older-"+uuid.NewString()+"@"+fx.domain,
		time.Now().Add(-6*time.Hour), nil, nil)
	newer := mustInsertSweepUser(t, ctx, pool, "newer-"+uuid.NewString()+"@"+fx.domain,
		time.Now(), nil, nil)

	// One candidate per pass, so the ordering is the only thing that decides
	// which of the two is served.
	cfg := signup.ReconcilerConfig{BatchLimit: 1}
	report, err := signup.NewReconciler(pool, fx.prov, cfg).Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, report.Candidates)

	require.Equal(t, 1, countMemberships(t, ctx, pool, older),
		"the identity closest to leaving the window must be the one a limited pass serves")
	require.Equal(t, 0, countMemberships(t, ctx, pool, newer))
}

// TestReconcilerFaultRecordSurvivesIdentityAgingOut is the regression guard for
// the review finding that the failure gauge clears itself exactly when work is
// permanently lost.
//
// The mechanism: recordSweep(report.Failed == 0) resets the consecutive-failure
// count on any pass with no faults, and a pass has no faults once the identity
// that kept faulting is older than the lookback window, because candidateQuery
// stops returning it. So the gauge, and the alert on it, go quiet at the moment
// provisioning has permanently failed for that person. This test asserts that
// reset still happens, since it is correct for what that gauge measures, AND
// that the two metrics added alongside it do not follow it down.
func TestReconcilerFaultRecordSurvivesIdentityAgingOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	// A resolver that faults rather than reaching any determination. This is the
	// transient shape Reconcile deliberately does not treat as terminal: it
	// returns an error, the sweep counts it in report.Failed, and the identity
	// stays a candidate until it ages out. SelfServeTenants is left false so no
	// personal tenant rescues it.
	failing := signup.NewProvisioner(signup.WebhookDeps{
		Pool: pool,
		Resolver: signup.NewResolver(signup.ResolverDeps{
			DomainLookup: func(context.Context, string) (uuid.UUID, error) {
				return uuid.Nil, errors.New("resolver unavailable")
			},
		}),
		Audit: audit.NewLogger(audit.LoggerDeps{
			Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
			WAL:  &noopWAL{},
		}),
	})

	domain := "faulting-" + uuid.NewString()[:8] + ".example"
	userID := mustInsertSweepUser(t, ctx, pool,
		"faulting-"+uuid.NewString()+"@"+domain, time.Now(), nil, nil)

	// BatchLimit 1 with oldest-first ordering keeps this pass on a single
	// identity, so unrelated rows in the shared test database cannot inflate the
	// fault count this test asserts on.
	rec := signup.NewReconciler(pool, failing, signup.ReconcilerConfig{BatchLimit: 1})

	report, err := rec.Sweep(ctx)
	require.NoError(t, err, "one identity faulting is not a failed sweep")
	require.Equal(t, 1, report.Failed, "the faulting identity must be counted")
	require.Equal(t, 1, rec.ConsecutiveFailures())
	require.Equal(t, 1, rec.Faults())
	strandedBefore := rec.StrandedIdentities()
	require.Equal(t, 0, countMemberships(t, ctx, pool, userID),
		"a faulting resolver must not have produced a membership")

	// Age the identity past the lookback window, which is what really happens
	// after 24 hours of a resolver that stays broken. Nothing else changes.
	_, err = pool.Exec(ctx,
		`UPDATE auth.users SET created_at = now() - interval '25 hours' WHERE id = $1`, userID)
	require.NoError(t, err)

	report, err = rec.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed)

	// The defect, asserted rather than fixed: the identity is permanently
	// unprovisioned and the consecutive-failure gauge reads zero.
	require.Equal(t, 0, rec.ConsecutiveFailures(),
		"documenting the reset this test exists because of, not endorsing it as the only signal")
	require.Equal(t, 0, countMemberships(t, ctx, pool, userID),
		"the identity is still without a tenant, which is what makes the reset misleading")

	// The fix. A monotonic counter cannot be walked back by the candidate
	// disappearing, and the stranded gauge rises at that exact moment.
	require.Equal(t, 1, rec.Faults(),
		"the record of a provisioning fault must survive the identity leaving the sweep's window")
	require.Equal(t, strandedBefore+1, rec.StrandedIdentities(),
		"an identity that aged out unprovisioned must show up as stranded")
}

// TestReconcilerStrandedCountIgnoresProvisionedIdentities keeps the stranded
// gauge from becoming a count of everything old. A gauge that rose on healthy
// identities would be permanently non-zero, and an alert that never clears is an
// alert nobody reads.
func TestReconcilerStrandedCountIgnoresProvisionedIdentities(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newReconcileFixture(t, ctx, pool)
	rec := signup.NewReconciler(pool, fx.prov, signup.ReconcilerConfig{})

	_, err := rec.Sweep(ctx)
	require.NoError(t, err)
	before := rec.StrandedIdentities()

	// Provisioned, then aged past the window. It left the sweep's reach holding a
	// tenant, so nothing was lost and nothing should be reported.
	settled := mustInsertSweepUser(t, ctx, pool,
		"settled-"+uuid.NewString()+"@"+fx.domain, time.Now(), nil, nil)
	_, err = rec.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, countMemberships(t, ctx, pool, settled))

	_, err = pool.Exec(ctx,
		`UPDATE auth.users SET created_at = now() - interval '25 hours' WHERE id = $1`, settled)
	require.NoError(t, err)

	_, err = rec.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, before, rec.StrandedIdentities(),
		"an identity that aged out holding a membership is not stranded")
}
