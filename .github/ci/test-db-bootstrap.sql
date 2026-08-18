-- CI-only bootstrap for HIVE_TEST_DB_URL-gated RLS test suites (issue
-- #311 follow-up: these suites existed but never ran in CI, hiding a real
-- bug — see apps/control-plane/internal/agenttask/repository.go's
-- Transition fix, PR #333). Supplies the Supabase-managed objects our
-- migrations assume already exist on a real project (roles, the auth
-- schema, auth.uid()/auth.jwt()) that a vanilla Postgres image does not
-- provide. Never run against a real environment — supabase/migrations/ are
-- the only source of truth for actual schema.
--
-- Two images run this file and it has to be a no-op on the parts either one
-- already has:
--
--   * pgvector/pgvector, used by ci.yml's go-tests job, has none of this. It
--     gets every object below.
--   * supabase/postgres, used by scripts/ci-throwaway-db.sh, ships the auth
--     schema, auth.users, auth.uid(), auth.jwt() and the supabase_auth_admin
--     role already, all owned by the supabase_admin superuser. The bootstrap
--     connects as postgres, which is NOT a superuser on that image, so an
--     unconditional CREATE OR REPLACE FUNCTION auth.uid() fails with "must be
--     owner of function uid" and an unconditional CREATE ROLE fails with
--     "role already exists". Every statement here is therefore guarded on
--     absence rather than written as a plain CREATE.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

DO $$
DECLARE
  r text;
BEGIN
  FOREACH r IN ARRAY ARRAY['hive_app', 'auditor_ro', 'authenticated', 'supabase_auth_admin'] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
      EXECUTE format('CREATE ROLE %I NOLOGIN', r);
    END IF;
  END LOOP;
END
$$;

-- Membership, so a test can SET ROLE into these. supabase_auth_admin is
-- deliberately absent from this list: the three migrations that name it only
-- GRANT EXECUTE ON FUNCTION to it, which needs the role to exist rather than
-- to be a member of it, and supabase/postgres reserves its memberships.
-- Asking anyway is not merely refused there, it segfaults the backend when
-- the refusal is caught in a PL/pgSQL handler, which takes the whole cluster
-- into recovery.
--
-- TO postgres, not TO CURRENT_USER: supabase/postgres segfaults its backend
-- on the CURRENT_USER form of a role grant, same recovery, different bug.
GRANT hive_app, auditor_ro, authenticated TO postgres;

CREATE SCHEMA IF NOT EXISTS auth;

CREATE TABLE IF NOT EXISTS auth.users (
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
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'auth' AND p.proname = 'uid'
  ) THEN
    EXECUTE $fn$
      CREATE FUNCTION auth.uid() RETURNS uuid
        LANGUAGE sql STABLE AS $body$
        SELECT NULLIF(current_setting('request.jwt.claim.sub', true), '')::uuid
      $body$
    $fn$;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'auth' AND p.proname = 'jwt'
  ) THEN
    EXECUTE $fn$
      CREATE FUNCTION auth.jwt() RETURNS jsonb
        LANGUAGE sql STABLE AS $body$
        SELECT NULLIF(current_setting('request.jwt.claims', true), '')::jsonb
      $body$
    $fn$;
  END IF;
END
$$;
