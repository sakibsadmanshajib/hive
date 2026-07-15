-- supabase/migrations/20260715_01_license_state.sql
--
-- Licensing entitlement seam (issue #304, D9). Persists the latest
-- validated license snapshot so tier is a plain queryable SQL attribute --
-- owner decision, issue #304 comment (2026-07-07): "License service keeps
-- tier as a plain queryable attribute (never hardcode all-gates-to-all-tiers
-- in a way that can't be revisited)". This table is a read cache written by
-- the control-plane licensing package (apps/control-plane/internal/licensing);
-- it is not read by any feature-gate enforcement path today -- seam only,
-- D9 tier-to-gate-key enforcement is explicitly deferred.
--
-- Singleton table: Hive Enterprise is single-tenant per install (one
-- license per deployment, matching the NVIDIA Delegated License Server
-- pattern), so there is exactly one row. The boolean primary key with a
-- CHECK(singleton) constraint enforces this at the schema level -- TRUE is
-- the only legal primary key value, so a second row can never be inserted.

BEGIN;

CREATE TABLE IF NOT EXISTS public.license_state (
    singleton    BOOLEAN     PRIMARY KEY DEFAULT TRUE,
    tier         TEXT        NOT NULL,
    seats        INTEGER     NOT NULL,
    issued_at    TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    validated_at TIMESTAMPTZ NOT NULL,
    valid        BOOLEAN     NOT NULL,
    reason       TEXT        NOT NULL DEFAULT '',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT license_state_singleton CHECK (singleton)
);

COMMENT ON TABLE public.license_state IS
    'Latest validated license entitlement snapshot (issue #304). Singleton row (one license per Hive Enterprise install). Not consulted by any feature-gate enforcement path -- seam only, D9 tier-to-gate-key restriction deferred.';

-- RLS: no client role (anon/authenticated) ever needs this table; only the
-- control-plane service role writes and reads it. Matches the
-- custom_providers / tenant_model_visibility pattern in
-- 20260611_01_provider_catalog_schema.sql.
ALTER TABLE public.license_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.license_state FORCE ROW LEVEL SECURITY;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename  = 'license_state'
      AND policyname = 'license_state_service_role_all'
  ) THEN
    CREATE POLICY license_state_service_role_all
      ON public.license_state
      FOR ALL TO hive_app
      USING (true)
      WITH CHECK (true);
  END IF;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON public.license_state TO hive_app;

COMMIT;
