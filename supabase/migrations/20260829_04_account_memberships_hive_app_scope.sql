-- supabase/migrations/20260829_04_account_memberships_hive_app_scope.sql
--
-- Issue #896: public.account_memberships RLS was a blanket
-- `FOR ALL TO hive_app USING (true) WITH CHECK (true)`
-- (20260529_01_rls_tenant_tables.sql), so the control-plane's own Go WHERE
-- predicates were the only enforcement of tenancy for the control-plane's DB
-- role. hive_app is NOT BYPASSRLS
-- (20260518_04_phase19_audit_rls_and_indexes.sql), so a query missing an
-- account_id/user_id predicate would have returned every tenant's rows.
--
-- Direct PostgREST access (anon/authenticated) was already zero rows before
-- this migration and stays that way: this table has never granted or
-- policied anything to those roles, and FORCE ROW LEVEL SECURITY denies by
-- default when no policy applies to the connecting role. Confirmed live
-- against a throwaway Postgres, unchanged by this migration. This migration
-- narrows the hive_app policy only.
--
-- This is the same session-scoped RLS pattern already proven in production
-- for public.egress_policies, public.marketplace_tenant_entries,
-- public.agent_tasks, public.user_memories, public.agent_task_schedules and
-- public.tenant_users itself (20260726_01_tenant_users_hive_app_grant.sql):
-- set_config('app.current_*', value, true) inside an explicit transaction
-- (LOCAL scope, required -- see egress/repository.go's withTenantTx comment
-- for the two ways a bare Exec+separate Query gets this wrong), read back by
-- the policy via NULLIF(current_setting('app.current_*', true), '')::uuid so
-- an unset variable folds to NULL and the comparison fails closed.
--
-- account_memberships has two access shapes, not one, so it needs two
-- session variables rather than reusing app.current_tenant_id (which is a
-- different id space -- public.tenants.id, not public.accounts.id -- and
-- would not cover either shape below regardless):
--
--   * Shape A ("my own memberships"): row.user_id = the acting caller's own
--     id. Every call site that reads or writes this shape always passes the
--     caller's own auth.Viewer.UserID, never a different user's id --
--     verified against every non-test call site in
--     apps/control-plane/internal/accounts/repository.go and
--     apps/control-plane/internal/platform/role_pgx.go before writing this
--     migration. Scoped by app.current_actor_user_id.
--   * Shape B ("members of one account I already administer"): row.
--     account_id = an account the Go layer has already authorized the caller
--     against; row.user_id can be any member. Used by the member-list page
--     and by an admin/owner changing a DIFFERENT member's role. Scoped by
--     app.current_account_id.
--
-- No caller needs both, none needs neither, so two PERMISSIVE policies OR'd
-- together (Postgres's default policy combination) cover the full access
-- surface without over-granting either shape to the other's callers. Both
-- are FOR ALL: unlike tenant_users' hive_app grant (SELECT only, because
-- membership writes went through a different path when that migration was
-- written), account_memberships was always fully read/written by hive_app
-- and stays so, just no longer unconditionally.
--
-- Depends on: 20260529_01_rls_tenant_tables.sql (creates the table's RLS and
-- the policy this migration replaces). apply-migrations.sh records each
-- migration file once in public.hive_schema_migrations and never re-runs it
-- (scripts/apply-migrations.sh), so this file running after 20260529 is
-- final: 20260529's own loop re-applying later would not resurrect the
-- blanket policy this file drops.
--
-- Also fixes a second, independently-discovered defect, same bug class as
-- issue #812 (marketplace_entries had a hive_app RLS policy but no
-- table-level GRANT, so every hive_app query 42501'd with "permission denied
-- for table marketplace_entries" -- an RLS policy narrows visible rows, it
-- does not substitute for the base GRANT). account_memberships has carried
-- the identical gap since 20260529_01_rls_tenant_tables.sql first created
-- it: that file enables RLS and creates the hive_app policy, but, unlike
-- every other table this repository has since given hive_app a working
-- policy on, it never grants hive_app anything on the table at all. Found
-- live against a throwaway Postgres while verifying this fix: `SET ROLE
-- hive_app; SELECT * FROM public.account_memberships;` fails with
-- `permission denied for table account_memberships` even with the old
-- `USING (true)` blanket-allow policy in place. Confirmed the same throwaway
-- database has neither an ALTER DEFAULT PRIVILEGES grant nor a hive_app role
-- inheritance path that would substitute for it.

BEGIN;

DROP POLICY IF EXISTS account_memberships_service_role_all ON public.account_memberships;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.account_memberships TO hive_app;

CREATE POLICY account_memberships_hive_app_actor_scope
  ON public.account_memberships
  AS PERMISSIVE
  FOR ALL
  TO hive_app
  USING      (user_id = NULLIF(current_setting('app.current_actor_user_id', true), '')::uuid)
  WITH CHECK (user_id = NULLIF(current_setting('app.current_actor_user_id', true), '')::uuid);

CREATE POLICY account_memberships_hive_app_account_scope
  ON public.account_memberships
  AS PERMISSIVE
  FOR ALL
  TO hive_app
  USING      (account_id = NULLIF(current_setting('app.current_account_id', true), '')::uuid)
  WITH CHECK (account_id = NULLIF(current_setting('app.current_account_id', true), '')::uuid);

-- A third, independently-discovered gap in the same live verification pass:
-- accounts.pgxRepository.ListMembersByAccountID (the member-list page) has
-- always LEFT JOINed auth.users to render a member's email instead of a bare
-- UUID. hive_app was never granted USAGE on the auth schema at all (only
-- public and storage -- deploy/supabase/init/00-extensions.sql), so that
-- join 42501's under hive_app regardless of RLS. Column-level GRANT rather
-- than the whole row: hive_app has no legitimate use for
-- encrypted_password, confirmation_token, or any other auth.users column
-- this feature does not render.
GRANT USAGE ON SCHEMA auth TO hive_app;
GRANT SELECT (id, email) ON auth.users TO hive_app;

COMMIT;
