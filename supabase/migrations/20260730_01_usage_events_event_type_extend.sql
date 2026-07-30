-- supabase/migrations/20260730_01_usage_events_event_type_extend.sql
--
-- PR #602 (streaming disconnect settlement) has edge-api write usage_events
-- rows with event_type 'upstream_error' and 'finalize_failed' (settleStream,
-- apps/edge-api/internal/inference/stream.go), and 'interrupted' (already
-- used as a request_attempts.status value, but never previously written as
-- a usage_events.event_type). None of the three were in the CHECK constraint
-- 20260330_02_usage_accounting.sql put on usage_events.event_type, so every
-- one of these inserts has been failing and silently dropped since the
-- disconnect-settlement code first shipped -- the app code discards the
-- insert error (see RecordUsageEvent callers in stream.go).
--
-- This matters beyond audit completeness: metering Step 2 is currently
-- shadow-mode recording exactly this data, and Step 4's future enforcement
-- thresholds get set from it. A silent hole in disconnect/error telemetry
-- biases that decision before it is even made.
--
-- Idempotent against a populated table: DROP CONSTRAINT IF EXISTS then
-- re-ADD with the extended, strictly broader list -- every row that already
-- satisfied the old constraint still satisfies this one, so no data rewrite
-- or backfill is needed.
--
-- usage_events is a hot metering table with continuous writes, so the ADD
-- CONSTRAINT + VALIDATE steps are split (NOT VALID, then a separate
-- VALIDATE CONSTRAINT statement, no shared BEGIN/COMMIT) rather than a bare
-- ADD CONSTRAINT: a bare ADD CONSTRAINT validates every existing row before
-- committing and holds ACCESS EXCLUSIVE for the whole scan, blocking reads
-- and writes for its duration. NOT VALID skips that scan when the
-- constraint is added (ACCESS EXCLUSIVE held only for the instant catalog
-- update), and the follow-up VALIDATE CONSTRAINT does the scan under
-- SHARE UPDATE EXCLUSIVE, which does not block concurrent reads or writes.
-- Matches the pattern already used in 20260507_01_invoices_period_order_check.sql.
-- Deliberately no wrapping BEGIN/COMMIT: keeping ADD and VALIDATE as
-- separate auto-committed statements is what lets VALIDATE run under the
-- weaker lock instead of inheriting ACCESS EXCLUSIVE from the same
-- transaction as ADD CONSTRAINT.

ALTER TABLE public.usage_events
    DROP CONSTRAINT IF EXISTS usage_events_event_type_check;

ALTER TABLE public.usage_events
    ADD CONSTRAINT usage_events_event_type_check
    CHECK (event_type IN (
        'accepted', 'reservation_created', 'stream_update', 'completed',
        'released', 'refunded', 'error', 'reconciled',
        'upstream_error', 'finalize_failed', 'interrupted'
    )) NOT VALID;

ALTER TABLE public.usage_events
    VALIDATE CONSTRAINT usage_events_event_type_check;
