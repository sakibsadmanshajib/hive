// Package authz — Phase 19 role-permission policy table.
//
// Extends the role-permission map (declared in permissions_phase19.go) with
// the Phase 19 permission grants. Populated at package init time so the
// rolePermissions table is ready before any handler-level RoleHas check
// runs. Adding a new permission set in a later phase: create another file
// in this package with its own init() block calling addPerms — no need to
// touch this file.
//
// Grants summary (read with the policy_test_phase19.go matrix for canonical
// expectations):
//
//   owner  : CHAT_INVOKE, TENANT_SETTING_READ, TENANT_SETTING_WRITE,
//            TENANT_SWITCH, AUDIT_READ
//   admin  : CHAT_INVOKE, TENANT_SETTING_READ, TENANT_SETTING_WRITE,
//            TENANT_SWITCH, AUDIT_READ
//   member : CHAT_INVOKE, TENANT_SWITCH
//   viewer : CHAT_INVOKE
//
// Rationale:
//   - All interactive roles can invoke chat (PermChatInvoke).
//   - Only owner/admin may read/write tenant settings or read audit log;
//     this mirrors the Phase 18 control-plane gating of workspace.settings
//     and audit surfaces.
//   - Member retains tenant-switch so multi-tenant users can change the
//     active workspace; viewer is intentionally read-only and cannot switch.
package authz

func init() {
	addPerms(RoleOwner,
		PermChatInvoke,
		PermTenantSettingRead,
		PermTenantSettingWrite,
		PermTenantSwitch,
		PermAuditRead,
	)
	addPerms(RoleAdmin,
		PermChatInvoke,
		PermTenantSettingRead,
		PermTenantSettingWrite,
		PermTenantSwitch,
		PermAuditRead,
	)
	addPerms(RoleMember,
		PermChatInvoke,
		PermTenantSwitch,
	)
	addPerms(RoleViewer,
		PermChatInvoke,
	)
}
