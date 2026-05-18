-- supabase/migrations/20260516_08_phase19_tenant_invites.sql
-- Phase 19 — invite tokens consumed during OAuth state callback.

BEGIN;

CREATE TABLE public.tenant_invites (
  token       text PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
  role        text NOT NULL CHECK (role IN ('OWNER','ADMIN','MEMBER','VIEWER')) DEFAULT 'MEMBER',
  email_hint  text,
  created_by  uuid REFERENCES auth.users(id),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  consumed_at timestamptz,
  consumed_by uuid REFERENCES auth.users(id)
);

CREATE INDEX tenant_invites_tenant_idx
  ON public.tenant_invites(tenant_id);

CREATE INDEX tenant_invites_active_idx
  ON public.tenant_invites(tenant_id)
  WHERE consumed_at IS NULL;

ALTER TABLE public.tenant_invites ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_invites_isolation ON public.tenant_invites
  FOR ALL TO authenticated
  USING (tenant_id = (auth.jwt() ->> 'tenant_id')::uuid);

GRANT SELECT, INSERT, UPDATE ON public.tenant_invites TO authenticated;
GRANT SELECT ON public.tenant_invites TO hive_app;

COMMIT;
