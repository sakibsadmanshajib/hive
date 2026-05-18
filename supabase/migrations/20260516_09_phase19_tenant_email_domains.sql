-- supabase/migrations/20260516_09_phase19_tenant_email_domains.sql
-- Phase 19 — EnterpriseEdge default tenant-domain auto-assignment.

BEGIN;

CREATE TABLE public.tenant_email_domains (
  domain     text PRIMARY KEY,
  tenant_id  uuid NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
  added_by   uuid REFERENCES auth.users(id),
  added_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX tenant_email_domains_tenant_idx
  ON public.tenant_email_domains(tenant_id);

ALTER TABLE public.tenant_email_domains ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_email_domains_isolation ON public.tenant_email_domains
  FOR ALL TO authenticated
  USING (tenant_id = (auth.jwt() ->> 'tenant_id')::uuid);

GRANT SELECT, INSERT, DELETE ON public.tenant_email_domains TO authenticated;
GRANT SELECT ON public.tenant_email_domains TO hive_app;

COMMIT;
