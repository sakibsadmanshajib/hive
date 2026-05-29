-- supabase/migrations/20260518_03_phase19_audit_partitions_default.sql
-- Phase 19 remediation (C1) — partition cliff before 2026-07-01.
--
-- The original Phase 19 audit + LLM-trace tables (20260516_04 + 20260516_06)
-- created only two explicit monthly partitions (_2026_05, _2026_06). Without
-- a DEFAULT partition every INSERT after 2026-06-30 23:59:59 UTC fails with
-- "no partition of relation \"audit_log\" found for row" — taking the entire
-- audit chain and LLM-trace pipeline offline until an operator manually adds
-- the next month. A scheduled monthly cron is the long-term answer (planned
-- under Phase 19 follow-up) but a DEFAULT partition is the unconditional
-- backstop so a missed cron is recoverable rather than catastrophic.
--
-- ROW-MOVE CONTRACT (do NOT use DETACH on the DEFAULT partition).
-- The monthly maintenance job must:
--   1. CREATE the next named partition (e.g. audit_log_2026_07).
--   2. In ONE transaction: INSERT … SELECT into the new partition + DELETE
--      from public.audit_log where ts falls in the new month. Postgres
--      routes the INSERT to the new partition and DELETE removes rows
--      currently sitting in DEFAULT.
-- DO NOT `ALTER TABLE audit_log DETACH PARTITION audit_log_default` —
-- detach-then-create fails if DEFAULT holds any row whose ts falls in the
-- new partition's range. The row-move pattern above is the only safe path.
--
-- UNIQUENESS CONTRACT for audit_log_default:
--   The named per-month partitions enforce `UNIQUE (seq)` because seq is
--   assigned partition-globally (writer reads `MAX(seq) WHERE ts >=
--   monthStart AND ts < monthEnd`). If DEFAULT ever holds rows from two
--   different months simultaneously (e.g. June rolled over before the
--   July partition was created, then July also rolled past creation),
--   each month's seq series independently restarts at 1 and a plain
--   `UNIQUE (seq)` would collide. The DEFAULT partition therefore uses
--   `UNIQUE (date_trunc('month', ts AT TIME ZONE 'UTC'), seq)` to scope
--   uniqueness per month — matches the writer's per-month MAX(seq) read.
--   The `AT TIME ZONE 'UTC'` cast is mandatory: date_trunc on a bare
--   timestamptz is only STABLE (its month boundary depends on the
--   session TimeZone), and Postgres rejects non-IMMUTABLE expressions in
--   index definitions ("functions in index expression must be marked
--   IMMUTABLE"). Casting to a fixed UTC wall-clock timestamp makes the
--   expression immutable and buckets by the same UTC calendar month the
--   writer uses (canonical timestamps are always UTC).
--
-- Constraints carried by inheritance (NOT re-declared):
--   `octet_length(prev_hash) = 32` and `octet_length(row_hash) = 32`
--   inherit from the audit_log parent's CHECKs automatically.
--   The llm_traces parent has no `seq` column and no hash chain, so the
--   uniqueness reasoning above does NOT apply to llm_traces_default.
--
-- GRANT inheritance: Postgres does NOT cascade table-level GRANTs to
-- partition child tables created AFTER the grant was made. Explicit
-- GRANT/REVOKE on the DEFAULT partitions is mandatory.

BEGIN;

CREATE TABLE IF NOT EXISTS public.audit_log_default
  PARTITION OF public.audit_log DEFAULT;

-- Per-month seq uniqueness inside the DEFAULT partition. Plain
-- UNIQUE(seq) would collide if two months land in DEFAULT
-- simultaneously (both restart seq at 1 inside their own month).
CREATE UNIQUE INDEX IF NOT EXISTS audit_log_default_month_seq_uidx
  ON public.audit_log_default (date_trunc('month', ts AT TIME ZONE 'UTC'), seq);

REVOKE ALL ON public.audit_log_default FROM PUBLIC;
GRANT INSERT, SELECT ON public.audit_log_default TO hive_app;
GRANT SELECT ON public.audit_log_default TO auditor_ro;

CREATE TABLE IF NOT EXISTS public.llm_traces_default
  PARTITION OF public.llm_traces DEFAULT;

-- llm_traces was missing a REVOKE PUBLIC in the original migration;
-- close that gap on the parent AND apply explicit grants to the new
-- DEFAULT partition (parent grants do not cascade to child partitions
-- created later).
REVOKE ALL ON public.llm_traces FROM PUBLIC;
REVOKE ALL ON public.llm_traces_default FROM PUBLIC;
GRANT INSERT, SELECT ON public.llm_traces_default TO hive_app;
GRANT SELECT ON public.llm_traces_default TO auditor_ro;

COMMIT;
