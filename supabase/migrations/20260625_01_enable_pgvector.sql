-- Enable the pgvector extension for the enterprise edge profile.
-- This migration is idempotent: IF NOT EXISTS means it is safe to run
-- on hosted Supabase (where the extension may already be present) and
-- on the self-hosted Postgres that ships with the enterprise compose profile.
--
-- Required by: RAG vector storage (#232), HNSW index on rag_chunks.embedding.
-- Dependency: Postgres image must include the vector extension library.
-- The enterprise compose profile uses pgvector/pgvector:pg16 which ships
-- the extension pre-installed.

CREATE EXTENSION IF NOT EXISTS vector;
