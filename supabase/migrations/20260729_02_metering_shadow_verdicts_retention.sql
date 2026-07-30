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
-- pg_cron: this repo has no prior pg_cron usage, so this migration enables
-- the extension itself (idempotent, CREATE EXTENSION IF NOT EXISTS) rather
-- than assuming it. pg_cron is a Supabase-supported Postgres Module
-- (preloaded on every hosted project, confirmed via Supabase docs), so
-- this is expected to succeed on Hive Cloud. If an Enterprise self-hosted
-- Postgres was not built with pg_cron in shared_preload_libraries, this
-- statement fails and the whole migration does not apply -- fail fast
-- rather than schedule a job that silently never runs.
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

CREATE EXTENSION IF NOT EXISTS pg_cron;

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

-- No SET search_path clause here: Postgres implements a procedure's SET
-- clause by wrapping the CALL in a sub-transaction to restore the setting
-- afterward, and that wrapper rejects a COMMIT inside the body ("invalid
-- transaction termination") -- confirmed by hand against a live container,
-- see PR description. Every reference below is already schema-qualified
-- (public.metering_shadow_verdicts, public.metering_shadow_verdicts_
-- retention_config), so search_path pinning buys nothing here anyway.
CREATE OR REPLACE PROCEDURE public.purge_metering_shadow_verdicts(batch_size integer DEFAULT 500)
LANGUAGE plpgsql
AS $$
DECLARE
  window_days   integer;
  deleted_count integer;
BEGIN
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

SELECT cron.schedule(
  'metering-shadow-verdicts-purge',
  '0 21 * * *', -- 21:00 UTC = 03:00 Asia/Dhaka
  $$CALL public.purge_metering_shadow_verdicts();$$
);

COMMIT;
