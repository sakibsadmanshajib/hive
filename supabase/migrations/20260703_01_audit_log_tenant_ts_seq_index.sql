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
--
-- Lock tradeoff (DB review follow-up):
--   CREATE INDEX CONCURRENTLY is NOT supported on a partitioned parent table
--   in Postgres (only on a single ordinary table, or on one partition at a
--   time). Building the index this way is the only direct, single-statement
--   option; Postgres recurses the CREATE INDEX to each existing partition
--   and takes a normal (non-concurrent) lock -- ACCESS EXCLUSIVE on each
--   partition individually, held only for that partition's build, one
--   partition at a time -- which blocks reads and writes against the
--   partition currently being indexed until its build finishes.
--
--   Accepted here because this table is pre-production scale (Carl.sh
--   sovereign edge, no live customer traffic writing to audit_log yet), so
--   the brief per-partition lock has no user-facing impact. If this ever
--   needs to run against a hot production audit_log, do NOT repeat this
--   pattern: build the index on each partition individually with
--   CREATE INDEX CONCURRENTLY (supported for a single non-partitioned
--   table), then ALTER INDEX ... ATTACH PARTITION into a parent index
--   created with ONLY, so no partition is ever locked for the DDL.
--
--   audit_log_tenant_ts_idx (tenant_id, ts DESC) already exists and overlaps
--   with the leading two columns of this index; left in place here per
--   review, tracked as a follow-up candidate for consolidation, not dropped
--   in this migration.

BEGIN;

CREATE INDEX IF NOT EXISTS audit_log_tenant_ts_seq_idx
    ON public.audit_log (tenant_id, ts, seq);

COMMIT;
