-- supabase/migrations/20260731_01_tenant_billing_account_backfill_v2.sql
-- Backend metering / D-030 prerequisite follow-up. Reruns the
-- tenant_billing_accounts sweep this table's mechanisms have needed since
-- 20260728_03_tenant_billing_account_backfill.sql's one-time run, not
-- touching that file (an applied migration is inert on a live database, see
-- hard-lessons).
--
-- This migration was originally split into two files (this one, Cloud-only,
-- reusing the pre-guard predicate; a second one extending to all
-- deployments with the convergence guard below). Collapsed into one per
-- review: keeping two files with two different predicates guaranteed the
-- exact divergence an independent re-review caught live — the first file
-- could map a tenant wrong before the second, guarded file ever ran. One
-- migration, one predicate, makes that divergence impossible rather than
-- merely fixed. If a future need arises to run only a Cloud subset again,
-- add the deployment filter back as a WHERE clause in a NEW file; do not
-- reintroduce a second, differently-guarded copy of this query.
--
-- Three changes over 20260728_03, all reasons documented below:
--
-- 1. NEVER REAPPLIED. New unambiguous tenants created after 2026-07-28
--    14:43 UTC (the one and only time 20260728_03's predicate ran against
--    live data) were simply never swept. This migration is safely
--    rerunnable (see the idempotency guards below) specifically so it can
--    be applied again whenever the accumulated gap needs closing.
--
-- 2. UNRESOLVED-MEMBER CONVERGENCE GUARD (CodeRabbit review on PR #624,
--    reproduced independently before being fixed). The prior candidate
--    query only counted DISTINCT resolved account_ids among a tenant's
--    ACTIVE members; an ACTIVE tenant_users row whose account_memberships
--    hadn't landed yet was invisible to that count, not absent from the
--    tenant. For a single-member tenant this self-corrects (0 resolved
--    reads as "not mapped yet"). For a tenant with two or more members
--    where only some have resolved, the unresolved ones were silently
--    ignored, so the tenant could be mapped to whichever member resolved
--    first even though a still-unresolved member might turn out to hold a
--    DIFFERENT account. Once mapped, nothing revisits it (this migration
--    and signup.EnsureTenantBillingAccount both check tenant_id already
--    mapped before doing anything else), so a premature mapping from this
--    gap is permanent. Fixed by requiring every ACTIVE member to have
--    resolved an ACTIVE account membership (unresolved_members = 0) before
--    a tenant is considered at all.
--
-- 3. ALL DEPLOYMENTS, NOT JUST HIVE_CLOUD. PR #620 (fail-closed API-key
--    tenant resolution) briefly carried its own Enterprise fallback (a
--    separate query resolving directly against tenants); that fallback was
--    reverted after independent security review because it let an unmapped
--    Cloud account be assigned an Enterprise tenant's entitlements whenever
--    the database happened to hold exactly one non-Cloud tenant row. #620
--    is back to a single resolution path: read tenant_billing_accounts,
--    fail closed if absent, no posture branch in the auth path. That means
--    an Enterprise (ENTERPRISE_EDGE) tenant needs a real row here or every
--    one of its API-key calls 403s on #620's merge.
--
--    Design choice, reasoned from the code: reuse this table rather than
--    add a second tenant-to-account identity table. The invariant is
--    identical in shape for both deployment types (one tenant, one
--    account, enforced by the same UNIQUE(account_id) and the same
--    convergence predicate) — only the label differs, "funds" for Cloud,
--    "identifies" for Enterprise. #620's GetTenantIDByAccountID already
--    reads this table and needs zero changes; a second table would make
--    that function branch or UNION across two sources, adding logic to the
--    exact auth path #620 was written to keep branch-free.
--
--    Cost of that choice, stated plainly: this table now serves tenant
--    IDENTITY RESOLUTION for Enterprise as well as BILLING for Cloud. Its
--    name and 20260728_01's stated intent (spec-2026-07-28-backend-metering.md
--    section 3.2) describe the billing half only. A tenant_billing_accounts
--    row for an ENTERPRISE_EDGE tenant carries no prepaid-billing meaning;
--    check the row's tenant's deployment column before assuming a row
--    implies a credit balance.
--
-- IDEMPOTENCY AND CROSS-TENANT COLLISION SAFETY (fixed here, per review).
-- The single-tenant idempotency guard (ON CONFLICT (tenant_id) DO NOTHING,
-- plus excluding any candidate account already claimed by an EXISTING
-- tenant_billing_accounts row) protects a rerun of this migration against
-- redoing prior work, but it does NOT protect a single run against two
-- DIFFERENT tenants in the SAME batch resolving to the SAME account_id
-- (plausible: one user is an active member of two different tenants
-- through the same account). Postgres evaluates an INSERT ... SELECT's
-- WHERE NOT EXISTS subquery against the table's pre-statement snapshot,
-- not incrementally as the statement's own rows are inserted, so two
-- same-batch candidates sharing an account_id both pass that check and the
-- second one to insert aborts the whole statement — and with it, per
-- ON_ERROR_STOP, the rest of the migration chain. That is a deploy-blocking
-- failure mode (the same shape that blocked both Go test suites when
-- pg_cron was unavailable earlier), not acceptable inherited baggage. An
-- earlier version of this comment claimed the NOT EXISTS guard alone made
-- a cross-tenant collision impossible; that claim was false, which is why
-- it has been replaced with this one.
--
-- Fixed by counting, for each otherwise-eligible candidate, how many
-- tenants in this same batch want the same account_id (a window function
-- partitioned by account_id) and excluding every contender when more than
-- one tenant wants the same account, rather than letting the insert fail.
-- A contested account is exactly the ambiguity this mechanism refuses to
-- guess at, so leaving all contenders unmapped is the same fail-closed
-- posture as every other exclusion here, not a new behavior. The RAISE
-- NOTICE below makes a skip observable instead of silent, echoing the
-- account_already_claimed_by_another_tenant reason
-- signup.EnsureTenantBillingAccount already uses for the single-tenant
-- version of this same situation.

BEGIN;

DO $$
DECLARE
  contested record;
BEGIN
  FOR contested IN
    WITH candidate AS (
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
      -- No deployment filter: every deployment is swept, per point 3 above.
      GROUP BY t.id
    ),
    eligible AS (
      SELECT tenant_id, account_id,
             count(*) OVER (PARTITION BY account_id) AS contenders
      FROM candidate
      WHERE distinct_accounts = 1
        AND unresolved_members = 0
        AND NOT EXISTS (
          SELECT 1 FROM public.tenant_billing_accounts existing
          WHERE existing.account_id = candidate.account_id
        )
    )
    SELECT account_id, array_agg(tenant_id) AS tenant_ids
    FROM eligible
    WHERE contenders > 1
    GROUP BY account_id
  LOOP
    RAISE NOTICE 'tenant billing account not mapped account=% reason=account_already_claimed_by_another_tenant contending_tenants=%',
      contested.account_id, contested.tenant_ids;
  END LOOP;
END $$;

WITH candidate AS (
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
  GROUP BY t.id
),
eligible AS (
  SELECT tenant_id, account_id,
         count(*) OVER (PARTITION BY account_id) AS contenders
  FROM candidate
  WHERE distinct_accounts = 1
    AND unresolved_members = 0
    AND NOT EXISTS (
      SELECT 1 FROM public.tenant_billing_accounts existing
      WHERE existing.account_id = candidate.account_id
    )
)
INSERT INTO public.tenant_billing_accounts (tenant_id, account_id)
SELECT tenant_id, account_id
FROM eligible
WHERE contenders = 1
ON CONFLICT (tenant_id) DO NOTHING;

COMMIT;
