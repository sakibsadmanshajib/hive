-- supabase/migrations/20260716_03_agent_tasks.sql
--
-- Agent task persistence, web side (issue #311, agent-subsystem blueprint
-- Step 3.4). A task started from one web session must be visible and
-- resumable from another web session for the same user, tenant-scoped. This
-- table is the sync contract's server-side backing store; the desktop
-- consumer in Wave 4 attaches to the same rows.
--
-- RLS-scoped like public.marketplace_tenant_entries
-- (20260716_01_marketplace_catalog.sql) and public.egress_policies
-- (20260715_03_egress_policy_ssot.sql): hive_app is NOT BYPASSRLS
-- (20260518_04_phase19_audit_rls_and_indexes.sql), so every query needs
-- app.current_tenant_id set LOCAL inside an explicit transaction (see
-- agenttask.Repository.withTenantTx). User-level scoping (a task is only
-- listed/read/cancelled by the user who started it) is enforced at the
-- application layer via an explicit user_id filter on every query, not by
-- RLS — same tenant, different users, may need cross-user visibility later
-- (e.g. a workspace-admin task inbox) without a schema change.
--
-- Depends on: 20260516_01_phase19_tenants.sql (public.tenants).

BEGIN;

CREATE TABLE public.agent_tasks (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    user_id             UUID        NOT NULL REFERENCES auth.users(id),
    pack                TEXT        NOT NULL CHECK (pack IN ('coding-pack', 'knowledge-work-pack')),
    status              TEXT        NOT NULL DEFAULT 'queued'
                                     CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    engine_session_ref  TEXT        NOT NULL DEFAULT '',
    result_summary_ref  TEXT        NOT NULL DEFAULT '',
    error_message       TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ
);

COMMENT ON TABLE public.agent_tasks IS
  'Agent task persistence for web-started tasks (issue #311, Wave 3.4). status is a queued -> running -> {succeeded, failed} state machine, with cancelled reachable from queued or running. engine_session_ref is the apps/agent-engine launch reference once the task starts running; result_summary_ref points at the stored task output (shape owned by whichever consumer writes it, e.g. artifacts hosting #312). No RLS-level user scoping: user_id is filtered at the application layer so a later cross-user view (workspace task inbox) needs no migration.';

CREATE INDEX agent_tasks_tenant_user_created_idx
    ON public.agent_tasks (tenant_id, user_id, created_at DESC);

ALTER TABLE public.agent_tasks ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'agent_tasks'
          AND policyname = 'agent_tasks_tenant_isolation'
    ) THEN
        -- NULLIF(..., '') guards the same Postgres GUC placeholder quirk
        -- documented in 20260715_03_egress_policy_ssot.sql: once a pooled
        -- connection has ever called set_config('app.current_tenant_id', ...,
        -- true), current_setting(name, true) can return '' rather than NULL
        -- after the LOCAL scope ends. Casting '' straight to ::uuid raises
        -- instead of cleanly filtering rows; NULLIF folds that case to NULL,
        -- which the comparison then treats as false — deny by default.
        CREATE POLICY agent_tasks_tenant_isolation
            ON public.agent_tasks AS PERMISSIVE FOR ALL TO hive_app
            USING      (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
            WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
    END IF;
END$$;

-- No DELETE: task rows are append-only status history, never removed by the
-- app (mirrors public.audit_log's posture). hive_app only needs to create
-- rows and transition status.
GRANT SELECT, INSERT, UPDATE ON public.agent_tasks TO hive_app;
GRANT SELECT ON public.agent_tasks TO auditor_ro;

COMMIT;
