package platform_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// Issue #790: public.tenant_users RLS never constrained the role column on a
// write, so a tenant ADMIN could PATCH their own row (or any row in the same
// tenant) through PostgREST, setting role: "OWNER", and become
// indistinguishable from the workspace owner. Proven live against a
// throwaway Postgres with a real GoTrue-issued JWT before writing this fix:
// PATCH /rest/v1/tenant_users as an ADMIN, body {"role":"OWNER"}, updated the
// row. supabase/migrations/20260829_03_tenant_users_role_escalation_guard.sql
// is the fix; these tests replay PostgREST's own RLS mechanism directly
// (connect as the authenticated Postgres role, set request.jwt.claims --
// the exact GUC auth.jwt() reads, see .github/ci/test-db-bootstrap.sql)
// rather than standing up a full HTTP PostgREST + GoTrue stack for a unit
// test, which is one of the two RLS enforcement paths this repository
// already tests this way (see role_rls_test.go / membership_role_rls_test.go
// for the hive_app-role counterpart).
//
// MaxConns is pinned to 1 so SET ROLE and the session-scoped
// request.jwt.claims persist across every call in one test: there is only
// one physical connection for this pool, and it is closed at test end, so
// nothing leaks onto a different tenant's request the way session-scoped
// set_config would on a shared pool.

func newAuthenticatedRLSPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.PoolWithConfig(t, "HIVE_TEST_DB_URL", func(cfg *pgxpool.Config) {
		cfg.MaxConns = 1
	})
	if _, err := pool.Exec(context.Background(), "SET ROLE authenticated"); err != nil {
		pool.Close()
		// Fail, never skip. A failed SET ROLE means the role under test is
		// not provisioned, so nothing below this line exercises the guard,
		// and a skip inside a green check is indistinguishable from a pass.
		// That is the exact shape of issue #1469, where CI granted a
		// hive_app role membership production never grants and this
		// package's RLS suites read as covered for months while proving
		// nothing; a silent skip here would be the same failure twice in
		// the same file.
		//
		// The local opt-out is deliberately a different path and cannot be
		// reached from CI: leave HIVE_TEST_DB_URL unset and
		// dbtest.RequireURL above skips the suite (and fails instead when
		// CI is set and the run is not -short, see packages/dbtest). Once a
		// DSN is configured the run is a required integration run, so a
		// role that cannot be assumed is a provisioning defect rather than
		// an opt-out.
		t.Fatalf("SET ROLE authenticated failed on a configured integration database (issue #1469: a skip here would report coverage this suite did not provide). Apply .github/ci/test-db-bootstrap.sql to provision the role, or unset HIVE_TEST_DB_URL to opt out locally: %v", err)
	}
	return pool
}

// seedTenantUserRow inserts a tenant and one tenant_users row over an
// unscoped connection, mirroring role_rls_test.go's seedMembership.
// authenticated has no INSERT policy that would let it create its own
// fixture (that is the whole point of the RLS this test exercises), so
// fixture setup deliberately does not go through the role under test.
func seedTenantUserRow(t *testing.T, tenantID, userID uuid.UUID, role, status string) {
	t.Helper()
	dsn := dbtest.RequireURL(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()

	if _, err := setup.Exec(ctx,
		`INSERT INTO public.tenants (id, slug, name, deployment)
		 VALUES ($1, $2, 'role escalation test', 'HIVE_CLOUD')
		 ON CONFLICT (id) DO NOTHING`,
		tenantID, "role-escalation-"+tenantID.String()); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := setup.Exec(ctx,
		`INSERT INTO auth.users (id, email) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
		userID, "role-escalation-"+userID.String()+"@hive-test.invalid"); err != nil {
		t.Fatalf("seed auth user: %v", err)
	}
	if _, err := setup.Exec(ctx,
		`INSERT INTO public.tenant_users (tenant_id, user_id, role, status)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = EXCLUDED.role, status = EXCLUDED.status`,
		tenantID, userID, role, status); err != nil {
		t.Fatalf("seed tenant_users row: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, tenantID)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM auth.users WHERE id = $1`, userID)
	})
}

// setRequestJWTClaims sets request.jwt.claims to exactly what a PostgREST
// request from this principal carries, matching the real shape confirmed
// against a live GoTrue-issued token for this repository's custom access
// token hook (supabase/migrations/20260516_07_phase19_custom_access_token_hook.sql):
// role and tenant_id at the top level of the claims object, read by
// auth.jwt()->>'role' / auth.jwt()->>'tenant_id' in the RLS policies under
// test.
func setRequestJWTClaims(t *testing.T, pool *pgxpool.Pool, userID, tenantID uuid.UUID, role string) {
	t.Helper()
	claims, err := json.Marshal(map[string]string{
		"sub":       userID.String(),
		"tenant_id": tenantID.String(),
		"role":      role,
	})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	// Session scope (is_local=false), not LOCAL: this pool's single physical
	// connection is dedicated to one test and closed at t.Cleanup, so nothing
	// leaks onto a different tenant's request the way it would on a shared
	// pool -- the same reasoning newRLSTestPool's SET ROLE already relies on.
	if _, err := pool.Exec(context.Background(),
		"SELECT set_config('request.jwt.claims', $1, false)", string(claims)); err != nil {
		t.Fatalf("set request.jwt.claims: %v", err)
	}
}

// TestTenantUsersRLS_AdminCannotSelfPromoteToOwner is issue #790's literal
// report: an ADMIN updating their OWN row to role: OWNER.
func TestTenantUsersRLS_AdminCannotSelfPromoteToOwner(t *testing.T) {
	pool := newAuthenticatedRLSPool(t)
	ctx := context.Background()
	tenantID, adminID := uuid.New(), uuid.New()
	seedTenantUserRow(t, tenantID, adminID, "ADMIN", "ACTIVE")
	setRequestJWTClaims(t, pool, adminID, tenantID, "ADMIN")

	_, err := pool.Exec(ctx,
		`UPDATE public.tenant_users SET role = 'OWNER' WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, adminID)
	if err == nil {
		t.Fatal("expected ADMIN self-promotion to OWNER to be rejected by RLS, but the UPDATE succeeded")
	}

	var role string
	if scanErr := pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, adminID).Scan(&role); scanErr != nil {
		t.Fatalf("read back role: %v", scanErr)
	}
	if role != "ADMIN" {
		t.Fatalf("expected role to remain ADMIN after the rejected write, got %q", role)
	}
}

// TestTenantUsersRLS_AdminCannotPromoteThirdPartyToOwner is the general case
// the issue's Option 1 also calls out: an ADMIN promoting a DIFFERENT
// member (not themselves) to OWNER must be equally rejected.
func TestTenantUsersRLS_AdminCannotPromoteThirdPartyToOwner(t *testing.T) {
	pool := newAuthenticatedRLSPool(t)
	ctx := context.Background()
	tenantID, adminID, targetID := uuid.New(), uuid.New(), uuid.New()
	seedTenantUserRow(t, tenantID, adminID, "ADMIN", "ACTIVE")
	seedTenantUserRow(t, tenantID, targetID, "MEMBER", "ACTIVE")
	setRequestJWTClaims(t, pool, adminID, tenantID, "ADMIN")

	_, err := pool.Exec(ctx,
		`UPDATE public.tenant_users SET role = 'OWNER' WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, targetID)
	if err == nil {
		t.Fatal("expected ADMIN promoting a third party to OWNER to be rejected by RLS, but the UPDATE succeeded")
	}
}

// TestTenantUsersRLS_AdminCannotInsertNewOwnerRow: the INSERT-side path (an
// ADMIN adding a brand-new member directly with role: OWNER), which the
// UPDATE-side tests above do not cover.
func TestTenantUsersRLS_AdminCannotInsertNewOwnerRow(t *testing.T) {
	pool := newAuthenticatedRLSPool(t)
	ctx := context.Background()
	tenantID, adminID, newUserID := uuid.New(), uuid.New(), uuid.New()
	seedTenantUserRow(t, tenantID, adminID, "ADMIN", "ACTIVE")

	dsn := dbtest.RequireURL(t, "HIVE_TEST_DB_URL")
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()
	if _, err := setup.Exec(ctx,
		`INSERT INTO auth.users (id, email) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
		newUserID, "role-escalation-insert-"+newUserID.String()+"@hive-test.invalid"); err != nil {
		t.Fatalf("seed auth user: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM auth.users WHERE id = $1`, newUserID)
	})

	setRequestJWTClaims(t, pool, adminID, tenantID, "ADMIN")

	_, err = pool.Exec(ctx,
		`INSERT INTO public.tenant_users (tenant_id, user_id, role, status) VALUES ($1, $2, 'OWNER', 'ACTIVE')`,
		tenantID, newUserID)
	if err == nil {
		t.Fatal("expected an ADMIN inserting a new OWNER row to be rejected by RLS, but the INSERT succeeded")
	}
}

// TestTenantUsersRLS_OwnerCanPromoteMemberToOwner is the positive control.
// Without it, a WITH CHECK that denies every write regardless of caller role
// would pass every test above for the wrong reason.
func TestTenantUsersRLS_OwnerCanPromoteMemberToOwner(t *testing.T) {
	pool := newAuthenticatedRLSPool(t)
	ctx := context.Background()
	tenantID, ownerID, memberID := uuid.New(), uuid.New(), uuid.New()
	seedTenantUserRow(t, tenantID, ownerID, "OWNER", "ACTIVE")
	seedTenantUserRow(t, tenantID, memberID, "MEMBER", "ACTIVE")
	setRequestJWTClaims(t, pool, ownerID, tenantID, "OWNER")

	tag, err := pool.Exec(ctx,
		`UPDATE public.tenant_users SET role = 'OWNER' WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, memberID)
	if err != nil {
		t.Fatalf("expected OWNER to be able to promote a member to OWNER, got error: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected exactly 1 row updated, got %d", tag.RowsAffected())
	}
}
