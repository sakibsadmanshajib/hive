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
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

func newBillingMapTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	if !strings.Contains(strings.ToLower(dsn), "test") {
		t.Fatalf("refusing to run: HIVE_TEST_DB_URL must point at a test database (DSN missing 'test' marker)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestProvisionDefaultWorkspace_MapsTenantBillingAccountOnceMembershipExists(t *testing.T) {
	pool := newBillingMapTestPool(t)
	ctx := context.Background()
	userID := uuid.New()
	tenantID := uuid.New()
	suffix := uuid.NewString()[:8]

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
		Email:         "billing-map-" + suffix + "@example.test",
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
