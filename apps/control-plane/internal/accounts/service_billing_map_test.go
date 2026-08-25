package accounts_test

// Regression guard for the tenant_billing_accounts creation-path race
// diagnosed 2026-07-31: signup.Provisioner's own mapping attempt fires the
// instant a tenant_users row is written, but the matching
// account_memberships row is created later, by this package's lazy
// provisionDefaultWorkspace. When the tenant_users write lands first (the
// common order, confirmed live), the old code never got a second chance to
// map that tenant. This test proves the WithBillingPool call site closes
// that gap: it seeds the tenant_users side first, exactly like Reconcile
// leaves it, then drives EnsureViewerContext (the real first-console-visit
// path) and asserts the mapping exists afterward.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

func newBillingMapTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return dbtest.Pool(t, "HIVE_TEST_DB_URL")
}

func TestProvisionDefaultWorkspace_MapsTenantBillingAccountOnceMembershipExists(t *testing.T) {
	pool := newBillingMapTestPool(t)
	ctx := context.Background()
	userID := uuid.New()
	tenantID := uuid.New()
	suffix := uuid.NewString()[:8]

	email := "billing-map-" + suffix + "@example.test"
	// tenant_users.user_id FKs to auth.users(id) in the real schema (this is
	// what CI's full-migration-chain run caught and a lighter throwaway
	// schema did not): seed the auth.users row first, same as mustInsertAuthUser
	// in webhook_test.go / seedMembershipOrderUser in
	// repository_membership_order_test.go do elsewhere in this repo.
	if _, err := pool.Exec(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data) VALUES ($1, $2, '{}'::jsonb)`,
		userID, email); err != nil {
		t.Fatalf("seed auth user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, userID) })

	// Seed the tenant_users side of the race first, exactly as
	// signup.Provisioner leaves it: an ACTIVE tenant_users row for a
	// HIVE_CLOUD tenant, with no account_membership yet and no billing
	// mapping yet.
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenants(id, slug, name, deployment) VALUES ($1, $2, $2, 'HIVE_CLOUD')`,
		tenantID, "billing-map-tenant-"+suffix); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM public.tenants WHERE id = $1`, tenantID) })

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status) VALUES ($1, $2, 'MEMBER', 'ACTIVE')`,
		tenantID, userID); err != nil {
		t.Fatalf("seed tenant_users: %v", err)
	}

	var mappedBefore bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM public.tenant_billing_accounts WHERE tenant_id = $1)`, tenantID,
	).Scan(&mappedBefore); err != nil {
		t.Fatalf("check mapped before: %v", err)
	}
	if mappedBefore {
		t.Fatalf("tenant unexpectedly already mapped before the test ran anything")
	}

	repo := accounts.NewPgxRepository(pool)
	svc := accounts.NewService(repo).WithBillingPool(pool)

	viewer := auth.Viewer{
		UserID:        userID,
		Email:         email,
		EmailVerified: true,
		FullName:      "Billing Map Test",
	}

	vc, err := svc.EnsureViewerContext(ctx, viewer, uuid.Nil)
	if err != nil {
		t.Fatalf("EnsureViewerContext: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.account_memberships WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.accounts WHERE owner_user_id = $1`, userID)
	})
	// tenant_billing_accounts.account_id is ON DELETE RESTRICT (deliberately,
	// see 20260728_01_tenant_billing_account.sql), so the accounts delete
	// above silently fails (its error is ignored, matching every other
	// cleanup in this file) unless this mapping row is gone first. Registered
	// after the accounts cleanup so it runs first under t.Cleanup's LIFO
	// order. Without this, the account this test creates outlives the test
	// and its constant-derived slug collides on the next local run.
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.tenant_billing_accounts WHERE tenant_id = $1`, tenantID)
	})

	var mappedAccountID uuid.UUID
	err = pool.QueryRow(ctx,
		`SELECT account_id FROM public.tenant_billing_accounts WHERE tenant_id = $1`, tenantID,
	).Scan(&mappedAccountID)
	if err != nil {
		t.Fatalf("expected tenant_billing_accounts row for tenant=%s after workspace provisioning, got error: %v", tenantID, err)
	}
	if mappedAccountID != vc.CurrentAccount.ID {
		t.Fatalf("mapped account %s does not match the account EnsureViewerContext just provisioned (%s)",
			mappedAccountID, vc.CurrentAccount.ID)
	}
}

// TestProvisionDefaultWorkspace_SkipsMappingWithoutBillingPool guards the
// nil-safe default: a Service built without WithBillingPool (the vast
// majority of existing callers, and every pre-existing test in this package)
// must keep working exactly as before, with no panic and no attempt to
// touch tenant_billing_accounts.
func TestProvisionDefaultWorkspace_SkipsMappingWithoutBillingPool(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	viewer := auth.Viewer{
		UserID:        uuid.New(),
		Email:         "no-billing-pool@example.test",
		EmailVerified: true,
	}

	if _, err := svc.EnsureViewerContext(context.Background(), viewer, uuid.Nil); err != nil {
		t.Fatalf("EnsureViewerContext: %v", err)
	}
}
