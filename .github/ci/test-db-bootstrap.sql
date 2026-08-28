-- CI-only bootstrap for HIVE_TEST_DB_URL-gated RLS test suites (issue
-- #311 follow-up: these suites existed but never ran in CI, hiding a real
-- bug — see apps/control-plane/internal/agenttask/repository.go's
-- Transition fix, PR #333). Supplies the Supabase-managed objects our
-- migrations assume already exist on a real project (roles, the auth
-- schema, auth.uid()/auth.jwt()) that a vanilla Postgres image does not
-- provide. Never run against a real environment — supabase/migrations/ are
-- the only source of truth for actual schema.
--
-- Every statement below is guarded on absence rather than written as a plain
-- CREATE. Be precise about why, because the reason is not what CI currently
-- proves:
--
--   * pgvector/pgvector:pg17 is the ONLY image any caller runs this against
--     today. ci.yml's go-tests job applies it to a pgvector service container,
--     and scripts/ci-throwaway-db.sh's non-gotrue branch applies it to the
--     pgvector container ci.yml's live-integration job starts. On that image
--     none of these objects pre-exist and postgres IS a superuser, so the
--     guards are all no-ops and nothing here is under test.
--   * supabase/postgres is NOT currently a caller. An earlier revision of this
--     branch used it for the throwaway database and was reverted (the postgres
--     role is not a superuser there, which forced a second connection as
--     supabase_admin for pg_cron and for auth-schema writes). The guards were
--     written against that image and are kept deliberately, because they cost
--     nothing and the alternative is rediscovering the failures below. They are
--     a defence against a future caller, not a description of a current one.
--
-- What was observed on supabase/postgres while it was a caller, and what the
-- guards therefore avoid: it ships the auth schema, auth.users, auth.uid(),
-- auth.jwt() and supabase_auth_admin already, all owned by the supabase_admin
-- superuser, so an unconditional CREATE OR REPLACE FUNCTION auth.uid() fails
-- with "must be owner of function uid" and an unconditional CREATE ROLE fails
-- with "role already exists".
--
-- Consequence worth stating plainly: because no CI leg runs this file against
-- supabase/postgres, a future edit that dropped a guard would ship green. If
-- that image ever becomes a caller again, add a leg that exercises it here
-- rather than trusting these comments.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

DO $$
DECLARE
  r text;
BEGIN
  -- service_role and anon added for
  -- 20260828_01_service_role_public_schema_grant.sql: PostgREST's anon and
  -- service_role have USAGE on the public schema (service_role also
  -- BYPASSRLS) on a real project, but no migration before that one ever
  -- named either role in a GRANT, so nothing here needed them to exist
  -- until then. authenticated already existed in this array; that
  -- migration also grants it, which needed no new role here. NOLOGIN only,
  -- no BYPASSRLS: the migration only needs the roles to exist as GRANT
  -- targets, and no suite here runs as either one, so the extra privilege
  -- would be untested surface, not a requirement.
  FOREACH r IN ARRAY ARRAY['hive_app', 'auditor_ro', 'authenticated', 'supabase_auth_admin', 'service_role', 'anon'] LOOP
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
    raw_user_meta_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Lifecycle columns real GoTrue carries. The signup reconciler sweep
    -- filters on all three: created_at bounds its lookback window, and a
    -- soft-deleted or banned identity must never be provisioned. They are
    -- nullable with no default here because that is how GoTrue declares
    -- created_at, which is exactly why the sweep refuses a NULL age
    -- instead of guessing one.
    created_at         TIMESTAMPTZ,
    deleted_at         TIMESTAMPTZ,
    banned_until       TIMESTAMPTZ
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
