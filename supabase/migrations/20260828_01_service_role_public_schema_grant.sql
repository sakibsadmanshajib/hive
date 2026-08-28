-- supabase/migrations/20260828_01_service_role_public_schema_grant.sql
--
-- Same bug class as 20260825_01_marketplace_entries_hive_app_grant.sql
-- (issue #958): a role with USAGE on a schema but no table-level GRANT
-- fails every query with "permission denied for table X", which reads like
-- an RLS problem and is in fact a missing GRANT. deploy/supabase/init/
-- 00-extensions.sql grants service_role USAGE ON SCHEMA public (line 64)
-- and BYPASSRLS on the role itself (CREATE ROLE service_role ... BYPASSRLS),
-- but that init file only ever backfilled ALTER DEFAULT PRIVILEGES for the
-- storage schema, whose header comment already documents this exact class
-- of defect for storage ("That is exactly what broke bucket creation on
-- the first enterprise-profile boot"). The public schema never got the
-- equivalent grant for service_role.
--
-- Found live while verifying the RAG feature-gate fix (PR #1257,
-- rag-demo-readiness.yml run 33209756779): scripts/verify-rag-roundtrip.py's
-- first REST call, `POST /rest/v1/tenants` authenticated as
-- SUPABASE_SERVICE_ROLE_KEY, failed with `42501 permission denied for table
-- tenants`. Root-caused with evidence before writing this fix, not by
-- assumption: a workflow step decoded the JWT's own `role` claim
-- (`service_role`) and confirmed it is byte-identical to
-- ENTERPRISE_SERVICE_ROLE_KEY, ruling out a stale/mismatched key. The
-- storage-schema precedent above is the actual cause once the key itself
-- was cleared.
--
-- Scope is service_role ONLY. An earlier revision of this migration also
-- granted anon and authenticated (matching scripts/ci-supabase-stack.sh's
-- own CI-only workaround), and adversarial review caught two reasons that
-- was wrong for the real deployment, not just a broader-than-needed
-- default:
--
--   1. It reopens a hole 20260823_02_agent_task_schedules.sql explicitly
--      closed. That migration REVOKEs EXECUTE on
--      agent_task_schedules_claim_due(...) and agent_tasks_list_active()
--      FROM PUBLIC precisely because "anon/authenticated could call this
--      SECURITY DEFINER function directly through PostgREST's RPC surface
--      (the anon key is public): a cross-tenant read of every tenant's
--      schedule rows plus a starvation DoS" (that migration's own words).
--      REVOKE ... FROM PUBLIC does not block a later direct GRANT to a
--      named role, and RLS is no backstop for a SECURITY DEFINER function
--      by design (it runs as the defining superuser, bypassing RLS on
--      purpose). `GRANT ALL ON ALL FUNCTIONS IN SCHEMA public TO anon,
--      authenticated` was exactly that later direct grant, on every
--      SECURITY DEFINER function in the schema, not just the two named
--      above. public.custom_access_token_hook carries the identical
--      FROM PUBLIC revoke across four migrations, most recently
--      20260823_03_owui_role_never_admin.sql.
--   2. ALTER DEFAULT PRIVILEGES grants the role directly, not through
--      PUBLIC, so it survives every REVOKE ... FROM PUBLIC precedent in
--      this repo and cannot be clawed back by the same pattern: the next
--      SECURITY DEFINER helper written the way this codebase already
--      writes them would read clean in review and still be callable by
--      anon. It would also have covered less than it looked like it did
--      (FOR ROLE postgres misses anything supabase_admin creates).
--
--   Also HIGH, not just these two CRITICAL findings: 54 of the ~80
--   public-schema tables carry no RLS policy at all (only 26 have
--   ENABLE ROW LEVEL SECURITY), including api_keys, credit_ledger_entries,
--   credit_grants, credit_reservations, accounts, account_memberships,
--   invoices, payment_intents, usage_events, files, rag_chunks,
--   audit_log_default, and llm_traces_default -- granting anon/authenticated
--   table access there has no RLS gate behind it at all. It also directly
--   reversed 20260822_01_tenant_email_domains_admin_only.sql's own
--   REVOKE INSERT, DELETE ... FROM authenticated, whose header says that
--   revoke "should not be done without a domain ownership check in front
--   of it".
--
--   No live customer-facing exposure resulted (deploy/docker/
--   Caddyfile.supabase keeps /rest/v1 off the public listener; port 8080
--   serves only /auth/v1 today), but that Caddyfile's own comment warns
--   "a public /rest/v1 would put the whole public schema one anon key away
--   from the internet, governed only by whatever grants happen to exist...
--   add a prefix here only together with the RLS policies and grants that
--   make it safe to serve anonymously" -- and this migration would have
--   quietly broken half of that promise, one route-config line away from
--   mattering. scripts/ci-supabase-stack.sh's three-role, functions-included
--   scope is correct for what it is: a throwaway CI database with no real
--   data and no exposure. It is not automatically correct for the
--   production data plane, and citing it as precedent for that was the
--   actual scoping mistake here -- this project's own "verify against the
--   real substrate" lesson, applied to a grant instead of a config value.
--
-- Blast radius, checked rather than assumed (PR #1257 discussion), still
-- accurate for the service_role-only scope below:
--   * scripts/seed-demo-owner.py -- same `/rest/v1` + SUPABASE_SERVICE_ROLE_KEY
--     pattern as verify-rag-roundtrip.py. Consistent with the live symptom
--     this whole investigation started from: the demo tenant's ENABLE_RAG
--     was found false despite this script unconditionally setting it true
--     on every run, meaning its last successful run against this box
--     predates the 2026-08-20 self-hosted cutover (or this defect).
--   * scripts/seed-owui-e2e-user.py -- same pattern (POST /rest/v1/tenants,
--     /rest/v1/accounts). Its scheduled caller, .github/workflows/
--     owui-nightly.yml, runs on a GitHub-hosted runner against its own
--     ephemeral stack, not this box; its two most recent failures
--     (2026-08-27, 2026-08-28) are a DIFFERENT, already-known defect (a DNS
--     "Name or service not known" resolving a stale SUPABASE_URL, the
--     issue #1059 class post-deploy-verify.yml's own header documents) --
--     not this one. Not confirmed live-affected by this migration; affected
--     by code-pattern inspection alone if ever pointed at this box.
--   * scripts/verify-rag-roundtrip.py -- confirmed live-blocked, fixed by
--     this migration (this PR).
--   * scripts/ci-supabase-stack.sh -- NOT affected: already carries its own
--     (CI-only) workaround, applied fresh every run.
--   * .github/ci/test-db-bootstrap.sql -- did not create the service_role
--     role AT ALL before this PR (nothing before this migration ever
--     referenced it in an actual GRANT), fixed in the same PR.
--   * control-plane and edge-api's own request path -- unaffected regardless
--     of scope: both connect with their own pgx pool as hive_app, never
--     through PostgREST. web-console's Supabase usage is auth-only (no
--     `.from()`/`.storage` call anywhere in that app).
--
-- Existing tables need the direct GRANT below; ALTER DEFAULT PRIVILEGES
-- alone only covers objects created AFTER it runs, exactly as
-- 00-extensions.sql's own storage-schema comment explains. Both are applied
-- here, for the same reason storage carries both. Explicit verbs, not ALL:
-- service_role's actual need (every ops/verification script's REST calls,
-- all upsert/select shaped) is SELECT/INSERT/UPDATE/DELETE on tables,
-- USAGE/SELECT on sequences for default-value inserts on any serial/
-- identity column; nothing here calls a function through this role, so no
-- FUNCTIONS grant is included -- add one, scoped and evidenced, if a real
-- caller ever needs it, rather than opening every current and future
-- SECURITY DEFINER function as a side effect of a table-access fix.

BEGIN;

-- Repair first, then grant. An earlier revision of THIS file (the one
-- adversarial review rejected, above) was already applied to the live demo
-- box by .github/workflows/rag-demo-readiness.yml's own "apply ahead of the
-- normal deploy migration" step, on PR #1257 run 33212925653. Narrowing the
-- grant below does not undo that: a GRANT already made is not withdrawn by a
-- later, narrower GRANT. Verified on the box on 2026-08-28, before writing
-- this block: anon, authenticated and service_role each held all seven table
-- privileges on all 82 public-schema tables (574 rows each in
-- information_schema.role_table_grants), pg_default_acl carried
-- {anon,authenticated,service_role} on tables, sequences AND functions, and
-- has_function_privilege('anon', 'public.agent_tasks_list_active()',
-- 'EXECUTE') answered true -- the exact cross-tenant SECURITY DEFINER read
-- 20260823_02_agent_task_schedules.sql closed. So the revokes below are not
-- defensive tidiness, they are the fix for a live state that exists right
-- now on the demo box and would otherwise survive this PR.
--
-- Safe on a database where that revision never ran (CI throwaway, a fresh
-- deploy): REVOKE of a privilege that was never granted is a no-op, not an
-- error. Not safe to write as a blanket revoke alone, though -- five earlier
-- migrations grant `authenticated` real, intended access, and a blanket
-- REVOKE would take those with it, so they are re-granted verbatim below.
-- The role names are all guaranteed to exist by this point:
-- deploy/supabase/init/00-extensions.sql creates them on every real and CI
-- stack, and .github/ci/test-db-bootstrap.sql creates them for the one CI
-- leg that does not run that init file.

REVOKE ALL ON ALL TABLES    IN SCHEMA public FROM anon, authenticated, service_role;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM anon, authenticated, service_role;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA public FROM anon, authenticated, service_role;

ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
  REVOKE ALL ON TABLES FROM anon, authenticated, service_role;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
  REVOKE ALL ON SEQUENCES FROM anon, authenticated, service_role;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
  REVOKE ALL ON FUNCTIONS FROM anon, authenticated, service_role;

-- The five grants `authenticated` is supposed to hold, restored exactly as
-- their own migrations state them. Verbatim from:
--   20260516_01_phase19_tenants.sql
--   20260516_03_phase19_tenant_users.sql
--   20260516_09_phase19_tenant_email_domains.sql, as narrowed by
--     20260822_01_tenant_email_domains_admin_only.sql (which revokes the
--     INSERT and DELETE that file granted, so only SELECT is restored here:
--     re-granting the other two would reverse an explicit security decision)
--   20260516_08_phase19_tenant_invites.sql
--   20260516_02_phase19_tenant_settings.sql
-- `anon` is granted nothing, because no migration in this repository ever
-- granted it anything: 00-extensions.sql gives it USAGE on the schema and
-- nothing else, which is the whole intended surface.
GRANT SELECT                         ON public.tenants              TO authenticated;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.tenant_users         TO authenticated;
GRANT SELECT                         ON public.tenant_email_domains TO authenticated;
GRANT SELECT, INSERT, UPDATE         ON public.tenant_invites       TO authenticated;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.tenant_settings      TO authenticated;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO service_role;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO service_role;

ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO service_role;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO service_role;

COMMIT;
