-- supabase/migrations/20260823_01_user_memories.sql
--
-- Cross-chat user memories, slice one (issue #172, ruling D-020): a thin,
-- Hive-owned store for short facts about a person that chat recall injects
-- into the system prompt across conversations. One subsystem per D-020; the
-- four-verb API (create/list/update/delete) lives in
-- apps/control-plane/internal/usermemories and the recall injection in the
-- edge-api chat dispatch path (apps/edge-api/internal/chat/memory.go).
-- Automatic extraction from conversations is a later wave; this table is
-- written only through the explicit API.
--
-- RLS-scoped exactly like public.agent_tasks (20260716_03_agent_tasks.sql):
-- hive_app is NOT BYPASSRLS (20260518_04_phase19_audit_rls_and_indexes.sql),
-- so every query needs app.current_tenant_id set LOCAL inside an explicit
-- transaction (withTenantTx in both repositories). User-level scoping (a
-- memory belongs to the one person who created it) is enforced at the
-- application layer via an explicit user_id filter on every query (the same
-- split agent_tasks uses), so a later shared view needs no schema change.
--
-- Bounds live in both layers: the CHECK below caps content at 500 chars at
-- the storage boundary, and the control-plane service additionally trims
-- whitespace, strips control characters, and evicts the oldest row beyond
-- 100 memories per (tenant, user).
--
-- Depends on: 20260516_01_phase19_tenants.sql (public.tenants).

BEGIN;

CREATE TABLE public.user_memories (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    user_id        UUID        NOT NULL REFERENCES auth.users(id),
    content        TEXT        NOT NULL
                               CHECK (char_length(content) BETWEEN 1 AND 500),
    source_chat_id TEXT        NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE public.user_memories IS
  'Cross-chat user memories (issue #172, ruling D-020). Short single-line facts about one person, written only through the four-verb internal API; recall injection reads the most recent rows into the chat system prompt. content is capped at 500 chars by CHECK plus service-layer sanitization (control characters stripped). source_chat_id is the optional originating conversation reference, never a prompt body. No RLS-level user scoping: user_id is filtered at the application layer, same split as public.agent_tasks.';

CREATE INDEX user_memories_tenant_user_created_idx
    ON public.user_memories (tenant_id, user_id, created_at DESC);

ALTER TABLE public.user_memories ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'user_memories'
          AND policyname = 'user_memories_tenant_isolation'
    ) THEN
        -- NULLIF(..., '') guards the same Postgres GUC placeholder quirk
        -- documented in 20260716_03_agent_tasks.sql: once a pooled connection
        -- has ever called set_config('app.current_tenant_id', ..., true),
        -- current_setting(name, true) can return '' rather than NULL after
        -- the LOCAL scope ends. Casting '' straight to ::uuid raises instead
        -- of cleanly filtering rows; NULLIF folds that case to NULL, which
        -- the comparison then treats as false, so the default is deny.
        CREATE POLICY user_memories_tenant_isolation
            ON public.user_memories AS PERMISSIVE FOR ALL TO hive_app
            USING      (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
            WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
    END IF;
END$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.user_memories TO hive_app;
GRANT SELECT ON public.user_memories TO auditor_ro;

COMMIT;
