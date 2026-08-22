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
-- What does NOT stay is the silent part. The final block below turns the
-- "available but unusable" case into a hard failure: on a host where the
-- extension files are on disk, this migration now demands a live, active,
-- correctly-pointed job and raises if it does not have one. That is the
-- installed-but-not-preloaded shape from issue #615, and it used to leave
-- nothing behind but a notice.
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
BEGIN
  SELECT EXISTS (
    SELECT 1 FROM pg_available_extensions WHERE name = 'pg_cron'
  ) INTO pg_cron_available;

  IF pg_cron_available THEN
    -- No exception handler here, unlike 20260729_02. There, swallowing the
    -- failure was right because the config table and the purge procedure below
    -- it had no pg_cron dependency and still had to be created. This file has
    -- no such payload: scheduling the job IS the whole content, so a failure
    -- here has nothing left to protect and must be loud.
    EXECUTE 'CREATE EXTENSION IF NOT EXISTS pg_cron';
  ELSE
    RAISE NOTICE 'pg_cron is not available on this Postgres install, so no nightly retention job is scheduled here. Invoke CALL public.purge_metering_shadow_verdicts() from an external scheduler instead. Expected on CI throwaway databases; NOT expected on the demo box, where scripts/check-retention-schedule.sh fails the deploy over it.';
  END IF;
END;
$pg_cron_extension$;

DO $pg_cron_schedule$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
    -- Same name, schedule and command as 20260729_02. cron.schedule upserts on
    -- (jobname, username), so re-running this against a database that already
    -- has the job is a no-op rather than a duplicate.
    --
    -- 21:00 UTC = 03:00 Asia/Dhaka, the lowest-traffic point for a BD-first
    -- product, unchanged from 20260729_02's reasoning.
    PERFORM cron.schedule(
      'metering-shadow-verdicts-purge',
      '0 21 * * *',
      $sched$CALL public.purge_metering_shadow_verdicts();$sched$
    );
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
