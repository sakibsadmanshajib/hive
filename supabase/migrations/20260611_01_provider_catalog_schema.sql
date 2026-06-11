-- =============================================================================
-- Phase 20-01: Provider Catalog schema
--   * Relax provider_routes CHECK constraint (openrouter/groq only -> non-empty)
--   * Create custom_providers table with seed rows for openrouter + groq
--   * Create tenant_model_visibility table
--   * ADD COLUMN tools_supported to provider_capabilities
--   * Extend model_aliases.visibility CHECK to include 'restricted'
--   * RLS: enable + force, FOR ALL TO hive_app policies (idempotent DO $$)
--   * GRANTs for hive_app on both new tables
--   * updated_at triggers (reuse public.set_updated_at())
-- =============================================================================

begin;

-- ---------------------------------------------------------------------------
-- Task 1: Relax provider_routes CHECK constraint
-- ---------------------------------------------------------------------------

ALTER TABLE public.provider_routes
  DROP CONSTRAINT IF EXISTS provider_routes_provider_check;

ALTER TABLE public.provider_routes
  ADD CONSTRAINT provider_routes_provider_nonempty
    CHECK (provider <> '');

-- ---------------------------------------------------------------------------
-- Task 2: custom_providers table
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.custom_providers (
  id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  slug           TEXT        NOT NULL UNIQUE,
  display_name   TEXT        NOT NULL,
  base_url       TEXT        NOT NULL,
  api_key_env    TEXT        NOT NULL,
  litellm_prefix TEXT        NOT NULL,
  enabled        BOOLEAN     NOT NULL DEFAULT true,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS custom_providers_slug_idx    ON public.custom_providers (slug);
CREATE INDEX IF NOT EXISTS custom_providers_enabled_idx ON public.custom_providers (enabled);

INSERT INTO public.custom_providers
  (slug, display_name, base_url, api_key_env, litellm_prefix, enabled)
VALUES
  ('openrouter', 'OpenRouter', 'https://openrouter.ai/api/v1',   'OPENROUTER_API_KEY', 'openrouter/', true),
  ('groq',       'Groq',       'https://api.groq.com/openai/v1', 'GROQ_API_KEY',       'groq/',       true)
ON CONFLICT (slug) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Task 3: tenant_model_visibility table
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.tenant_model_visibility (
  id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id  UUID        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
  alias_id   TEXT        NOT NULL REFERENCES public.model_aliases(alias_id) ON DELETE CASCADE,
  visible    BOOLEAN     NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, alias_id)
);

CREATE INDEX IF NOT EXISTS tmv_tenant_idx ON public.tenant_model_visibility (tenant_id);
CREATE INDEX IF NOT EXISTS tmv_alias_idx  ON public.tenant_model_visibility (alias_id);

-- ---------------------------------------------------------------------------
-- Task 4: provider_capabilities — ADD COLUMN tools_supported
-- ---------------------------------------------------------------------------

ALTER TABLE public.provider_capabilities
  ADD COLUMN IF NOT EXISTS tools_supported BOOLEAN NOT NULL DEFAULT false;

-- ---------------------------------------------------------------------------
-- Task 5: model_aliases.visibility CHECK extended to include 'restricted'
-- ---------------------------------------------------------------------------

ALTER TABLE public.model_aliases
  DROP CONSTRAINT IF EXISTS model_aliases_visibility_check;

ALTER TABLE public.model_aliases
  ADD CONSTRAINT model_aliases_visibility_check
    CHECK (visibility IN ('public', 'preview', 'internal', 'restricted'));

-- ---------------------------------------------------------------------------
-- Task 6: RLS — custom_providers
-- ---------------------------------------------------------------------------

ALTER TABLE public.custom_providers ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.custom_providers FORCE ROW LEVEL SECURITY;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename  = 'custom_providers'
      AND policyname = 'custom_providers_service_role_all'
  ) THEN
    CREATE POLICY custom_providers_service_role_all
      ON public.custom_providers
      FOR ALL TO hive_app
      USING (true)
      WITH CHECK (true);
  END IF;
END $$;

-- ---------------------------------------------------------------------------
-- Task 6: RLS — tenant_model_visibility
-- ---------------------------------------------------------------------------

ALTER TABLE public.tenant_model_visibility ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.tenant_model_visibility FORCE ROW LEVEL SECURITY;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename  = 'tenant_model_visibility'
      AND policyname = 'tenant_model_visibility_service_role_all'
  ) THEN
    CREATE POLICY tenant_model_visibility_service_role_all
      ON public.tenant_model_visibility
      FOR ALL TO hive_app
      USING (true)
      WITH CHECK (true);
  END IF;
END $$;

-- GRANTs (RLS policies do not imply DML privileges)
GRANT SELECT, INSERT, UPDATE, DELETE ON public.custom_providers         TO hive_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.tenant_model_visibility  TO hive_app;

-- ---------------------------------------------------------------------------
-- Task 7: updated_at triggers
-- ---------------------------------------------------------------------------

-- Ensure the function exists (idempotent; all prior migrations already create it,
-- but guard here for standalone replay safety).
CREATE OR REPLACE FUNCTION public.set_updated_at()
  RETURNS TRIGGER
  LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

CREATE OR REPLACE TRIGGER set_custom_providers_updated_at
  BEFORE UPDATE ON public.custom_providers
  FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

CREATE OR REPLACE TRIGGER set_tenant_model_visibility_updated_at
  BEFORE UPDATE ON public.tenant_model_visibility
  FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

commit;
