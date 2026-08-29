-- supabase/migrations/20260829_03_tenant_users_role_escalation_guard.sql
--
-- Issue #790: public.tenant_users RLS never constrained the role column on a
-- write, so a tenant ADMIN could PATCH their own row (or any row in the same
-- tenant) through PostgREST, setting role: "OWNER", and become
-- indistinguishable from the workspace owner. Confirmed live against a
-- throwaway Postgres before this migration: PATCH /rest/v1/tenant_users as an
-- authenticated ADMIN, body {"role":"OWNER"}, succeeded and returned the
-- updated row.
--
-- WorkspaceAdminGate (apps/control-plane/internal/platform/workspace_admin_gate.go)
-- already establishes the product's own model at the application layer: OWNER
-- is strictly stronger than ADMIN, and feature-gate/marketplace admin
-- surfaces gate on OWNER specifically, not on OWNER-or-ADMIN. This migration
-- makes the database agree: a row can only end up with role = 'OWNER' when
-- the caller's own JWT role claim is already 'OWNER'. That closes both the
-- literal self-promotion case and the general case (an ADMIN promoting a
-- third party to OWNER), on both INSERT (adding a member as OWNER) and
-- UPDATE (promoting an existing member).
--
-- Two independently-named policy pairs exist on this table today, both
-- unrestricted on role, because 20260518_04_phase19_audit_rls_and_indexes.sql
-- (the M5 SELECT-wrap of auth.jwt()) DROP POLICY IF EXISTS'd the SINGULAR
-- name (tenant_users_insert_admin) before creating it, while the ORIGINAL
-- policy from 20260516_03_phase19_tenant_users.sql is named with the PLURAL
-- (tenant_users_insert_admins). The DROP therefore never matched anything and
-- both policies have coexisted since, OR'd together by Postgres's permissive-
-- policy combination rule. A restriction added to only one of the two would
-- have been silently defeated by the other still allowing the write. This
-- migration drops BOTH names for INSERT and UPDATE and creates one canonical
-- policy of each, keeping the SELECT-wrapped auth.jwt() form (cheaper per
-- statement, not per row) and adding the role guard.
--
-- DELETE is untouched: it carries no WITH CHECK and never writes role, so it
-- was never part of this escalation path. SELECT is untouched: both existing
-- SELECT policies (tenant_users_select_isolation, tenant_users_select_own)
-- already carry an identical, correct tenant_id predicate; that duplication
-- is real but read-only and out of scope for this fix.
--
-- Depends on: 20260516_03_phase19_tenant_users.sql,
-- 20260518_04_phase19_audit_rls_and_indexes.sql.

BEGIN;

DROP POLICY IF EXISTS tenant_users_insert_admins ON public.tenant_users;
DROP POLICY IF EXISTS tenant_users_insert_admin ON public.tenant_users;
CREATE POLICY tenant_users_insert_admin
  ON public.tenant_users
  FOR INSERT
  TO authenticated
  WITH CHECK (
    tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid
    AND ((SELECT auth.jwt()) ->> 'role') IN ('OWNER', 'ADMIN')
    AND (role <> 'OWNER' OR ((SELECT auth.jwt()) ->> 'role') = 'OWNER')
  );

DROP POLICY IF EXISTS tenant_users_update_admins ON public.tenant_users;
DROP POLICY IF EXISTS tenant_users_update_admin ON public.tenant_users;
CREATE POLICY tenant_users_update_admin
  ON public.tenant_users
  FOR UPDATE
  TO authenticated
  USING (
    tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid
    AND ((SELECT auth.jwt()) ->> 'role') IN ('OWNER', 'ADMIN')
  )
  WITH CHECK (
    tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid
    AND ((SELECT auth.jwt()) ->> 'role') IN ('OWNER', 'ADMIN')
    AND (role <> 'OWNER' OR ((SELECT auth.jwt()) ->> 'role') = 'OWNER')
  );

COMMIT;
