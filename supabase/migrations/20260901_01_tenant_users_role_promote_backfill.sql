-- supabase/migrations/20260901_01_tenant_users_role_promote_backfill.sql
--
-- The promotion half of the reconciliation 20260828_02 deliberately left
-- undone (issues #1244, #1245, #1646).
--
-- public.tenant_users.role was write once until PR #1287: every signup path
-- inserts 'MEMBER' (apps/control-plane/internal/signup/reconcile.go's
-- provision), and nothing updated it afterwards. public.account_memberships
-- is the table the product actually keeps current, because
-- accounts.Service.UpdateMemberRole writes it on every Members page role
-- change. platform.WorkspaceAdminGate authorizes off the first table while
-- the console's own page gate reads the second, so a workspace owner
-- promoted before PR #1287 shipped renders the Feature gates and Marketplace
-- pages and is then answered 403 by the data fetch behind them, landing on
-- the "Managed by your administrator" empty state (issue #1646, measured
-- live on 2026-09-01).
--
-- 20260828_02 corrected the demotion direction only: a tenant_users 'OWNER'
-- for a user account_memberships no longer considers an active owner is a
-- stale grant, and removing authority is always safe. It declined the
-- promotion direction because it could not tell a genuine multi member
-- business tenant apart from a personal (single user) tenant, whose sole
-- member insertPersonalMembership hardcodes to 'MEMBER' on purpose so a self
-- serve signup never reaches those same admin surfaces.
--
-- That discriminator exists now and is already load bearing in code:
-- public.tenants.personal_owner_user_id (20260801_10), which
-- signup.SyncTenantMembershipRole gates its own promotion arm on for exactly
-- this reason. This migration applies that function's promotion rule to the
-- rows that predate it, and nothing else. It is the same rule, in the same
-- direction, on the same predicates:
--
--   * public.tenants.personal_owner_user_id IS NULL, so a personal tenant is
--     never promoted. Live count of rows this skips is reported below rather
--     than silently dropped: they are the deliberate design, not drift, and
--     whether a personal tenant owner should administer their own workspace
--     is a product decision, not a data correctness fix.
--   * tu.status = 'ACTIVE', so a SUSPENDED or INVITED row is never stamped
--     OWNER. Suspension is how a tenant removes authority without deleting
--     the row, and reactivation must not silently restore an owner.
--   * an ACTIVE public.account_memberships row with role 'owner' on the
--     account public.tenant_billing_accounts maps this tenant to. That
--     mapping is unique on BOTH columns (20260728_01), so exactly one
--     account funds exactly one tenant and "owner of the mapped account"
--     cannot mean "owner of somebody else's workspace".
--
-- Narrower than the function in one respect, deliberately. The function
-- promotes any non OWNER role whose account membership says owner; this
-- migration promotes only tu.role = 'MEMBER'. public.tenant_users.role has a
-- four value domain (OWNER, ADMIN, MEMBER, VIEWER) while
-- public.account_memberships.role has two, and 'MEMBER' is the value every
-- signup path writes, so it is the only value that is evidence of "never
-- updated" rather than of a tier somebody chose. An ADMIN or VIEWER row is
-- reported below and left alone; a one time backfill is the wrong place to
-- widen a role nobody can now ask about, and the deploy time check added
-- with this migration (scripts/check-tenant-role-divergence.sh) fails loudly
-- if one ever appears, rather than leaving it to be discovered by a customer.
--
-- Safely re runnable: every predicate below is a condition the UPDATE itself
-- clears, so a second run promotes nothing and reports zero.
--
-- Depends on: 20260516_03_phase19_tenant_users.sql,
-- 20260728_01_tenant_billing_account.sql, 20260801_10_tenants_personal_owner.sql.

DO $$
DECLARE
  promoted_count    int;
  personal_skipped  int;
  other_tier_left   int;
BEGIN
  WITH promote AS (
    UPDATE public.tenant_users tu
       SET role = 'OWNER'
      FROM public.tenant_billing_accounts tba
      JOIN public.tenants t
        ON t.id = tba.tenant_id
     WHERE tu.tenant_id = tba.tenant_id
       AND tu.status = 'ACTIVE'
       AND tu.role = 'MEMBER'
       AND t.personal_owner_user_id IS NULL
       AND EXISTS (
             SELECT 1 FROM public.account_memberships am
              WHERE am.account_id = tba.account_id
                AND am.user_id    = tu.user_id
                AND am.role       = 'owner'
                AND am.status     = 'active'
           )
     RETURNING 1
  )
  SELECT count(*) INTO promoted_count FROM promote;

  SELECT count(*) INTO personal_skipped
    FROM public.tenant_users tu
    JOIN public.tenant_billing_accounts tba ON tba.tenant_id = tu.tenant_id
    JOIN public.tenants t ON t.id = tba.tenant_id
    JOIN public.account_memberships am
      ON am.account_id = tba.account_id
     AND am.user_id    = tu.user_id
     AND am.role       = 'owner'
     AND am.status     = 'active'
   WHERE tu.status = 'ACTIVE'
     AND tu.role <> 'OWNER'
     AND t.personal_owner_user_id IS NOT NULL;

  SELECT count(*) INTO other_tier_left
    FROM public.tenant_users tu
    JOIN public.tenant_billing_accounts tba ON tba.tenant_id = tu.tenant_id
    JOIN public.tenants t ON t.id = tba.tenant_id
    JOIN public.account_memberships am
      ON am.account_id = tba.account_id
     AND am.user_id    = tu.user_id
     AND am.role       = 'owner'
     AND am.status     = 'active'
   WHERE tu.status = 'ACTIVE'
     AND tu.role NOT IN ('OWNER', 'MEMBER')
     AND t.personal_owner_user_id IS NULL;

  RAISE NOTICE 'tenant_users role promotion backfill (issue #1646): promoted % row(s) to OWNER on business tenants; skipped % personal tenant row(s) by design (see this file''s header); left % row(s) whose tenant_users role is a tier this migration will not widen (ADMIN or VIEWER) -- scripts/check-tenant-role-divergence.sh reports those on every deploy', promoted_count, personal_skipped, other_tier_left;
END $$;
