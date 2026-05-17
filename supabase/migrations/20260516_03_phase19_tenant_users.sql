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

-- SELECT: any active member of the tenant may read membership rows for
-- that tenant. Cross-tenant reads are blocked by the tenant_id check.
CREATE POLICY tenant_users_select_isolation ON public.tenant_users
  FOR SELECT
  TO authenticated
  USING (tenant_id = (auth.jwt() ->> 'tenant_id')::uuid);

-- INSERT / UPDATE / DELETE: restricted to OWNER and ADMIN of the same
-- tenant. The role claim is injected by the custom_access_token_hook
-- and matches the actor's role in the *currently selected* tenant.
-- Without this, any authenticated member could escalate privilege by
-- modifying another member's role or status.
CREATE POLICY tenant_users_insert_admins ON public.tenant_users
  FOR INSERT
  TO authenticated
  WITH CHECK (
    tenant_id = (auth.jwt() ->> 'tenant_id')::uuid
    AND (auth.jwt() ->> 'role') IN ('OWNER','ADMIN')
  );

CREATE POLICY tenant_users_update_admins ON public.tenant_users
  FOR UPDATE
  TO authenticated
  USING (
    tenant_id = (auth.jwt() ->> 'tenant_id')::uuid
    AND (auth.jwt() ->> 'role') IN ('OWNER','ADMIN')
  )
  WITH CHECK (
    tenant_id = (auth.jwt() ->> 'tenant_id')::uuid
    AND (auth.jwt() ->> 'role') IN ('OWNER','ADMIN')
  );

CREATE POLICY tenant_users_delete_admins ON public.tenant_users
  FOR DELETE
  TO authenticated
  USING (
    tenant_id = (auth.jwt() ->> 'tenant_id')::uuid
    AND (auth.jwt() ->> 'role') IN ('OWNER','ADMIN')
  );

GRANT SELECT, INSERT, UPDATE, DELETE ON public.tenant_users TO authenticated;

COMMIT;
