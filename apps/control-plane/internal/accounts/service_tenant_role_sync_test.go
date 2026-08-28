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
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signup"
	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// roleSyncFixtureOpts tunes tenantRoleSyncFixture for the cases that need a
// shape other than "business tenant, both users account owners, ACTIVE
// tenant_users row". Zero value is that default.
type roleSyncFixtureOpts struct {
	// targetAccountRole seeds targetID's public.account_memberships.role.
	// Defaults to "owner". Set to "member" to exercise a caller that
	// re-issues a role the membership row already carries.
	targetAccountRole string
	// tenantUsersStatus seeds public.tenant_users.status. Defaults to "ACTIVE".
	tenantUsersStatus string
	// personalTenant marks the tenant as targetID's personal tenant
	// (public.tenants.personal_owner_user_id), the discriminator
	// signup.SyncTenantMembershipRole gates its promotion arm on, and seeds
	// the account as account_type 'personal' to match.
	personalTenant bool
}

// tenantRoleSyncFixture seeds one tenant mapped to one account with two ACTIVE
// account members (actorID, always an owner; targetID, opts.targetAccountRole)
// and a stale tenant_users row for targetID carrying targetTenantRole -- the
// pre-sync state these tests prove UpdateMemberRole now corrects. Two owners is
// load-bearing in the default shape: UpdateMemberRole rejects demoting the sole
// remaining owner (ErrLastOwner), so a single-owner fixture could never
// exercise the demotion path this test exists for.
func tenantRoleSyncFixture(t *testing.T, pool *pgxpool.Pool, targetTenantRole string, opts roleSyncFixtureOpts) (accountID, tenantID, actorID, targetID uuid.UUID) {
	t.Helper()
	if opts.targetAccountRole == "" {
		opts.targetAccountRole = "owner"
	}
	if opts.tenantUsersStatus == "" {
		opts.tenantUsersStatus = "ACTIVE"
	}
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

	accountType := "business"
	if opts.personalTenant {
		accountType = "personal"
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.accounts(id, slug, display_name, account_type, owner_user_id)
		 VALUES ($1, $2, 'Role Sync Test', $4, $3)`,
		accountID, "role-sync-account-"+suffix, actorID, accountType); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.accounts WHERE id = $1`, accountID) })

	for _, m := range []struct {
		userID uuid.UUID
		role   string
	}{
		{actorID, "owner"},
		{targetID, opts.targetAccountRole},
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

	var personalOwner *uuid.UUID
	if opts.personalTenant {
		personalOwner = &targetID
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenants(id, slug, name, deployment, personal_owner_user_id)
		 VALUES ($1, $2, $2, 'HIVE_CLOUD', $3)`,
		tenantID, "role-sync-tenant-"+suffix, personalOwner); err != nil {
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
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status) VALUES ($1, $2, $3, $4)`,
		tenantID, targetID, targetTenantRole, opts.tenantUsersStatus); err != nil {
		t.Fatalf("seed tenant_users: %v", err)
	}

	return accountID, tenantID, actorID, targetID
}

func TestUpdateMemberRole_DemotionRevokesTenantUsersOwnerRole(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	// Stale tenant_users says OWNER (pre-fix leftover); account_memberships
	// also currently says owner, matching -- until the demotion below.
	accountID, tenantID, actorID, targetID := tenantRoleSyncFixture(t, pool, "OWNER", roleSyncFixtureOpts{})

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
	accountID, tenantID, actorID, targetID := tenantRoleSyncFixture(t, pool, "MEMBER", roleSyncFixtureOpts{})

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

// A personal tenant's sole user must never reach tenant_users OWNER through
// this path, even when they are an owner of the mapped account. That is the
// invariant signup.insertPersonalMembership's 'MEMBER' hardcode exists for,
// and the escalation this test reproduces is reachable entirely through the
// public API: the account's own user invites a second identity as a co-owner
// (Service.CreateInvitation has no account_type guard and 20260727_02 allows
// role 'owner'), and that co-owner then promotes them here. Before the
// personal_owner_user_id guard in signup.SyncTenantMembershipRole, this wrote
// 'OWNER' and handed a self-serve signup WorkspaceAdminGate's feature-gate and
// marketplace admin surfaces plus egress allowlist control over their own
// tenant.
func TestUpdateMemberRole_PersonalTenantNeverPromotedToTenantOwner(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	accountID, tenantID, actorID, targetID := tenantRoleSyncFixture(t, pool, "MEMBER", roleSyncFixtureOpts{
		targetAccountRole: "member",
		personalTenant:    true,
	})

	repo := accounts.NewPgxRepository(pool)
	svc := accounts.NewService(repo).WithBillingPool(pool)
	roleSvc := platform.NewTenantRoleService(platform.NewPgxTenantRoleStore(pool))

	viewer := auth.Viewer{UserID: actorID, EmailVerified: true}
	if err := svc.UpdateMemberRole(ctx, accountID, viewer, targetID, "owner"); err != nil {
		t.Fatalf("UpdateMemberRole (promote on personal tenant): %v", err)
	}

	var tenantUsersRole string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetID).Scan(&tenantUsersRole); err != nil {
		t.Fatalf("read back tenant_users.role: %v", err)
	}
	if tenantUsersRole != "MEMBER" {
		t.Fatalf("tenant_users.role = %q on a personal tenant after an account promotion, want MEMBER (privilege escalation: the personal-tenant MEMBER hardcode was overwritten)", tenantUsersRole)
	}

	owner, err := roleSvc.IsTenantOwner(ctx, targetID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner after promotion: %v", err)
	}
	if owner {
		t.Fatal("privilege escalation: a personal tenant's sole user now passes WorkspaceAdminGate on their own tenant")
	}
}

// public.tenant_users.role has four values; public.account_memberships.role has
// two. A billing role change must move the OWNER bit only and leave the tiers
// this sync was never told about alone: flattening ADMIN to MEMBER destroys the
// INSERT/UPDATE/DELETE grant the tenant_users RLS policies give
// role IN ('OWNER','ADMIN'), and flattening VIEWER to MEMBER is an outright
// widening, since public.owui_role carries VIEWER through to the chat surface
// as its own tier (20260823_03).
func TestUpdateMemberRole_PreservesNonOwnerTenantRoleTiers(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	for _, tenantRole := range []string{"ADMIN", "VIEWER"} {
		t.Run(tenantRole, func(t *testing.T) {
			accountID, tenantID, actorID, targetID := tenantRoleSyncFixture(t, pool, tenantRole, roleSyncFixtureOpts{})

			repo := accounts.NewPgxRepository(pool)
			svc := accounts.NewService(repo).WithBillingPool(pool)

			viewer := auth.Viewer{UserID: actorID, EmailVerified: true}
			if err := svc.UpdateMemberRole(ctx, accountID, viewer, targetID, "member"); err != nil {
				t.Fatalf("UpdateMemberRole (demote): %v", err)
			}

			var got string
			if err := pool.QueryRow(ctx,
				`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
				tenantID, targetID).Scan(&got); err != nil {
				t.Fatalf("read back tenant_users.role: %v", err)
			}
			if got != tenantRole {
				t.Fatalf("tenant_users.role = %q after an unrelated account role change, want %q unchanged (the sync flattened a tenant tier it was never told about)", got, tenantRole)
			}
		})
	}
}

// Re-issuing a role the membership row already carries must still run the
// sync. The propagation is best-effort, so a transient failure (a cancelled
// request context, a database blip) leaves account_memberships and
// tenant_users disagreeing in the permissive direction with only a log line to
// show for it; re-applying the same role from the Members page is the only
// operator-reachable repair, and it was a no-op while UpdateMemberRole
// returned early on target.Role == newRole.
func TestUpdateMemberRole_SameRoleReissueRepairsTenantUsersDrift(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	// account_memberships already says member; tenant_users still says OWNER,
	// which is the drift a failed earlier sync leaves behind.
	accountID, tenantID, actorID, targetID := tenantRoleSyncFixture(t, pool, "OWNER", roleSyncFixtureOpts{
		targetAccountRole: "member",
	})

	repo := accounts.NewPgxRepository(pool)
	svc := accounts.NewService(repo).WithBillingPool(pool)

	viewer := auth.Viewer{UserID: actorID, EmailVerified: true}
	if err := svc.UpdateMemberRole(ctx, accountID, viewer, targetID, "member"); err != nil {
		t.Fatalf("UpdateMemberRole (re-issue same role): %v", err)
	}

	var got string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetID).Scan(&got); err != nil {
		t.Fatalf("read back tenant_users.role: %v", err)
	}
	if got != "MEMBER" {
		t.Fatalf("tenant_users.role = %q after re-issuing the current role, want MEMBER (a repeated role change must repair a failed earlier propagation, not short-circuit)", got)
	}
}

// Suspension removes a member's tenant authority without deleting the row, so
// a promotion must not stamp OWNER onto a row that is not ACTIVE: whatever
// reactivates it would restore an owner the tenant never granted, and
// signup/reconcile.go's ON CONFLICT DO NOTHING insert never corrects an
// existing row's role.
func TestUpdateMemberRole_PromotionSkipsNonActiveTenantUsersRow(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	accountID, tenantID, actorID, targetID := tenantRoleSyncFixture(t, pool, "MEMBER", roleSyncFixtureOpts{
		targetAccountRole: "member",
		tenantUsersStatus: "SUSPENDED",
	})

	repo := accounts.NewPgxRepository(pool)
	svc := accounts.NewService(repo).WithBillingPool(pool)

	viewer := auth.Viewer{UserID: actorID, EmailVerified: true}
	if err := svc.UpdateMemberRole(ctx, accountID, viewer, targetID, "owner"); err != nil {
		t.Fatalf("UpdateMemberRole (promote): %v", err)
	}

	var got string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetID).Scan(&got); err != nil {
		t.Fatalf("read back tenant_users.role: %v", err)
	}
	if got != "MEMBER" {
		t.Fatalf("tenant_users.role = %q on a SUSPENDED row after an account promotion, want MEMBER unchanged (a reactivation would hand back an owner the tenant never granted)", got)
	}
}

// hiveAppPool connects as hive_app, the non-BYPASSRLS role every Phase 19+
// grant migration exists for, so grant and policy gaps fail here instead of in
// a deployment. MaxConns is pinned to 1 so every Acquire returns the physical
// connection the SET ROLE was issued on. Mirrors
// apps/control-plane/internal/platform/role_rls_test.go's newRLSTestPool.
func hiveAppPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.PoolWithConfig(t, "HIVE_TEST_DB_URL", func(cfg *pgxpool.Config) {
		cfg.MaxConns = 1
	})
	if _, err := pool.Exec(context.Background(), "SET ROLE hive_app"); err != nil {
		t.Skipf("SET ROLE hive_app failed (is hive_app provisioned + migrations applied on this test DB?): %v", err)
	}
	return pool
}

// Pins the deployment posture this sync actually depends on, in both
// directions, because the suite above runs as the superuser DSN CI bootstraps
// and so cannot observe either half.
//
// Read half: 20260828_01 grants hive_app SELECT on
// public.tenant_billing_accounts with a permissive policy, so the sync's
// account to tenant lookup works under the hardened role. Without that grant
// the join would permission-fail.
//
// Write half: it does NOT work under hive_app, deliberately. hive_app has
// SELECT only on public.tenant_users (20260726_01) and no grant at all on
// public.tenants, so the statement fails loudly with permission denied, the
// call site logs it, and nothing is silently skipped. This assertion is the
// forcing function on the trap described in 20260828_01's header: a bare
// GRANT UPDATE with no matching policy would turn that loud error into
// RowsAffected() == 0, reported as the benign-looking reason=no_match. When
// someone does open the write half, this test must go red and be updated
// together with the policy that makes the write actually match rows.
func TestSyncTenantMembershipRole_HiveAppPosture(t *testing.T) {
	seed := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	accountID, tenantID, _, targetID := tenantRoleSyncFixture(t, seed, "OWNER", roleSyncFixtureOpts{
		targetAccountRole: "member",
	})

	app := hiveAppPool(t)

	var mapped int
	if err := app.QueryRow(ctx,
		`SELECT count(*) FROM public.tenant_billing_accounts WHERE account_id = $1`,
		accountID).Scan(&mapped); err != nil {
		t.Fatalf("hive_app SELECT on tenant_billing_accounts: %v (20260828_01's grant or policy is missing)", err)
	}
	if mapped != 1 {
		t.Fatalf("hive_app read %d tenant_billing_accounts rows for the seeded account, want 1 (the RLS policy hides the mapping this sync joins through)", mapped)
	}

	_, _, err := signup.SyncTenantMembershipRole(ctx, app, accountID, targetID)
	if err == nil {
		t.Fatal("SyncTenantMembershipRole succeeded on a hive_app connection: the tenant_users write half is supposed to be bypass-RLS-only. If write access was opened deliberately, update 20260828_01's header and this test together, and prove the new policy matches rows rather than silently reporting no_match")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("SyncTenantMembershipRole under hive_app failed with %v, want a permission denied error (a different failure means this test is no longer pinning what it claims)", err)
	}

	var got string
	if err := seed.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetID).Scan(&got); err != nil {
		t.Fatalf("read back tenant_users.role: %v", err)
	}
	if got != "OWNER" {
		t.Fatalf("tenant_users.role = %q, want OWNER unchanged: the hive_app attempt must fail loudly rather than partially applying", got)
	}
}
