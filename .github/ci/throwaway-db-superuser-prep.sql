-- Superuser preparation for a throwaway supabase/postgres container.
--
-- Run as supabase_admin over the container's own local socket, BEFORE
-- scripts/ci-throwaway-db.sh. Everything here is something a hosted Supabase
-- project already has and this image leaves to its superuser, and the
-- bootstrap connects as `postgres`, which on this image is NOT a superuser.
--
-- Keep this file to things the platform genuinely provides. Granting
-- `postgres` blanket superuser instead would be one line and would also hide
-- every privilege error a migration would really hit on Hive Cloud, where
-- `postgres` is not a superuser either.

-- pg_cron is preloaded on every hosted Supabase project, so on a real
-- deployment 20260729_02's CREATE EXTENSION is a no-op and its cron.schedule
-- call runs. Here the extension files are on disk but nothing has installed
-- them, and installing needs superuser. Without this the migration takes its
-- documented absent-extension branch, raises a NOTICE, and reports success
-- having created no retention job at all.
CREATE EXTENSION IF NOT EXISTS pg_cron;

-- cron.schedule() is then called by `postgres` during the migration.
GRANT USAGE ON SCHEMA cron TO postgres;
GRANT ALL ON ALL TABLES IN SCHEMA cron TO postgres;
GRANT ALL ON ALL SEQUENCES IN SCHEMA cron TO postgres;

-- The auth schema on this image is owned by supabase_admin. On a hosted
-- project GoTrue owns it and has already created auth.users; here nothing
-- has, and .github/ci/test-db-bootstrap.sql supplies the stand-in that
-- thirteen migrations foreign-key to. CREATE on the schema is what lets it.
GRANT USAGE, CREATE ON SCHEMA auth TO postgres;
