-- CI-only bootstrap: stub the Supabase-managed `auth` schema and roles
-- that real Supabase projects provide out of the box but the bare
-- pgvector/pgvector:pg15 service image does not.
--
-- This file is applied BEFORE supabase/migrations/*.sql in phase-19.yml.
-- It is intentionally minimal: only the objects the Phase 19 migrations
-- and the test suites touch.

BEGIN;

-- Supabase managed roles. CREATE ROLE IF NOT EXISTS is not supported by
-- Postgres, so DO $$ ... $$ blocks guard against re-runs.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'authenticated') THEN
    CREATE ROLE authenticated NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'anon') THEN
    CREATE ROLE anon NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'service_role') THEN
    CREATE ROLE service_role NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'supabase_auth_admin') THEN
    CREATE ROLE supabase_auth_admin NOLOGIN;
  END IF;
END $$;

CREATE SCHEMA IF NOT EXISTS auth;
GRANT USAGE ON SCHEMA auth TO authenticated, anon, service_role, supabase_auth_admin;

-- Minimal stub of auth.users so FKs from public tables resolve.
CREATE TABLE IF NOT EXISTS auth.users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email text,
  raw_user_meta_data jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- auth.jwt() returns the current request's JWT claims as JSONB. In
-- production Supabase wires this to the GoTrue session; in CI we read
-- request.jwt.claims from the session config (set via SET LOCAL by
-- tests that need it) and fall back to NULL otherwise.
CREATE OR REPLACE FUNCTION auth.jwt() RETURNS jsonb LANGUAGE sql STABLE AS $$
  SELECT NULLIF(current_setting('request.jwt.claims', true), '')::jsonb;
$$;

-- auth.uid() — convenience helper Supabase RLS policies sometimes use.
CREATE OR REPLACE FUNCTION auth.uid() RETURNS uuid LANGUAGE sql STABLE AS $$
  SELECT (auth.jwt() ->> 'sub')::uuid;
$$;

GRANT EXECUTE ON FUNCTION auth.jwt() TO authenticated, anon, service_role, supabase_auth_admin;
GRANT EXECUTE ON FUNCTION auth.uid() TO authenticated, anon, service_role, supabase_auth_admin;

-- pgcrypto for gen_random_uuid() used in tests and migrations.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

COMMIT;
