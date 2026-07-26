package tenants_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestCustomAccessTokenHook_AddsOwuiRoleWithoutChangingRoleClaim guards the
// exact regression found 2026-07-26: every self-serve signup (first user of
// a new tenant is always OWNER) got stuck on Open WebUI's "Account
// Activation Pending" screen because OWUI's OAUTH_ALLOWED_ROLES vocabulary
// (ADMIN,MEMBER,VIEWER) has no OWNER entry and it read the JWT's
// tenant-authorization 'role' claim directly.
// supabase/migrations/20260726_01_owui_role_claim.sql fixes this with a
// separate 'owui_role' claim (OWNER -> ADMIN, everything else passthrough)
// rather than remapping 'role' itself, because 'role' is also read by RLS
// policies across tenant_users/tenant_settings/audit_log/audit_outbox/
// llm_traces/manifest and by apps/edge-api/internal/auth/jwt_supabase.go.
// This test calls public.custom_access_token_hook directly (the same
// function Supabase Auth invokes at token issuance) for every role value
// and asserts:
//  1. 'owui_role' is ADMIN for OWNER and identical to 'role' for everyone
//     else -- the new behaviour Open WebUI needs.
//  2. 'role' is byte-for-byte the raw tenant_users.role value for every
//     role including OWNER -- proof the pre-existing authorization claim's
//     derivation was not touched by this migration.
func TestCustomAccessTokenHook_AddsOwuiRoleWithoutChangingRoleClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := newTenantsPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	cases := []struct {
		role     string
		wantOwui string
	}{
		{role: "OWNER", wantOwui: "ADMIN"},
		{role: "ADMIN", wantOwui: "ADMIN"},
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
			require.Equal(t, tc.wantOwui, gotOwuiRole,
				"owui_role must map OWNER->ADMIN and pass every other role through unchanged")
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
