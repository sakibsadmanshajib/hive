-- supabase/migrations/20260823_01_agent_task_events.sql
--
-- Agent task event log (D-045 agent-surface rebuild, Wave 3). One row per
-- sandbox lifecycle/tool-call/message/file event, appended by
-- apps/control-plane/internal/agenttask's eventsync syncer and served to
-- customers through /v1/agent/tasks/{id}/events. Append-only by grant: no
-- UPDATE, no DELETE for hive_app, mirroring public.agent_tasks' posture.
--
-- RLS-scoped like public.agent_tasks (20260716_03_agent_tasks.sql): hive_app
-- is NOT BYPASSRLS (20260518_04_phase19_audit_rls_and_indexes.sql), so every
-- query runs inside withTenantTx with app.current_tenant_id set LOCAL. User
-- scoping stays at the application layer via an explicit user_id check on the
-- parent task before any event read, same as the task table.
--
-- Depends on: 20260716_03_agent_tasks.sql (public.agent_tasks).

BEGIN;

CREATE TABLE public.agent_task_events (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id         UUID NOT NULL REFERENCES public.agent_tasks(id) ON DELETE CASCADE,
    seq             BIGINT NOT NULL,
    source_event_id TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL CHECK (kind IN
                      ('status','tool_call','tool_result','message','error','file')),
    payload         JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, seq)
);

COMMENT ON TABLE public.agent_task_events IS
  'Append-only event log per agent task (D-045 Wave 3). seq is per-task monotonic, assigned by the syncer inside withTenantTx. source_event_id is the sandbox event id (or a deterministic synthetic id for status/file events) and the partial unique index on it makes a launcher restart degrade to missed events, never duplicated ones. No UPDATE/DELETE grant: rows are never rewritten.';

-- Dedup: a syncer restart or a launcher restart re-pulls the same sandbox
-- events; this partial unique index (empty source_event_id is exempt, though
-- every current writer always sets it) turns the re-insert into a no-op via
-- ON CONFLICT ... DO NOTHING.
CREATE UNIQUE INDEX agent_task_events_source_dedup
    ON public.agent_task_events (task_id, source_event_id) WHERE source_event_id <> '';

-- The read path: /v1/agent/tasks/{id}/events?after_seq=N&limit=M resolves to
-- WHERE task_id = $1 AND seq > $2 ORDER BY seq LIMIT $3, which this index
-- serves directly.
CREATE INDEX agent_task_events_read_idx ON public.agent_task_events (task_id, seq);

ALTER TABLE public.agent_task_events ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'agent_task_events'
          AND policyname = 'agent_task_events_tenant_isolation'
    ) THEN
        -- NULLIF(..., '') guards the same Postgres GUC placeholder quirk
        -- 20260716_03 documents: once a pooled connection has ever called
        -- set_config('app.current_tenant_id', ..., true), current_setting can
        -- return '' after the LOCAL scope ends; casting '' to ::uuid raises,
        -- NULLIF folds it to NULL and the comparison denies by default.
        CREATE POLICY agent_task_events_tenant_isolation
            ON public.agent_task_events AS PERMISSIVE FOR ALL TO hive_app
            -- The event row carries no tenant_id of its own; scoping goes
            -- through the parent task, whose own RLS sees the same
            -- app.current_tenant_id inside policy evaluation.
            USING      (task_id IN (SELECT id FROM public.agent_tasks
                                    WHERE tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid))
            WITH CHECK (task_id IN (SELECT id FROM public.agent_tasks
                                    WHERE tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid));
    END IF;
END$$;

-- Append-only grants: SELECT to read through the customer/internal read
-- routes, INSERT for the syncer. No UPDATE, no DELETE: an event row is never
-- rewritten (mirrors public.agent_tasks' no-DELETE posture, one step
-- stricter).
GRANT SELECT, INSERT ON public.agent_task_events TO hive_app;
GRANT SELECT ON public.agent_task_events TO auditor_ro;

COMMIT;
