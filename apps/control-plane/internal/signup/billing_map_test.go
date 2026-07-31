package signup_test

// Direct regression coverage for EnsureTenantBillingAccount's widened
// predicate and its observable-on-non-mapping behavior (both requested by
// review: drop the old distinct_members = 1 restriction, and never let a
// non-mapping outcome pass silently). Does not reuse newReconcileFixture:
// that fixture's audit/resolver/OWUI plumbing is irrelevant here, this
// function only touches the pool directly.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signup"
)

func mustInsertBillingMapTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, deployment string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenants(id, slug, name, deployment) VALUES ($1, $2, $2, $3)`,
		id, "billing-map-"+uuid.NewString()[:8], deployment)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, id) })
	return id
}

func mustInsertBillingMapAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO public.accounts(id, slug, display_name, owner_user_id) VALUES ($1, $2, $2, $3)`,
		id, "billing-map-acct-"+uuid.NewString()[:8], uuid.New())
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.accounts WHERE id = $1`, id) })
	return id
}

func mustAddBillingMapMember(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, accountID uuid.UUID) {
	t.Helper()
	userID := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO public.account_memberships(account_id, user_id, role, status) VALUES ($1, $2, 'member', 'active')`,
		accountID, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status) VALUES ($1, $2, 'MEMBER', 'ACTIVE')`,
		tenantID, userID)
	require.NoError(t, err)
}

func billingMappedAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (uuid.UUID, bool) {
	t.Helper()
	var accountID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT account_id FROM public.tenant_billing_accounts WHERE tenant_id = $1`, tenantID).Scan(&accountID)
	if err != nil {
		return uuid.Nil, false
	}
	return accountID, true
}

func TestEnsureTenantBillingAccount_MapsTwoMembersAgreeingOnOneAccount(t *testing.T) {
	// This is the case the old distinct_members = 1 restriction refused
	// forever: two members, one account, still unambiguous.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	tenantID := mustInsertBillingMapTenant(t, ctx, pool, "HIVE_CLOUD")
	accountID := mustInsertBillingMapAccount(t, ctx, pool)
	mustAddBillingMapMember(t, ctx, pool, tenantID, accountID)
	mustAddBillingMapMember(t, ctx, pool, tenantID, accountID)

	mapped, reason, err := signup.EnsureTenantBillingAccount(ctx, pool, tenantID)
	require.NoError(t, err)
	require.True(t, mapped, "expected a mapping after two members agree on one account")
	require.Empty(t, reason)

	mappedAccount, ok := billingMappedAccount(t, ctx, pool, tenantID)
	require.True(t, ok, "expected a mapping after two members agree on one account")
	require.Equal(t, accountID, mappedAccount)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.tenant_billing_accounts WHERE tenant_id = $1`, tenantID)
	})
}

func TestEnsureTenantBillingAccount_LeavesAmbiguousTenantUnmapped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	tenantID := mustInsertBillingMapTenant(t, ctx, pool, "HIVE_CLOUD")
	accountA := mustInsertBillingMapAccount(t, ctx, pool)
	accountB := mustInsertBillingMapAccount(t, ctx, pool)
	mustAddBillingMapMember(t, ctx, pool, tenantID, accountA)
	mustAddBillingMapMember(t, ctx, pool, tenantID, accountB)

	mapped, reason, err := signup.EnsureTenantBillingAccount(ctx, pool, tenantID)
	require.NoError(t, err)
	require.False(t, mapped)
	require.Contains(t, reason, "no_unambiguous_candidate")

	_, ok := billingMappedAccount(t, ctx, pool, tenantID)
	require.False(t, ok, "an ambiguous tenant (members on two accounts) must stay unmapped, not error and not guess")
}

func TestEnsureTenantBillingAccount_LeavesAccountAlreadyClaimedByAnotherTenantUnmapped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	claimedAccount := mustInsertBillingMapAccount(t, ctx, pool)
	firstTenant := mustInsertBillingMapTenant(t, ctx, pool, "HIVE_CLOUD")
	mustAddBillingMapMember(t, ctx, pool, firstTenant, claimedAccount)
	firstMapped, firstReason, err := signup.EnsureTenantBillingAccount(ctx, pool, firstTenant)
	require.NoError(t, err)
	require.True(t, firstMapped)
	require.Empty(t, firstReason)
	mapped, ok := billingMappedAccount(t, ctx, pool, firstTenant)
	require.True(t, ok)
	require.Equal(t, claimedAccount, mapped)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.tenant_billing_accounts WHERE tenant_id = $1`, firstTenant)
	})

	// A second, distinct tenant whose only member also happens to share the
	// same already-claimed account (UNIQUE(account_id) forbids the account
	// funding two tenants). The insert's NOT EXISTS guard must skip it
	// without erroring the whole call.
	secondTenant := mustInsertBillingMapTenant(t, ctx, pool, "HIVE_CLOUD")
	mustAddBillingMapMember(t, ctx, pool, secondTenant, claimedAccount)

	secondMapped, secondReason, err := signup.EnsureTenantBillingAccount(ctx, pool, secondTenant)
	require.NoError(t, err)
	require.False(t, secondMapped)
	require.Contains(t, secondReason, "account_already_claimed_by_another_tenant")
	require.Contains(t, secondReason, claimedAccount.String())

	_, ok = billingMappedAccount(t, ctx, pool, secondTenant)
	require.False(t, ok, "an account already funding a different tenant must not be claimed twice")
}
