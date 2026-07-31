-- supabase/migrations/20260731_02_tenant_billing_account_all_deployments.sql
-- Backend metering / D-030 prerequisite follow-up. Reruns the
-- tenant_billing_accounts sweep with two changes over
-- 20260731_01_tenant_billing_account_backfill_v2.sql, both idempotent and
-- additive, not touching that file (an applied migration is inert on a live
-- database, see hard-lessons).
--
-- 1. UNRESOLVED-MEMBER GUARD (CodeRabbit review on PR #624, verified
--    independently). The prior candidate query only counted DISTINCT
--    resolved account_ids among a tenant's ACTIVE members; an ACTIVE
--    tenant_users row whose account_memberships hasn't landed yet is
--    invisible to that count, not absent from the tenant. For a
--    single-member tenant this self-corrects (0 resolved accounts means
--    "not mapped yet", and the next attempt after that member's account
--    exists reads 1). But for a tenant with two or more active members where
--    only some have resolved, the unresolved ones were silently ignored, so
--    the tenant could be mapped to whichever member resolved first even
--    though a still-unresolved member might turn out to hold a DIFFERENT
--    account. Once mapped, nothing revisits it (both this migration and
--    EnsureTenantBillingAccount check tenant_id already mapped before doing
--    anything else), so a premature mapping from this gap is permanent. This
--    sweep now requires every ACTIVE member to have resolved an ACTIVE
--    account membership before considering a tenant at all.
--
-- 2. ALL DEPLOYMENTS, NOT JUST HIVE_CLOUD. PR #620 (fail-closed API-key
--    tenant resolution) had briefly carried its own Enterprise fallback
--    (a separate query against tenants directly); that fallback was reverted
--    after independent security review because it let an unmapped Cloud
--    account be assigned an Enterprise tenant's entitlements whenever the
--    database happened to hold exactly one non-Cloud tenant row. #620 is now
--    back to a single resolution path: read tenant_billing_accounts, fail
--    closed if absent, no posture branch in the auth path. That means an
--    Enterprise (ENTERPRISE_EDGE) tenant needs a REAL row here or every one
--    of its API-key calls 403s on #620's merge.
--
--    Design choice, reasoned from the code, not asked: reuse this table
--    rather than add a second tenant-to-account identity table.
--      - The invariant is identical in shape for both deployment types: one
--        tenant resolves to exactly one account, enforced by the same
--        UNIQUE(account_id) constraint and the same convergence predicate.
--        Only the label differs — "funds" for Cloud, "identifies" for
--        Enterprise — not the relationship.
--      - #620's GetTenantIDByAccountID already reads this table and needs
--        ZERO changes. A second table would make that function branch or
--        UNION across two sources, adding logic to the exact auth path #620
--        was written to simplify and keep branch-free.
--      - signup.EnsureTenantBillingAccount already never filtered on
--        tenants.deployment (see its doc comment) — the Cloud scoping lived
--        only in the backfill migrations. Both live call sites already
--        attempt this mapping for Enterprise tenants today; only the
--        one-time sweep needed to stop excluding them.
--    Cost of this choice, stated plainly rather than left for the next
--    person to discover: this table now serves tenant IDENTITY RESOLUTION
--    for Enterprise as well as BILLING for Cloud. Its name and its original
--    migration's stated intent (spec-2026-07-28-backend-metering.md section
--    3.2) describe the billing half only. A tenant mapped here via this
--    migration or via EnsureTenantBillingAccount for an ENTERPRISE_EDGE
--    tenant carries no prepaid-billing meaning; it is purely "this is the
--    account whose API key resolves to this tenant". Read the row's tenant's
--    deployment column before assuming a tenant_billing_accounts row implies
--    a credit balance.
--
-- Same idempotent guards as every prior sweep in this family: ON CONFLICT
-- (tenant_id) DO NOTHING, plus NOT EXISTS against an account already claimed
-- by an earlier row in this same run, so a coincidental cross-tenant match
-- cannot violate UNIQUE(account_id) and abort the migration. Safe to apply
-- against a populated table, safe to rerun.

BEGIN;

INSERT INTO public.tenant_billing_accounts (tenant_id, account_id)
SELECT candidate.tenant_id, candidate.account_id
FROM (
  SELECT
    t.id AS tenant_id,
    (array_agg(DISTINCT am.account_id) FILTER (WHERE am.account_id IS NOT NULL))[1] AS account_id,
    count(DISTINCT am.account_id) FILTER (WHERE am.account_id IS NOT NULL) AS distinct_accounts,
    count(*) FILTER (WHERE am.account_id IS NULL) AS unresolved_members
  FROM public.tenants t
  JOIN public.tenant_users tu
    ON tu.tenant_id = t.id
   AND tu.status = 'ACTIVE'
  LEFT JOIN public.account_memberships am
    ON am.user_id = tu.user_id
   AND am.status = 'active'
  -- No deployment filter: every deployment is swept, per point 2 above.
  GROUP BY t.id
) candidate
WHERE candidate.distinct_accounts = 1
  AND candidate.unresolved_members = 0
  AND NOT EXISTS (
    SELECT 1 FROM public.tenant_billing_accounts existing
    WHERE existing.account_id = candidate.account_id
  )
ON CONFLICT (tenant_id) DO NOTHING;

COMMIT;
