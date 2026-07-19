-- supabase/migrations/20260719_01_rag_embedding_config.sql
--
-- Single source of truth for the active RAG embedding configuration, plus a
-- re-embed progress table (admin-selectable embedding dimension redesign, PR2;
-- closes the cross-service inconsistency #368).
--
-- Why a config row: edge-api (query path) and control-plane (ingest + re-embed
-- path) each resolve EMBEDDING_MODEL / EMBEDDING_DIM from the environment
-- independently. Nothing forced them to agree, and nothing recorded which
-- (model, dim, pgvector type, index) the live rag_chunks.embedding column was
-- actually provisioned to. This singleton row is that record. Both services
-- read it at startup and fail closed (disable the RAG feature gate) when their
-- resolved env config does not match it, instead of serving cross-space
-- queries against a column provisioned for a different model or dimension.
--
-- Why runtime provisioning, not a fixed migration, resizes the column: the
-- embedding dimension is admin-chosen at runtime, so a static CHECK-in migration
-- cannot express the target vector(N)/halfvec(N) type. The control-plane
-- provisioning routine (apps/control-plane/internal/rag/provision.go) recreates
-- rag_chunks.embedding to match this row's (pgvector_type, dim, opclass). This
-- migration only creates the config + progress tables and seeds the default
-- that matches the shipped column (vector(1024) HNSW cosine, per
-- 20260625_05_carl_rag.sql).
--
-- Depends on: 20260625_05_carl_rag.sql (public.rag_chunks, hive_app, auditor_ro).

BEGIN;

-- Singleton: exactly one row, pinned to id = 1 by the PK + CHECK. Both
-- services read this row; only the control-plane provisioning routine writes
-- it (it runs the DDL that makes the column match), so writes are naturally
-- serialized through that one code path.
CREATE TABLE IF NOT EXISTS public.rag_embedding_config (
    id             SMALLINT    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    -- Canonical model id or LiteLLM route alias resolved by packages/embedmodel.
    model          TEXT        NOT NULL,
    -- Provisioned vector width. The rag_chunks.embedding column is
    -- pgvector_type(dim); a service whose EMBEDDING_DIM differs disables RAG.
    dim            INT         NOT NULL CHECK (dim > 0),
    -- 'vector' (<=2000 indexed) or 'halfvec' (2001..4000 indexed, or
    -- 4001..16000 brute-force). Matches embedmodel.ResolvePgvector.
    pgvector_type  TEXT        NOT NULL DEFAULT 'vector'
                               CHECK (pgvector_type IN ('vector','halfvec')),
    -- HNSW operator class, or '' when the dimension is stored unindexed
    -- (brute-force sequential scan, opt-in only).
    opclass        TEXT        NOT NULL DEFAULT 'vector_cosine_ops',
    -- Whether an ANN index backs the column (false = brute-force scan).
    indexable      BOOLEAN     NOT NULL DEFAULT TRUE,
    -- Set by the provisioning routine once the column + index actually match.
    provisioned_at TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the shipped serverless-demo default, matching both .env.example and the
-- column shipped by 20260625_05_carl_rag.sql: qwen3-embedding-8b via
-- route-openrouter-embedding-fallback (MRL, native 4096) MRL-reduced to 1024,
-- stored as vector(1024) with HNSW vector_cosine_ops. This equals the existing
-- rag_chunks.embedding vector(1024) shape, so a fresh demo runs with zero
-- provisioning and the startup env/config reconcile matches out of the box. An
-- enterprise deployment on a different model (e.g. bge-m3, also 1024/vector)
-- updates this row through the provisioning routine, which re-embeds the
-- corpus; until then a mismatched EMBEDDING_MODEL fails RAG closed rather than
-- querying across two vector spaces. INSERT ... ON CONFLICT DO NOTHING keeps
-- re-runs idempotent without clobbering an admin-applied config.
INSERT INTO public.rag_embedding_config
    (id, model, dim, pgvector_type, opclass, indexable, provisioned_at)
VALUES
    (1, 'route-openrouter-embedding-fallback', 1024, 'vector', 'vector_cosine_ops', TRUE, now())
ON CONFLICT (id) DO NOTHING;

COMMENT ON TABLE public.rag_embedding_config IS
    'Singleton active RAG embedding configuration (model, dim, pgvector type, index). Single source of truth read by edge-api and control-plane at startup; written only by the control-plane provisioning routine. Closes #368.';

-- Re-embed progress. One row per provisioning-triggered corpus rebuild, so a
-- re-embed that is interrupted (process restart) is resumable and observable.
-- Not tenant-scoped: a model/dim switch is an operator action across the whole
-- corpus, so this is an operational/global table, not RLS-guarded per tenant.
CREATE TABLE IF NOT EXISTS public.rag_reembed_jobs (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    model        TEXT        NOT NULL,
    dim          INT         NOT NULL CHECK (dim > 0),
    total_docs   INT         NOT NULL DEFAULT 0,
    done_docs    INT         NOT NULL DEFAULT 0,
    status       TEXT        NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending','running','completed','failed')),
    error_msg    TEXT,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS rag_reembed_jobs_status_idx
    ON public.rag_reembed_jobs (status, started_at DESC);

COMMENT ON TABLE public.rag_reembed_jobs IS
    'Progress ledger for corpus re-embed runs triggered by an embedding model/dim switch. Resumable after restart; append-only per run.';

-- Privileges. hive_app (the NOT BYPASSRLS app role) only READS both tables:
-- it reconciles its resolved env config against the active config row at
-- startup. The config row is the fail-closed control record; it is written
-- solely by the provisioning routine, which connects with a privileged
-- (DDL-capable) role that also runs the matching column DDL, so hive_app must
-- not be able to UPDATE it out from under the physical schema. The re-embed
-- worker likewise runs on the privileged pool and does not write
-- rag_reembed_jobs through hive_app, so no INSERT/UPDATE grant is needed there.
-- auditor_ro reads both for the audit trail.
GRANT SELECT ON public.rag_embedding_config TO hive_app;
GRANT SELECT ON public.rag_reembed_jobs     TO hive_app;
GRANT SELECT ON public.rag_embedding_config TO auditor_ro;
GRANT SELECT ON public.rag_reembed_jobs     TO auditor_ro;

COMMIT;
