-- supabase/migrations/20260625_06_audit_cold_archive_manifest.sql
--
-- Audit cold-archive manifest for PHIPA (10-year) / Quebec Law 25 retention.
-- Part of Carl.sh sovereign edge (#241).
--
-- Tables:
--   public.audit_cold_archive_manifest
--       Tracks every JSONL+gzip batch archived to local cold storage.
--       One row per (tenant_id, partition month) -- immutable after insert.
--       Rows are never updated or deleted by application code; the RLS
--       policy enforces read-only access for tenant roles.
--
-- Schema reconciliation (compliance fix):
--   An earlier migration, 20260516_04_phase19_audit_log.sql, also created a
--   table named public.audit_cold_archive_manifest with a different, now
--   superseded shape (partition_name / parquet_path / parquet_sha256 ...).
--   That earlier shape is NOT the one the application uses: InsertManifest and
--   the retention/purge queries in
--   apps/control-plane/internal/auditarchive/pg_repository.go read and write
--   the (id / tenant_id / partition_month / object_key / sha256_hash /
--   first_seq / last_seq / purge_after) shape defined below, and the
--   immutability trigger in 20260625_08 references the same columns.
--
--   Because this file was originally written with CREATE TABLE IF NOT EXISTS,
--   on any from-scratch replay it silently no-opped onto the stale shape and
--   then the UNIQUE INDEX on (tenant_id, partition_month) failed with
--   "column tenant_id does not exist", breaking every clean `supabase db push`
--   (fresh-DB init and enterprise self-host). This migration now explicitly
--   drops the stale table and its May-era policies first, then creates the
--   authoritative shape.
--
--   Data safety: the stale table can never hold rows. Every application write
--   targets the columns defined below, which do not exist on the stale shape,
--   so any INSERT against the stale table errors out before committing. The
--   drop therefore loses no audit data (verified: 0 rows on staging).
--
-- Design notes:
--   - object_key is the storage path (e.g. audit/cold/<tenant>/<YYYY-MM>.jsonl.gz).
--   - sha256_hash (bytea, 32 bytes) covers the compressed JSONL object so
--     the file can be re-verified against the manifest at any time.
--   - row_count records how many audit_log rows were archived in this batch.
--   - first_seq / last_seq bracket the seq range so chain continuity can be
--     checked without re-reading the archive file.
--   - purge_after is computed at archive time (archived_at + retention_years);
--     the cron checks this column to purge expired cold objects.

BEGIN;

-- Remove the stale (20260516_04) shape and its May-era policies from
-- 20260518_04 before recreating the authoritative table. IF EXISTS keeps this
-- idempotent whether or not those objects are present in a given environment.
DROP POLICY IF EXISTS manifest_service_role_all ON public.audit_cold_archive_manifest;
DROP POLICY IF EXISTS manifest_auditor_select ON public.audit_cold_archive_manifest;
DROP TABLE IF EXISTS public.audit_cold_archive_manifest CASCADE;

CREATE TABLE public.audit_cold_archive_manifest (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    -- ON DELETE RESTRICT: tenant deletion must not silently orphan or destroy
    -- cold-archive objects. PHIPA / Law 25 require 10-year retention; a tenant
    -- lifecycle event cannot override that. The tenant record must be retained
    -- (or the manifest rows migrated) before the tenant row can be removed.
    tenant_id       UUID         NOT NULL REFERENCES public.tenants(id) ON DELETE RESTRICT,
    -- Year-month of the archived partition (always the 1st of the month, UTC).
    partition_month TIMESTAMPTZ  NOT NULL,
    -- Path in cold storage (local filesystem via Supabase Storage S3 backend).
    object_key      TEXT         NOT NULL,
    -- SHA-256 digest of the compressed JSONL file (32 raw bytes).
    sha256_hash     BYTEA        NOT NULL,
    row_count       BIGINT       NOT NULL DEFAULT 0,
    first_seq       BIGINT       NOT NULL,
    last_seq        BIGINT       NOT NULL,
    archived_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- When the cold object may be purged (PHIPA = 10 years from archive date).
    purge_after     TIMESTAMPTZ  NOT NULL
);

-- One manifest entry per (tenant, month).
CREATE UNIQUE INDEX audit_cold_archive_manifest_tenant_month
    ON public.audit_cold_archive_manifest (tenant_id, partition_month);

-- Covering index for the purge scan (purge_after ASC).
CREATE INDEX audit_cold_archive_manifest_purge
    ON public.audit_cold_archive_manifest (purge_after);

-- ── Grants ───────────────────────────────────────────────────────────────────
-- Relocated here from the removed 20260518_04 manifest block (that block
-- targeted the stale table dropped above). The service role (control-plane)
-- bypasses RLS for inserts and the purge scan; hive_app is a non-bypass
-- application role that still needs explicit table privileges, and auditor_ro
-- reads the manifest for chain spot-checks.
REVOKE ALL ON public.audit_cold_archive_manifest FROM PUBLIC;
GRANT INSERT, SELECT, UPDATE ON public.audit_cold_archive_manifest TO hive_app;
GRANT SELECT ON public.audit_cold_archive_manifest TO auditor_ro;

-- ── RLS ──────────────────────────────────────────────────────────────────────
ALTER TABLE public.audit_cold_archive_manifest ENABLE ROW LEVEL SECURITY;

-- Tenant users may list their own manifest entries (read-only).
-- The app sets app.current_tenant_id via SET LOCAL before queries.
CREATE POLICY audit_cold_archive_manifest_tenant_read
    ON public.audit_cold_archive_manifest
    FOR SELECT
    USING (
        tenant_id::text = current_setting('app.current_tenant_id', true)
    );

-- Service-role (control-plane) bypasses RLS for inserts and the purge scan.
-- No UPDATE / DELETE policy is defined for tenant roles -- manifest is
-- write-once from the application perspective.

COMMENT ON TABLE public.audit_cold_archive_manifest IS
    'Immutable manifest of audit_log cold-archive batches. '
    'PHIPA / Quebec Law 25 -- retain 10 years. '
    'One row per (tenant_id, partition_month). '
    'Application code must never UPDATE or DELETE rows.';

COMMIT;
