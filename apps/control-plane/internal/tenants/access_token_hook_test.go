package tenants_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestCustomAccessTokenHook_OwuiRoleNeverGrantsInstanceAdmin guards two
// regressions at once, one older than the other.
//
// The older one, found 2026-07-26: every self-serve signup (first user of a
// new tenant is always OWNER) got stuck on Open WebUI's "Account Activation
// Pending" screen because OWUI's OAUTH_ALLOWED_ROLES vocabulary
// (ADMIN,MEMBER,VIEWER) has no OWNER entry and it read the JWT's
// tenant-authorization 'role' claim directly. The fix was a separate
// 'owui_role' claim rather than a remap of 'role' itself, because 'role' is
// also read by RLS policies across tenant_users/tenant_settings/audit_log/
// audit_outbox/llm_traces/manifest and by
// apps/edge-api/internal/auth/jwt_supabase.go. Every role must therefore
// still resolve to some value in that allow-list.
//
// The newer one, issue #748: that separate claim mapped OWNER onto 'ADMIN',
// which is exactly the value deploy/docker/docker-compose.yml lists in
// OAUTH_ADMIN_ROLES, so a customer's tenant role decided whether that
// customer administered the shared chat instance. Instance admin is a
// platform attribute (public.accounts.is_platform_admin plus an ACTIVE owner
// membership, the predicate in apps/control-plane/internal/platform/
// role_pgx.go), never a tenant role. supabase/migrations/
// 20260823_03_owui_role_never_admin.sql maps OWNER and ADMIN to MEMBER so
// both keep chat access and neither is promoted.
//
// This test calls public.custom_access_token_hook directly, the same function
// Supabase Auth invokes at token issuance, for every role value and asserts:
//  1. 'owui_role' is never 'ADMIN', and is always a value OWUI's
//     OAUTH_ALLOWED_ROLES accepts, so no tenant role is stranded and none is
//     promoted.
//  2. 'role' is byte-for-byte the raw tenant_users.role value for every role
//     including OWNER, proof the authorization claim's derivation is still
//     untouched.
func TestCustomAccessTokenHook_OwuiRoleNeverGrantsInstanceAdmin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := newTenantsPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	// OAUTH_ADMIN_ROLES in deploy/docker/docker-compose.yml. Any claim value
	// in this set is an instance-admin grant at the Open WebUI end.
	const owuiAdminRole = "ADMIN"
	// OAUTH_ALLOWED_ROLES in the same file, minus the admin entry.
	allowedNonAdmin := map[string]bool{"MEMBER": true, "VIEWER": true}

	cases := []struct {
		role     string
		wantOwui string
	}{
		{role: "OWNER", wantOwui: "MEMBER"},
		{role: "ADMIN", wantOwui: "MEMBER"},
		{role: "MEMBER", wantOwui: "MEMBER"},
		{role: "VIEWER", wantOwui: "VIEWER"},
	}

	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			tenantID := mustInsertTenant(t, ctx, pool, "hook-"+tc.role, "HIVE_CLOUD")
			userID := mustInsertAuthUser(t, ctx, pool, tc.role+"@hook.example")
			mustInsertMembership(t, ctx, pool, tenantID, userID, tc.role)

			gotRole, gotOwuiRole := invokeAccessTokenHook(t, ctx, pool, userID)

			require.Equal(t, tc.role, gotRole,
				"role claim must remain the raw tenant_users.role value, unchanged by this migration")
			require.NotEqual(t, owuiAdminRole, gotOwuiRole,
				"tenant role %q must not resolve to Open WebUI's admin role: instance admin is "+
					"accounts.is_platform_admin, never a tenant role (issue #748)", tc.role)
			require.True(t, allowedNonAdmin[gotOwuiRole],
				"owui_role %q for tenant role %q is outside OAUTH_ALLOWED_ROLES minus ADMIN, which "+
					"strands the user on the activation-pending screen", gotOwuiRole, tc.role)
			require.Equal(t, tc.wantOwui, gotOwuiRole,
				"owui_role must map OWNER and ADMIN to MEMBER and pass MEMBER and VIEWER through")
		})
	}
}

// invokeAccessTokenHook calls public.custom_access_token_hook the same way
// Supabase Auth does at token issuance (event = {user_id, claims}) and
// returns the resulting 'role' and 'owui_role' claims.
func invokeAccessTokenHook(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) (role, owuiRole string) {
	t.Helper()

	err := pool.QueryRow(ctx, `
		WITH hook AS (
			SELECT public.custom_access_token_hook(
				jsonb_build_object('user_id', $1::text, 'claims', '{}'::jsonb)
			) AS out
		)
		SELECT out->'claims'->>'role', out->'claims'->>'owui_role' FROM hook`,
		userID).Scan(&role, &owuiRole)
	require.NoError(t, err)
	return role, owuiRole
}
