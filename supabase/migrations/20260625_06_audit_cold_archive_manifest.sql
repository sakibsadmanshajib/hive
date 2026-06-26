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

CREATE TABLE IF NOT EXISTS public.audit_cold_archive_manifest (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID         NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
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
CREATE UNIQUE INDEX IF NOT EXISTS audit_cold_archive_manifest_tenant_month
    ON public.audit_cold_archive_manifest (tenant_id, partition_month);

-- Covering index for the purge scan (purge_after ASC).
CREATE INDEX IF NOT EXISTS audit_cold_archive_manifest_purge
    ON public.audit_cold_archive_manifest (purge_after);

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
