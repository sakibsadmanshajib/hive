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
--
-- SCOPE, stated plainly so nobody reads this migration as making the sync work
-- under hive_app. It does not, and is not trying to. SyncTenantMembershipRole
-- reads two more tables and writes a third, and hive_app has:
--   * public.tenants          -- no grant at all (20260516_01 grants SELECT to
--                                authenticated only), and the sync joins it for
--                                personal_owner_user_id.
--   * public.tenant_users     -- SELECT only, by explicit decision
--                                (20260726_01_tenant_users_hive_app_grant.sql:
--                                "membership writes stay with the
--                                PostgREST/service-role path"), and the only
--                                hive_app policy on it is FOR SELECT.
-- So a control-plane connected as hive_app fails this sync with permission
-- denied, which pool.Exec returns as an error and the call site logs as
-- "tenant_users role sync failed". That is the loud failure, and it is the
-- intended outcome: the whole tenant_users write path, this sync and signup's
-- own INSERT alike, is bypass-RLS-only today, exactly as
-- 20260801_10_tenants_personal_owner.sql already recorded ("the live
-- control-plane DSN authenticates as a role with rolbypassrls, which is also
-- why the existing public.tenant_users writes in signup work with only a
-- SELECT grant to hive_app"). This migration closes the READ half only,
-- because a missing SELECT grant on tenant_billing_accounts would have been
-- the first thing to fail and it costs nothing to fix.
--
-- DO NOT close the write half with a bare GRANT UPDATE ON public.tenant_users
-- TO hive_app. The existing hive_app policy on that table is keyed on
-- NULLIF(current_setting('app.current_tenant_id', true), '')::uuid, and the
-- accounts path never sets that GUC (only the agenttask and usermemories
-- repositories do, inside their own transactions). An UPDATE policy copied
-- from the SELECT one would match zero rows, the sync would report
-- reason=no_match, and the call site treats no_match as benign. That trades a
-- loud permission error for a silent no-op, which is strictly worse. Granting
-- write access here is a deliberate posture change that has to arrive with a
-- policy this call path can actually satisfy, and with the promotion guards in
-- tenant_role_sync.go still enforced. The hive_app-connected assertions in
-- apps/control-plane/internal/accounts/service_tenant_role_sync_test.go pin
-- both halves of the state above, so that change cannot land silently.

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
