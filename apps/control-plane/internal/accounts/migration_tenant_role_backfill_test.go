package accounts_test

// Regression guard for issue #1646: the promotion half of the tenant_users
// role reconciliation, shipped as
// supabase/migrations/20260901_01_tenant_users_role_promote_backfill.sql.
//
// The migration is one of the two authorization writes in this repository
// that nothing else covers (the other, the write side sync, is guarded by
// service_tenant_role_sync_test.go). It widens a role, so what it must NOT
// touch matters more than what it must: a personal tenant's sole member is
// hardcoded 'MEMBER' on purpose, and stamping OWNER there hands every self
// serve signup the feature gate and marketplace surfaces
// platform.WorkspaceAdminGate exists to withhold.
//
// The SQL is read from disk rather than restated here. A copy in this file
// would pass while the file that actually runs on the demo box said something
// else, which is the exact failure shape (two sources of the same truth
// drifting apart) that produced issue #1646 in the first place.
//
// The migration has already been applied by the time these tests run, since
// the throwaway database is built by the whole chain in order. Re running its
// DO block is therefore also the re runnability assertion the file's header
// claims, at no extra cost.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// promoteBackfillPath is the migration under test, relative to this package.
const promoteBackfillPath = "../../../../supabase/migrations/20260901_01_tenant_users_role_promote_backfill.sql"

// runPromoteBackfill executes the migration file's own SQL against pool.
func runPromoteBackfill(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	sql, err := os.ReadFile(filepath.Clean(promoteBackfillPath))
	if err != nil {
		t.Fatalf("read %s: %v", promoteBackfillPath, err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatalf("run promotion backfill migration: %v", err)
	}
}

// tenantUsersRole reads back the role the migration is supposed to have
// decided about.
func tenantUsersRole(t *testing.T, pool *pgxpool.Pool, tenantID, userID uuid.UUID) string {
	t.Helper()
	var role string
	if err := pool.QueryRow(context.Background(),
		`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID).Scan(&role); err != nil {
		t.Fatalf("read back tenant_users.role: %v", err)
	}
	return role
}

// A business tenant's account owner whose tenant_users row still carries the
// 'MEMBER' every signup path writes is issue #1646 itself: the console admits
// the page, the control plane answers 403, and the owner is told to ask their
// administrator. The migration promotes exactly this row, and the assertion
// that matters is not the column value but IsTenantOwner, the function
// WorkspaceAdminGate actually calls.
func TestPromoteBackfill_BusinessTenantAccountOwnerIsPromoted(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	_, tenantID, _, targetID := tenantRoleSyncFixture(t, pool, "MEMBER", roleSyncFixtureOpts{})
	roleSvc := platform.NewTenantRoleService(platform.NewPgxTenantRoleStore(pool))

	before, err := roleSvc.IsTenantOwner(ctx, targetID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner before backfill: %v", err)
	}
	if before {
		t.Fatal("fixture is wrong: the target must be denied by WorkspaceAdminGate before the backfill runs")
	}

	runPromoteBackfill(t, pool)

	if got := tenantUsersRole(t, pool, tenantID, targetID); got != "OWNER" {
		t.Fatalf("tenant_users.role = %q after the backfill, want OWNER (issue #1646: a real workspace owner is still 403'd on the feature gate and marketplace surfaces)", got)
	}
	after, err := roleSvc.IsTenantOwner(ctx, targetID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner after backfill: %v", err)
	}
	if !after {
		t.Fatal("issue #1646: WorkspaceAdminGate still denies a promoted account owner after the backfill")
	}

	// Re runnable, as the migration's header claims: a second run decides
	// nothing new and must not error.
	runPromoteBackfill(t, pool)
	if got := tenantUsersRole(t, pool, tenantID, targetID); got != "OWNER" {
		t.Fatalf("tenant_users.role = %q after a second run, want OWNER", got)
	}
}

// The one that would be a privilege escalation. A personal tenant's sole
// member owns their own billing account, so account_memberships says 'owner'
// for every self serve signup on the box; only tenants.personal_owner_user_id
// tells that apart from a business owner. Promoting it would hand feature
// gate and marketplace control to every signup, which is a product decision
// nobody has made, not a data correctness fix.
func TestPromoteBackfill_PersonalTenantIsNeverPromoted(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()

	_, tenantID, _, targetID := tenantRoleSyncFixture(t, pool, "MEMBER", roleSyncFixtureOpts{personalTenant: true})
	roleSvc := platform.NewTenantRoleService(platform.NewPgxTenantRoleStore(pool))

	runPromoteBackfill(t, pool)

	if got := tenantUsersRole(t, pool, tenantID, targetID); got != "MEMBER" {
		t.Fatalf("tenant_users.role = %q on a personal tenant after the backfill, want MEMBER: this migration must never widen a personal tenant, see its header and signup/personal_tenant.go", got)
	}
	granted, err := roleSvc.IsTenantOwner(ctx, targetID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner after backfill: %v", err)
	}
	if granted {
		t.Fatal("the backfill granted WorkspaceAdminGate authority on a personal tenant")
	}
}

// The plain authorization case: an account member is not an account owner, so
// nothing about their tenant_users row is stale and nothing may change.
func TestPromoteBackfill_AccountMemberIsNeverPromoted(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")

	_, tenantID, _, targetID := tenantRoleSyncFixture(t, pool, "MEMBER",
		roleSyncFixtureOpts{targetAccountRole: "member"})

	runPromoteBackfill(t, pool)

	if got := tenantUsersRole(t, pool, tenantID, targetID); got != "MEMBER" {
		t.Fatalf("tenant_users.role = %q for an account member after the backfill, want MEMBER", got)
	}
}

// Suspension is how a tenant removes a member's authority without deleting
// the row. Stamping OWNER onto a suspended row restores an owner the tenant
// never granted the moment anything reactivates it, which is the same guard
// signup.SyncTenantMembershipRole carries on its own promotion arm.
func TestPromoteBackfill_SuspendedTenantRowIsNeverPromoted(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")

	_, tenantID, _, targetID := tenantRoleSyncFixture(t, pool, "MEMBER",
		roleSyncFixtureOpts{tenantUsersStatus: "SUSPENDED"})

	runPromoteBackfill(t, pool)

	if got := tenantUsersRole(t, pool, tenantID, targetID); got != "MEMBER" {
		t.Fatalf("tenant_users.role = %q on a SUSPENDED row after the backfill, want MEMBER", got)
	}
}

// 'MEMBER' is the value every signup path writes, so it is the only value
// that is evidence of "never updated". ADMIN is a tier somebody chose, and a
// one time backfill declines to widen it; scripts/check-tenant-role-divergence.sh
// reports it on every deploy instead.
func TestPromoteBackfill_AdminTierIsNotWidened(t *testing.T) {
	pool := dbtest.Pool(t, "HIVE_TEST_DB_URL")

	_, tenantID, _, targetID := tenantRoleSyncFixture(t, pool, "ADMIN", roleSyncFixtureOpts{})

	runPromoteBackfill(t, pool)

	if got := tenantUsersRole(t, pool, tenantID, targetID); got != "ADMIN" {
		t.Fatalf("tenant_users.role = %q after the backfill, want ADMIN left untouched", got)
	}
}
