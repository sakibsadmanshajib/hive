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
-- A DEFAULT partition is intentionally allowed to hold rows that "leak past"
-- the schedule; the monthly maintenance task will detach + reattach those
-- rows into a freshly-created proper partition. Reads of the chain remain
-- correct because the verifier orders by (tenant_id, seq) within the same
-- ts window, and INSERTs into the DEFAULT partition still acquire the same
-- per-partition seq locks via the partitioned table.
--
-- Constraints carried into the DEFAULT partition mirror the explicit ones:
--   - octet_length(prev_hash) = 32 and octet_length(row_hash) = 32 inherit
--     from the parent table CHECKs automatically — no re-declaration.
--   - The UNIQUE INDEX on seq is per-partition; we add it to the DEFAULT
--     so that two writes that fall into the default cannot collide on seq.

BEGIN;

CREATE TABLE IF NOT EXISTS public.audit_log_default
  PARTITION OF public.audit_log DEFAULT;

CREATE UNIQUE INDEX IF NOT EXISTS audit_log_default_seq_uidx
  ON public.audit_log_default (seq);

CREATE TABLE IF NOT EXISTS public.llm_traces_default
  PARTITION OF public.llm_traces DEFAULT;

COMMIT;
