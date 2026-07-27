package catalog

import "strings"

// AliasVisibleToTenant is the single source of truth for per-tenant model
// entitlement. The catalog listing and the inference-time entitlement check
// both resolve every verdict through this function (see queryTenantVisibility
// and filterVisibleForTenant in repository.go), so what a tenant is shown and
// what a tenant may invoke cannot disagree.
//
// tenantOverride is public.tenant_model_visibility.visible for the
// (tenant, alias) pair, or nil when the tenant has no row for that alias.
//
// Rules, in priority order:
//
//  1. An explicit row with visible=false blocks the alias, whatever its class.
//  2. public and preview aliases are entitled by default. Deployments ship with
//     an empty tenant_model_visibility table, so "no row means allowed" is load
//     bearing: any other default would revoke chat for every existing tenant.
//  3. restricted aliases require an explicit visible=true row.
//  4. Anything else (internal, or an unrecognised visibility class) is never
//     entitled. Failing closed on an unknown class keeps a future migration
//     from silently widening access.
func AliasVisibleToTenant(aliasVisibility string, tenantOverride *bool) bool {
	if tenantOverride != nil && !*tenantOverride {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(aliasVisibility)) {
	case "public", "preview":
		return true
	case "restricted":
		return tenantOverride != nil && *tenantOverride
	default:
		return false
	}
}
