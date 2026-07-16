-- supabase/migrations/20260716_02_artifacts.sql
--
-- Artifacts hosting (issue #312, agent-subsystem blueprint Step 3.3).
-- Self-contained HTML blobs (Claude-Artifacts-style), persisted and served
-- at a stable, versioned URL. The HTML bytes themselves live in the
-- existing Supabase Storage `hive-files` bucket (packages/storage.Storage,
-- same client the files/images/rag handlers use); only metadata + the
-- storage_path pointer live here.
--
-- Two tables:
--   public.artifacts          One row per artifact id. latest_version is a
--                              denormalized counter bumped by
--                              apps/edge-api/internal/artifacts.Repo.AddVersion
--                              in the same transaction as the version insert,
--                              so GET /artifacts/{id} (latest) never needs a
--                              MAX(version) scan.
--   public.artifact_versions  One immutable row per redeploy. A same-id
--                              redeploy inserts a new row and mints a new
--                              version at the same URL; existing versions
--                              are never mutated or deleted individually.
--
-- Security: RLS on both tables, tenant-isolation policy mirrors
-- public.marketplace_tenant_entries (20260716_01_marketplace_catalog.sql,
-- NULLIF guard for the pooled-connection GUC quirk). A second PERMISSIVE
-- SELECT-only policy on each table admits public artifacts to anonymous
-- readers -- Postgres OR-combines permissive policies for the same command,
-- so an anonymous request (no app.current_tenant_id set at all) can still
-- read a row when is_public = true, while INSERT/UPDATE/DELETE remain
-- governed solely by the tenant-isolation policy. This lets
-- GET /artifacts/{id} serve a shared artifact with no service-role bypass
-- or SECURITY DEFINER function required.
--
-- Depends on: 20260516_01_phase19_tenants.sql (public.tenants).

BEGIN;

CREATE TABLE public.artifacts (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    created_by     UUID        REFERENCES auth.users(id),
    name           TEXT        NOT NULL DEFAULT '',
    is_public      BOOLEAN     NOT NULL DEFAULT false,
    latest_version INT         NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE public.artifacts IS
  'Claude-Artifacts-style hosted HTML blobs (issue #312). is_public gates the anonymous-read RLS policy on this table and public.artifact_versions; private by default. latest_version is bumped by artifacts.Repo.AddVersion in the same transaction as the version insert.';

CREATE TABLE public.artifact_versions (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id  UUID        NOT NULL REFERENCES public.artifacts(id) ON DELETE CASCADE,
    -- Denormalized from artifacts.tenant_id (mirrors public.rag_chunks
    -- alongside document_id): lets the tenant-isolation policy below apply
    -- directly to this table without a subquery on every row.
    tenant_id    UUID        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    version      INT         NOT NULL,
    storage_path TEXT        NOT NULL,
    size_bytes   BIGINT      NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (artifact_id, version)
);

COMMENT ON TABLE public.artifact_versions IS
  'Immutable per-redeploy version rows for public.artifacts. storage_path points at the HTML blob in the hive-files Supabase Storage bucket.';

CREATE INDEX artifacts_tenant_idx ON public.artifacts (tenant_id, created_at DESC);
CREATE INDEX artifact_versions_artifact_idx ON public.artifact_versions (artifact_id, version DESC);
CREATE INDEX artifact_versions_tenant_idx ON public.artifact_versions (tenant_id);

ALTER TABLE public.artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.artifact_versions ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'artifacts'
          AND policyname = 'artifacts_tenant_isolation'
    ) THEN
        CREATE POLICY artifacts_tenant_isolation
            ON public.artifacts AS PERMISSIVE FOR ALL TO hive_app
            USING      (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
            WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
    END IF;
END$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'artifacts'
          AND policyname = 'artifacts_public_read'
    ) THEN
        -- SELECT-only permissive policy, OR-combined with the tenant
        -- isolation policy above: lets anonymous readers (no
        -- app.current_tenant_id set) see a row once its owner has shared
        -- it, without ever granting them INSERT/UPDATE/DELETE.
        CREATE POLICY artifacts_public_read
            ON public.artifacts FOR SELECT TO hive_app
            USING (is_public = true);
    END IF;
END$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'artifact_versions'
          AND policyname = 'artifact_versions_tenant_isolation'
    ) THEN
        CREATE POLICY artifact_versions_tenant_isolation
            ON public.artifact_versions AS PERMISSIVE FOR ALL TO hive_app
            USING      (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
            WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
    END IF;
END$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'artifact_versions'
          AND policyname = 'artifact_versions_public_read'
    ) THEN
        CREATE POLICY artifact_versions_public_read
            ON public.artifact_versions FOR SELECT TO hive_app
            USING (
                EXISTS (
                    SELECT 1 FROM public.artifacts a
                    WHERE a.id = artifact_versions.artifact_id AND a.is_public = true
                )
            );
    END IF;
END$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.artifacts         TO hive_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.artifact_versions TO hive_app;
GRANT SELECT ON public.artifacts         TO auditor_ro;
GRANT SELECT ON public.artifact_versions TO auditor_ro;

COMMIT;
