// Package authz_test — Phase 19 policy matrix test.
//
// This file enumerates the role × permission decision table for the Phase 19
// edge-api RBAC extension. Each row is one cell of the canonical matrix; the
// table is the authoritative spec — read it to understand what each role is
// granted in Phase 19.
//
// Mirrors the matrix-table style of
// apps/control-plane/internal/authz/policy_test.go::TestPolicyMatrix.
package authz_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hivegpt/hive/apps/edge-api/internal/authz"
)

// TestPolicy_Phase19_Permissions enumerates every (role, perm) cell defined
// by the Phase 19 grant table plus a representative set of unknown-role and
// unknown-perm cases to lock in default-deny semantics.
func TestPolicy_Phase19_Permissions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		role  authz.Role
		perm  authz.Permission
		allow bool
	}{
		// --- owner: full Phase 19 grant ---
		{authz.RoleOwner, authz.PermChatInvoke, true},
		{authz.RoleOwner, authz.PermTenantSettingRead, true},
		{authz.RoleOwner, authz.PermTenantSettingWrite, true},
		{authz.RoleOwner, authz.PermTenantSwitch, true},
		{authz.RoleOwner, authz.PermAuditRead, true},

		// --- admin: full Phase 19 grant (mirrors owner) ---
		{authz.RoleAdmin, authz.PermChatInvoke, true},
		{authz.RoleAdmin, authz.PermTenantSettingRead, true},
		{authz.RoleAdmin, authz.PermTenantSettingWrite, true},
		{authz.RoleAdmin, authz.PermTenantSwitch, true},
		{authz.RoleAdmin, authz.PermAuditRead, true},

		// --- member: chat + tenant switch only ---
		{authz.RoleMember, authz.PermChatInvoke, true},
		{authz.RoleMember, authz.PermTenantSettingRead, false},
		{authz.RoleMember, authz.PermTenantSettingWrite, false},
		{authz.RoleMember, authz.PermTenantSwitch, true},
		{authz.RoleMember, authz.PermAuditRead, false},

		// --- viewer: chat only ---
		{authz.RoleViewer, authz.PermChatInvoke, true},
		{authz.RoleViewer, authz.PermTenantSettingRead, false},
		{authz.RoleViewer, authz.PermTenantSettingWrite, false},
		{authz.RoleViewer, authz.PermTenantSwitch, false},
		{authz.RoleViewer, authz.PermAuditRead, false},

		// --- default-deny: unknown role ---
		{authz.Role("guest"), authz.PermChatInvoke, false},
		{authz.Role(""), authz.PermChatInvoke, false},

		// --- default-deny: unknown permission ---
		{authz.RoleOwner, authz.Permission("not.a.real.perm"), false},
		{authz.RoleAdmin, authz.Permission(""), false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.role)+"/"+string(tc.perm), func(t *testing.T) {
			t.Parallel()
			require.Equal(t,
				tc.allow,
				authz.RoleHas(tc.role, tc.perm),
				"RoleHas(%q, %q) want %v", tc.role, tc.perm, tc.allow,
			)
		})
	}
}
