-- Featuregate data-model rework (issue #293).
--
-- Before this migration, apps/control-plane/internal/featuregate/handler.go
-- hardcoded which five tenant_setting_key values it exposed to edge-api, so
-- every new gate cost a matching edit in both control-plane and edge-api.
-- public.feature_gate_keys is now the single authoritative registry of every
-- key featuregate exposes: the Go handler joins tenant_settings against this
-- table and returns whatever rows exist as a dynamic key->bool map, so it
-- never needs a code change again. Adding a new gate key from here on is a
-- migration-only change: one INSERT here, plus the usual
-- ALTER TYPE public.tenant_setting_key ADD VALUE IF NOT EXISTS for a
-- genuinely new key.
--
-- This also gives a future per-deployment-mode default layer (owner
-- constraint, agent-subsystem blueprint Step 1.1) a place to attach without
-- altering this table or tenant_settings: a later migration can add a
-- separate deployment_mode_gate_defaults(mode, key, enabled) table that
-- foreign-keys to feature_gate_keys.key, with no schema change here.
--
-- No DOWN migration: removing a row would silently stop exposing a gate key
-- that a tenant may still have an explicit tenant_settings row for.

BEGIN;

CREATE TABLE public.feature_gate_keys (
  key         public.tenant_setting_key PRIMARY KEY,
  label       text NOT NULL,
  category    text NOT NULL DEFAULT 'feature',
  created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE public.feature_gate_keys IS
  'Registry of every tenant_setting_key exposed through featuregate. Row presence, not the tenant_setting_key enum alone, controls what apps/control-plane/internal/featuregate and apps/edge-api/internal/featuregate return; neither package hardcodes a key list.';

-- Every key already live in tenant_setting_key as of this migration.
INSERT INTO public.feature_gate_keys (key, label, category) VALUES
  ('ENABLE_PUBLIC_BILLING',      'Public billing',              'billing'),
  ('ENABLE_BKASH',               'bKash payment rail',          'billing'),
  ('ENABLE_SSLCOMMERZ',          'SSLCommerz payment rail',     'billing'),
  ('ENABLE_STRIPE',              'Stripe payment rail',         'billing'),
  ('ENABLE_CREDIT_POOL',         'Shared credit pool',          'billing'),
  ('ENABLE_PER_USER_CAP',        'Per-user spend cap',          'billing'),
  ('ENABLE_EXTRA_USAGE',         'Extra usage beyond plan',     'billing'),
  ('ENABLE_RAG_PERSONAL',        'Personal RAG workspace',      'rag'),
  ('ENABLE_RAG_SHARED_KB',       'Shared knowledge base RAG',   'rag'),
  ('ENABLE_MULTI_TENANT',        'Multi-tenant mode',           'admin'),
  ('ENABLE_SSO_GOOGLE',          'Google OIDC SSO',             'sso'),
  ('ENABLE_SSO_MICROSOFT',       'Microsoft OIDC SSO',          'sso'),
  ('ENABLE_SSO_SAML',            'SAML 2.0 SSO',                'sso'),
  ('ENABLE_AUDIT_SINK_ELK',      'Audit sink: ELK',             'audit_sink'),
  ('ENABLE_AUDIT_SINK_LOKI',     'Audit sink: Loki',            'audit_sink'),
  ('ENABLE_AUDIT_SINK_DATADOG',  'Audit sink: Datadog',         'audit_sink'),
  ('ENABLE_AUDIT_SINK_SPLUNK',   'Audit sink: Splunk',          'audit_sink'),
  ('ENABLE_AUDIT_SINK_LANGFUSE', 'Audit sink: Langfuse',        'audit_sink'),
  ('ENABLE_AUDIT_SINK_SENTRY',   'Audit sink: Sentry',          'audit_sink'),
  ('ENABLE_ADMIN_CONSOLE',       'Admin console access',        'admin'),
  ('ENABLE_PROVIDER_CUSTOM',     'Custom provider endpoints',   'admin'),
  ('ENABLE_RAG',                 'Carl.sh RAG capability',      'carl'),
  ('ENABLE_VOICE',               'Carl.sh voice capability',    'carl'),
  ('ENABLE_RELAY',               'Carl.sh relay capability',    'carl'),
  ('ENABLE_COWORK',              'Carl.sh cowork capability',   'carl')
ON CONFLICT (key) DO NOTHING;

-- No GRANT to authenticated. Nothing in this PR reads this table as that
-- role: apps/control-plane/internal/tenant/settings.Resolver.AllEnabled
-- queries it via the service-role pool (bypass-RLS, same posture as
-- tenant_settings), and edge-api never queries Postgres directly, it
-- consumes the resolved Gates map over the wire. Every capability-key label
-- (billing, admin, audit-sink names) would otherwise be readable by any
-- authenticated user of any tenant, wrong default for a data-sovereign
-- product. The Step 1.2 admin gate UI reads this list through a control-plane
-- endpoint, not a direct table grant; add a narrower admin-only grant or RLS
-- policy then, if that endpoint ever needs to run as the authenticated role
-- instead of service role.

COMMIT;
