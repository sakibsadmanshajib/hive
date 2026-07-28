-- supabase/migrations/20260728_03_tenant_billing_account_backfill.sql
-- Backend metering, Step 1 (schema and backfill only, no behaviour change).
-- Backfills public.tenant_billing_accounts for tenants that already have an
-- unambiguous billing account, so Step 4 (fail-closed enforcement, a later
-- PR) does not 402/403 every request for a tenant this migration could have
-- mapped safely. See vault spec-2026-07-28-backend-metering.md sections 3.2
-- and 9.2.
--
-- Deliberately NOT the band-aid D-005 rejects (resolving billability from
-- the account whose owner_user_id equals some member): that picks one
-- member arbitrarily and is wrong whenever a tenant has members on more than
-- one account. This backfill only maps a tenant when EVERY active member
-- with an active account membership converges on exactly the same single
-- account. That is a structural fact already present in the data (typically
-- because the tenant and its account were provisioned together), not a
-- guess among several valid candidates. A tenant whose members disagree, or
-- who have no account membership at all, is left unmapped by this
-- migration and must be resolved by a human decision, not a heuristic.
--
-- ENTERPRISE_EDGE tenants are scoped out entirely: per spec section 4 rule
-- 2, an Enterprise tenant has no prepaid relationship with Hive and the
-- deployment-posture rule short-circuits before a billing account is ever
-- consulted, so leaving them unmapped is correct, not an oversight.

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
  -- Defensive: skip a candidate account that some earlier row in this same
  -- backfill already claimed, so a coincidental cross-tenant match cannot
  -- violate tenant_billing_accounts' UNIQUE(account_id) and abort the whole
  -- migration. A skip here surfaces as an unmapped tenant, not a fudge.
  AND NOT EXISTS (
    SELECT 1 FROM public.tenant_billing_accounts existing
    WHERE existing.account_id = candidate.account_id
  )
ON CONFLICT (tenant_id) DO NOTHING;

COMMIT;
