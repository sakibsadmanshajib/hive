package accounts_test

// Regression guard for issue #1245: a Members-page role change
// (accounts.Service.UpdateMemberRole) must propagate onto public.tenant_users,
// because platform.WorkspaceAdminGate (and egress.Service) authorize off that
// table, and before this PR nothing ever wrote to it after signup. A demoted
// owner kept backend-enforced owner authority forever.
//
// This suite drives the real pgx-backed accounts.Service against a live
// Postgres (see packages/dbtest), seeds a tenant_users row the way it looked
// BEFORE this fix existed -- a stale role nothing updates -- and asserts both
// ends of the propagation:
//
//   - demotion: a stale tenant_users OWNER row must flip to MEMBER, and
//     platform.TenantRoleService.IsTenantOwner (the function
//     WorkspaceAdminGate.Require actually calls) must revoke.
//   - promotion: a stale tenant_users MEMBER row for a user just promoted to
//     account owner must flip to OWNER, and IsTenantOwner must grant.
//
// Run against pre-fix code (accounts.Service.UpdateMemberRole with the
// signup.SyncTenantMembershipRole call site removed), the demotion assertion
// fails: tenant_users.role stays 'OWNER' and IsTenantOwner keeps returning
// true. That is the red state this guards against.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// tenantRoleSyncFixture seeds one tenant mapped to one business account with
// two ACTIVE owners (actorID, targetID), and a stale tenant_users row for
// targetID carrying targetTenantRole -- the pre-sync state this test proves
// UpdateMemberRole now corrects. Two owners is load-bearing:
// UpdateMemberRole rejects demoting the sole remaining owner (ErrLastOwner),
// so a single-owner fixture could never exercise the demotion path this test
// exists for.
func tenantRoleSyncFixture(t *testing.T, pool *pgxpool.Pool, targetTenantRole string) (accountID, tenantID, actorID, targetID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	accountID = uuid.New()
	tenantID = uuid.New()
	actorID = uuid.New()
	targetID = uuid.New()

	for _, u := range []struct {
		id    uuid.UUID
		email string
	}{
		{actorID, "role-sync-actor-" + suffix + "@example.test"},
		{targetID, "role-sync-target-" + suffix + "@example.test"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO auth.users(id, email, raw_user_meta_data) VALUES ($1, $2, '{}'::jsonb)`,
			u.id, u.email); err != nil {
			t.Fatalf("seed auth user %s: %v", u.email, err)
		}
		id := u.id
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM auth.users WHERE id = $1`, id) })
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.accounts(id, slug, display_name, account_type, owner_user_id)
		 VALUES ($1, $2, 'Role Sync Test', 'business', $3)`,
		accountID, "role-sync-account-"+suffix, actorID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.accounts WHERE id = $1`, accountID) })

	for _, m := range []struct {
		userID uuid.UUID
		role   string
	}{
		{actorID, "owner"},
		{targetID, "owner"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.account_memberships(id, account_id, user_id, role, status)
			 VALUES ($1, $2, $3, $4, 'active')`,
			uuid.New(), accountID, m.userID, m.role); err != nil {
			t.Fatalf("seed account_membership: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.account_memberships WHERE account_id = $1`, accountID)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenants(id, slug, name, deployment) VALUES ($1, $2, $2, 'HIVE_CLOUD')`,
		tenantID, "role-sync-tenant-"+suffix); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, tenantID) })

	// tenant_billing_accounts.account_id is ON DELETE RESTRICT, so this must
	// be deleted before the accounts cleanup above runs; t.Cleanup is LIFO,
	// so registering it AFTER guarantees it runs FIRST.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.tenant_billing_accounts WHERE tenant_id = $1`, tenantID)
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_billing_accounts(tenant_id, account_id) VALUES ($1, $2)`,
		tenantID, accountID); err != nil {
		t.Fatalf("seed tenant_billing_accounts: %v", err)
	}

	// The stale tenant_users row: seeded directly, exactly as it would have
	// been left by signup (or, for the OWNER case, by a manual seed script --
	// see scripts/seed-demo-owner.py, the one place that has ever written
	// tenant_users.role = 'OWNER'), never updated since.
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status) VALUES ($1, $2, $3, 'ACTIVE')`,
		tenantID, targetID, targetTenantRole); err != nil {
		t.Fatalf("seed tenant_users: %v", err)
	}

	return accountID, tenantID, actorID, targetID
}

func TestUpdateMemberRole_DemotionRevokesTenantUsersOwnerRole(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	// Stale tenant_users says OWNER (pre-fix leftover); account_memberships
	// also currently says owner, matching -- until the demotion below.
	accountID, tenantID, actorID, targetID := tenantRoleSyncFixture(t, pool, "OWNER")

	repo := accounts.NewPgxRepository(pool)
	svc := accounts.NewService(repo).WithBillingPool(pool)
	roleSvc := platform.NewTenantRoleService(platform.NewPgxTenantRoleStore(pool))

	before, err := roleSvc.IsTenantOwner(ctx, targetID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner before demotion: %v", err)
	}
	if !before {
		t.Fatal("fixture is wrong: target must read as tenant owner before the demotion")
	}

	viewer := auth.Viewer{UserID: actorID, EmailVerified: true}
	if err := svc.UpdateMemberRole(ctx, accountID, viewer, targetID, "member"); err != nil {
		t.Fatalf("UpdateMemberRole (demote): %v", err)
	}

	var tenantUsersRole string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetID).Scan(&tenantUsersRole); err != nil {
		t.Fatalf("read back tenant_users.role: %v", err)
	}
	if tenantUsersRole != "MEMBER" {
		t.Fatalf("tenant_users.role = %q after demotion, want MEMBER (issue #1245: demotion did not propagate)", tenantUsersRole)
	}

	after, err := roleSvc.IsTenantOwner(ctx, targetID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner after demotion: %v", err)
	}
	if after {
		t.Fatal("issue #1245 regression: WorkspaceAdminGate's IsTenantOwner still grants owner authority after a Members-page demotion")
	}
}

func TestUpdateMemberRole_PromotionGrantsTenantUsersOwnerRole(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	// Stale tenant_users says MEMBER; account_memberships also currently says
	// owner already (both fixture users are seeded as account owners), so
	// this test exercises UpdateMemberRole demoting THEN re-promoting the
	// target, isolating the promotion propagation path independent of the
	// demotion test above.
	accountID, tenantID, actorID, targetID := tenantRoleSyncFixture(t, pool, "MEMBER")

	repo := accounts.NewPgxRepository(pool)
	svc := accounts.NewService(repo).WithBillingPool(pool)
	roleSvc := platform.NewTenantRoleService(platform.NewPgxTenantRoleStore(pool))

	viewer := auth.Viewer{UserID: actorID, EmailVerified: true}
	// Demote first so the subsequent promotion is a real role transition
	// (UpdateMemberRole no-ops when the requested role already matches).
	if err := svc.UpdateMemberRole(ctx, accountID, viewer, targetID, "member"); err != nil {
		t.Fatalf("UpdateMemberRole (demote to set up promotion case): %v", err)
	}

	before, err := roleSvc.IsTenantOwner(ctx, targetID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner before promotion: %v", err)
	}
	if before {
		t.Fatal("fixture is wrong: target must not read as tenant owner before the promotion")
	}

	if err := svc.UpdateMemberRole(ctx, accountID, viewer, targetID, "owner"); err != nil {
		t.Fatalf("UpdateMemberRole (promote): %v", err)
	}

	var tenantUsersRole string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetID).Scan(&tenantUsersRole); err != nil {
		t.Fatalf("read back tenant_users.role: %v", err)
	}
	if tenantUsersRole != "OWNER" {
		t.Fatalf("tenant_users.role = %q after promotion, want OWNER (issue #1244: promotion did not propagate)", tenantUsersRole)
	}

	after, err := roleSvc.IsTenantOwner(ctx, targetID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner after promotion: %v", err)
	}
	if !after {
		t.Fatal("issue #1244 regression: WorkspaceAdminGate's IsTenantOwner still denies a legitimately promoted co-owner")
	}
}
