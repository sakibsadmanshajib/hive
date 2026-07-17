-- supabase/migrations/20260717_03_rag_embedding_dim_drop_check.sql
--
-- Dimension-agnostic RAG embedding foundation (admin-controlled embedding
-- redesign, PR1). 20260715_01_rag_embedding_provenance.sql pinned
-- rag_documents.embedding_dim to CHECK (embedding_dim = 1024), matching the
-- one dimension the Go code assumed at the time. That code now reads its
-- expected width from EMBEDDING_DIM (see apps/edge-api/internal/rag/types.go
-- and apps/control-plane/internal/rag/chunk.go), so a deployment configured
-- for a different embedding model must be able to write a different value
-- into this column without a schema change.
--
-- Scope: this migration only drops the CHECK constraint. It does NOT alter
-- rag_chunks.embedding's column type (still vector(1024)) and does NOT
-- backfill or re-embed any row -- resizing the live vector column and
-- migrating existing documents onto a new model is the re-embed job (PR2),
-- not built here. Until that job exists, running with EMBEDDING_DIM != 1024
-- will surface as a pgvector dimension-mismatch error on insert, not a
-- graceful application-level reject; the Go-side embedmodel registry guard
-- (packages/embedmodel) exists to catch an inconsistent config before that
-- point, not to make the column itself elastic.
--
-- Depends on: 20260715_01_rag_embedding_provenance.sql (adds the column and
-- constraint this migration removes).

BEGIN;

ALTER TABLE public.rag_documents
    DROP CONSTRAINT IF EXISTS rag_documents_embedding_dim_check;

COMMENT ON COLUMN public.rag_documents.embedding_dim IS
    'Embedding vector width in effect when this document was embedded, taken from EMBEDDING_DIM at ingest time. No longer CHECKed to a fixed value (see 20260717_03): rag_chunks.embedding is still a fixed vector(1024) column today, so a value other than 1024 here means this document''s vectors do not fit that column until the re-embed job (PR2) resizes it and migrates the row.';

COMMIT;
