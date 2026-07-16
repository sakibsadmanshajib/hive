-- supabase/migrations/20260716_04_agent_tasks_instructions.sql
--
-- Adds the free-text instructions/prompt a task's conversation starts from
-- (issue #311 contract gap: agent_tasks had no way to tell the agent-engine
-- what to do). Nullable: a task created with no instructions is a valid
-- state (e.g. a workspace-only session), read back as '' at the application
-- layer via COALESCE rather than a Go *string, matching how the other
-- optional text columns on this table already read (engine_session_ref,
-- result_summary_ref, error_message) — the difference here is only that
-- those default to '' at the DB level and this one allows NULL, since a
-- blank vs. absent prompt is a meaningful distinction for future callers
-- even though today's read path collapses both to "".
--
-- Depends on: 20260716_03_agent_tasks.sql.

BEGIN;

ALTER TABLE public.agent_tasks
    ADD COLUMN instructions TEXT;

COMMENT ON COLUMN public.agent_tasks.instructions IS
  'Free-text prompt/goal the task''s conversation starts from (apps/agent-engine/internal/controlclient''s initial_message). NULL means no instructions were provided. Skills (issue #300) route on a "Skill: X" prefix inside this text.';

COMMIT;
