-- =============================================================================
-- usage_events 'completed' dedup + ledger-reconciled rescale backfill
-- (issue #1180, review round 2).
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
--   not. Every authoritative row written in the OLD unit is therefore off
--   from its matching ledger entry by exactly 10000x (a stale unit, not a
--   wrong rate) -- confirmed live: 018658cd.. reads -12 in usage_events
--   against -120000 in the ledger for the identical attempt, and
--   -12 * 10000 = -120000 exactly, same pattern on every stale-unit row
--   checked.
--
-- REVIEW ROUND 2: WHY THIS FILE NO LONGER USES created_at < applied_at
--   The first version of this migration bounded the rescale backfill on
--   usage_events.created_at < credit_unit_rescale.applied_at, mirroring
--   20260823_40's own straggler-detection language. That bound
--   UNDER-APPLIES: 20260823_40 itself documents a deploy-gap race where the
--   OLD binary keeps writing OLD-unit rows for some seconds-to-minutes
--   AFTER the migration's COMMIT (between COMMIT and the container
--   recreate), so a straggler row can carry created_at > applied_at while
--   still being an old-unit value. A created_at bound skips exactly those
--   rows, permanently: usage_events carries no positive new-unit stamp of
--   its own, so nothing could later tell a missed straggler apart from a
--   genuinely small, correctly-scaled delta.
--
--   Fix: stop inferring the unit from a timestamp and reconcile directly
--   against credit_ledger_entries instead, which 20260823_40 DID correctly
--   backfill and which this migration's own header already establishes as
--   the authoritative surface (finalizeLocked charges the ledger with the
--   same actualCredits value it mirrors onto usage_events). For every
--   surviving 'completed' row, if its hive_credit_delta disagrees with the
--   matching credit_ledger_entries.credits_delta (same account_id,
--   same attempt_id, entry_type = 'usage_charge') by EXACTLY a factor of
--   10000, it is unambiguously the stale-unit case and gets overwritten
--   with the ledger's value -- not multiplied, copied, so the result is
--   the true figure regardless of which direction any rounding went. A
--   disagreement that is NOT exactly 10000x is left untouched: that would
--   be a different, unknown mismatch this migration has no evidence about,
--   and silently "fixing" it would be a guess, not a correction. This
--   covers every stale-unit row there is or ever was, including the
--   deploy-gap stragglers a timestamp bound could never see, with no
--   dependency on created_at or on credit_unit_rescale.applied_at at all.
--
-- WHAT THIS FILE DOES (order matters: guard, then dedup, then reconcile, so
-- reconciliation sees each attempt exactly once)
--   0. Refuse to run if any (account_id, request_attempt_id) has THREE OR
--      MORE 'completed' rows. The merge in step 1 is a pairwise
--      keep-earliest/fold-latest operation; live evidence supports at most
--      two (576 pairs, 0 triples, 2026-08-25), and a many-to-one UPDATE on
--      an unexpected triple would let Postgres pick an arbitrary source row
--      for the folded token columns rather than a deliberate one. A triple
--      is a different, unexplained shape that a human should look at, not
--      an assumption this migration should make silently. Raises and rolls
--      back the whole transaction; changes nothing.
--   1. For every (account_id, request_attempt_id) with more than one
--      event_type = 'completed' row, keep the earliest (the authoritative,
--      ledger-matching one), fold the token/provider columns from the
--      later row into it via GREATEST/COALESCE (never destructive: the
--      authoritative row's own columns are 0 in every case measured), stamp
--      which duplicate id was merged into internal_metadata for forensics,
--      and delete the later row. hive_credit_delta and event_type on the
--      keeper are untouched.
--   2. Reconcile stale-unit rows against the ledger (see above): copy
--      credit_ledger_entries.credits_delta onto any surviving row whose
--      hive_credit_delta is exactly 1/10000th of it. Flag key/value
--      ('credit_unit', 'legacy-1usd-100k-credits') is the exact one
--      20260823_40 already uses on credit_ledger_entries and
--      credit_reservation_events, so a corrected row is identifiable the
--      same way across all three tables.
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
--   Step 0 only reads; nothing to replay.
--   Step 1 is naturally idempotent: after the first run no (account_id,
--   request_attempt_id) has more than one 'completed' row, so the dedup CTE
--   selects nothing on replay.
--   Step 2 is naturally idempotent too, independent of the credit_unit flag:
--   once a row is corrected it agrees with the ledger, so the "disagrees by
--   exactly 10000x" predicate no longer matches it on replay. The flag is
--   set for forensic identifiability, not for idempotency.
--   Step 3 uses IF NOT EXISTS.
--
-- ROLLBACK ASYMMETRY (documented, not fixed here; see PR body)
--   This migration has no down-migration. Rolling it back while
--   control-plane's binary stays on the version that assumes
--   ux_usage_events_completed_attempt exists (the ON CONFLICT target in
--   usage/repository.go's RecordEvent) breaks every 'completed' usage_events
--   write: Postgres rejects an ON CONFLICT clause naming an index that does
--   not exist. That is a loud error on every completed request, not a silent
--   money defect, which is why it is acceptable -- but a migration-only
--   rollback must never be attempted without rolling the binary back first.
--
-- POST-MERGE FIX (2026-08-25, before this file ever ran successfully anywhere)
--   This file merged (PR #1194) and failed on the first deploy attempt
--   (deploy-demo-box run 32912034013): "insert or update on table
--   usage_events violates foreign key constraint
--   usage_events_request_attempt_id_fkey ... Key (request_attempt_id)=
--   (819cc2ad-...) is not present in table request_attempts". BEGIN/COMMIT
--   wraps the whole file, so the failure rolled back cleanly -- verified live
--   (duplicate 'completed' pairs and the missing index were both still in
--   their pre-migration state after the failed run) -- and the file was never
--   recorded in public.hive_schema_migrations, so amending it here in place
--   is safe: nothing has ever applied this exact SQL.
--
--   Root cause is issue #1102, not this migration: a retention purge deletes
--   public.request_attempts rows without cascading (or nulling) the rows in
--   usage_events, credit_reservations and credit_reconciliation_jobs that
--   reference them, despite the FK itself being ON DELETE CASCADE -- the
--   purge bypasses the cascade trigger rather than going through it. Live
--   count 2026-08-25: 483 orphaned usage_events rows spanning 2026-04-01
--   through 2026-08-18, i.e. an ongoing, ordinary state of this table, not a
--   one-off. Step 2's reconciliation UPDATE has no reason to touch a row
--   whose parent attempt no longer exists -- there is nothing left to
--   reconcile it against with confidence -- so it now skips any row lacking a
--   live request_attempts parent, via the added EXISTS guard below. Step 1's
--   dedup UPDATE/DELETE needed no equivalent change: it was proven safe
--   against this same live orphaned data (twice, in a rolled-back replay)
--   before this fix was written. Fixing the purge itself is issue #1102's
--   job, not this migration's.
-- =============================================================================

BEGIN;

SET LOCAL lock_timeout = '5s';

-- ---------------------------------------------------------------------------
-- Step 0: refuse to guess on a shape this migration has no evidence for.
-- ---------------------------------------------------------------------------
DO $triple_guard$
DECLARE
    triple_count integer;
BEGIN
    SELECT count(*) INTO triple_count FROM (
        SELECT 1
        FROM public.usage_events
        WHERE event_type = 'completed'
        GROUP BY account_id, request_attempt_id
        HAVING count(*) > 2
    ) t;

    IF triple_count > 0 THEN
        RAISE EXCEPTION 'usage_events has % attempt(s) with 3+ completed rows; the pairwise dedup in this migration assumes at most 2 (live shape measured 2026-08-25: 576 pairs, 0 triples). A triple needs a human to pick the right row, not an automatic GREATEST/COALESCE guess across an unplanned third writer. Resolve manually (identify the extra row(s), decide which to keep), then re-run this migration.', triple_count;
    END IF;
END
$triple_guard$;

-- ---------------------------------------------------------------------------
-- Step 1: fold duplicate 'completed' rows into the earliest (authoritative)
-- one per attempt.
-- ---------------------------------------------------------------------------
CREATE TEMP TABLE _usage_events_completed_ranked ON COMMIT DROP AS
SELECT id, account_id, request_attempt_id, input_tokens, output_tokens,
       cache_read_tokens, cache_write_tokens, provider_request_id,
       row_number() OVER (
         PARTITION BY account_id, request_attempt_id ORDER BY created_at ASC, id ASC
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
-- Step 2: reconcile stale-unit rows against the ledger (see header for why
-- this replaced a created_at boundary). No dependency on
-- credit_unit_rescale.applied_at, so it catches deploy-gap stragglers a
-- timestamp bound structurally cannot.
--
-- The EXISTS guard is the post-merge fix documented at the top of this file
-- (issue #1102): a row whose request_attempt_id no longer has a parent in
-- request_attempts is left untouched, not reconciled. Skipping it is
-- deliberate, not a workaround for the FK error alone -- there is no live
-- attempt row left to trust the reconciliation against either way.
-- ---------------------------------------------------------------------------
UPDATE public.usage_events ue
   SET hive_credit_delta = cle.credits_delta,
       internal_metadata = ue.internal_metadata || jsonb_build_object(
           'credit_unit', 'legacy-1usd-100k-credits',
           'ledger_reconciled_from', ue.hive_credit_delta)
  FROM public.credit_ledger_entries cle
 WHERE cle.entry_type = 'usage_charge'
   AND cle.account_id = ue.account_id
   AND cle.attempt_id = ue.request_attempt_id
   AND ue.hive_credit_delta <> 0
   AND ue.hive_credit_delta <> cle.credits_delta
   AND ue.hive_credit_delta * 10000 = cle.credits_delta
   AND EXISTS (
     SELECT 1 FROM public.request_attempts ra WHERE ra.id = ue.request_attempt_id
   );

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
