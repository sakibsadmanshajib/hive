package audit

// Security-tier actions fail closed on Postgres write errors. LLM-tier
// actions fall back to local WAL on Postgres failure. Membership in this
// set is the source of truth; do not gate on Severity alone.
var securityActions = map[string]struct{}{
	"AUTH_SIGNIN_SUCCESS":           {},
	"AUTH_SIGNIN_FAILURE":           {},
	"AUTH_SIGNUP_SUCCESS":           {},
	"AUTH_JWT_INVALID":              {},
	"AUTH_JWT_EXPIRED":              {},
	"AUTH_SESSION_REVOKED":          {},
	"AUTH_SIGNIN_FAILURE_NO_TENANT": {},
	"AUTH_ROLE_CHANGE":              {},
	"RBAC_GRANT":                    {},
	"RBAC_REVOKE":                   {},
	"RBAC_DENY":                     {},
	"CROSS_TENANT_ATTEMPT":          {},
	"TENANT_SETTING_UPDATE":         {},
	"TENANT_SWITCH":                 {},
	"TENANT_USER_ADD":               {},
	"TENANT_USER_REMOVE":            {},
	"OWUI_GROUP_CREATE_SUCCESS":     {},
	"OWUI_GROUP_CREATE_FAILURE":     {},
	"OWUI_GROUP_ADD_SUCCESS":        {},
	"OWUI_GROUP_ADD_FAILURE":        {},
	"API_KEY_ISSUE":                 {},
	"API_KEY_REVOKE":                {},
	"CRYPTO_KEY_ROTATE":             {},
	"TLS_CERT_ROTATE":               {},
	"JWKS_FETCH_FAILURE":            {},
	"MIGRATION_APPLY":               {},
	"DEPLOY_PUSH":                   {},
	"BACKUP_INTEGRITY_FAIL":         {},
	"AUDIT_CHAIN_VERIFY_FAIL":       {},
	"SERVER_PANIC":                  {},
	"WEBHOOK_SIGNATURE_FAIL":        {},
	"INCIDENT_DECLARE":              {},
	"INCIDENT_RESOLVE":              {},
}

// IsSecurityAction reports whether the action must use the sync-block path.
func IsSecurityAction(action string) bool {
	_, ok := securityActions[action]
	return ok
}
