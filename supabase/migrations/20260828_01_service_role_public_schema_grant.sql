-- supabase/migrations/20260828_01_service_role_public_schema_grant.sql
--
-- Same bug class as 20260825_01_marketplace_entries_hive_app_grant.sql
-- (issue #958): a role with USAGE on a schema but no table-level GRANT
-- fails every query with "permission denied for table X", which reads like
-- an RLS problem and is in fact a missing GRANT. deploy/supabase/init/
-- 00-extensions.sql grants anon, authenticated and service_role USAGE ON
-- SCHEMA public (line 64) and BYPASSRLS on service_role specifically
-- (CREATE ROLE service_role ... BYPASSRLS), but that init file only ever
-- backfilled ALTER DEFAULT PRIVILEGES for the storage schema, whose header
-- comment already documents this exact class of defect for storage ("That
-- is exactly what broke bucket creation on the first enterprise-profile
-- boot"). The public schema never got the equivalent grant for any of the
-- three roles.
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
-- Scope widened past service_role after finding scripts/ci-supabase-stack.sh
-- already carries the full fix for this exact gap, predating this migration
-- and never propagated to the real deployment's init file:
--
--   log "==> API-role grants the hosted platform applies for us"
--   # On a hosted Supabase project the platform holds default privileges that
--   # give anon, authenticated and service_role table access in public; RLS
--   # is what actually gates anon and authenticated, and service_role is
--   # BYPASSRLS... supabase/migrations grants none of this because it never
--   # had to. Without it PostgREST answers 403 to every request...
--   GRANT ALL ON ALL TABLES    IN SCHEMA public TO anon, authenticated, service_role;
--   GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO anon, authenticated, service_role;
--   GRANT ALL ON ALL FUNCTIONS IN SCHEMA public TO anon, authenticated, service_role;
--
-- That comment is the actual design intent, stated once, correctly, by
-- whoever wrote ci-supabase-stack.sh: a self-hosted deployment must grant
-- explicitly what a hosted Supabase project's platform grants implicitly.
-- This migration is that same fix, finally applied where the CI script's
-- own comment says it belongs -- the real deployment, not just its
-- throwaway CI stand-in -- so this migration matches ci-supabase-stack.sh's
-- scope exactly (three roles, tables, sequences, and functions) rather than
-- re-deriving a narrower one.
--
-- Blast radius, checked rather than assumed (PR #1257 discussion):
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
--   * scripts/ci-supabase-stack.sh -- NOT affected: already carries the
--     complete workaround above, applied fresh every run. This is the
--     migration's own source, not a casualty.
--   * .github/ci/test-db-bootstrap.sql -- did not create the service_role
--     role AT ALL before this PR (nothing before this migration ever
--     referenced it in an actual GRANT), fixed in the same PR. Whether its
--     HIVE_TEST_DB_URL-gated RLS suites also need anon/authenticated table
--     grants (as opposed to just the role existing) is not established
--     either way here: those suites drive Postgres directly via pgx, not
--     through PostgREST, and it is not yet confirmed whether any of them
--     SET ROLE into anon/authenticated for a query this scope would affect.
--     Flagged, not chased further in this migration.
--   * control-plane and edge-api's own request path -- unaffected regardless
--     of scope: both connect with their own pgx pool as hive_app, never
--     through PostgREST. web-console's Supabase usage is auth-only (no
--     `.from()`/`.storage` call anywhere in that app). No RLS policy in this
--     schema that names `TO authenticated` or `TO anon` (e.g. phase-19's
--     tenant_settings/tenant_users policies) had a live caller reaching it
--     through PostgREST either, so granting them here changes zero currently
--     observable behavior; it only lets those already-written policies be
--     reachable at all, matching what they were written assuming.
--
-- Existing tables need the direct GRANT below; ALTER DEFAULT PRIVILEGES
-- alone only covers objects created AFTER it runs, exactly as
-- 00-extensions.sql's own storage-schema comment explains. Both are applied
-- here, for the same reason storage carries both.

BEGIN;

GRANT ALL ON ALL TABLES    IN SCHEMA public TO anon, authenticated, service_role;
GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO anon, authenticated, service_role;
GRANT ALL ON ALL FUNCTIONS IN SCHEMA public TO anon, authenticated, service_role;

ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
  GRANT ALL ON TABLES TO anon, authenticated, service_role;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
  GRANT ALL ON SEQUENCES TO anon, authenticated, service_role;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
  GRANT ALL ON FUNCTIONS TO anon, authenticated, service_role;

COMMIT;
