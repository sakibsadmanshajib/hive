-- supabase/migrations/20260828_01_service_role_public_schema_grant.sql
--
-- Same bug class as 20260825_01_marketplace_entries_hive_app_grant.sql
-- (issue #958): a role with USAGE on a schema but no table-level GRANT
-- fails every query with "permission denied for table X", which reads like
-- an RLS problem and is in fact a missing GRANT. deploy/supabase/init/
-- 00-extensions.sql grants service_role USAGE ON SCHEMA public (line 64)
-- and BYPASSRLS on the role itself (CREATE ROLE service_role ... BYPASSRLS),
-- but that init file only backfills ALTER DEFAULT PRIVILEGES for the
-- storage schema, whose header comment already documents this exact class
-- of defect for storage ("That is exactly what broke bucket creation on
-- the first enterprise-profile boot"). The public schema never got the
-- equivalent grant, so service_role -- which is supposed to bypass RLS
-- entirely -- cannot read or write ANY public-schema table via PostgREST.
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
-- This has been latent rather than a regression: nothing in the running
-- product's own request path uses PostgREST-as-service_role against public
-- schema tables (control-plane connects with its own pgx pool as hive_app;
-- web-console's Supabase usage is auth-only, no `.from()`/`.storage` call
-- anywhere in that app). Only ops/verification scripts that authenticate
-- through Supabase's REST API with the service-role key hit this --
-- scripts/seed-demo-owner.py, scripts/verify-rag-roundtrip.py -- and this is
-- the first of those to have actually run live against the self-hosted
-- stack (the RAG feature gate blocked it until this same PR enabled it).
--
-- Existing tables need the direct GRANT below; ALTER DEFAULT PRIVILEGES
-- alone only covers objects created AFTER it runs, exactly as
-- 00-extensions.sql's own storage-schema comment explains. Both are applied
-- here, for the same reason storage carries both.
--
-- Scope: service_role only, matching the storage-schema precedent and this
-- migration's own evidence. anon/authenticated are left untouched: nothing
-- observed proves they need it, and widening two roles' access across every
-- public-schema table is a materially bigger security decision than fixing
-- the one role (BYPASSRLS, server-side only, never issued to a browser)
-- this defect was actually found on.

BEGIN;

GRANT ALL ON ALL TABLES IN SCHEMA public TO service_role;
GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO service_role;

ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
  GRANT ALL ON TABLES TO service_role;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
  GRANT ALL ON SEQUENCES TO service_role;

COMMIT;
