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

BEGIN;

ALTER TABLE public.usage_events
    DROP CONSTRAINT IF EXISTS usage_events_event_type_check;

ALTER TABLE public.usage_events
    ADD CONSTRAINT usage_events_event_type_check
    CHECK (event_type IN (
        'accepted', 'reservation_created', 'stream_update', 'completed',
        'released', 'refunded', 'error', 'reconciled',
        'upstream_error', 'finalize_failed', 'interrupted'
    ));

COMMIT;
