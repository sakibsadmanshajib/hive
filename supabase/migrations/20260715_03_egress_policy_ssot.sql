-- supabase/migrations/20260715_03_egress_policy_ssot.sql
--
-- Egress policy single source of truth (#308). One control-plane-owned
-- allowlist per tenant, with an optional per-user override — admin
-- configurable per tenant AND per user (some users need internet access for
-- web search, package downloads, and doc lookups; others don't). Read by two
-- future consumers behind one API so the policy never drifts between
-- surfaces the way HIVE_SOVEREIGN already has (#245): the server-side
-- OpenHands allowed_hosts workspace config (wave 2) and the desktop firewall
-- rule generator (wave 4). Neither consumer is wired by this migration.
--
-- Depends on: 20260516_01_phase19_tenants.sql (public.tenants)
--
-- Row shape:
--   user_id IS NULL      -> the tenant-wide default row (at most one per tenant)
--   user_id IS NOT NULL  -> a per-user override row (at most one per tenant+user)
-- A present user override fully replaces the tenant default for that user
-- (application-layer decision in apps/control-plane/internal/egress); this
-- migration only enforces "at most one row per scope".

BEGIN;

CREATE TABLE IF NOT EXISTS public.egress_policies (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    user_id       UUID        NULL,
    allowed_hosts TEXT[]      NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one tenant-wide default row (user_id IS NULL) per tenant.
CREATE UNIQUE INDEX IF NOT EXISTS egress_policies_tenant_default_uidx
    ON public.egress_policies (tenant_id)
    WHERE user_id IS NULL;

-- At most one override row per (tenant, user).
CREATE UNIQUE INDEX IF NOT EXISTS egress_policies_tenant_user_uidx
    ON public.egress_policies (tenant_id, user_id)
    WHERE user_id IS NOT NULL;

-- Row Level Security: no cross-tenant reads or writes (mirrors 20260625_05_carl_rag.sql).
ALTER TABLE public.egress_policies ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'egress_policies'
          AND policyname = 'egress_policies_tenant_isolation'
    ) THEN
        -- NULLIF(..., '') guards a Postgres GUC placeholder quirk: once a
        -- session has called set_config('app.current_tenant_id', ..., true)
        -- at least once, current_setting(name, true) on that same
        -- connection returns '' (not NULL) after the LOCAL scope ends,
        -- rather than reverting to "never defined". Casting '' straight to
        -- ::uuid raises invalid input syntax instead of cleanly filtering
        -- rows, which turns a bare/unscoped query on a reused pooled
        -- connection into a 500 instead of a fail-closed empty result.
        -- NULLIF folds that '' case to NULL first so the comparison (and
        -- therefore the whole USING/WITH CHECK expression) evaluates to
        -- NULL, which Postgres treats as false — deny by default either way.
        CREATE POLICY egress_policies_tenant_isolation
            ON public.egress_policies AS PERMISSIVE FOR ALL TO hive_app
            USING      (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
            WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
    END IF;
END$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.egress_policies TO hive_app;
GRANT SELECT ON public.egress_policies TO auditor_ro;

COMMIT;
