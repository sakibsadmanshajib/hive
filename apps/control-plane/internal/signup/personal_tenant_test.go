package signup_test

// Personal-tenant provisioning (issue #625).
//
// Every test here runs against the full migration chain via HIVE_TEST_DB_URL.
// The DB-level guarantee under test is the partial unique index
// tenants(personal_owner_user_id), so a fixture schema missing that index (or
// missing the auth.users foreign keys the seeds depend on) would pass while
// production fails, which is exactly the failure mode these tests exist to
// prevent. PR #624 shipped a test that went green locally while violating
// tenant_users_user_id_fkey, because the local container lacked the key.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/apikeys"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signup"
)

// selfServeFixture is a Provisioner whose resolver never matches: no invite
// token, no registered email domain. That is precisely the self-serve signup
// #625 describes, the one that used to end at OutcomeNoTenant forever.
type selfServeFixture struct {
	prov   *signup.Provisioner
	userID uuid.UUID
	email  string
}

func newSelfServeFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, selfServe bool) *selfServeFixture {
	t.Helper()

	email := "selfserve-" + uuid.NewString()[:8] + "@unclaimed.example"
	userID := mustInsertAuthUser(t, ctx, pool, email)
	ptCleanupUser(t, pool, userID)

	noMatch := func(ctx context.Context, key string) (uuid.UUID, error) { return uuid.Nil, signup.ErrNoMatch }

	return &selfServeFixture{
		userID: userID,
		email:  email,
		prov: signup.NewProvisioner(signup.WebhookDeps{
			Pool:     pool,
			Resolver: signup.NewResolver(signup.ResolverDeps{InviteLookup: noMatch, DomainLookup: noMatch}),
			Audit: audit.NewLogger(audit.LoggerDeps{
				Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
				WAL:  &noopWAL{},
			}),
			EnsureGroup:      func(ctx context.Context, name string) (string, error) { return "grp-" + name, nil },
			AddUser:          func(ctx context.Context, groupID, email string) error { return nil },
			SelfServeTenants: selfServe,
			SharedSecret:     "shh",
		}),
	}
}

// ptCleanupUser drops the personal tenant before the auth user, so the
// tenants.personal_owner_user_id reference never blocks teardown.
func ptCleanupUser(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM public.tenants WHERE personal_owner_user_id = $1`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth.users WHERE id = $1`, userID)
	})
}

// ptAccount seeds an account owned by ownerID with ownerID as an ACTIVE
// member, which is the shape accounts.Service.provisionDefaultWorkspace
// produces and the shape EnsureTenantBillingAccount can resolve.
func ptAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ownerID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO public.accounts(id, slug, display_name, account_type, owner_user_id)
		 VALUES ($1, $2, $2, 'personal', $3)`,
		id, "pt-acct-"+uuid.NewString()[:8], ownerID)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.accounts WHERE id = $1`, id) })

	_, err = pool.Exec(ctx,
		`INSERT INTO public.account_memberships(account_id, user_id, role, status)
		 VALUES ($1, $2, 'owner', 'active')`, id, ownerID)
	require.NoError(t, err)
	return id
}

// ptAPIKey seeds an active, unrevoked key on accountID: the state that makes
// an unmapped account a live 403 rather than a dormant row.
func ptAPIKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID, createdBy uuid.UUID) {
	t.Helper()
	sum := sha256.Sum256([]byte("pt-" + uuid.NewString()))
	id := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO public.api_keys(id, account_id, nickname, token_hash, redacted_suffix, status, created_by_user_id)
		 VALUES ($1, $2, 'pt-key', $3, 'abcd', 'active', $4)`,
		id, accountID, hex.EncodeToString(sum[:]), createdBy)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.api_keys WHERE id = $1`, id) })
}

func ptPersonalTenants(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.tenants WHERE personal_owner_user_id = $1`, userID).Scan(&n))
	return n
}

func ptTenantOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (uuid.UUID, bool) {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM public.tenants WHERE personal_owner_user_id = $1`, userID).Scan(&id); err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func ptMappedAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (uuid.UUID, bool) {
	t.Helper()
	var acct uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT account_id FROM public.tenant_billing_accounts WHERE tenant_id = $1`, tenantID).Scan(&acct); err != nil {
		return uuid.Nil, false
	}
	return acct, true
}

// (a) A fresh self-serve signup ends with a tenant AND a billing mapping.
// Before #625 this user reached OutcomeNoTenant and stayed there forever, so
// every API key on their account answered 403 account_not_provisioned once
// PR #620 made tenant resolution fail closed.
func TestReconcileProvisionsPersonalTenantForSelfServeSignup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newSelfServeFixture(t, ctx, pool, true)
	accountID := ptAccount(t, ctx, pool, fx.userID)

	outcome, err := fx.prov.Reconcile(ctx, signup.ReconcileInput{UserID: fx.userID, Email: fx.email})
	require.NoError(t, err)
	require.Equal(t, signup.OutcomeProvisioned, outcome)

	tenantID, ok := ptTenantOf(t, ctx, pool, fx.userID)
	require.True(t, ok, "self-serve signup must end with a personal tenant")
	require.Equal(t, 1, countMemberships(t, ctx, pool, fx.userID))
	require.Equal(t, "ACTIVE", membershipStatus(t, ctx, pool, fx.userID, tenantID))

	mapped, ok := ptMappedAccount(t, ctx, pool, tenantID)
	require.True(t, ok, "the new tenant must be mapped to the owner's billing account")
	require.Equal(t, accountID, mapped)
}

// (b) Running provisioning twice is a no-op, asserted on row counts rather
// than on the returned outcome: a second tenant would be invisible to an
// outcome-only assertion.
func TestReconcilePersonalTenantIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newSelfServeFixture(t, ctx, pool, true)
	ptAccount(t, ctx, pool, fx.userID)
	in := signup.ReconcileInput{UserID: fx.userID, Email: fx.email}

	for i := 0; i < 3; i++ {
		outcome, err := fx.prov.Reconcile(ctx, in)
		require.NoError(t, err)
		require.Equal(t, signup.OutcomeProvisioned, outcome)
	}

	require.Equal(t, 1, ptPersonalTenants(t, ctx, pool, fx.userID))
	require.Equal(t, 1, countMemberships(t, ctx, pool, fx.userID))
}

// (c) Two concurrent provisioning attempts produce exactly ONE tenant. This is
// the test that proves the partial unique index on
// tenants(personal_owner_user_id) rather than a check-then-act in Go: with
// application-level checking only, both callers read "no tenant" and both
// insert, and the loser of that race is a permanent duplicate tenant with its
// own billing identity and its own entitlement surface.
func TestReconcilePersonalTenantConcurrentCallsCreateExactlyOneTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newSelfServeFixture(t, ctx, pool, true)
	ptAccount(t, ctx, pool, fx.userID)
	in := signup.ReconcileInput{UserID: fx.userID, Email: fx.email}

	const racers = 8
	var wg sync.WaitGroup
	errs := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = fx.prov.Reconcile(ctx, in)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "racer %d", i)
	}
	require.Equal(t, 1, ptPersonalTenants(t, ctx, pool, fx.userID))
	require.Equal(t, 1, countMemberships(t, ctx, pool, fx.userID))
}

// (d) An account with genuinely ambiguous ownership is left UNMAPPED rather
// than guessed. The user is an ACTIVE member of two different accounts, so
// there is no single answer to "which account bills this tenant". PR #624's
// guard must survive: the tenant is still created (the user needs one to
// authenticate at all) but no mapping is invented.
func TestReconcilePersonalTenantLeavesAmbiguousOwnershipUnmapped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newSelfServeFixture(t, ctx, pool, true)
	ptAccount(t, ctx, pool, fx.userID)
	ptAccount(t, ctx, pool, fx.userID)

	outcome, err := fx.prov.Reconcile(ctx, signup.ReconcileInput{UserID: fx.userID, Email: fx.email})
	require.NoError(t, err)
	require.Equal(t, signup.OutcomeProvisioned, outcome)

	tenantID, ok := ptTenantOf(t, ctx, pool, fx.userID)
	require.True(t, ok)
	_, mapped := ptMappedAccount(t, ctx, pool, tenantID)
	require.False(t, mapped, "two candidate accounts must never be resolved by guessing one")
}

// Enterprise posture: nothing fires. Membership on a customer-hosted
// deployment is administered, so a self-serve signup with no invite and no
// domain match must still end at OutcomeNoTenant with no tenant written.
func TestReconcileDoesNotCreatePersonalTenantWhenSelfServeDisabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newSelfServeFixture(t, ctx, pool, false)

	outcome, err := fx.prov.Reconcile(ctx, signup.ReconcileInput{UserID: fx.userID, Email: fx.email})
	require.NoError(t, err)
	require.Equal(t, signup.OutcomeNoTenant, outcome)
	require.Equal(t, 0, ptPersonalTenants(t, ctx, pool, fx.userID))
	require.Equal(t, 0, countMemberships(t, ctx, pool, fx.userID))
}

// Invite and domain attachment behaviour is unchanged: a user whose email
// domain IS registered still joins that existing tenant, and no personal
// tenant is minted alongside it.
func TestReconcileStillPrefersDomainMatchOverPersonalTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newReconcileFixture(t, ctx, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM public.tenants WHERE personal_owner_user_id = $1`, fx.userID)
	})

	outcome, err := fx.prov.Reconcile(ctx, signup.ReconcileInput{UserID: fx.userID, Email: fx.email})
	require.NoError(t, err)
	require.Equal(t, signup.OutcomeProvisioned, outcome)

	require.Equal(t, 1, countMemberships(t, ctx, pool, fx.userID))
	require.Equal(t, "ACTIVE", membershipStatus(t, ctx, pool, fx.userID, fx.tenantID))
	require.Equal(t, 0, ptPersonalTenants(t, ctx, pool, fx.userID),
		"a domain-matched user belongs to the org tenant, never to a personal one as well")
}

// ORG-MEMBERSHIP SEMANTICS, enforced rather than merely documented.
//
// The question #625 requires answering: what happens to an account whose
// owning user is later added to a real organization tenant. The answer is
// STAYS. The account keeps billing to its personal tenant permanently, and the
// user simply gains a second tenant_users row for the org.
//
// Why "stays" and not "moves": public.tenant_billing_accounts is UNIQUE on
// account_id and keyed on tenant_id, so an account can fund exactly one tenant
// and "belongs to both" is not representable. Re-pointing the mapping would
// retroactively re-attribute every credit and usage row already recorded under
// the personal tenant into the organization, which is the exact leak of one
// organization's usage into another that this design must prevent. Ledgers are
// append only, so there is no correct way to move history.
func TestPersonalTenantAndItsBillingMappingSurviveLaterOrgMembership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	fx := newSelfServeFixture(t, ctx, pool, true)
	personalAccount := ptAccount(t, ctx, pool, fx.userID)

	_, err := fx.prov.Reconcile(ctx, signup.ReconcileInput{UserID: fx.userID, Email: fx.email})
	require.NoError(t, err)
	personalTenant, ok := ptTenantOf(t, ctx, pool, fx.userID)
	require.True(t, ok)
	mapped, ok := ptMappedAccount(t, ctx, pool, personalTenant)
	require.True(t, ok)
	require.Equal(t, personalAccount, mapped)

	// An administrator later adds this user to a real organization tenant.
	orgTenant := mustInsertTenant(t, ctx, pool, "pt-org-"+uuid.NewString()[:8], "HIVE_CLOUD")
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, orgTenant) })
	_, err = pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status) VALUES ($1, $2, 'MEMBER', 'ACTIVE')`,
		orgTenant, fx.userID)
	require.NoError(t, err)

	// Reconcile runs again (console revisit, webhook redelivery). It must not
	// move, duplicate, or re-point anything.
	_, err = fx.prov.Reconcile(ctx, signup.ReconcileInput{UserID: fx.userID, Email: fx.email})
	require.NoError(t, err)

	stillMapped, ok := ptMappedAccount(t, ctx, pool, personalTenant)
	require.True(t, ok, "the personal tenant keeps its billing mapping")
	require.Equal(t, personalAccount, stillMapped, "the account must never be re-pointed at the org tenant")

	_, orgClaimed := ptMappedAccount(t, ctx, pool, orgTenant)
	require.False(t, orgClaimed,
		"the org tenant must not acquire the user's personal account as its billing account")

	require.Equal(t, 1, ptPersonalTenants(t, ctx, pool, fx.userID), "still exactly one personal tenant")
	require.Equal(t, 2, countMemberships(t, ctx, pool, fx.userID),
		"identity membership is many, billing attribution is one")
}

// (e) After the backfill, an API-key request from a previously locked-out
// account resolves a tenant instead of failing closed. The assertion runs
// through apikeys.Repository.GetTenantIDByAccountID, which is the exact lookup
// PR #620 fails closed on (403 account_not_provisioned), so a pass here means
// the real gate opens, not merely that a row exists somewhere.
func TestBackfillPersonalTenantsUnblocksLockedOutAPIKeyAccount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	userID := mustInsertAuthUser(t, ctx, pool, "locked-"+uuid.NewString()[:8]+"@unclaimed.example")
	ptCleanupUser(t, pool, userID)
	accountID := ptAccount(t, ctx, pool, userID)
	ptAPIKey(t, ctx, pool, accountID, userID)

	repo := apikeys.NewPgxRepository(pool)
	before, err := repo.GetTenantIDByAccountID(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, before, "precondition: the account is locked out (403 account_not_provisioned)")

	report, err := signup.BackfillPersonalTenants(ctx, pool, true)
	require.NoError(t, err)
	require.Contains(t, report.Provisioned, accountID)

	after, err := repo.GetTenantIDByAccountID(ctx, accountID)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, after, "backfilled account must now resolve a tenant")

	tenantID, ok := ptTenantOf(t, ctx, pool, userID)
	require.True(t, ok)
	require.Equal(t, tenantID, after)
}

// The backfill reuses the live mapping mechanism rather than a bespoke rule.
// An owner who ALREADY holds an active tenant membership must never be given a
// second tenant; the backfill instead retries EnsureTenantBillingAccount on
// the tenant they already have, which is the same call the live path makes.
func TestBackfillPersonalTenantsMapsExistingTenantInsteadOfMintingSecond(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	userID := mustInsertAuthUser(t, ctx, pool, "hastenant-"+uuid.NewString()[:8]+"@unclaimed.example")
	ptCleanupUser(t, pool, userID)

	orgTenant := mustInsertTenant(t, ctx, pool, "pt-has-"+uuid.NewString()[:8], "HIVE_CLOUD")
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, orgTenant) })
	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status) VALUES ($1, $2, 'MEMBER', 'ACTIVE')`,
		orgTenant, userID)
	require.NoError(t, err)

	accountID := ptAccount(t, ctx, pool, userID)
	ptAPIKey(t, ctx, pool, accountID, userID)

	report, err := signup.BackfillPersonalTenants(ctx, pool, true)
	require.NoError(t, err)
	require.Contains(t, report.Provisioned, accountID)

	require.Equal(t, 0, ptPersonalTenants(t, ctx, pool, userID),
		"an owner who already has a tenant must not be given a second one")
	mapped, ok := ptMappedAccount(t, ctx, pool, orgTenant)
	require.True(t, ok, "the existing tenant is what gets mapped")
	require.Equal(t, accountID, mapped)
}

// Ambiguity survives the backfill too. An owner with no tenant and TWO active
// account memberships has no single billing answer, so the backfill leaves the
// account unmapped and reports a reason rather than guessing. A wrong mapping
// is permanent and is worse than a 403.
func TestBackfillPersonalTenantsLeavesAmbiguousAccountUnmappedWithReason(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	userID := mustInsertAuthUser(t, ctx, pool, "ambig-"+uuid.NewString()[:8]+"@unclaimed.example")
	ptCleanupUser(t, pool, userID)
	first := ptAccount(t, ctx, pool, userID)
	second := ptAccount(t, ctx, pool, userID)
	ptAPIKey(t, ctx, pool, first, userID)
	ptAPIKey(t, ctx, pool, second, userID)

	report, err := signup.BackfillPersonalTenants(ctx, pool, true)
	require.NoError(t, err)

	require.NotContains(t, report.Provisioned, first)
	require.NotContains(t, report.Provisioned, second)
	require.Contains(t, report.Skipped, first, "an ambiguous account is reported, never guessed")
	require.NotEmpty(t, report.Skipped[first], "a skip must carry a reason an operator can act on")

	tenantID, ok := ptTenantOf(t, ctx, pool, userID)
	require.True(t, ok, "the user still gets a tenant so they can authenticate")
	_, mapped := ptMappedAccount(t, ctx, pool, tenantID)
	require.False(t, mapped, "but no billing mapping is invented")
}

// A second account owned by someone whose tenant is ALREADY billed by their
// first account stays locked out and is reported, never silently counted as
// provisioned. EnsureTenantBillingAccount answers "mapped" for that tenant
// because a mapping exists, but it maps a DIFFERENT account, so the backfill
// has to confirm the mapping is to the account it is currently sweeping.
// Without that confirmation the sweep would report an account unblocked while
// its every request still returns 403.
func TestBackfillPersonalTenantsReportsAccountWhoseTenantIsBilledByAnother(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	userID := mustInsertAuthUser(t, ctx, pool, "second-"+uuid.NewString()[:8]+"@unclaimed.example")
	ptCleanupUser(t, pool, userID)

	tenantID := mustInsertTenant(t, ctx, pool, "pt-billed-"+uuid.NewString()[:8], "HIVE_CLOUD")
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, tenantID) })
	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status) VALUES ($1, $2, 'MEMBER', 'ACTIVE')`,
		tenantID, userID)
	require.NoError(t, err)

	billing := ptAccount(t, ctx, pool, userID)
	_, err = pool.Exec(ctx,
		`INSERT INTO public.tenant_billing_accounts(tenant_id, account_id) VALUES ($1, $2)`,
		tenantID, billing)
	require.NoError(t, err)

	// A second account owned by the same user, key-bearing and unmapped.
	secondAccount := ptAccount(t, ctx, pool, userID)
	ptAPIKey(t, ctx, pool, secondAccount, userID)

	report, err := signup.BackfillPersonalTenants(ctx, pool, true)
	require.NoError(t, err)

	require.NotContains(t, report.Provisioned, secondAccount,
		"an account whose tenant is billed by a different account is not unblocked")
	require.Contains(t, report.Skipped, secondAccount)
	require.Equal(t, 0, ptPersonalTenants(t, ctx, pool, userID),
		"the owner already has a tenant, so no personal tenant is minted")
}

// The backfill carries the same deployment posture gate the live path carries.
// On Hive Enterprise it must not mint a personal tenant, because Enterprise
// posture is that membership is administered, and a tenant created there would
// also be mislabelled HIVE_CLOUD. The account is reported instead. Without this
// the backfill would be a way around the gate Reconcile enforces.
func TestBackfillPersonalTenantsCreatesNothingWhenSelfServeDisabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	userID := mustInsertAuthUser(t, ctx, pool, "ent-"+uuid.NewString()[:8]+"@unclaimed.example")
	ptCleanupUser(t, pool, userID)
	accountID := ptAccount(t, ctx, pool, userID)
	ptAPIKey(t, ctx, pool, accountID, userID)

	report, err := signup.BackfillPersonalTenants(ctx, pool, false)
	require.NoError(t, err)

	require.NotContains(t, report.Provisioned, accountID)
	require.Contains(t, report.Skipped, accountID)
	require.Equal(t, "personal_tenant_disabled_for_deployment", report.Skipped[accountID])
	require.Equal(t, 0, ptPersonalTenants(t, ctx, pool, userID),
		"Enterprise posture must never gain a personal tenant via the backfill")
	require.Equal(t, 0, countMemberships(t, ctx, pool, userID))
}

// With self-serve disabled the sweep is still useful: an owner who ALREADY has
// a tenant gets their billing mapping retried, which is posture-neutral and
// works on every deployment.
func TestBackfillPersonalTenantsStillMapsExistingTenantWhenSelfServeDisabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	userID := mustInsertAuthUser(t, ctx, pool, "entmap-"+uuid.NewString()[:8]+"@unclaimed.example")
	ptCleanupUser(t, pool, userID)

	tenantID := mustInsertTenant(t, ctx, pool, "pt-ent-"+uuid.NewString()[:8], "ENTERPRISE_EDGE")
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, tenantID) })
	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status) VALUES ($1, $2, 'MEMBER', 'ACTIVE')`,
		tenantID, userID)
	require.NoError(t, err)

	accountID := ptAccount(t, ctx, pool, userID)
	ptAPIKey(t, ctx, pool, accountID, userID)

	report, err := signup.BackfillPersonalTenants(ctx, pool, false)
	require.NoError(t, err)

	require.Contains(t, report.Provisioned, accountID)
	mapped, ok := ptMappedAccount(t, ctx, pool, tenantID)
	require.True(t, ok)
	require.Equal(t, accountID, mapped)
	require.Equal(t, 0, ptPersonalTenants(t, ctx, pool, userID))
}

// Re-running the backfill changes nothing: the same partial unique index that
// makes the concurrent case safe makes the repeat case a no-op.
func TestBackfillPersonalTenantsIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	userID := mustInsertAuthUser(t, ctx, pool, "twice-"+uuid.NewString()[:8]+"@unclaimed.example")
	ptCleanupUser(t, pool, userID)
	accountID := ptAccount(t, ctx, pool, userID)
	ptAPIKey(t, ctx, pool, accountID, userID)

	first, err := signup.BackfillPersonalTenants(ctx, pool, true)
	require.NoError(t, err)
	require.Contains(t, first.Provisioned, accountID)

	second, err := signup.BackfillPersonalTenants(ctx, pool, true)
	require.NoError(t, err)
	require.NotContains(t, second.Provisioned, accountID)
	require.Equal(t, 1, ptPersonalTenants(t, ctx, pool, userID))
}

// The partial unique index is the load-bearing constraint. Assert it directly,
// so a migration that drops or widens it fails here rather than silently
// re-opening the duplicate-tenant race the Go code relies on it to close.
func TestTenantsPersonalOwnerUniqueIndexRejectsASecondPersonalTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	var isUnique bool
	err := pool.QueryRow(ctx, `
		SELECT i.indisunique
		  FROM pg_index i
		  JOIN pg_class c ON c.oid = i.indexrelid
		 WHERE c.relname = 'tenants_personal_owner_user_id_key'
	`).Scan(&isUnique)
	require.NoError(t, err, "migration must create tenants_personal_owner_user_id_key")
	require.True(t, isUnique)

	userID := mustInsertAuthUser(t, ctx, pool, "dup-"+uuid.NewString()[:8]+"@unclaimed.example")
	ptCleanupUser(t, pool, userID)

	insert := func() error {
		_, err := pool.Exec(ctx,
			`INSERT INTO public.tenants(slug, name, deployment, personal_owner_user_id)
			 VALUES ($1, $1, 'HIVE_CLOUD', $2)`, "dup-"+uuid.NewString()[:8], userID)
		return err
	}
	require.NoError(t, insert())
	err = insert()
	require.Error(t, err, "a second personal tenant for one user must be rejected by the database")
	require.Contains(t, err.Error(), "tenants_personal_owner_user_id_key")
}
