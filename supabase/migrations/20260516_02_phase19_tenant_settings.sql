-- supabase/migrations/20260516_02_phase19_tenant_settings.sql
-- Phase 19 — central enum of every gateable feature + per-tenant settings.

BEGIN;

CREATE TYPE public.tenant_setting_key AS ENUM (
  'ENABLE_PUBLIC_BILLING',
  'ENABLE_BKASH',
  'ENABLE_SSLCOMMERZ',
  'ENABLE_STRIPE',
  'ENABLE_CREDIT_POOL',
  'ENABLE_PER_USER_CAP',
  'ENABLE_EXTRA_USAGE',
  'ENABLE_RAG_PERSONAL',
  'ENABLE_RAG_SHARED_KB',
  'ENABLE_MULTI_TENANT',
  'ENABLE_SSO_GOOGLE',
  'ENABLE_SSO_MICROSOFT',
  'ENABLE_SSO_SAML',
  'ENABLE_AUDIT_SINK_ELK',
  'ENABLE_AUDIT_SINK_LOKI',
  'ENABLE_AUDIT_SINK_DATADOG',
  'ENABLE_AUDIT_SINK_SPLUNK',
  'ENABLE_AUDIT_SINK_LANGFUSE',
  'ENABLE_ADMIN_CONSOLE',
  'ENABLE_PROVIDER_CUSTOM'
);

CREATE TABLE public.tenant_settings (
  tenant_id   uuid NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
  key         public.tenant_setting_key NOT NULL,
  enabled     boolean NOT NULL,
  value_json  jsonb,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  uuid REFERENCES auth.users(id),
  PRIMARY KEY (tenant_id, key)
);

CREATE INDEX tenant_settings_key_enabled_idx
  ON public.tenant_settings(key) WHERE enabled = true;

ALTER TABLE public.tenant_settings ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_settings_isolation ON public.tenant_settings
  FOR ALL
  TO authenticated
  USING (tenant_id = (auth.jwt() ->> 'tenant_id')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON public.tenant_settings TO authenticated;

-- Notify channel for in-process cache invalidation.
CREATE OR REPLACE FUNCTION public.notify_tenant_settings_changed()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('tenant_settings_changed',
    json_build_object('tenant_id', NEW.tenant_id, 'key', NEW.key)::text);
  RETURN NEW;
END;
$$;

CREATE TRIGGER tenant_settings_notify
AFTER INSERT OR UPDATE OR DELETE ON public.tenant_settings
FOR EACH ROW EXECUTE FUNCTION public.notify_tenant_settings_changed();

COMMIT;
