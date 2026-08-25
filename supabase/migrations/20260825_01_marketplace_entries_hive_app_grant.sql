-- supabase/migrations/20260825_01_marketplace_entries_hive_app_grant.sql
--
-- Fixes issue #958's live finding: 20260716_01_marketplace_catalog.sql's
-- own header claims public.marketplace_entries is read and written "only
-- [by] control-plane's own pool", but never actually granted that pool
-- anything on the table. apps/control-plane/cmd/server/main.go wires
-- marketplace.NewPgxRepository off the same `pool` variable every other
-- repository in that file uses, and 20260625_06_audit_cold_archive_manifest.sql
-- documents that pool in production terms: "the control-plane connects as
-- hive_app... the platform pool does no SET ROLE, it connects as hive_app
-- directly." hive_app was never granted anything on marketplace_entries, so
-- every catalog CRUD call (list/create/update/delete an MCP server, rule,
-- skill, or prompt template) has been failing with "permission denied for
-- table marketplace_entries" since the feature shipped -- silently, because
-- marketplace/repository_test.go's live suite had never run in CI (it read
-- HIVE_TEST_DB_URL, was never named in any workflow's go test invocation,
-- and unconditionally skipped everywhere).
--
-- No RLS here, matching the original migration's header ("Global
-- admin-curated catalog... no RLS... only control-plane's own pool reads or
-- writes it, gated at the HTTP layer by platform-admin"): this grant does
-- not change that trust model, it just lets the pool the feature actually
-- runs on reach the table at all.

BEGIN;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.marketplace_entries TO hive_app;

COMMIT;
