-- supabase/migrations/20260703_01_audit_log_tenant_ts_seq_index.sql
--
-- Cleanup deferred from #258 (issue #261): the audit cold-archive fetch
-- (auditarchive.PgRepository.FetchOlderThan) filters on
-- (tenant_id, ts) and orders by (ts, seq) so the JSONL export preserves
-- chain order. Without a covering index for that exact shape, the planner
-- has to add an explicit sort node over every matched row for a
-- high-volume tenant instead of reading them back out already ordered.
--
-- audit_log is range-partitioned by ts (see 20260516_04_phase19_audit_log.sql);
-- this index is created on the parent so Postgres propagates it to each
-- monthly partition (and to future ones created by the same DDL pattern).

BEGIN;

CREATE INDEX IF NOT EXISTS audit_log_tenant_ts_seq_idx
    ON public.audit_log (tenant_id, ts, seq);

COMMIT;
