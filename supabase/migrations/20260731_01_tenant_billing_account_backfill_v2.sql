-- supabase/migrations/20260731_01_tenant_billing_account_backfill_v2.sql
-- Backend metering, Step 1 follow-up (schema and backfill only, no behaviour
-- change). Reruns the same sweep as 20260728_03_tenant_billing_account_backfill.sql
-- against everything that has accumulated since that migration's one-time
-- run, for two reasons found while diagnosing why PR #620 (fail-closed
-- API-key tenant resolution) would 403 far more accounts than expected:
--
-- 1. This backfill never reran. New HIVE_CLOUD tenants that became
--    unambiguously mappable after 2026-07-28 14:43 UTC (the one and only time
--    this predicate has ever run against live data) stayed unmapped simply
--    because nothing swept them.
-- 2. The creation-path attempt (signup.EnsureTenantBillingAccount, née
--    ensureTenantBillingAccount) that is supposed to catch new tenants as
--    they are provisioned has its own race: it fires the instant a
--    tenant_users row is written, but the matching account_memberships row
--    is written by an independently timed process (the accounts package's
--    lazy default-workspace provisioning) that can land seconds later.
--    Confirmed live: several same-day single-member, single-account,
--    HIVE_CLOUD tenants were still unmapped despite qualifying. The
--    application-code half of this fix (calling the same mapping attempt
--    again once the account side lands) closes the race for new signups;
--    this migration is the one-time sweep for what already accumulated.
--
-- Identical predicate and safety guards to 20260728_03, restated as its own
-- migration rather than editing that one (an applied migration is inert on a
-- live database — see hard-lessons). Idempotent and safe to apply against a
-- populated table: ON CONFLICT (tenant_id) DO NOTHING plus the NOT EXISTS
-- guard against a candidate account already claimed make a second run of
-- this same file, or of the whole migration chain, a no-op.

BEGIN;

INSERT INTO public.tenant_billing_accounts (tenant_id, account_id)
SELECT candidate.tenant_id, candidate.account_id
FROM (
  SELECT
    t.id AS tenant_id,
    (array_agg(DISTINCT am.account_id) FILTER (WHERE am.account_id IS NOT NULL))[1] AS account_id,
    count(DISTINCT am.account_id) FILTER (WHERE am.account_id IS NOT NULL) AS distinct_accounts
  FROM public.tenants t
  JOIN public.tenant_users tu
    ON tu.tenant_id = t.id
   AND tu.status = 'ACTIVE'
  JOIN public.account_memberships am
    ON am.user_id = tu.user_id
   AND am.status = 'active'
  WHERE t.deployment = 'HIVE_CLOUD'
  GROUP BY t.id
) candidate
WHERE candidate.distinct_accounts = 1
  AND NOT EXISTS (
    SELECT 1 FROM public.tenant_billing_accounts existing
    WHERE existing.account_id = candidate.account_id
  )
ON CONFLICT (tenant_id) DO NOTHING;

COMMIT;
