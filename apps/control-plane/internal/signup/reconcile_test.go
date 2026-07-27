package signup_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signup"
)

// TestReconcileProvisionsMembershipForDomainMatch is the base case the whole
// fix rests on: a user who exists in auth.users with no tenant_users row, whose
// email domain is registered to a tenant, ends up an ACTIVE member. Before this
// change nothing in the repository could put that row there unless a Supabase
// Database Webhook had been created by hand in the dashboard.
func TestReconcileProvisionsMembershipForDomainMatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newReconcileFixture(t, ctx, pool)

	outcome, err := fx.prov.Reconcile(ctx, signup.ReconcileInput{
		UserID: fx.userID,
		Email:  fx.email,
	})
	require.NoError(t, err)
	require.Equal(t, signup.OutcomeProvisioned, outcome)

	require.Equal(t, 1, countMemberships(t, ctx, pool, fx.userID))
	require.Equal(t, "MEMBER", membershipRole(t, ctx, pool, fx.userID, fx.tenantID))
	require.Equal(t, "ACTIVE", membershipStatus(t, ctx, pool, fx.userID, fx.tenantID))
}

// TestReconcileIsIdempotentOnRepeatCalls covers the console calling the
// endpoint more than once, which it will: every page load by a user whose token
// predates provisioning triggers it until the session refreshes.
func TestReconcileIsIdempotentOnRepeatCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newReconcileFixture(t, ctx, pool)
	in := signup.ReconcileInput{UserID: fx.userID, Email: fx.email}

	for i := 0; i < 3; i++ {
		outcome, err := fx.prov.Reconcile(ctx, in)
		require.NoError(t, err)
		require.Equal(t, signup.OutcomeProvisioned, outcome)
	}

	require.Equal(t, 1, countMemberships(t, ctx, pool, fx.userID))

	// Only the first call should have needed to resolve. Afterwards the
	// existing-membership short-circuit answers without touching the resolver
	// or Open WebUI, which is what keeps the repeat call cheap and stops a
	// second membership being attached to an already-placed user.
	require.Equal(t, int32(1), fx.resolveCalls.Load(),
		"an existing ACTIVE membership must short-circuit before resolution")
	require.Equal(t, int32(1), fx.ensureGroupCalls.Load(),
		"Open WebUI group wiring must not be re-run for an existing member")
}

// TestReconcileConcurrentCallsCreateExactlyOneMembership is the concurrency
// case. Two console tabs, or a webhook redelivery racing a console call, both
// see no membership, both resolve to the same tenant, and both insert. Exactly
// one row must exist afterwards. The guarantee comes from the tenant_users
// primary key (tenant_id, user_id) combined with ON CONFLICT DO NOTHING, and
// no tenant is created on either path so no race can produce two tenants.
func TestReconcileConcurrentCallsCreateExactlyOneMembership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newReconcileFixture(t, ctx, pool)

	const racers = 8
	var (
		wg       sync.WaitGroup
		start    = make(chan struct{})
		outcomes = make([]signup.Outcome, racers)
		errs     = make([]error, racers)
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			outcomes[idx], errs[idx] = fx.prov.Reconcile(ctx, signup.ReconcileInput{
				UserID: fx.userID,
				Email:  fx.email,
			})
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < racers; i++ {
		require.NoErrorf(t, errs[i], "racer %d must not error", i)
		require.Equalf(t, signup.OutcomeProvisioned, outcomes[i],
			"racer %d must report provisioned", i)
	}

	require.Equal(t, 1, countMemberships(t, ctx, pool, fx.userID),
		"concurrent reconciles must converge on exactly one membership")
	require.Equal(t, 1, countTenantsForSlugPrefix(t, ctx, pool, fx.slug),
		"reconcile must never create a tenant, let alone two")
}

// TestReconcileReportsNoTenantWhenNothingClaimsTheUser proves the terminal
// path. A nil error with OutcomeNoTenant is a successful determination, and the
// console renders its designed state from it rather than a 500.
func TestReconcileReportsNoTenantWhenNothingClaimsTheUser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newReconcileFixture(t, ctx, pool)
	stranger := mustInsertAuthUser(t, ctx, pool, "stranger-"+uuid.NewString()+"@nowhere.invalid")

	outcome, err := fx.prov.Reconcile(ctx, signup.ReconcileInput{
		UserID: stranger,
		Email:  "stranger@nowhere.invalid",
	})
	require.NoError(t, err, "no_tenant is a determination, not a failure")
	require.Equal(t, signup.OutcomeNoTenant, outcome)
	require.Equal(t, 0, countMemberships(t, ctx, pool, stranger))
}

// TestReconcileRejectsEmptyIdentity guards the input contract. Reconcile writes
// membership rows, so it must refuse to run on a zero user id rather than
// resolve something for nobody.
func TestReconcileRejectsEmptyIdentity(t *testing.T) {
	prov := signup.NewProvisioner(signup.WebhookDeps{})

	_, err := prov.Reconcile(context.Background(), signup.ReconcileInput{Email: "a@b.example"})
	require.Error(t, err)

	_, err = prov.Reconcile(context.Background(), signup.ReconcileInput{UserID: uuid.New()})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// POST /api/v1/viewer/tenant-provision
// ---------------------------------------------------------------------------

// TestViewerHandlerDerivesIdentityFromTokenNotBody is the load-bearing security
// test for the new endpoint. A tenant-less token is meant to reach exactly this
// one route, so the route must not let its caller name anybody. The request
// body here asks to provision a completely different user; the membership that
// results must belong to the context viewer, and the named victim must gain
// nothing.
func TestViewerHandlerDerivesIdentityFromTokenNotBody(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newReconcileFixture(t, ctx, pool)
	victim := mustInsertAuthUser(t, ctx, pool, "victim-"+uuid.NewString()+"@"+fx.domain)

	handler := signup.NewViewerHandler(fx.prov)

	// The attacker controls the body completely: another user id, another
	// email on the same claiming domain, and an invite token.
	body := `{"user_id":"` + victim.String() + `","email":"victim@` + fx.domain +
		`","invite_token":"stolen-token","tenant_id":"` + uuid.NewString() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/viewer/tenant-provision",
		strings.NewReader(body))
	req = req.WithContext(auth.WithViewer(ctx, auth.Viewer{
		UserID: fx.userID,
		Email:  fx.email,
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":"provisioned"}`, rec.Body.String())

	require.Equal(t, 1, countMemberships(t, ctx, pool, fx.userID),
		"the authenticated viewer is the one who gets provisioned")
	require.Equal(t, 0, countMemberships(t, ctx, pool, victim),
		"a body-named user must never be provisioned")

	// The stolen invite token must not have been consulted at all: this route
	// does not accept one.
	require.Equal(t, int32(0), fx.inviteCalls.Load(),
		"the authenticated route must never resolve an invite token from a body")
}

// TestViewerHandlerRequiresAuthentication keeps the route closed to an
// unauthenticated caller even if it is ever mounted without the middleware.
func TestViewerHandlerRequiresAuthentication(t *testing.T) {
	handler := signup.NewViewerHandler(signup.NewProvisioner(signup.WebhookDeps{}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/viewer/tenant-provision", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestViewerHandlerRejectsNonPost stops the route being driven by a plain
// navigation or an image tag, which is the cheapest form of cross-site request.
func TestViewerHandlerRejectsNonPost(t *testing.T) {
	handler := signup.NewViewerHandler(signup.NewProvisioner(signup.WebhookDeps{}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/viewer/tenant-provision", nil)
	req = req.WithContext(auth.WithViewer(context.Background(), auth.Viewer{
		UserID: uuid.New(), Email: "a@b.example",
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// TestViewerHandlerLeaksNoInternalsOnFailure keeps the provider-blind error
// convention on a surface that renders into an unprovisioned browser session.
// A misconfigured provisioner produces an internal error whose text must not
// travel to the client.
func TestViewerHandlerLeaksNoInternalsOnFailure(t *testing.T) {
	// Pool nil, Resolver nil: Reconcile reports misconfiguration.
	handler := signup.NewViewerHandler(signup.NewProvisioner(signup.WebhookDeps{}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/viewer/tenant-provision", nil)
	req = req.WithContext(auth.WithViewer(context.Background(), auth.Viewer{
		UserID: uuid.New(), Email: "a@b.example",
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.JSONEq(t, `{"error":"provisioning unavailable"}`, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "misconfigured")
}

// ---------------------------------------------------------------------------
// fixture
// ---------------------------------------------------------------------------

type reconcileFixture struct {
	prov             *signup.Provisioner
	userID           uuid.UUID
	tenantID         uuid.UUID
	email            string
	domain           string
	slug             string
	resolveCalls     atomic.Int32
	inviteCalls      atomic.Int32
	ensureGroupCalls atomic.Int32
}

// newReconcileFixture builds a tenant that claims a unique email domain, an
// auth.users row on that domain with no membership, and a Provisioner wired to
// the live pool. Counters record whether the resolver and the Open WebUI
// wiring were reached, which is how the short-circuit assertions work.
func newReconcileFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *reconcileFixture {
	t.Helper()

	fx := &reconcileFixture{}
	fx.slug = "recon-" + uuid.NewString()[:8]
	fx.domain = fx.slug + ".example"
	fx.email = "newcomer@" + fx.domain
	fx.tenantID = mustInsertTenant(t, ctx, pool, fx.slug, "HIVE_CLOUD")
	fx.userID = mustInsertAuthUser(t, ctx, pool, fx.email)

	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_email_domains(domain, tenant_id) VALUES ($1, $2)`,
		fx.domain, fx.tenantID)
	require.NoError(t, err)

	resolver := signup.NewResolver(signup.ResolverDeps{
		InviteLookup: func(ctx context.Context, token string) (uuid.UUID, error) {
			fx.inviteCalls.Add(1)
			return uuid.Nil, signup.ErrNoMatch
		},
		DomainLookup: func(ctx context.Context, domain string) (uuid.UUID, error) {
			fx.resolveCalls.Add(1)
			var id uuid.UUID
			err := pool.QueryRow(ctx,
				`SELECT tenant_id FROM public.tenant_email_domains WHERE domain=$1`,
				domain).Scan(&id)
			if err != nil {
				return uuid.Nil, signup.ErrNoMatch
			}
			return id, nil
		},
	})

	fx.prov = signup.NewProvisioner(signup.WebhookDeps{
		Pool:     pool,
		Resolver: resolver,
		Audit: audit.NewLogger(audit.LoggerDeps{
			Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
			WAL:  &noopWAL{},
		}),
		EnsureGroup: func(ctx context.Context, name string) (string, error) {
			fx.ensureGroupCalls.Add(1)
			return "grp-" + name, nil
		},
		AddUser:      func(ctx context.Context, groupID, email string) error { return nil },
		SharedSecret: "shh",
	})
	return fx
}

func countMemberships(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.tenant_users WHERE user_id=$1`, userID).Scan(&n))
	return n
}

func countTenantsForSlugPrefix(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.tenants WHERE slug LIKE $1`, slug+"%").Scan(&n))
	return n
}

func membershipRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID, tenantID uuid.UUID) string {
	t.Helper()
	var role string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE user_id=$1 AND tenant_id=$2`,
		userID, tenantID).Scan(&role))
	return role
}

func membershipStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID, tenantID uuid.UUID) string {
	t.Helper()
	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM public.tenant_users WHERE user_id=$1 AND tenant_id=$2`,
		userID, tenantID).Scan(&status))
	return status
}
