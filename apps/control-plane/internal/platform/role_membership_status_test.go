package platform_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// Membership status is an authorization input, and the unit tests in this
// package stub RoleStore, so they say nothing about the predicates the real
// SQL applies. These tests run the production queries against a live database
// so that a missing status filter fails here instead of granting a stranger
// the credit-grant endpoint.
//
// public.account_memberships.status is either 'active' or 'invited'. Only an
// active membership is a membership: an invited row records that somebody was
// offered a seat, not that they took it.

// newMembershipStatusTestPool connects with the same unscoped role the other
// live-database suites in this module use. The DSN must carry a "test" marker
// because these tests delete the rows they insert.
func newMembershipStatusTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		// CI wires HIVE_TEST_DB_URL for the live-database step and passes
		// -short for the step that has no database. A missing DSN in the
		// live step means this suite skipped instead of running, which is
		// the silent-green shape issues #701 and #708 were filed for, so
		// fail loudly there. Local runs (CI unset) still skip.
		if os.Getenv("CI") != "" && !testing.Short() {
			t.Fatal("HIVE_TEST_DB_URL not set in CI: this suite guards a privilege escalation and must not silently skip")
		}
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

// seedAccountMembership inserts one auth user, one account, and one membership
// row joining them, and registers cleanup for all three. platformAdmin sets
// public.accounts.is_platform_admin, which is the flag IsPlatformAdmin reads.
func seedAccountMembership(t *testing.T, pool *pgxpool.Pool, platformAdmin bool, role, status string) (userID, accountID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	marker := uuid.NewString()

	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		"membership-status-"+marker+"@hive-test.invalid").Scan(&userID); err != nil {
		t.Fatalf("insert auth user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, userID) })

	slug := "membership-status-" + marker
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts(id, slug, display_name, account_type, owner_user_id, is_platform_admin)
		 VALUES (gen_random_uuid(), $1, $1, 'business', $2, $3) RETURNING id`,
		slug, userID, platformAdmin).Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM public.accounts WHERE id = $1`, accountID) })

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.account_memberships(id, account_id, user_id, role, status)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4)`,
		accountID, userID, role, status); err != nil {
		t.Fatalf("insert membership: %v", err)
	}
	return userID, accountID
}

// TestIsPlatformAdmin_RequiresActiveMembership is the regression guard for the
// escalation: IsPlatformAdmin used to select on role = 'owner' alone, so a user
// invited as owner to a platform-admin account held platform-admin authority
// (credit minting, provider administration) from the moment the invitation row
// was written, before accepting anything.
//
// The active case runs first and must pass, so a fixture that silently inserts
// nothing cannot make the invited case pass for the wrong reason.
func TestIsPlatformAdmin_RequiresActiveMembership(t *testing.T) {
	pool := newMembershipStatusTestPool(t)
	svc := platform.NewRoleService(platform.NewPgxRoleStore(pool))
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		role   string
		status string
		want   bool
	}{
		{"active owner is platform admin", "owner", "active", true},
		{"invited owner is not platform admin", "owner", "invited", false},
		{"active member is not platform admin", "member", "active", false},
		{"invited member is not platform admin", "member", "invited", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID, _ := seedAccountMembership(t, pool, true, tc.role, tc.status)

			got, err := svc.IsPlatformAdmin(ctx, userID)
			if err != nil {
				t.Fatalf("IsPlatformAdmin: %v", err)
			}
			if got != tc.want {
				t.Fatalf("IsPlatformAdmin for role=%s status=%s = %v, want %v", tc.role, tc.status, got, tc.want)
			}
		})
	}
}

// TestIsWorkspaceOwner_RequiresActiveMembership covers the sibling predicate,
// which read account_memberships with the same missing status filter and gates
// the workspace budget writes and the egress policy surface.
func TestIsWorkspaceOwner_RequiresActiveMembership(t *testing.T) {
	pool := newMembershipStatusTestPool(t)
	svc := platform.NewRoleService(platform.NewPgxRoleStore(pool))
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		role   string
		status string
		want   bool
	}{
		{"active owner owns the workspace", "owner", "active", true},
		{"invited owner does not own the workspace", "owner", "invited", false},
		{"active member does not own the workspace", "member", "active", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID, accountID := seedAccountMembership(t, pool, false, tc.role, tc.status)

			got, err := svc.IsWorkspaceOwner(ctx, userID, accountID)
			if err != nil {
				t.Fatalf("IsWorkspaceOwner: %v", err)
			}
			if got != tc.want {
				t.Fatalf("IsWorkspaceOwner for role=%s status=%s = %v, want %v", tc.role, tc.status, got, tc.want)
			}
		})
	}
}
