-- supabase/migrations/20260822_01_metering_retention_pg_cron_schedule.sql
-- Schedule the nightly metering retention job on a database that can now run
-- it. Issue #615, and the second half of #645.
--
-- Why a second migration instead of editing 20260729_02
-- ----------------------------------------------------
-- 20260729_02_metering_shadow_verdicts_retention.sql already created the
-- config table, the purge procedure, and the cron schedule, the last one
-- behind two guards that degrade to a RAISE NOTICE when pg_cron is absent.
-- On the self-hosted demo box pg_cron WAS absent: the data plane ran
-- pgvector/pgvector:pg16, which carries the vector extension and nothing
-- else, so both guards took their skip branch and no nightly job was ever
-- created. That file is recorded as applied in public.hive_schema_migrations,
-- so editing it changes nothing on any existing database: the ledger keys on
-- filename and would never re-run it. Hence a new file.
--
-- The guards themselves were not the defect. What hid this was the deploy's
-- retention report reading the hosted Supabase project (pg_cron preloaded
-- there) instead of the database the stack uses, so it printed
-- "scheduled and active" about a database the application never touches. The
-- hosted project has since been deleted, scripts/check-retention-schedule.sh
-- now asserts the cluster it is reading, and it now fails rather than warns.
--
-- What changed underneath: deploy/docker/Dockerfile.supabase-db adds
-- postgresql-16-cron to the same base image, and docker-compose.enterprise.yml
-- starts the server with shared_preload_libraries=pg_cron. Both are properties
-- of the deployment definition, so a container recreate or a fresh volume still
-- has them. CREATE EXTENSION below therefore succeeds on this deployment for
-- the first time.
--
-- Portability, and why the guards stay
-- ------------------------------------
-- CI's throwaway database is a stock postgres image with no pg_cron, and
-- scripts/ci-throwaway-db.sh asserts the absent branch by name. Enterprise
-- self-hosted Postgres is not guaranteed to have it either. CREATE EXTENSION
-- does not degrade gracefully (it raises and aborts the whole script), so the
-- availability guard stays.
--
-- What does NOT stay is the silent part, for the role that could actually have
-- done the work. Where the extension files are on disk AND the migration runs
-- as a superuser, the final block demands a live, active, correctly-pointed job
-- and raises if it does not have one. That is the installed-but-not-preloaded
-- shape from issue #615, and it used to leave nothing behind but a notice.
--
-- The superuser condition is not decoration. CI's migration-applier workflow
-- runs against supabase/postgres, where pg_cron is available but the `postgres`
-- role is not a superuser, so CREATE EXTENSION and cron.schedule both fail on
-- privilege alone. A role with no right to create an extension cannot be held
-- responsible for an unscheduled job, and failing the whole chain over it would
-- block every deployment whose migration role is not a superuser. Those roles
-- get a notice naming the role and the error instead. The demo box migrates as
-- a superuser, so it gets the hard failure.
--
-- This matters most where there is no deploy-time check at all: an Enterprise
-- self-hosted install (scripts/install.sh) never runs
-- scripts/check-retention-schedule.sh, so these RAISEs are the only signal that
-- path has.
--
-- What the job deletes
-- --------------------
-- Rows of public.metering_shadow_verdicts whose created_at is older than
-- metering_shadow_verdicts_retention_config.retention_days (90 on this
-- deployment), in committed batches of 500, and nothing else. That table holds
-- grading telemetry for the metering shadow: opaque uuids, enums, bools and
-- token counts, no prompt or completion content. It is not billing evidence.
-- public.credit_ledger_entries is append-only real money and is not referenced
-- by the procedure at all.

BEGIN;

DO $pg_cron_extension$
DECLARE
  pg_cron_available boolean;
  is_super          boolean;
BEGIN
  SELECT EXISTS (
    SELECT 1 FROM pg_available_extensions WHERE name = 'pg_cron'
  ) INTO pg_cron_available;
  SELECT rolsuper INTO is_super FROM pg_roles WHERE rolname = current_user;

  IF pg_cron_available THEN
    -- The handler is back, unlike the first revision of this file, but it now
    -- re-raises for the role that could have succeeded. Both halves were
    -- learned the hard way rather than reasoned about.
    --
    -- Removing it entirely broke CI: the migration-applier workflow runs
    -- against supabase/postgres, where pg_cron IS available but the `postgres`
    -- role is NOT a superuser (already recorded in
    -- .github/ci/test-db-bootstrap.sql), so CREATE EXTENSION raised and took
    -- the whole chain down. A role with no rights to create extensions cannot
    -- be held responsible for an unscheduled retention job.
    --
    -- Keeping it unconditional was the original defect: on the demo box, where
    -- the migration runs as a superuser and the image now ships the extension,
    -- a failure here means the deployment is misconfigured (pg_cron missing
    -- from shared_preload_libraries, issue #615) and must stop the deploy
    -- rather than leave a notice nobody rereads.
    BEGIN
      EXECUTE 'CREATE EXTENSION IF NOT EXISTS pg_cron';
    EXCEPTION WHEN OTHERS THEN
      IF is_super THEN
        RAISE; -- superuser: nothing else can fix this, so fail loudly
      END IF;
      RAISE NOTICE 'pg_cron is available but CREATE EXTENSION failed as non-superuser % (%): no nightly retention job is scheduled by this migration. Schedule it as a role that can create the extension, or invoke CALL public.purge_metering_shadow_verdicts() from an external scheduler.', current_user, SQLERRM;
    END;
  ELSE
    RAISE NOTICE 'pg_cron is not available on this Postgres install, so no nightly retention job is scheduled here. Invoke CALL public.purge_metering_shadow_verdicts() from an external scheduler instead. Expected on CI throwaway databases; NOT expected on the demo box, where scripts/check-retention-schedule.sh fails the deploy over it.';
  END IF;
END;
$pg_cron_extension$;

DO $pg_cron_schedule$
DECLARE
  is_super boolean;
BEGIN
  SELECT rolsuper INTO is_super FROM pg_roles WHERE rolname = current_user;

  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
    -- Same name, schedule and command as 20260729_02. cron.schedule upserts on
    -- (jobname, username), so re-running this against a database that already
    -- has the job is a no-op rather than a duplicate.
    --
    -- 21:00 UTC = 03:00 Asia/Dhaka, the lowest-traffic point for a BD-first
    -- product, unchanged from 20260729_02's reasoning.
    --
    -- Same asymmetry as the block above, for the same reason: cron.schedule is
    -- superuser-only by default, so a non-superuser role gets a notice rather
    -- than an aborted migration chain, and a superuser gets the failure.
    BEGIN
      PERFORM cron.schedule(
        'metering-shadow-verdicts-purge',
        '0 21 * * *',
        $sched$CALL public.purge_metering_shadow_verdicts();$sched$
      );
    EXCEPTION WHEN OTHERS THEN
      IF is_super THEN
        RAISE;
      END IF;
      RAISE NOTICE 'cron.schedule failed as non-superuser % (%): no nightly retention job is scheduled by this migration.', current_user, SQLERRM;
    END;
  END IF;
END;
$pg_cron_schedule$;

DO $pg_cron_assert$
DECLARE
  job_count integer;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'pg_cron') THEN
    RETURN; -- Nothing to assert: the skip branch above is the documented outcome.
  END IF;

  -- Asserted only for a role that could have done the work. A non-superuser
  -- role has already said so in a notice above, and failing the migration for
  -- a privilege it was never granted would block every deployment whose
  -- migration role is not a superuser, CI's supabase/postgres included.
  IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
    RETURN;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
    RAISE EXCEPTION 'pg_cron is available on this host but the extension is not installed after CREATE EXTENSION, so nightly metering retention would silently not run. The usual cause is pg_cron missing from shared_preload_libraries (issue #615): it is a startup-time setting and cannot be fixed from here.';
  END IF;

  SELECT count(*) INTO job_count
  FROM cron.job
  WHERE jobname = 'metering-shadow-verdicts-purge'
    AND active
    AND command LIKE '%purge_metering_shadow_verdicts%';

  IF job_count = 0 THEN
    RAISE EXCEPTION 'pg_cron is installed but there is no active cron job named metering-shadow-verdicts-purge calling public.purge_metering_shadow_verdicts, so nightly metering retention would silently not run.';
  END IF;
END;
$pg_cron_assert$;

COMMIT;
