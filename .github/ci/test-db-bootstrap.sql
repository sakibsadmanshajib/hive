-- CI-only bootstrap for HIVE_TEST_DB_URL-gated RLS test suites (issue
-- #311 follow-up: these suites existed but never ran in CI, hiding a real
-- bug — see apps/control-plane/internal/agenttask/repository.go's
-- Transition fix, PR #333). Supplies the Supabase-managed objects our
-- migrations assume already exist on a real project (roles, the auth
-- schema, auth.uid()/auth.jwt()) that a vanilla Postgres image does not
-- provide. Never run against a real environment — supabase/migrations/ are
-- the only source of truth for actual schema.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE ROLE hive_app NOLOGIN;
CREATE ROLE auditor_ro NOLOGIN;
CREATE ROLE authenticated NOLOGIN;
CREATE ROLE supabase_auth_admin NOLOGIN;
GRANT hive_app, auditor_ro, authenticated, supabase_auth_admin TO postgres;

CREATE SCHEMA IF NOT EXISTS auth;

CREATE TABLE auth.users (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email              TEXT,
    raw_user_meta_data JSONB NOT NULL DEFAULT '{}'::jsonb
);

-- Minimal stand-ins for Supabase's GoTrue-provided functions. Our own RLS
-- policies mostly key off the app.current_tenant_id GUC
-- (withTenantTx-style helpers across apps/control-plane/internal), not
-- these, but a handful of phase-19 policies (tenants_select_own et al.)
-- reference auth.jwt()/auth.uid() directly, and CREATE POLICY fails at
-- parse time if the functions don't exist at all.
CREATE OR REPLACE FUNCTION auth.uid() RETURNS uuid
    LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('request.jwt.claim.sub', true), '')::uuid
$$;

CREATE OR REPLACE FUNCTION auth.jwt() RETURNS jsonb
    LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('request.jwt.claims', true), '')::jsonb
$$;
