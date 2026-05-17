// Package authz — Phase 19 permission and role extension for edge-api.
//
// This file introduces the Phase 19 RBAC primitives (Role, Permission, and the
// rolePermissions registry) into the edge-api authz package. Phase 18 shipped a
// full RBAC matrix in apps/control-plane/internal/authz; this file mirrors that
// model on the edge data plane so request-time perm checks (chat invocation,
// tenant setting reads/writes, tenant switching, audit log reads) can be made
// without a control-plane round trip.
//
// Style mirrors apps/control-plane/internal/authz/permissions.go:
//   - Permission is a typed string. Wire values are stable.
//   - Role is a typed string keyed off the workspace membership role.
//   - rolePermissions is the single source of truth for role→perm grants.
//
// The rolePermissions table is populated by init() in role_policy_phase19.go.
package authz

// Role is the workspace membership role on the edge-api side. Wire values are
// stable strings and must match the tokens minted upstream by control-plane.
type Role string

const (
	// RoleOwner is the workspace creator. Holds all Phase 19 permissions.
	RoleOwner Role = "owner"
	// RoleAdmin is an elevated operator role, typically a tenant administrator
	// promoted by the owner. Holds all Phase 19 permissions except those that
	// are reserved for the owner alone.
	RoleAdmin Role = "admin"
	// RoleMember is a standard tenant member. May invoke chat and switch
	// tenant context but cannot mutate tenant settings or read audit log.
	RoleMember Role = "member"
	// RoleViewer is a read-only role with chat invocation rights only.
	RoleViewer Role = "viewer"
)

// Permission is a typed authorization token. Wire values are stable — changing
// them is a breaking API contract change.
type Permission string

const (
	// PermChatInvoke gates the customer-facing chat / completion / responses
	// endpoints. Held by every interactive workspace role.
	PermChatInvoke Permission = "CHAT_INVOKE"

	// PermTenantSettingRead gates read access to per-tenant configuration
	// (model routing overrides, rate-limit policies, feature flags).
	PermTenantSettingRead Permission = "TENANT_SETTING_READ"

	// PermTenantSettingWrite gates mutation of per-tenant configuration.
	// Restricted to owner/admin.
	PermTenantSettingWrite Permission = "TENANT_SETTING_WRITE"

	// PermTenantSwitch gates the ability to change the active tenant context
	// in multi-workspace sessions.
	PermTenantSwitch Permission = "TENANT_SWITCH"

	// PermAuditRead gates read access to the workspace audit log.
	// Restricted to owner/admin in Phase 19.
	PermAuditRead Permission = "AUDIT_READ"
)

// rolePermissions maps each Role to the set of Permissions it carries.
// Populated by init() in role_policy_phase19.go so the policy table can be
// extended additively without touching this file in later phases.
var rolePermissions = map[Role][]Permission{}

// addPerms appends one or more Permissions to a Role's policy entry.
// Exposed for Phase 19+ extensions: callers may add additional grants from an
// init() block in their own file without rewriting the central table.
//
// Idempotent across init() ordering, but not safe for concurrent use after
// program start. All mutations must happen during package initialization.
//
// addPerms is init-only; do NOT call after program start (rolePermissions is
// read concurrently without a lock).
func addPerms(r Role, ps ...Permission) {
	rolePermissions[r] = append(rolePermissions[r], ps...)
}

// RoleHas reports whether r is granted perm under the Phase 19 policy table.
// Returns false for unknown roles or permissions — default-deny semantics
// match Phase 18 (apps/control-plane/internal/authz/policy.go Policy.Can).
func RoleHas(r Role, perm Permission) bool {
	grants, ok := rolePermissions[r]
	if !ok {
		return false
	}
	for _, p := range grants {
		if p == perm {
			return true
		}
	}
	return false
}
