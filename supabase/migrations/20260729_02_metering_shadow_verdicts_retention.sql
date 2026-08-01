-- supabase/migrations/20260729_02_metering_shadow_verdicts_retention.sql
-- Metering Step 2 wave 3: retention support, part 2 of 2. See issue #603.
--
-- Decision: ninety day retention on the metering shadow verdict table,
-- enforced by a nightly batched pg_cron DELETE. This table stores no
-- prompt or completion content, only opaque uuids, enums, bools, and token
-- counts (its own migration comment, 20260728_04, states its purpose is
-- grading precedence-rule and pricing-formula correctness against real
-- traffic before Step 4), so the window is a disk-cost/noise call, not a
-- compliance one. Post-enforcement retention (once verdicts become
-- billing evidence rather than telemetry, Step 4) is a separate decision,
-- deferred, and will likely be longer. Enterprise self-hosted deployments
-- get a configurable window rather than a hardcoded one, because customer
-- audit rules vary.
--
-- Depends on 20260729_01 (plain btree on created_at) already existing;
-- without it this job sequential-scans the table on every run.
--
-- pg_cron: this repo has no prior pg_cron usage. pg_cron is a
-- Supabase-supported Postgres Module (preloaded on every hosted project,
-- confirmed via Supabase docs), so on Hive Cloud the extension and the
-- schedule both come up automatically. Enterprise self-hosted Postgres is
-- not guaranteed to have pg_cron built in (CI's stock postgres:17 image
-- does not, which is how this guard was found), and CREATE EXTENSION does
-- not degrade gracefully -- it raises and aborts the whole script. So the
-- extension creation and the cron.schedule call are each wrapped in a
-- pg_available_extensions / pg_extension check: when pg_cron is present
-- they run as before, when it is absent they RAISE NOTICE and skip. The
-- table, the config row, and the purge PROCEDURE have no pg_cron
-- dependency and are always created; an operator without pg_cron invokes
-- CALL public.purge_metering_shadow_verdicts() from an external scheduler
-- instead of the built-in cron job.
--
-- Schedule: 21:00 UTC daily = 03:00 Bangladesh time (UTC+6), the lowest
-- traffic point for a BD-first chat product, chosen to keep the deletes off
-- the table during business hours per the shared-Postgres pool ceiling
-- (see .wolf/cerebrum.md project_supabase_pool_ceiling). Note pg_cron's
-- background worker connects directly to Postgres, not through the
-- Supavisor/pgbouncer session-mode pool CI/agents/chat share, so it never
-- takes one of those 15 clients -- but it is still real IO/locking on a
-- shared box, hence still off-hours and still batched.
--
-- Batching: 500 rows per DELETE, looped with an explicit COMMIT after each
-- batch (PROCEDURE, not FUNCTION, specifically so it can COMMIT mid-run).
-- Rows are narrow (uuids/enums/bools/bigints, no prompt/completion text),
-- so 500 rows is a small, fast, sub-second statement -- enough to drain
-- realistic nightly growth in a handful of iterations, small enough that
-- no single DELETE ever holds row locks for longer than that. Each COMMIT
-- releases those locks immediately rather than holding one lock for the
-- whole purge.
--
-- Configurable window: retention_days lives in a one-row config table, not
-- a literal in the job body. Enterprise self-hosted operators change it
-- with a plain UPDATE, no migration edit required:
--   UPDATE public.metering_shadow_verdicts_retention_config
--   SET retention_days = 30;

BEGIN;

DO $pg_cron_setup$
DECLARE
  pg_cron_available boolean;
BEGIN
  SELECT EXISTS (
    SELECT 1 FROM pg_available_extensions WHERE name = 'pg_cron'
  ) INTO pg_cron_available;

  IF pg_cron_available THEN
    -- The pg_available_extensions check above only proves the extension files
    -- are on disk. CREATE EXTENSION can still raise: pg_cron must also be in
    -- shared_preload_libraries (issue #615, the installed but not preloaded
    -- variant), and creating an extension requires privileges the migration
    -- role may not hold on a managed instance. Neither case is a reason to
    -- abort the whole migration, because everything below this block, the
    -- config table and the purge procedure, has no pg_cron dependency at all.
    -- Without this handler the failure aborts the transaction and takes the
    -- deploy gate with it, which is precisely the trap this file already
    -- documents avoiding for the absent-extension case.
    BEGIN
      EXECUTE 'CREATE EXTENSION IF NOT EXISTS pg_cron';
    EXCEPTION WHEN OTHERS THEN
      RAISE NOTICE 'pg_cron is available but CREATE EXTENSION failed (%): automatic nightly retention scheduling is disabled. Invoke public.purge_metering_shadow_verdicts() from an external scheduler instead.', SQLERRM;
    END;
  ELSE
    RAISE NOTICE 'pg_cron extension is not available on this Postgres install; automatic nightly retention scheduling is disabled. Invoke public.purge_metering_shadow_verdicts() from an external scheduler instead.';
  END IF;
END;
$pg_cron_setup$;

CREATE TABLE IF NOT EXISTS public.metering_shadow_verdicts_retention_config (
  id             smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1), -- singleton row
  retention_days integer NOT NULL DEFAULT 90 CHECK (retention_days > 0)
);
COMMENT ON TABLE public.metering_shadow_verdicts_retention_config IS
  'Single-row config for the nightly metering_shadow_verdicts purge job. '
  'UPDATE retention_days directly to change the window -- no migration '
  'needed. Hive Cloud default is 90 days; Enterprise self-hosted operators '
  'set their own value here per their audit rules.';

INSERT INTO public.metering_shadow_verdicts_retention_config (id, retention_days)
VALUES (1, 90)
ON CONFLICT (id) DO NOTHING;

-- search_path is pinned inside the procedure body (below), not as a SET
-- clause on CREATE PROCEDURE: Postgres implements a procedure's SET clause
-- by wrapping the CALL in a sub-transaction to restore the setting
-- afterward, and that wrapper rejects a COMMIT inside the body ("invalid
-- transaction termination") -- confirmed by hand against a live container,
-- see PR description. Body-position SET coexists fine with the internal
-- COMMIT (same trick already used below for lock_timeout), and it closes
-- the defense-in-depth gap an unpinned path would leave: without it, a
-- schema earlier on a caller's search_path could shadow an unqualified
-- reference even though every table reference here is already schema
-- qualified.
CREATE OR REPLACE PROCEDURE public.purge_metering_shadow_verdicts(batch_size integer DEFAULT 500)
LANGUAGE plpgsql
AS $$
DECLARE
  window_days   integer;
  deleted_count integer;
BEGIN
  SET search_path = pg_catalog, pg_temp;
  SET lock_timeout = '5s';

  SELECT retention_days INTO window_days
  FROM public.metering_shadow_verdicts_retention_config
  WHERE id = 1;
  window_days := COALESCE(window_days, 90);

  LOOP
    DELETE FROM public.metering_shadow_verdicts
    WHERE id IN (
      SELECT id FROM public.metering_shadow_verdicts
      WHERE created_at < now() - (window_days || ' days')::interval
      ORDER BY created_at
      LIMIT batch_size
    );
    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    COMMIT;
    EXIT WHEN deleted_count < batch_size;
  END LOOP;
END;
$$;
COMMENT ON PROCEDURE public.purge_metering_shadow_verdicts(integer) IS
  'Nightly batched retention delete for metering_shadow_verdicts (issue '
  '#603). Runs as a loop of bounded DELETEs, each its own committed '
  'transaction, so no single lock is held for the full purge. Window comes '
  'from metering_shadow_verdicts_retention_config, not a literal here.';

DO $pg_cron_schedule$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
    PERFORM cron.schedule(
      'metering-shadow-verdicts-purge',
      '0 21 * * *', -- 21:00 UTC = 03:00 Asia/Dhaka
      $sched$CALL public.purge_metering_shadow_verdicts();$sched$
    );
  ELSE
    RAISE NOTICE 'pg_cron is not installed; skipping cron.schedule for metering-shadow-verdicts-purge. Automatic retention is disabled -- CALL public.purge_metering_shadow_verdicts() from an external scheduler instead.';
  END IF;
END;
$pg_cron_schedule$;

COMMIT;
