-- supabase/migrations/20260828_01_tenant_billing_accounts_hive_app_grant.sql
--
-- Let the control-plane read the tenant<->account billing mapping under the
-- production DB role. Needed by
-- apps/control-plane/internal/signup.SyncTenantMembershipRole (issue #1245):
-- accounts.Service.UpdateMemberRole now does the reverse (account_id ->
-- tenant_id) lookup on this table so a Members-page role change can
-- propagate onto public.tenant_users.
--
-- public.tenant_billing_accounts (20260728_01_tenant_billing_account.sql) was
-- created with RLS enabled and deliberately NO policy and NO grant, on the
-- reasoning that only a BYPASSRLS service-role connection would ever read it.
-- That has held for the forward direction so far (EnsureTenantBillingAccount,
-- called from signup.Provisioner and
-- accounts.Service.provisionDefaultWorkspace), because every deployment that
-- has run it so far happened to connect as the BYPASSRLS `postgres` pooled
-- role. hive_app is NOT BYPASSRLS
-- (20260518_04_phase19_audit_rls_and_indexes.sql), and a deployment that
-- connects control-plane as hive_app -- the hardened posture every other
-- Phase 19+ table grant in this migration history exists for -- would
-- permission-fail on this table exactly the way
-- 20260726_01_tenant_users_hive_app_grant.sql found for tenant_users. Close
-- that gap before a second reader (this one) hits it live.
--
-- SELECT only, control-plane-only, no PostgREST authenticated policy: mirrors
-- the table's own no-policy-for-customers stance, and account_memberships'
-- existing FOR ALL TO hive_app USING(true) pattern
-- (20260529_01_rls_tenant_tables.sql), narrowed to SELECT because nothing
-- here writes this table.

BEGIN;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'tenant_billing_accounts'
          AND policyname = 'tenant_billing_accounts_hive_app_read'
    ) THEN
        CREATE POLICY tenant_billing_accounts_hive_app_read
            ON public.tenant_billing_accounts AS PERMISSIVE FOR SELECT TO hive_app
            USING (true);
    END IF;
END$$;

GRANT SELECT ON public.tenant_billing_accounts TO hive_app;

COMMIT;
