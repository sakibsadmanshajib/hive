-- supabase/migrations/20260823_02_agent_task_schedules.sql
--
-- Scheduled agent tasks (routines): a user-defined recurring prompt that the
-- control-plane scheduler turns into a real agent_tasks row on its cadence,
-- through the same Service.CreateTask path a manual creation uses so
-- metering/quota/gating apply identically. First slice: fixed cadences only
-- (daily, weekly, interval:N hours), no cron-expression UX.
--
-- RLS mirrors public.agent_tasks (20260716_03_agent_tasks.sql): hive_app is
-- NOT BYPASSRLS, tenant scoping is enforced by the agent_task_schedules_tenant_
-- isolation policy via app.current_tenant_id, and user-level scoping (a
-- schedule is only listed/edited/deleted by its own user) is an explicit
-- user_id filter at the application layer, same rationale as agent_tasks.
-- Unlike agent_tasks, rows are user-managed state, not append-only history:
-- hive_app gets DELETE.
--
-- The scheduler's cross-tenant claim query lives in the SECURITY DEFINER
-- function agent_task_schedules_claim_due (bottom of this file), NOT in a
-- table policy, for exactly the reason 20260716_05_agent_tasks_service_scan.sql
-- documents: a blanket cross-tenant policy would OR-combine with the tenant
-- isolation policy and cancel it for every ordinary hive_app read.
--
-- Depends on: 20260516_01_phase19_tenants.sql (public.tenants),
-- 20260716_03_agent_tasks.sql (public.agent_tasks).

BEGIN;

CREATE TABLE public.agent_task_schedules (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    user_id      UUID        NOT NULL REFERENCES auth.users(id),
    name         TEXT        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    instructions TEXT        NOT NULL CHECK (char_length(instructions) BETWEEN 1 AND 4000),
    -- Restricted cadence grammar, first slice: two presets plus a bounded
    -- hourly interval. The regex bounds N to 1..168 (one hour to one week)
    -- so a typo like interval:0 or interval:99999 can neither hot-loop the
    -- scheduler nor silently park a schedule forever.
    schedule     TEXT        NOT NULL
                             CHECK (schedule ~ '^(daily|weekly|interval:(16[0-8]|1[0-5][0-9]|[1-9][0-9]|[1-9]))$'),
    enabled      BOOLEAN     NOT NULL DEFAULT true,
    next_run_at  TIMESTAMPTZ,
    last_run_at  TIMESTAMPTZ,
    last_task_id UUID        REFERENCES public.agent_tasks(id) ON DELETE SET NULL,
    last_error   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE public.agent_task_schedules IS
  'Scheduled agent tasks (routines), first slice. schedule holds a fixed cadence: daily, weekly, or interval:N (N hours, 1..168). The control-plane scheduler claims due rows through agent_task_schedules_claim_due (FOR UPDATE SKIP LOCKED, next_run_at advanced at claim time so a tick can never double-fire) and creates one agent_tasks row per run through the same service path as a manual task, so scheduled runs consume credits exactly like manual ones. last_error carries a provider-blind message from the most recent failed run attempt.';

CREATE INDEX agent_task_schedules_due_idx
    ON public.agent_task_schedules (next_run_at)
    WHERE enabled;

ALTER TABLE public.agent_task_schedules ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'agent_task_schedules'
          AND policyname = 'agent_task_schedules_tenant_isolation'
    ) THEN
        -- NULLIF(..., '') guards the Postgres GUC placeholder quirk documented
        -- in 20260716_03_agent_tasks.sql: a pooled connection can return ''
        -- from current_setting(name, true) after the LOCAL scope ends, and
        -- casting '' to ::uuid raises instead of denying. NULLIF folds it to
        -- NULL, which compares false: deny by default.
        CREATE POLICY agent_task_schedules_tenant_isolation
            ON public.agent_task_schedules AS PERMISSIVE FOR ALL TO hive_app
            USING      (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
            WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
    END IF;
END$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.agent_task_schedules TO hive_app;
GRANT SELECT ON public.agent_task_schedules TO auditor_ro;

-- The scheduler loop's one cross-tenant statement. Claims up to p_batch due
-- enabled schedules atomically: the data-modifying CTE locks the matched rows
-- (FOR UPDATE SKIP LOCKED, so two concurrent control-plane instances claim
-- disjoint sets) and advances next_run_at in the SAME statement that returns
-- the rows, which is what makes a tick idempotent — once claimed, the row no
-- longer matches "due" and a concurrent or retried tick cannot fire it again.
-- The advance happens at claim time even if the task create that follows
-- fails; the failure path records last_error and the schedule simply waits
-- out its (already advanced) next cadence instead of hot-looping.
--
-- SECURITY DEFINER for the same reason agent_tasks_list_active() is
-- (20260716_05): this must see every tenant's rows and a table-level policy
-- granting that would cancel agent_task_schedules_tenant_isolation. The
-- function is the ONLY cross-tenant path; the returned column list and order
-- is exactly what apps/control-plane/internal/agentsched's scanSchedule
-- expects.
CREATE OR REPLACE FUNCTION public.agent_task_schedules_claim_due(
    p_now   TIMESTAMPTZ,
    p_batch INTEGER
)
RETURNS TABLE (
    id           UUID,
    tenant_id    UUID,
    user_id      UUID,
    name         TEXT,
    instructions TEXT,
    schedule     TEXT,
    enabled      BOOLEAN,
    next_run_at  TIMESTAMPTZ,
    last_run_at  TIMESTAMPTZ,
    last_task_id UUID,
    last_error   TEXT,
    created_at   TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ
)
LANGUAGE sql VOLATILE
SECURITY DEFINER
SET search_path = ''
AS $$
    WITH due AS (
        SELECT s.id
          FROM public.agent_task_schedules s
         WHERE s.enabled AND s.next_run_at <= p_now
         ORDER BY s.next_run_at ASC
         LIMIT GREATEST(COALESCE(p_batch, 1), 1)
         FOR UPDATE SKIP LOCKED
    ),
    claimed AS (
        UPDATE public.agent_task_schedules s
           SET next_run_at = CASE s.schedule
                                 WHEN 'daily'   THEN p_now + INTERVAL '24 hours'
                                 WHEN 'weekly'  THEN p_now + INTERVAL '7 days'
                                 ELSE p_now + make_interval(hours => substring(s.schedule FROM 'interval:(\d+)')::int)
                             END,
               last_run_at = p_now,
               last_error  = '',
               updated_at  = p_now
          FROM due
         WHERE s.id = due.id
        RETURNING s.id, s.tenant_id, s.user_id, s.name, s.instructions, s.schedule,
                  s.enabled, s.next_run_at, s.last_run_at, s.last_task_id, s.last_error,
                  s.created_at, s.updated_at
    )
    SELECT c.id, c.tenant_id, c.user_id, c.name, c.instructions, c.schedule,
           c.enabled, c.next_run_at, c.last_run_at, c.last_task_id, c.last_error,
           c.created_at, c.updated_at
      FROM claimed c;
$$;

COMMENT ON FUNCTION public.agent_task_schedules_claim_due(TIMESTAMPTZ, INTEGER) IS
  'Scheduler-only cross-tenant claim (scheduled agent tasks, first slice). SECURITY DEFINER bypasses RLS internally for this one fixed statement; agent_task_schedules_tenant_isolation is never weakened at the table level. Advances next_run_at at claim time so each tick fires a schedule at most once. See apps/control-plane/internal/agentsched/scheduler.go.';

-- Postgres grants EXECUTE on every new function to PUBLIC by default; the
-- explicit hive_app grant below does not remove that. Without this revoke,
-- Supabase's anon/authenticated roles could call this SECURITY DEFINER
-- function directly through PostgREST's RPC surface (the anon key is
-- public): a cross-tenant read of every tenant's schedule rows plus a
-- starvation DoS, each call claiming due rows and advancing next_run_at
-- without ever creating a task.
REVOKE ALL ON FUNCTION public.agent_task_schedules_claim_due(TIMESTAMPTZ, INTEGER) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.agent_task_schedules_claim_due(TIMESTAMPTZ, INTEGER) TO hive_app;

-- Same default-PUBLIC grant hole, closed here for the poller's sibling
-- SECURITY DEFINER function rather than editing its already-applied
-- migration (20260716_05_agent_tasks_service_scan.sql): an edit there would
-- do nothing on databases that have already run it.
REVOKE ALL ON FUNCTION public.agent_tasks_list_active() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.agent_tasks_list_active() TO hive_app;

COMMIT;
