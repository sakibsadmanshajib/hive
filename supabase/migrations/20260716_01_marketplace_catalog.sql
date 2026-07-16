-- supabase/migrations/20260716_01_marketplace_catalog.sql
--
-- MCP and skills marketplace, admin-curated baseline (issue #309,
-- agent-subsystem blueprint Step 2.3). One distribution mechanism covering
-- MCP servers plus rules, skills, and prompt templates (Cursor Team
-- Marketplace pattern): a single global catalog an admin curates, and a
-- per-tenant enablement table recording which catalog entries that tenant
-- has turned on. The user-added, desktop-only tier is Wave 4, out of scope
-- here.
--
-- Two tables, two different trust postures:
--
--   public.marketplace_entries        Global admin-curated catalog. Same
--                                      posture as public.feature_gate_keys
--                                      (20260625_04_carl_featuregate_keys.sql):
--                                      no RLS, no GRANT to authenticated —
--                                      only control-plane's own pool reads or
--                                      writes it, gated at the HTTP layer by
--                                      platform-admin (see
--                                      apps/control-plane/internal/marketplace).
--
--   public.marketplace_tenant_entries Per-tenant enablement. RLS-scoped like
--                                      public.egress_policies
--                                      (20260715_03_egress_policy_ssot.sql):
--                                      hive_app is NOT BYPASSRLS, so every
--                                      query needs app.current_tenant_id set
--                                      LOCAL inside an explicit transaction
--                                      (see marketplace.Repository.withTenantTx).
--
-- Depends on: 20260516_01_phase19_tenants.sql (public.tenants).

BEGIN;

CREATE TABLE public.marketplace_entries (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        TEXT        NOT NULL CHECK (kind IN ('mcp_server', 'rule', 'skill', 'prompt_template')),
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    config      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_by  UUID        REFERENCES auth.users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE public.marketplace_entries IS
  'Admin-curated marketplace catalog: MCP servers, rules, skills, and prompt templates any tenant may enable (issue #309). config is kind-specific (an MCP server stores command/args/env or url/transport in the shape apps/agent-engine consumes via marketplaceclient). No RLS and no GRANT to authenticated: this is shared platform catalog data, not tenant data, read and written only through apps/control-plane/internal/marketplace, gated at the HTTP layer by platform-admin.';

-- At most one entry per (kind, name) — the admin curation UX shows one row
-- per logical connector/skill, not silently-shadowing duplicates.
CREATE UNIQUE INDEX marketplace_entries_kind_name_uidx
    ON public.marketplace_entries (kind, name);

CREATE TABLE public.marketplace_tenant_entries (
    tenant_id  UUID        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    entry_id   UUID        NOT NULL REFERENCES public.marketplace_entries(id) ON DELETE CASCADE,
    enabled_by UUID        REFERENCES auth.users(id),
    enabled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, entry_id)
);

COMMENT ON TABLE public.marketplace_tenant_entries IS
  'Per-tenant enablement of a marketplace_entries row. Presence of a (tenant_id, entry_id) row means that tenant has enabled that catalog entry; absence means disabled. RLS-scoped like public.egress_policies.';

ALTER TABLE public.marketplace_tenant_entries ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'marketplace_tenant_entries'
          AND policyname = 'marketplace_tenant_entries_tenant_isolation'
    ) THEN
        -- NULLIF(..., '') guards the same Postgres GUC placeholder quirk
        -- documented in 20260715_03_egress_policy_ssot.sql: once a pooled
        -- connection has ever called set_config('app.current_tenant_id', ...,
        -- true), current_setting(name, true) can return '' rather than NULL
        -- after the LOCAL scope ends. Casting '' straight to ::uuid raises
        -- instead of cleanly filtering rows; NULLIF folds that case to NULL,
        -- which the comparison then treats as false — deny by default.
        CREATE POLICY marketplace_tenant_entries_tenant_isolation
            ON public.marketplace_tenant_entries AS PERMISSIVE FOR ALL TO hive_app
            USING      (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
            WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
    END IF;
END$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.marketplace_tenant_entries TO hive_app;
GRANT SELECT ON public.marketplace_tenant_entries TO auditor_ro;

COMMIT;
