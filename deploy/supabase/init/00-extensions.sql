-- Bootstrap extensions and schemas required by the self-hosted Supabase stack.
-- This file runs inside docker-entrypoint-initdb.d on first Postgres startup.
-- It is idempotent: all statements use IF NOT EXISTS.
--
-- Required by:
--   supabase-auth (GoTrue)    - needs the `auth` schema
--   supabase-rest (PostgREST) - needs the `storage` and `graphql_public` schemas
--   supabase-storage          - needs the `storage` schema
--   RAG migration (#232)      - needs the `vector` extension

-- Extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "vector";

-- Schemas consumed by the Supabase self-host components
CREATE SCHEMA IF NOT EXISTS auth;
CREATE SCHEMA IF NOT EXISTS storage;
CREATE SCHEMA IF NOT EXISTS graphql_public;
CREATE SCHEMA IF NOT EXISTS extensions;

-- Roles required by GoTrue and PostgREST self-host configurations.
-- These are created by the official Supabase self-host init scripts; we
-- replicate only what the enterprise edge stack actually needs.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'anon') THEN
    CREATE ROLE anon NOLOGIN NOINHERIT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'authenticated') THEN
    CREATE ROLE authenticated NOLOGIN NOINHERIT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'service_role') THEN
    CREATE ROLE service_role NOLOGIN NOINHERIT BYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'supabase_admin') THEN
    CREATE ROLE supabase_admin NOLOGIN NOINHERIT BYPASSRLS;
  END IF;
  -- Owners of the auth and storage schemas on hosted Supabase. Nothing here
  -- logs in as either: GoTrue and the Storage API connect as postgres. They
  -- exist so a pg_restore of a hosted dump, whose objects are owned by these
  -- roles, does not fail on an unknown owner. Cheaper to create now than to
  -- discover mid-restore.
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'supabase_auth_admin') THEN
    CREATE ROLE supabase_auth_admin NOLOGIN NOINHERIT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'supabase_storage_admin') THEN
    CREATE ROLE supabase_storage_admin NOLOGIN NOINHERIT;
  END IF;
  -- hive_app is the application role used by control-plane and edge-api.
  -- RLS policies in supabase/migrations/20260529_01_rls_tenant_tables.sql
  -- grant full access to this role (NOLOGIN, non-BYPASSRLS so RLS still applies).
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'hive_app') THEN
    CREATE ROLE hive_app NOLOGIN;
  END IF;
END
$$;

-- Grant hive_app usage on schemas it needs to read and write application data.
GRANT USAGE ON SCHEMA public TO hive_app;
GRANT USAGE ON SCHEMA storage TO hive_app;

-- Grant usage on schemas to application roles
GRANT USAGE ON SCHEMA public TO anon, authenticated, service_role;
GRANT USAGE ON SCHEMA storage TO anon, authenticated, service_role;
GRANT USAGE ON SCHEMA extensions TO anon, authenticated, service_role;

-- Table privileges in the storage schema, for tables that do not exist yet.
--
-- The Storage API creates storage.buckets, storage.objects and friends from
-- its own migrations, which run after this file. A plain GRANT here would
-- therefore grant on nothing, and the service_role connection would answer
-- every bucket call with SQLSTATE 42501. The Storage API reports that code as
-- "new row violates row-level security policy" whatever the actual cause, so
-- it reads as an RLS problem and is in fact a missing GRANT. That is exactly
-- what broke bucket creation on the first enterprise-profile boot.
--
-- Default privileges apply to whatever postgres creates later, which is what
-- the Storage API connects as.
--
-- service_role only, deliberately. Nothing in this product talks to Storage
-- from a browser: edge-api and control-plane hold the service key and mediate
-- every file operation. Granting anon and authenticated as hosted Supabase
-- does would only be safe alongside real RLS policies on these tables, and
-- there are none here. Add both together if a browser-direct path ever lands.
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA storage
  GRANT ALL ON TABLES TO service_role;
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA storage
  GRANT ALL ON SEQUENCES TO service_role;
