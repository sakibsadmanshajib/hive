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
    source_event_id TEXT NOT NULL DEFAULT ''
                    CHECK (char_length(source_event_id) <= 512),
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

-- The cursor read (/events?after_seq=N&limit=M resolves to WHERE task_id = $1
-- AND seq > $2 ORDER BY seq LIMIT $3) needs NO index of its own: the implicit
-- btree of the UNIQUE (task_id, seq) constraint above already serves it,
-- equality column first, range second, index-ordered output. A dedicated
-- (task_id, seq) index would duplicate that and double write amplification on
-- an append-heavy table.

-- Retention support: the purge job scans by age, so it needs this plain btree
-- or every nightly run sequential-scans the table.
CREATE INDEX agent_task_events_created_at_idx ON public.agent_task_events (created_at);

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

-- INSERT on the table is not enough: the IDENTITY column draws its values from
-- this sequence, and hive_app is not the owner, so every append fails with
-- "permission denied for sequence agent_task_events_id_seq" without these.
-- Documented failure shape: 20260516_04_phase19_audit_log.sql lines 69-72,
-- same grant there.
GRANT USAGE, SELECT ON SEQUENCE public.agent_task_events_id_seq TO hive_app;

-- =============================================================================
-- Retention (folded in at review rather than left as a fast-follow). Rows carry
-- up to 64 KiB payloads plus sandbox dumps, so growth is bounded by this job,
-- following 20260729_02_metering_shadow_verdicts_retention.sql's shape exactly:
-- one-row configurable window, batched committed deletes over created_at
-- (indexed above), pg_cron schedule behind guards that degrade to a NOTICE when
-- pg_cron is absent, re-raising only for a superuser that could have succeeded
-- (the 20260822_01 asymmetry, issue #615/#645). An operator without pg_cron
-- invokes CALL public.purge_agent_task_events() from an external scheduler.
--
-- Window default: 90 days, the same disk-cost call as the metering verdicts.
-- These rows DO carry transcript/tool content, so an Enterprise operator with
-- stricter audit rules lowers it with a plain UPDATE, no migration:
--   UPDATE public.agent_task_events_retention_config SET retention_days = 30;
-- =============================================================================

CREATE TABLE IF NOT EXISTS public.agent_task_events_retention_config (
    id             smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1), -- singleton row
    retention_days integer NOT NULL DEFAULT 90 CHECK (retention_days > 0)
);
COMMENT ON TABLE public.agent_task_events_retention_config IS
  'Single-row config for the nightly agent_task_events purge job. UPDATE '
  'retention_days directly to change the window -- no migration needed. '
  'Default 90 days; Enterprise self-hosted operators set their own value per '
  'their audit rules.';

INSERT INTO public.agent_task_events_retention_config (id, retention_days)
VALUES (1, 90)
ON CONFLICT (id) DO NOTHING;

-- search_path pinned in the body, not a SET clause on CREATE PROCEDURE: the
-- SET-clause wrapper sub-transaction rejects the internal COMMIT ("invalid
-- transaction termination"), the trap 20260729_02 documents from a live
-- container.
CREATE OR REPLACE PROCEDURE public.purge_agent_task_events(batch_size integer DEFAULT 500)
LANGUAGE plpgsql
AS $$
DECLARE
    window_days   integer;
    deleted_count integer;
BEGIN
    SET search_path = pg_catalog, pg_temp;
    SET lock_timeout = '5s';

    SELECT retention_days INTO window_days
    FROM public.agent_task_events_retention_config
    WHERE id = 1;
    window_days := COALESCE(window_days, 90);

    LOOP
        DELETE FROM public.agent_task_events
        WHERE id IN (
            SELECT id FROM public.agent_task_events
            WHERE created_at < now() - (window_days || ' days')::interval
            ORDER BY created_at
            LIMIT batch_size
        );
        GET DIAGNOSTICS deleted_count = ROW_COUNT;
        COMMIT;
        EXIT WHEN deleted_count < batch_size;
    END LOOP;
END;
$$;
COMMENT ON PROCEDURE public.purge_agent_task_events(integer) IS
  'Nightly batched retention delete for agent_task_events (D-045 Wave 3). '
  'Runs as a loop of bounded DELETEs, each its own committed transaction, so '
  'no single lock is held for the full purge. Window comes from '
  'agent_task_events_retention_config, not a literal here.';

DO $pg_cron_setup$
DECLARE
    pg_cron_available boolean;
    is_super          boolean;
BEGIN
    SELECT EXISTS (
        SELECT 1 FROM pg_available_extensions WHERE name = 'pg_cron'
    ) INTO pg_cron_available;
    SELECT rolsuper INTO is_super FROM pg_roles WHERE rolname = current_user;

    IF pg_cron_available THEN
        BEGIN
            EXECUTE 'CREATE EXTENSION IF NOT EXISTS pg_cron';
        EXCEPTION WHEN OTHERS THEN
            IF is_super THEN
                RAISE; -- superuser: nothing else can fix this, so fail loudly
            END IF;
            RAISE NOTICE 'pg_cron is available but CREATE EXTENSION failed as non-superuser % (%): no nightly agent-task-event retention job is scheduled by this migration. Schedule it as a role that can create the extension, or invoke CALL public.purge_agent_task_events() from an external scheduler.', current_user, SQLERRM;
        END;
    ELSE
        RAISE NOTICE 'pg_cron is not available on this Postgres install, so no nightly agent-task-event retention job is scheduled here. Invoke CALL public.purge_agent_task_events() from an external scheduler instead. Expected on CI throwaway databases.';
    END IF;
END;
$pg_cron_setup$;

DO $pg_cron_schedule$
DECLARE
    is_super boolean;
BEGIN
    SELECT rolsuper INTO is_super FROM pg_roles WHERE rolname = current_user;

    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron')
       AND EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'pg_cron') THEN
        BEGIN
            PERFORM cron.schedule(
                'agent-task-events-purge',
                '0 21 * * *', -- 21:00 UTC = 03:00 Asia/Dhaka, lowest traffic
                $sched$CALL public.purge_agent_task_events();$sched$
            );
        EXCEPTION WHEN OTHERS THEN
            IF is_super THEN
                RAISE;
            END IF;
            RAISE NOTICE 'cron.schedule failed as non-superuser % (%): no nightly agent-task-event retention job is scheduled by this migration.', current_user, SQLERRM;
        END;
    END IF;
END;
$pg_cron_schedule$;

COMMIT;
