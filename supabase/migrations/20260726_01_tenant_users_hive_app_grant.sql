-- supabase/migrations/20260726_01_tenant_users_hive_app_grant.sql
--
-- Let the control-plane read tenant-scoped roles under the production DB role.
--
-- public.tenant_users was created for the PostgREST path only: 20260516_03_
-- phase19_tenant_users.sql grants SELECT/INSERT/UPDATE/DELETE TO authenticated
-- and 20260518_04_phase19_audit_rls_and_indexes.sql adds RLS policies keyed on
-- auth.jwt(). Nothing ever granted hive_app anything on it, and every policy on
-- it is unqualified (so PUBLIC, which includes hive_app) and evaluates
-- auth.jwt(), which is NULL outside a PostgREST request. hive_app is NOT
-- BYPASSRLS, so a control-plane query against this table permission-fails or
-- returns zero rows in production.
--
-- That went unnoticed because every other Phase-19+ table the control-plane
-- reads got an explicit hive_app grant plus an app.current_tenant_id policy
-- (public.egress_policies in 20260715_03_egress_policy_ssot.sql,
-- public.marketplace_tenant_entries in 20260716_01_marketplace_catalog.sql,
-- public.agent_tasks in 20260716_03_agent_tasks.sql), and because a superuser
-- connection string masks the gap entirely.
--
-- platform.TenantRoleService.IsTenantOwner is the first control-plane reader of
-- this table, and it gates the /api/v1/egress-policy/ admin surface. Without
-- this migration that surface answers 403 or 500 for every legitimate tenant
-- owner in production while passing on a superuser dev connection.
--
-- Depends on: 20260516_03_phase19_tenant_users.sql (public.tenant_users)

BEGIN;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'tenant_users'
          AND policyname = 'tenant_users_hive_app_tenant_isolation'
    ) THEN
        -- PERMISSIVE, so this ORs with the existing auth.jwt()-keyed policies
        -- rather than replacing them: the PostgREST path keeps working
        -- unchanged and hive_app gains its own scoped read.
        --
        -- SELECT only. The control-plane reads roles here; membership writes
        -- stay with the PostgREST/service-role path that owns them today.
        --
        -- NULLIF(..., '') guards the Postgres GUC placeholder quirk documented
        -- at length in 20260715_03_egress_policy_ssot.sql: once a session has
        -- called set_config('app.current_tenant_id', ..., true), later
        -- current_setting(name, true) calls on that pooled connection return ''
        -- rather than NULL, and ''::uuid raises invalid input syntax instead of
        -- filtering. Folding '' to NULL first makes the comparison NULL, which
        -- Postgres treats as false, so an unscoped query fails closed with zero
        -- rows instead of erroring.
        CREATE POLICY tenant_users_hive_app_tenant_isolation
            ON public.tenant_users AS PERMISSIVE FOR SELECT TO hive_app
            USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
    END IF;
END$$;

GRANT SELECT ON public.tenant_users TO hive_app;

COMMIT;
