-- supabase/migrations/20260516_01_phase19_tenants.sql
-- Phase 19 — tenants table. Root of every tenant-scoped relation.

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS public.tenants (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  slug        text UNIQUE NOT NULL,
  name        text NOT NULL,
  deployment  text NOT NULL CHECK (deployment IN ('HIVE_CLOUD','ENTERPRISE_EDGE')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  archived_at timestamptz
);

CREATE INDEX IF NOT EXISTS tenants_deployment_idx ON public.tenants(deployment);
CREATE INDEX IF NOT EXISTS tenants_active_idx ON public.tenants(id) WHERE archived_at IS NULL;

ALTER TABLE public.tenants ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenants_self_read ON public.tenants
  FOR SELECT
  TO authenticated
  USING (id = (auth.jwt() ->> 'tenant_id')::uuid);

GRANT SELECT ON public.tenants TO authenticated;

COMMIT;
