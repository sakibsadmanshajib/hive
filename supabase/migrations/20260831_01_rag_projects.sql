-- supabase/migrations/20260831_01_rag_projects.sql
--
-- Projects, the store half (issue #1595, spec task 1).
--
-- A project is a named, durable, attachable retrieval context over documents
-- the tenant already owns. Issue #1595 settles that Projects resolves to
-- Hive's own RAG store (public.rag_documents / public.rag_chunks) rather than
-- to the forked Open WebUI knowledge collections, which is what D-044 requires
-- (Open WebUI is a view; state belongs to the control plane) and what lets
-- Work mode retrieve in process instead of calling the view's HTTP API.
--
-- Nothing reads these columns yet. This migration is additive only: every
-- existing document keeps project_id NULL and every existing query, all of
-- which are tenant scoped and project blind, behaves exactly as before.
--
-- Ownership model, and why it needs an application-layer check as well as RLS.
-- A project is owned by one user within one tenant (issue #1595 section 14; per
-- group sharing is explicitly out of scope). RLS here is keyed on
-- app.current_tenant_id, exactly like public.rag_documents, so it stops a
-- cross-TENANT read. It cannot stop a cross-MEMBER read, because two members of
-- the same tenant present the same app.current_tenant_id. The project_id on a
-- retrieval request arrives from the client, so owner_user_id must be verified
-- in Go before any project scoped filtering happens. That check lives in
-- apps/edge-api/internal/rag.Repo.GetProject plus its callers, and the
-- refusal is proven by test rather than assumed.
--
-- Depends on: 20260625_05_carl_rag.sql (public.rag_documents)
--             20260516_01_phase19_tenants.sql (public.tenants)

BEGIN;

CREATE TABLE IF NOT EXISTS public.rag_projects (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Cascade on tenant deletion, matching public.rag_documents.
    tenant_id      UUID        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    -- No ON DELETE action, matching public.agent_tasks.user_id: a project is
    -- customer content, so removing the owning account must fail loudly rather
    -- than silently discard the project and orphan its documents.
    owner_user_id  UUID        NOT NULL REFERENCES auth.users(id),
    name           TEXT        NOT NULL CHECK (length(btrim(name)) > 0),
    -- Per project instructions (issue #380's other half, issue #1595 section 8).
    -- User authored, therefore trusted, and injected ahead of the retrieved
    -- passages rather than inside their untrusted-data block. Read fresh at
    -- request time, never copied into a conversation's own system field, so an
    -- edit applies to conversations that already exist.
    instructions   TEXT        NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE public.rag_projects IS
  'A named retrieval context over public.rag_documents rows (issue #1595). Owned by one user within one tenant: RLS covers the tenant boundary, and owner_user_id is enforced at the application layer because two members of one tenant present the same app.current_tenant_id. Attachable to an ordinary chat and to Work mode alike; it is not a destination.';

COMMENT ON COLUMN public.rag_projects.owner_user_id IS
  'The project owner. Checked in Go before any project scoped retrieval, because project_id arrives from the client and RLS is tenant scoped rather than user scoped.';

-- Documents join a project by pointing at it. SET NULL rather than CASCADE:
-- deleting a project must not silently destroy the tenant's uploaded
-- documents, which stay in the store unscoped and re-attachable.
ALTER TABLE public.rag_documents
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES public.rag_projects(id) ON DELETE SET NULL;

COMMENT ON COLUMN public.rag_documents.project_id IS
  'The project this document belongs to, or NULL for a document uploaded straight through the API with no project. Never trusted from a client on its own: the caller must be proven to own the project first.';

-- Owner scoped listing, which is the only project read the surface performs.
CREATE INDEX IF NOT EXISTS rag_projects_tenant_owner_created_idx
    ON public.rag_projects (tenant_id, owner_user_id, created_at DESC);

-- Project scoped retrieval joins rag_chunks to its document and filters on
-- this pair, so the pair is the index.
CREATE INDEX IF NOT EXISTS rag_documents_tenant_project_idx
    ON public.rag_documents (tenant_id, project_id);

ALTER TABLE public.rag_projects ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'rag_projects'
          AND policyname = 'rag_projects_tenant_isolation'
    ) THEN
        -- Same key and same PERMISSIVE FOR ALL TO hive_app shape as
        -- rag_documents_tenant_isolation in 20260625_05_carl_rag.sql. The one
        -- deliberate difference is NULLIF, carried over from
        -- 20260716_03_agent_tasks.sql: once a pooled connection has ever called
        -- set_config('app.current_tenant_id', ..., true), current_setting with
        -- the two-arg form can return '' rather than NULL after the LOCAL scope
        -- ends, and casting '' straight to uuid raises instead of cleanly
        -- denying. NULLIF folds that to NULL, which the comparison treats as
        -- false, so the policy fails closed.
        CREATE POLICY rag_projects_tenant_isolation
            ON public.rag_projects AS PERMISSIVE FOR ALL TO hive_app
            USING      (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
            WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
    END IF;
END$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.rag_projects TO hive_app;
GRANT SELECT ON public.rag_projects TO auditor_ro;

COMMIT;
