package settings

// Key is a tenant-setting identifier. It must mirror the
// public.tenant_setting_key Postgres enum exactly.
//
// Dynamic gate exposure (issue #293): the set of keys featuregate returns to
// edge-api is driven by the public.feature_gate_keys table, not by this Go
// const list. Adding a new gate key that should be exposed through
// featuregate is a migration-only change (INSERT INTO feature_gate_keys, and
// ALTER TYPE ... ADD VALUE for a genuinely new enum value). A Key const here
// remains useful as a typed, compile-checked symbol for call sites elsewhere
// in control-plane that read a specific setting (billing, SSO), but is not
// required to make a key visible through featuregate.
type Key string

const (
	EnablePublicBilling  Key = "ENABLE_PUBLIC_BILLING"
	EnableBkash          Key = "ENABLE_BKASH"
	EnableSSLCommerz     Key = "ENABLE_SSLCOMMERZ"
	EnableStripe         Key = "ENABLE_STRIPE"
	EnableCreditPool     Key = "ENABLE_CREDIT_POOL"
	EnablePerUserCap     Key = "ENABLE_PER_USER_CAP"
	EnableExtraUsage     Key = "ENABLE_EXTRA_USAGE"
	EnableRAGPersonal    Key = "ENABLE_RAG_PERSONAL"
	EnableRAGSharedKB    Key = "ENABLE_RAG_SHARED_KB"
	EnableMultiTenant    Key = "ENABLE_MULTI_TENANT"
	EnableSSOGoogle      Key = "ENABLE_SSO_GOOGLE"
	EnableSSOMicrosoft   Key = "ENABLE_SSO_MICROSOFT"
	EnableSSOSaml        Key = "ENABLE_SSO_SAML"
	EnableAdminConsole   Key = "ENABLE_ADMIN_CONSOLE"
	EnableProviderCustom Key = "ENABLE_PROVIDER_CUSTOM"

	// The six ENABLE_AUDIT_SINK_* keys were deliberately removed here
	// (issue #755). Their registry rows are gone
	// (20260829_03_retire_audit_sink_feature_gates.sql) and audit sink
	// enablement is deployment configuration, read only from the process
	// environment by internal/auditworker/sinkconfig. The Postgres enum
	// labels survive because dropping an enum label needs a type rewrite;
	// they are inert. Do not re-declare these constants: a typed symbol
	// here is exactly what would let a per-tenant read grow back.

	// Agent-subsystem feature gates (issue #238; "carl" category retired,
	// see 20260719_01_rename_carl_feature_category.sql).
	EnableRAG    Key = "ENABLE_RAG"
	EnableVoice  Key = "ENABLE_VOICE"
	EnableRelay  Key = "ENABLE_RELAY"
	EnableCowork Key = "ENABLE_COWORK"
)
