package settings

// Key is a tenant-setting identifier. It must mirror the
// public.tenant_setting_key Postgres enum exactly.
type Key string

const (
	EnablePublicBilling     Key = "ENABLE_PUBLIC_BILLING"
	EnableBkash             Key = "ENABLE_BKASH"
	EnableSSLCommerz        Key = "ENABLE_SSLCOMMERZ"
	EnableStripe            Key = "ENABLE_STRIPE"
	EnableCreditPool        Key = "ENABLE_CREDIT_POOL"
	EnablePerUserCap        Key = "ENABLE_PER_USER_CAP"
	EnableExtraUsage        Key = "ENABLE_EXTRA_USAGE"
	EnableRAGPersonal       Key = "ENABLE_RAG_PERSONAL"
	EnableRAGSharedKB       Key = "ENABLE_RAG_SHARED_KB"
	EnableMultiTenant       Key = "ENABLE_MULTI_TENANT"
	EnableSSOGoogle         Key = "ENABLE_SSO_GOOGLE"
	EnableSSOMicrosoft      Key = "ENABLE_SSO_MICROSOFT"
	EnableSSOSaml           Key = "ENABLE_SSO_SAML"
	EnableAuditSinkELK      Key = "ENABLE_AUDIT_SINK_ELK"
	EnableAuditSinkLoki     Key = "ENABLE_AUDIT_SINK_LOKI"
	EnableAuditSinkDatadog  Key = "ENABLE_AUDIT_SINK_DATADOG"
	EnableAuditSinkSplunk   Key = "ENABLE_AUDIT_SINK_SPLUNK"
	EnableAuditSinkLangfuse Key = "ENABLE_AUDIT_SINK_LANGFUSE"
	EnableAuditSinkSentry   Key = "ENABLE_AUDIT_SINK_SENTRY"
	EnableAdminConsole      Key = "ENABLE_ADMIN_CONSOLE"
	EnableProviderCustom    Key = "ENABLE_PROVIDER_CUSTOM"

	// Carl.sh sovereign workspace feature gates (issue #238).
	EnableRAG    Key = "ENABLE_RAG"
	EnableVoice  Key = "ENABLE_VOICE"
	EnableRelay  Key = "ENABLE_RELAY"
	EnableCowork Key = "ENABLE_COWORK"
)
