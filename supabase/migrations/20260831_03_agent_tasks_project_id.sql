-- supabase/migrations/20260831_03_agent_tasks_project_id.sql
--
-- Records which project a Work mode run consulted (issue #1595 spec task 8,
-- advancing issue #1312).
--
-- Nullable, because most tasks carry no project, and SET NULL rather than
-- CASCADE on the project's deletion: public.agent_tasks is append-only status
-- history (see 20260716_03_agent_tasks.sql, which grants no DELETE to
-- hive_app), so deleting a project must not delete the record that a run
-- happened. The row keeps its own history and simply stops naming a project
-- that no longer exists.
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
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES public.rag_projects(id) ON DELETE SET NULL;

COMMENT ON COLUMN public.agent_tasks.project_id IS
  'The project whose passages were retrieved for this run, or NULL for a run with no project attached. Written only after edge-api has verified the submitting user owns the project, because the value arrives from the client.';

COMMIT;
