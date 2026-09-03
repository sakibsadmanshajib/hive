-- supabase/migrations/20260902_03_user_instructions.sql
--
-- Per-user custom instructions (issue #1363): the standing "how should the
-- assistant respond" text one person writes once and every one of their chat
-- turns is shaped by. Chat dispatch reads it alongside the recall block and
-- prepends it to the system messages of every request
-- (apps/edge-api/internal/chat/memory.go).
--
-- WHY THIS IS NOT public.user_memories
--
-- Issue #1363 proposed extending that table with a type discriminator, and it
-- was the right instinct (same tenant scoping, same injection point) but the
-- wrong shape once written out. user_memories is a LIST: many rows per user,
-- each at most 500 characters, capped at 100 with oldest-first eviction, and
-- written by extraction rather than by the person. Instructions are a
-- SINGLETON: exactly one row per (tenant, user), long, author-written, and
-- never evicted. Folding them together means every existing query in
-- apps/control-plane/internal/usermemories/repository.go grows a discriminator
-- filter, and the day one of them is missed a user's instructions are either
-- evicted by the fact cap or listed back to them as a "fact" they can delete
-- from the memory surface #1359 will build. A separate table needs no
-- discriminator on any query, so that class of defect cannot occur.
--
-- The primary key IS the singleton constraint: (tenant_id, user_id) with no
-- surrogate id, so the write path is an upsert and a second row is not
-- representable. This is deliberately not a scope column with a NULL for
-- "user level": per-project instructions (#380, #1595) are a different owner
-- and a different lifetime, and they get their own column on their own table
-- when that lands rather than a nullable foreign key sitting empty here.
--
-- RLS is copied verbatim from 20260823_01_user_memories.sql, including the
-- NULLIF guard on the GUC. hive_app is NOT BYPASSRLS
-- (20260518_04_phase19_audit_rls_and_indexes.sql), so every query needs
-- app.current_tenant_id set LOCAL inside an explicit transaction. User-level
-- scoping is the explicit user_id filter every query carries, the same
-- application-layer split public.agent_tasks and public.user_memories use.
--
-- Depends on: 20260516_01_phase19_tenants.sql (public.tenants).

BEGIN;

CREATE TABLE public.user_instructions (
    tenant_id  UUID        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL REFERENCES auth.users(id),
    -- 4000 characters is roughly a page and a half of prose, comfortably more
    -- than the standing preferences people actually write and far below any
    -- context budget it could crowd out. The control-plane-side cap in
    -- apps/edge-api/internal/userinstructions/store.go enforces the same
    -- number before the write reaches here, so this CHECK is the storage
    -- backstop rather than the user-facing error.
    content    TEXT        NOT NULL
                           CHECK (char_length(content) BETWEEN 1 AND 4000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id)
);

COMMENT ON TABLE public.user_instructions IS
  'Per-user custom instructions (issue #1363). Exactly one row per (tenant, user), enforced by the primary key: the standing instruction text that chat dispatch prepends to the system messages of every request for that person. Distinct from public.user_memories, which is a capped, evicted list of short extracted facts. content is capped at 4000 characters by CHECK plus the same cap applied at the edge-api write boundary, where control characters other than newline and tab are also stripped. Written only by the person it belongs to, through GET/PUT/DELETE /v1/user/instructions.';

ALTER TABLE public.user_instructions ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'user_instructions'
          AND policyname = 'user_instructions_tenant_isolation'
    ) THEN
        -- NULLIF(..., '') guards the Postgres GUC placeholder quirk documented
        -- at length in 20260716_03_agent_tasks.sql: once a pooled connection
        -- has ever called set_config('app.current_tenant_id', ..., true),
        -- current_setting(name, true) can return '' rather than NULL after the
        -- LOCAL scope ends, and casting '' straight to ::uuid raises instead of
        -- cleanly filtering rows. NULLIF folds that case to NULL, which the
        -- comparison treats as false, so the default is deny.
        CREATE POLICY user_instructions_tenant_isolation
            ON public.user_instructions AS PERMISSIVE FOR ALL TO hive_app
            USING      (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
            WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
    END IF;
END$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.user_instructions TO hive_app;
GRANT SELECT ON public.user_instructions TO auditor_ro;

COMMIT;
