-- =============================================================================
-- usage_events 'completed' dedup + credit-unit rescale backfill (issue #1180).
--
-- WHAT WAS FOUND
--   Every request that goes through a reservation writes TWO usage_events
--   rows with the same (account_id, request_attempt_id, event_type =
--   'completed'):
--     1. Written by control-plane's own accounting.finalizeLocked
--        (apps/control-plane/internal/accounting/service.go), in-process,
--        during FinalizeReservation. hive_credit_delta = -actualCredits, the
--        exact figure charged to credit_ledger_entries in the same call.
--        This row is authoritative -- it matches the ledger by construction.
--     2. Written by edge-api's recordCompletedEvent (apps/edge-api/internal/
--        inference/orchestrator.go and stream.go), a SEPARATE, unconditional
--        HTTP POST to /internal/usage/events that always follows the
--        FinalizeReservation call. hive_credit_delta there is set to
--        usage.TotalTokens -- a raw token COUNT, not a credit amount, not
--        even negative. It is not a rescale or rounding difference from the
--        ledger figure, it is a categorically wrong value in that column.
--   Live measurement on the demo box (2026-08-25, all history): 576 pairs of
--   duplicate 'completed' rows, zero triples. In every single pair the
--   earlier-written row's hive_credit_delta is <= 0 (matches the ledger
--   pattern) and in 575 of 576 the later row's hive_credit_delta equals
--   input_tokens + output_tokens exactly (the TotalTokens assignment). This
--   is deterministic, not a race: edge-api only calls recordCompletedEvent
--   AFTER the synchronous FinalizeReservation HTTP call returns, so the
--   authoritative row is always inserted first.
--
--   A second, narrower defect compounds this: the authoritative row's own
--   input_tokens/output_tokens are 0, because neither orchestrator.go's nor
--   stream.go's FinalizeReservation call site populates
--   FinalizeReservationInput.InputTokens/OutputTokens, even though the wire
--   contract and control-plane's own plumbing already support it (#856).
--   That gap is in apps/edge-api, out of this migration's reach; the dedup
--   below compensates for it by keeping the token counts the second
--   (edge-api) write actually carries while keeping the credit delta the
--   first (ledger-backed) write actually carries.
--
--   Separately, and NOT the same root cause: usage_events.hive_credit_delta
--   was missing from the WHAT IS SCALED list in
--   20260823_40_credit_unit_rescale_billion.sql (D-046, factor 10000, 1 USD
--   1,000,000 -> 1,000,000,000 credits). credit_ledger_entries.credits_delta
--   was backfilled by that migration; usage_events.hive_credit_delta was
--   not. Every authoritative row written before that migration's boundary is
--   therefore off from its matching ledger entry by exactly 10000x (a stale
--   unit, not a wrong rate) -- confirmed live: 018658cd.. reads -12 in
--   usage_events against -120000 in the ledger for the identical attempt,
--   and -12 * 10000 = -120000 exactly, same pattern on every pre-boundary
--   row checked.
--
-- WHAT THIS FILE DOES (order matters: dedup first, THEN rescale, so the
-- rescale sees each attempt exactly once)
--   1. For every (account_id, request_attempt_id) with more than one
--      event_type = 'completed' row, keep the earliest (the authoritative,
--      ledger-matching one), fold the token/provider columns from the
--      later row(s) into it via GREATEST/COALESCE (never destructive: the
--      authoritative row's own columns are 0 in every case measured), stamp
--      which duplicate id(s) were merged into internal_metadata for
--      forensics, and delete the later row(s). hive_credit_delta and
--      event_type on the keeper are untouched.
--   2. Backfill the credit-unit rescale that 20260823_40 missed: multiply
--      hive_credit_delta by 10000 for every usage_events row with a nonzero
--      delta, created before the rescale marker's applied_at boundary, not
--      already flagged. Flag key/value ('credit_unit',
--      'legacy-1usd-100k-credits') is the exact one 20260823_40 already
--      uses on credit_ledger_entries and credit_reservation_events, so a
--      row is now identifiable as pre-rescale the same way across all three
--      tables.
--   3. Add a partial unique index so a future duplicate 'completed' POST
--      (edge-api is unchanged by this fix; it will keep sending one) folds
--      into the existing row instead of inserting a second one. This is the
--      arbiter the application-side ON CONFLICT in
--      apps/control-plane/internal/usage/repository.go targets.
--
-- WHY PLAIN CREATE INDEX, NOT CONCURRENTLY
--   Supabase migrations run inside one transaction (BEGIN/COMMIT below),
--   and CREATE INDEX CONCURRENTLY cannot run inside a transaction block, the
--   same constraint 20260824_01 already documented for this database.
--   usage_events is 2,173 rows / 1.4 MB on the demo box (measured
--   2026-08-25); a plain index build here is milliseconds, not the
--   ACCESS EXCLUSIVE hazard it would be on a table with real volume.
--
-- IDEMPOTENCY
--   Step 1 is naturally idempotent: after the first run no (account_id,
--   request_attempt_id) has more than one 'completed' row, so the dedup CTE
--   selects nothing on replay.
--   Step 2 guards on the same jsonb flag 20260823_40 already established
--   (`NOT (internal_metadata ? 'credit_unit')`), so a replay scales nothing
--   a prior run (or this run) already touched.
--   Step 3 uses IF NOT EXISTS.
-- =============================================================================

BEGIN;

SET LOCAL lock_timeout = '5s';

-- ---------------------------------------------------------------------------
-- Step 1: fold duplicate 'completed' rows into the earliest (authoritative)
-- one per attempt.
-- ---------------------------------------------------------------------------
CREATE TEMP TABLE _usage_events_completed_ranked ON COMMIT DROP AS
SELECT id, account_id, request_attempt_id, input_tokens, output_tokens,
       cache_read_tokens, cache_write_tokens, provider_request_id,
       row_number() OVER (
         PARTITION BY account_id, request_attempt_id ORDER BY created_at ASC
       ) AS rn
FROM public.usage_events
WHERE event_type = 'completed';

CREATE TEMP TABLE _usage_events_completed_keepers ON COMMIT DROP AS
SELECT * FROM _usage_events_completed_ranked WHERE rn = 1;

CREATE TEMP TABLE _usage_events_completed_dupes ON COMMIT DROP AS
SELECT * FROM _usage_events_completed_ranked WHERE rn > 1;

UPDATE public.usage_events ue
SET input_tokens        = GREATEST(ue.input_tokens, d.input_tokens),
    output_tokens        = GREATEST(ue.output_tokens, d.output_tokens),
    cache_read_tokens    = GREATEST(ue.cache_read_tokens, d.cache_read_tokens),
    cache_write_tokens   = GREATEST(ue.cache_write_tokens, d.cache_write_tokens),
    provider_request_id  = COALESCE(ue.provider_request_id, d.provider_request_id),
    internal_metadata    = ue.internal_metadata || jsonb_build_object(
        'merged_duplicate_completed_event_ids',
        (SELECT jsonb_agg(dd.id) FROM _usage_events_completed_dupes dd
          WHERE dd.account_id = ue.account_id
            AND dd.request_attempt_id = ue.request_attempt_id)
    )
FROM _usage_events_completed_dupes d
JOIN _usage_events_completed_keepers k
  ON k.account_id = d.account_id AND k.request_attempt_id = d.request_attempt_id
WHERE ue.id = k.id;

DELETE FROM public.usage_events ue
USING _usage_events_completed_dupes d
WHERE ue.id = d.id;

-- ---------------------------------------------------------------------------
-- Step 2: rescale backfill 20260823_40 omitted for this table.
-- ---------------------------------------------------------------------------
DO $backfill$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.credit_unit_rescale) THEN
        -- Rescale never ran on this database (e.g. a fresh CI throwaway
        -- seeded straight from HEAD) -- nothing pre-dates a boundary that
        -- does not exist, so there is nothing to backfill.
        RETURN;
    END IF;

    UPDATE public.usage_events
       SET hive_credit_delta = hive_credit_delta * 10000,
           internal_metadata = internal_metadata || jsonb_build_object(
               'credit_unit', 'legacy-1usd-100k-credits')
     WHERE hive_credit_delta <> 0
       AND created_at < (SELECT applied_at FROM public.credit_unit_rescale)
       AND NOT (internal_metadata ? 'credit_unit');
END
$backfill$;

-- ---------------------------------------------------------------------------
-- Step 3: prevent future duplicates. ON CONFLICT target for
-- apps/control-plane/internal/usage/repository.go's RecordEvent.
-- ---------------------------------------------------------------------------
CREATE UNIQUE INDEX IF NOT EXISTS ux_usage_events_completed_attempt
  ON public.usage_events (account_id, request_attempt_id)
  WHERE event_type = 'completed';

COMMENT ON INDEX public.ux_usage_events_completed_attempt IS
  'One completed usage_events row per attempt (issue #1180). Arbiter for RecordEvent''s ON CONFLICT: a second completed write (edge-api''s redundant POST, unchanged by this fix) folds its token counts into the existing row instead of inserting a duplicate with a bogus hive_credit_delta.';

COMMIT;
