-- supabase/migrations/20260716_05_agent_tasks_service_scan.sql
--
-- The task status poller (issue #311 follow-up, SYNC_CONTRACT.md's Engine
-- seam section) needs to list active tasks across every tenant on an
-- interval.
--
-- CORRECTED after security review (2026-07-16): the first version of this
-- migration added a table-level PERMISSIVE SELECT policy
-- (`USING (true) TO hive_app`) alongside the existing tenant-isolation
-- policy. Postgres OR-combines PERMISSIVE policies for the same command, so
-- that policy did not just add a new cross-tenant read path — it CANCELLED
-- agent_tasks_tenant_isolation for every hive_app SELECT, including the
-- ordinary Get/List calls a customer request makes. A user in two or more
-- tenants would have been able to read other tenants' tasks through the
-- normal API. audit_log_service_role_all
-- (20260518_04_phase19_audit_rls_and_indexes.sql) is not a valid precedent
-- for this table: audit_log has no competing tenant-scoped hive_app policy
-- to cancel, agent_tasks does.
--
-- Fix: a SECURITY DEFINER function is the poller's ONLY cross-tenant read
-- path. It runs with the function owner's privileges (the same role that
-- ran this migration, which owns public.agent_tasks and was never given
-- FORCE ROW LEVEL SECURITY), so it bypasses RLS internally without any
-- table-level policy change at all — agent_tasks_tenant_isolation is
-- completely untouched, exactly as it was before this migration. Mirrors
-- 20260516_07_phase19_custom_access_token_hook.sql's
-- custom_access_token_hook, this repo's existing precedent for "a narrow,
-- unavoidably cross-tenant read inside a function, not a table policy".
--
-- The function returns the exact column list and order
-- apps/control-plane/internal/agenttask's scanTask expects (mirroring the
-- explicit column lists the rest of repository.go already uses, never
-- `SELECT *`), is capped at 500 rows per call (a batch limit, not a hard
-- correctness bound — a tenant with more than 500 simultaneously active
-- tasks needs a paged poller, tracked as a follow-up if that ever happens),
-- and is backed by a partial index matching its WHERE clause exactly.
--
-- Depends on: 20260716_03_agent_tasks.sql, 20260716_04_agent_tasks_instructions.sql.

BEGIN;

CREATE INDEX agent_tasks_active_idx
    ON public.agent_tasks (created_at)
    WHERE status IN ('queued', 'running') AND engine_session_ref <> '';

CREATE OR REPLACE FUNCTION public.agent_tasks_list_active()
RETURNS TABLE (
    id                 UUID,
    tenant_id          UUID,
    user_id            UUID,
    pack               TEXT,
    instructions       TEXT,
    status             TEXT,
    engine_session_ref TEXT,
    result_summary_ref TEXT,
    error_message      TEXT,
    created_at         TIMESTAMPTZ,
    updated_at         TIMESTAMPTZ,
    started_at         TIMESTAMPTZ,
    finished_at        TIMESTAMPTZ
)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = ''
AS $$
    SELECT t.id, t.tenant_id, t.user_id, t.pack, COALESCE(t.instructions, ''),
           t.status, t.engine_session_ref, t.result_summary_ref, t.error_message,
           t.created_at, t.updated_at, t.started_at, t.finished_at
      FROM public.agent_tasks t
     WHERE t.status IN ('queued', 'running') AND t.engine_session_ref <> ''
     ORDER BY t.created_at ASC
     LIMIT 500;
$$;

COMMENT ON FUNCTION public.agent_tasks_list_active() IS
  'Poller-only cross-tenant read (issue #311 follow-up). SECURITY DEFINER bypasses RLS internally for exactly this one fixed query; agent_tasks_tenant_isolation is never weakened at the table level. See apps/control-plane/internal/agenttask/poller.go.';

GRANT EXECUTE ON FUNCTION public.agent_tasks_list_active() TO hive_app;

COMMIT;
