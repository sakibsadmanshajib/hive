-- supabase/migrations/20260828_02_tenant_users_role_reconcile_backfill.sql
--
-- One-time reconciliation for issue #1245: public.tenant_users.role was
-- write-once until this PR (see 20260828_01 and
-- apps/control-plane/internal/signup/tenant_role_sync.go), so a tenant_users
-- row that still says 'OWNER' for a user public.account_memberships no
-- longer considers an ACTIVE owner of the mapped account is exactly the live
-- exposure #1245 describes: a demoted owner keeping backend authority
-- forever. accounts.Service.UpdateMemberRole now keeps the two in sync going
-- forward; this migration corrects rows that drifted before that code
-- shipped.
--
-- Demotion direction only. Promotion (a tenant_users row still 'MEMBER' for a
-- user who IS the mapped account's ACTIVE owner -- issue #1244's milder,
-- no-data-exposure case) is deliberately NOT auto-corrected here. This
-- migration cannot tell apart two situations that look identical in the
-- data: a genuine multi-member business tenant whose co-owner promotion
-- predates the sync this PR adds (safe, and arguably overdue, to promote),
-- versus a personal (single-member) tenant, which
-- apps/control-plane/internal/signup/personal_tenant.go's
-- insertPersonalMembership hardcodes to 'MEMBER' ON PURPOSE so that user
-- never reaches WorkspaceAdminGate's feature-gate/marketplace admin
-- surfaces, even though they own their own account. Auto-promoting would
-- risk silently granting workspace-admin authority nobody asked to grant.
-- The count below is surfaced instead of guessed; promoting it is a
-- follow-up product decision, not a data-correctness bug.
--
-- Demotion carries no such ambiguity: it only ever REMOVES authority a row
-- should not have, so it is safe to apply unconditionally.

DO $$
DECLARE
  demoted_count      int;
  promotable_count   int;
BEGIN
  WITH demote AS (
    UPDATE public.tenant_users tu
       SET role = 'MEMBER'
      FROM public.tenant_billing_accounts tba
     WHERE tu.tenant_id = tba.tenant_id
       AND tu.status = 'ACTIVE'
       AND tu.role = 'OWNER'
       AND NOT EXISTS (
             SELECT 1 FROM public.account_memberships am
              WHERE am.account_id = tba.account_id
                AND am.user_id    = tu.user_id
                AND am.role       = 'owner'
                AND am.status     = 'active'
           )
     RETURNING 1
  )
  SELECT count(*) INTO demoted_count FROM demote;

  SELECT count(*) INTO promotable_count
    FROM public.tenant_users tu
    JOIN public.tenant_billing_accounts tba ON tba.tenant_id = tu.tenant_id
    JOIN public.account_memberships am
      ON am.account_id = tba.account_id
     AND am.user_id    = tu.user_id
     AND am.role       = 'owner'
     AND am.status     = 'active'
   WHERE tu.status = 'ACTIVE'
     AND tu.role  != 'OWNER';

  RAISE NOTICE 'tenant_users role reconciliation (issue #1245): demoted % stale OWNER row(s) to MEMBER; % row(s) have a live ACTIVE account owner but a non-OWNER tenant_users role and were left untouched pending a deliberate product decision (issue #1244) -- see this file''s header comment', demoted_count, promotable_count;
END $$;
