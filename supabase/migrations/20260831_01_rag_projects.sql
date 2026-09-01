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
-- Ownership model, and why the application layer carries both halves of it.
-- A project is owned by one user within one tenant (issue #1595 section 14; per
-- group sharing is explicitly out of scope).
--
-- The cross-TENANT half. The policy below is keyed on app.current_tenant_id,
-- exactly like public.rag_documents. It is correct, and it is inert on every
-- deployment this repository ships: the policy is granted TO hive_app, and the
-- services connect as postgres, which carries rolbypassrls, with no SET ROLE
-- anywhere under apps/ (issues #1469, #1444, #1446). So the tenant boundary in
-- production is the explicit tenant_id predicate every statement in
-- apps/edge-api/internal/rag/projects.go carries, plus the composite foreign
-- key below. The policy is kept for the day #1469 is fixed, and
-- projects_rls_test.go proves it under a CI-only SET ROLE rather than under
-- anything running.
--
-- The cross-MEMBER half. No tenant scoped mechanism can supply it: two members
-- of one tenant are the same tenant. The project_id on a retrieval request
-- arrives from the client, so owner_user_id is verified in Go before any
-- project scoped filtering happens. That check lives in
-- apps/edge-api/internal/rag.authorizeProjectOwnership and its callers, and the
-- refusal is proven by test rather than assumed.
--
-- What that check does NOT provide. public.rag_documents has no uploader
-- column, so a project's documents are readable by any member of the tenant
-- through an unscoped search, which is what POST /v1/rag/chat always issues.
-- The check refuses a project id, not a document: a project organises a
-- tenant's corpus, it does not hide part of it from the workspace. Recorded in
-- issue #1643 so the Projects surface does not describe a project as private
-- while that is true.
--
-- Depends on: 20260625_05_carl_rag.sql (public.rag_documents)
--             20260715_05_rag_rls_nullif_guard.sql (the current NULLIF-guarded
--                 rag_documents_tenant_isolation this policy copies)
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
  'A named retrieval context over public.rag_documents rows (issue #1595). Owned by one user within one tenant. The tenant boundary is the explicit tenant_id predicate every statement carries plus the composite foreign key on rag_documents; the RLS policy states the same rule but binds on hive_app, which no deployment connects as (issue #1469). owner_user_id is enforced in the application layer, because no tenant scoped mechanism can separate two members of one tenant. Attachable to an ordinary chat and to Work mode alike; it is not a destination.';

COMMENT ON COLUMN public.rag_projects.owner_user_id IS
  'The project owner. Checked in Go before any project scoped retrieval, because project_id arrives from the client and every database side guard here is tenant scoped rather than user scoped.';

-- Documents join a project by pointing at it. SET NULL rather than CASCADE:
-- deleting a project must not silently destroy the tenant's uploaded
-- documents, which stay in the store unscoped and re-attachable.
ALTER TABLE public.rag_documents
    ADD COLUMN IF NOT EXISTS project_id UUID;

-- The reference is (tenant_id, project_id) rather than project_id alone, so the
-- database refuses a document in tenant A pointing at a project in tenant B.
-- A single column reference constrains existence only, and referential
-- integrity checks bypass row security by design, so with one column the sole
-- thing holding that invariant would be the EXISTS clause inside
-- Repo.AttachDocument and any future writer of the column would reintroduce the
-- hole. Postgres 15 and later can express it; the stack is on pg17.
--
-- The column list on ON DELETE SET NULL is load bearing. Without it Postgres
-- nulls every column of the referencing key, and rag_documents.tenant_id is NOT
-- NULL, so every project delete would fail with a not-null violation.
--
-- MATCH SIMPLE (the default) means the constraint is not checked while
-- project_id IS NULL, which is exactly what an unattached document needs.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'rag_projects_tenant_id_id_key'
          AND conrelid = 'public.rag_projects'::regclass
    ) THEN
        ALTER TABLE public.rag_projects
            ADD CONSTRAINT rag_projects_tenant_id_id_key UNIQUE (tenant_id, id);
    END IF;
END$$;

-- Drops the single column constraint an earlier run of this file may have
-- created inline, so re-applying converges on the composite one.
ALTER TABLE public.rag_documents
    DROP CONSTRAINT IF EXISTS rag_documents_project_id_fkey;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'rag_documents_project_same_tenant_fkey'
          AND conrelid = 'public.rag_documents'::regclass
    ) THEN
        ALTER TABLE public.rag_documents
            ADD CONSTRAINT rag_documents_project_same_tenant_fkey
            FOREIGN KEY (tenant_id, project_id)
            REFERENCES public.rag_projects (tenant_id, id)
            ON DELETE SET NULL (project_id);
    END IF;
END$$;

COMMENT ON COLUMN public.rag_documents.project_id IS
  'The project this document belongs to, or NULL for a document uploaded straight through the API with no project. Never trusted from a client on its own: the caller must be proven to own the project first.';

-- Owner scoped listing, which is the only project read the surface performs.
CREATE INDEX IF NOT EXISTS rag_projects_tenant_owner_created_idx
    ON public.rag_projects (tenant_id, owner_user_id, created_at DESC);

-- project_id leads, and the index is partial, because the consumer that cannot
-- do without it is the referential action above: deleting a project runs
-- SELECT ... FROM public.rag_documents WHERE tenant_id = $1 AND project_id = $2
-- FOR KEY SHARE, and a customer triggered sequential scan plus row lock pass
-- over a growing table is not acceptable. A leading tenant_id could not answer
-- the single column form of that check at all, and Postgres 17 has no index
-- skip scan.
--
-- Nothing is lost by the order: rag_documents_tenant_idx already covers
-- filtering on tenant_id alone, and searchChunksQuery reaches the document by
-- primary key (d.id = c.document_id) and then filters project_id on the fetched
-- row, so it uses neither shape.
--
-- The partial predicate is free here: project_id = $n implies project_id IS NOT
-- NULL, which the planner can prove, and nearly every row is NULL.
DROP INDEX IF EXISTS public.rag_documents_tenant_project_idx;
CREATE INDEX IF NOT EXISTS rag_documents_project_tenant_idx
    ON public.rag_documents (project_id, tenant_id)
    WHERE project_id IS NOT NULL;

ALTER TABLE public.rag_projects ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'rag_projects'
          AND policyname = 'rag_projects_tenant_isolation'
    ) THEN
        -- Byte for byte the current rag_documents_tenant_isolation, as
        -- 20260715_05_rag_rls_nullif_guard.sql replaced it under issue #320.
        -- Same key, same PERMISSIVE FOR ALL TO hive_app shape, same NULLIF
        -- guard: rag_documents has carried the guard since July, so this is a
        -- copy of the live policy rather than a divergence from it. The reason
        -- the guard exists, restated because it is not obvious: once a pooled
        -- connection has ever called
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
