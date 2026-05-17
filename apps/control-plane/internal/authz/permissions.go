// Package authz owns the Permission enum, Actor struct, and Policy.Can decision
// function for control-plane authorization. All handler-level authz checks must
// route through this package. No bare `Role == "owner"` or `EmailVerified &&`
// expressions are permitted outside internal/authz/ and internal/platform/.
package authz

import "sort"

// Permission is a typed resource.action authorization token.
// Wire values are stable — changing them is a breaking API change.
type Permission string

const (
	PermBillingView       Permission = "billing.view"
	PermBillingWrite      Permission = "billing.write"
	PermAPIKeysRead       Permission = "api_keys.read"
	PermAPIKeysWrite      Permission = "api_keys.write"
	PermAnalyticsView     Permission = "analytics.view"
	PermMembersInvite     Permission = "members.invite"
	PermMembersManage     Permission = "members.manage"
	PermWorkspaceSettings Permission = "workspace.settings"
	PermGrantsCreate      Permission = "grants.create"
	PermLedgerView        Permission = "ledger.view"
	PermPlatformAdmin     Permission = "platform.admin"
)

// entry describes one row in the permission registry.
type entry struct {
	// RequiresVerified indicates the actor's email must be verified to hold
	// this permission. Derived from the Phase 18 audit of existing call sites.
	RequiresVerified bool
}

// registry is the single source of truth for codegen and Policy decisions.
// RequiresVerified values are derived from the Phase 18 backend audit:
// - All write/manage/admin perms require verification.
// - analytics.view, ledger.view also require verification (audit: accounting/http.go:347,
//   ledger/http.go:165, usage/http.go:393 all gate on !EmailVerified).
// - billing.view, api_keys.read do NOT require verification (read-only; no audit gate).
var registry = map[Permission]entry{
	PermBillingView:       {RequiresVerified: false},
	PermBillingWrite:      {RequiresVerified: true},
	PermAPIKeysRead:       {RequiresVerified: false},
	PermAPIKeysWrite:      {RequiresVerified: true},
	PermAnalyticsView:     {RequiresVerified: true},
	PermMembersInvite:     {RequiresVerified: true},
	PermMembersManage:     {RequiresVerified: true},
	PermWorkspaceSettings: {RequiresVerified: true},
	PermGrantsCreate:      {RequiresVerified: true},
	PermLedgerView:        {RequiresVerified: true},
	PermPlatformAdmin:     {RequiresVerified: true},
}

// AllPermissions returns every registered Permission in stable (lexically sorted)
// order. Used by codegen and AllGranted to ensure deterministic output.
func AllPermissions() []Permission {
	perms := make([]Permission, 0, len(registry))
	for p := range registry {
		perms = append(perms, p)
	}
	sort.Slice(perms, func(i, j int) bool {
		return string(perms[i]) < string(perms[j])
	})
	return perms
}

// RequiresVerified reports whether perm requires email verification.
// Returns false for unknown permissions (safe default — callers MUST still
// enforce role-based access; verification is an additional gate, not a
// substitute for role checks).
func RequiresVerified(perm Permission) bool {
	e, ok := registry[perm]
	return ok && e.RequiresVerified
}
