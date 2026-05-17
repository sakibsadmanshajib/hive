-- supabase/migrations/20260516_03_phase19_tenant_users.sql
-- Phase 19 — user-to-tenant membership with role and status.

BEGIN;

CREATE TABLE public.tenant_users (
  tenant_id   uuid NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
  user_id     uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  role        text NOT NULL CHECK (role IN ('OWNER','ADMIN','MEMBER','VIEWER')),
  status      text NOT NULL CHECK (status IN ('ACTIVE','SUSPENDED','INVITED')),
  invited_by  uuid REFERENCES auth.users(id),
  joined_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id)
);

CREATE INDEX tenant_users_user_idx ON public.tenant_users(user_id);
CREATE INDEX tenant_users_status_idx ON public.tenant_users(tenant_id, status);

ALTER TABLE public.tenant_users ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_users_isolation ON public.tenant_users
  FOR ALL
  TO authenticated
  USING (tenant_id = (auth.jwt() ->> 'tenant_id')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON public.tenant_users TO authenticated;

COMMIT;
