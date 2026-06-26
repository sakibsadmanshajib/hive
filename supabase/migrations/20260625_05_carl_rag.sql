-- supabase/migrations/20260625_05_carl_rag.sql
--
-- RAG schema for Carl.sh sovereign edge (#232).
-- Depends on: 20260625_01_enable_pgvector.sql (CREATE EXTENSION vector)
--
-- Tables:
--   public.rag_documents  one row per uploaded document per tenant
--   public.rag_chunks     text chunks with 1024-dim bge-m3 embeddings
--
-- Security: Row Level Security on both tables. The application sets
-- app.current_tenant_id before every statement; RLS enforces the match.
--
-- HNSW index: cosine distance, m=16, ef_construction=64 per spec.

BEGIN;

CREATE TABLE IF NOT EXISTS public.rag_documents (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID        NOT NULL,
    name         TEXT        NOT NULL,
    mime_type    TEXT        NOT NULL DEFAULT 'text/plain',
    size_bytes   BIGINT      NOT NULL DEFAULT 0,
    status       TEXT        NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending','processing','embedded','error')),
    error_msg    TEXT,
    storage_path TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- embedding dimension = 1024 (bge-m3).
CREATE TABLE IF NOT EXISTS public.rag_chunks (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID        NOT NULL,
    document_id UUID        NOT NULL REFERENCES public.rag_documents(id) ON DELETE CASCADE,
    chunk_index INT         NOT NULL,
    content     TEXT        NOT NULL,
    token_count INT         NOT NULL DEFAULT 0,
    embedding   vector(1024),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- HNSW cosine index for vector similarity search (m=16, ef_construction=64).
CREATE INDEX IF NOT EXISTS rag_chunks_embedding_hnsw_idx
    ON public.rag_chunks
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- Composite index for document-level chunk retrieval.
CREATE INDEX IF NOT EXISTS rag_chunks_tenant_document_idx
    ON public.rag_chunks (tenant_id, document_id);

CREATE INDEX IF NOT EXISTS rag_documents_tenant_idx
    ON public.rag_documents (tenant_id, created_at DESC);

-- Row Level Security: no cross-tenant reads or writes.
ALTER TABLE public.rag_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.rag_chunks    ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'rag_documents'
          AND policyname = 'rag_documents_tenant_isolation'
    ) THEN
        CREATE POLICY rag_documents_tenant_isolation
            ON public.rag_documents AS PERMISSIVE FOR ALL TO hive_app
            USING (tenant_id = current_setting('app.current_tenant_id')::uuid);
    END IF;
END$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'rag_chunks'
          AND policyname = 'rag_chunks_tenant_isolation'
    ) THEN
        CREATE POLICY rag_chunks_tenant_isolation
            ON public.rag_chunks AS PERMISSIVE FOR ALL TO hive_app
            USING (tenant_id = current_setting('app.current_tenant_id')::uuid);
    END IF;
END$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.rag_documents TO hive_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.rag_chunks    TO hive_app;
GRANT SELECT ON public.rag_documents TO auditor_ro;
GRANT SELECT ON public.rag_chunks    TO auditor_ro;

COMMIT;
