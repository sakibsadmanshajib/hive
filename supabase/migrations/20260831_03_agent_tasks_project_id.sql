-- supabase/migrations/20260831_03_agent_tasks_project_id.sql
--
-- Records which project a Work mode run consulted (issue #1595 spec task 8,
-- advancing issue #1312).
--
-- Nullable, because most tasks carry no project, and SET NULL rather than
-- CASCADE on the project's deletion: public.agent_tasks is append-only status
-- history (see 20260716_03_agent_tasks.sql, which grants no DELETE to
-- hive_app), so deleting a project must not delete the record that a run
-- happened.
--
-- Said precisely, because "append-only" oversells what survives. The
-- referential action is an UPDATE against these rows, and referential actions
-- run with the table owner's privileges and bypass row security, so it succeeds
-- regardless of hive_app holding no DELETE and regardless of
-- agent_tasks_tenant_isolation. The row survives; the provenance does not. What
-- is erased is the one fact this column exists to record, which project a run
-- consulted. That is the accepted trade here: RESTRICT would refuse to delete
-- any project a run had ever referenced, which is poor product behaviour, and
-- snapshotting a project_name at insert is a bigger change than this slice.
-- Whoever needs durable provenance should snapshot the name, and should not
-- assume this column still answers the question after a delete.
--
-- The pack CHECK constraint is untouched. Issue #1595 keeps the wire
-- identifier knowledge-work-pack exactly as it is: no customer reads it,
-- renaming it would need this constraint changed plus a validator change, and
-- what the issue removes is the words "Knowledge work" from the surface, not
-- the pack value.
--
-- Authorization note. This column is written from a client supplied project_id,
-- so the value is only ever persisted after the caller has been proven to own
-- that project. The check runs in edge-api before control-plane is called at
-- all; see apps/edge-api/internal/agenttask.Handler.handleCreate and the
-- cross-member and cross-tenant refusal tests beside it.
--
-- Depends on: 20260716_03_agent_tasks.sql (public.agent_tasks)
--             20260831_01_rag_projects.sql (public.rag_projects)

BEGIN;

ALTER TABLE public.agent_tasks
    ADD COLUMN IF NOT EXISTS project_id UUID;

-- Same composite reference and the same reasoning as rag_documents in
-- 20260831_01: (tenant_id, project_id) rather than project_id alone, so a task
-- row in tenant A cannot name a project in tenant B whatever writes the column,
-- and the ON DELETE column list so nulling the key does not try to null the NOT
-- NULL tenant_id. The unique constraint it references is created there.
ALTER TABLE public.agent_tasks
    DROP CONSTRAINT IF EXISTS agent_tasks_project_id_fkey;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'agent_tasks_project_same_tenant_fkey'
          AND conrelid = 'public.agent_tasks'::regclass
    ) THEN
        ALTER TABLE public.agent_tasks
            ADD CONSTRAINT agent_tasks_project_same_tenant_fkey
            FOREIGN KEY (tenant_id, project_id)
            REFERENCES public.rag_projects (tenant_id, id)
            ON DELETE SET NULL (project_id);
    END IF;
END$$;

-- Index the referencing key. Without it, deleting a project runs the integrity
-- check as SELECT ... FROM public.agent_tasks WHERE tenant_id = $1 AND
-- project_id = $2 FOR KEY SHARE with nothing to use: a sequential scan plus a
-- row lock pass over an append-only table that only grows, holding locks
-- against concurrent status transitions on unrelated tasks, on a path any
-- customer can trigger through DELETE /v1/rag/projects/{id}. Partial because
-- nearly every row is NULL and project_id = $2 implies project_id IS NOT NULL,
-- which the planner can prove.
CREATE INDEX IF NOT EXISTS agent_tasks_project_tenant_idx
    ON public.agent_tasks (project_id, tenant_id)
    WHERE project_id IS NOT NULL;

COMMENT ON COLUMN public.agent_tasks.project_id IS
  'The project whose passages were retrieved for this run, or NULL for a run with no project attached, or NULL because the project was later deleted (ON DELETE SET NULL): this records provenance while the project exists and does not outlive it. Written only after edge-api has verified the submitting user owns the project, because the value arrives from the client.';

COMMIT;
