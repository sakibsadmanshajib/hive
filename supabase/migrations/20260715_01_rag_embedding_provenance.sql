-- supabase/migrations/20260715_01_rag_embedding_provenance.sql
--
-- Embedding provenance metadata for the RAG corpus (#232 follow-up,
-- agent-subsystem blueprint Step 2.1).
--
-- Context: 20260625_05_carl_rag.sql (PR #256, merged, issue #232 closed)
-- already ships public.rag_documents / public.rag_chunks with a fixed
-- embedding vector(1024) column, an HNSW cosine index, and tenant RLS.
-- That schema already lets the embedding provider swap (serverless today,
-- local bge-m3 later per #295) with zero migration, because the column
-- type pins the dimension. What it does not record is WHICH model
-- produced a given document's vectors. Different models are different
-- vector spaces even at the same dimension (OpenAI text-embedding-3-small
-- truncated to 1024 dims is not comparable to bge-m3's native 1024 dims),
-- so a future provider swap without provenance tracking would silently
-- mix incompatible vector spaces in ANN search with no way to detect or
-- selectively re-embed the stale rows. This migration adds document-level
-- provenance columns so that gap is closeable later without another
-- migration.
--
-- Granularity: per document (rag_documents), not per chunk. All chunks of
-- one document are embedded together in one ingestion pass in the shipped
-- ingestion code (apps/control-plane/internal/rag/ingest.go), so document
-- level is the natural unit and avoids redundant per-row storage.
--
-- Depends on: 20260625_05_carl_rag.sql (public.rag_documents must exist).
--
-- Backfill note: existing rows predate this column. They are backfilled
-- with the DEFAULT below because that is the only embedding route wired
-- in deploy/litellm/config.yaml at the time of writing (route-openrouter-
-- embedding, see apps/edge-api/internal/rag/embed.go). This is a
-- best-effort label for historical rows, not a verified per-row fact;
-- future ingestion code should write the real route name explicitly
-- instead of relying on the default (endpoint-layer change, out of scope
-- for this schema-only migration).
--
-- No RLS change needed: the existing rag_documents_tenant_isolation policy
-- (20260625_05) is row-level (FOR ALL), so it already covers these new
-- columns.

BEGIN;

ALTER TABLE public.rag_documents
    ADD COLUMN IF NOT EXISTS embedding_model TEXT NOT NULL DEFAULT 'route-openrouter-embedding';

ALTER TABLE public.rag_documents
    ADD COLUMN IF NOT EXISTS embedding_dim INT NOT NULL DEFAULT 1024
        CHECK (embedding_dim = 1024);

COMMENT ON COLUMN public.rag_documents.embedding_model IS
    'LiteLLM route/model id that produced this document''s chunk embeddings (e.g. route-openrouter-embedding today, bge-m3 post-#295). Provenance only; does not alter runtime behavior.';

COMMENT ON COLUMN public.rag_documents.embedding_dim IS
    'Embedding vector width in effect when this document was embedded. Pinned to 1024 to match rag_chunks.embedding vector(1024); changing the dimension is itself a schema migration, not a data update.';

-- Documents grouped by embedding source, e.g. to find rows needing
-- re-embedding after a provider swap.
CREATE INDEX IF NOT EXISTS rag_documents_embedding_model_idx
    ON public.rag_documents (embedding_model);

COMMIT;
