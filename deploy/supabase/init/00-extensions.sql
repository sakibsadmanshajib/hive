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
END
$$;

-- Grant usage on schemas to application roles
GRANT USAGE ON SCHEMA public TO anon, authenticated, service_role;
GRANT USAGE ON SCHEMA storage TO anon, authenticated, service_role;
GRANT USAGE ON SCHEMA extensions TO anon, authenticated, service_role;
