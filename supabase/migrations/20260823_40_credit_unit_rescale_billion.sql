-- =============================================================================
-- Credit unit rescale: 1 USD = 1,000,000,000 Hive Credits (owner directive,
-- 2026-08-23). Companion to apps/control-plane/internal/payments/types.go,
-- which changes CreditsPerUSD from 100,000 to 1,000,000,000 in the same pull
-- request.
--
-- WHY A DATA MIGRATION AND NOT JUST A CONSTANT CHANGE
--   The credit unit is load-bearing everywhere a number is stored. Changing
--   the constant alone would leave every existing balance, hold, charge and
--   catalog price denominated in the OLD unit while new code bills in the NEW
--   one: an account holding 9,999,799 old credits would read as about $0.0001
--   overnight instead of its real $99.99. This migration multiplies every
--   stored credit figure by exactly 10,000 (= 1,000,000,000 / 100,000) so the
--   REAL MONEY value of every row is unchanged across the cutover. Zero stays
--   zero under multiplication, so zero balances need no special case.
--
-- WHAT IS SCALED (every column that stores an absolute credit quantity)
--   public.model_aliases
--     input_price_credits, output_price_credits,
--     cache_read_price_credits, cache_write_price_credits
--       credits per MILLION metered units (tokens / characters / seconds).
--       Without this, every request would charge 10,000x less real money than
--       the day before. The upstream_actual alias (openrouter-auto) carries
--       NULL in all four columns; x keeps NULL, so the pricing-mode shape
--       CHECK is untouched.
--     reservation_estimate_credits
--       the up-front hold for the upstream_actual alias (2.00 USD-equivalent
--       before and after: 200000 old = 2,000,000,000 new). The Go guard
--       TestTheHoldProvablyCoversTheWorstBoundedRequest re-reads this value
--       through the rescale factor, so a hold that stops covering the bounded
--       worst-case request still fails CI.
--   public.credit_ledger_entries.credits_delta
--     the immutable ledger. Balance = sum(credits_delta), so this is the row
--     that keeps every customer's apparent money intact.
--   public.credit_reservations.reserved_credits / consumed_credits /
--     released_credits -- live holds included. An active hold left in old
--     units would release 9,999x less than it took.
--   public.credit_reservation_events.credits_delta
--   public.payment_intents.credits
--     CRITICAL ordering property: deploy-demo-box.yml runs migrations BEFORE
--     recreating containers, so an intent created before the deploy can be
--     settled by the NEW binary afterwards. Its grant amount comes from
--     intent.Credits; leaving it unscaled would grant 10,000x less than the
--     customer paid for.
--   public.account_budget_thresholds.threshold_credits
--   public.api_key_policies.budget_limit_credits
--   public.api_key_usage_rollups.consumed_credits
--   public.api_key_budget_windows.consumed_credits / reserved_credits
--   public.batches.estimated_credits / actual_credits
--   public.batch_lines.consumed_credits (numeric(20,6); x10000 is exact)
--   public.llm_traces.cost_credits
--
-- WHAT IS DELIBERATELY NOT SCALED
--   public.metering_shadow_verdicts.estimated_credits_legacy is NOT a credit
--   figure at all: its own column comment records "today's int64(total_tokens)
--   convention", i.e. raw token counts. estimated_credits_per_model is
--   provisional shadow telemetry with its unit explicitly still TBD. Neither
--   feeds a balance, a hold or a charge.
--   Budgets hard_cap_bdt_subunits / soft_cap_bdt_subunits are BDT paisa, not
--   credits. Rate limits (requests_per_minute, tokens_per_minute,
--   rolling_five_hour_limit, weekly_limit) are tokens or requests, not
--   credits. Token counts everywhere are untouched, obviously.
--
-- AUDIT MARKING: HOW TO TELL AN OLD-UNIT ROW FROM A NEW-UNIT ONE
--   Three mechanisms, in increasing order of convenience:
--   1. Every rescaled nonzero ledger entry and reservation event gains a
--      metadata key: {"credit_unit": "legacy-1usd-100k-credits"}. New rows
--      written by the new binary simply do not carry it. This is the
--      per-row flag an auditor can filter on. Zero-delta rows carry no flag
--      because zero means the same thing in both units.
--   2. public.credit_unit_rescale holds one row with the transaction's
--      applied_at timestamp: any credit-denominated row created BEFORE that
--      instant was written by the old binary in old units (and has been
--      rescaled here); anything after is native new-unit. This is the
--      boundary for tables without a metadata column.
--   3. This file itself, recorded in the migration history, is the durable
--      declaration of when the unit changed.
--
-- IDEMPOTENCY / REPLAY PROOF
--   The whole body is guarded by the marker table: if
--   public.credit_unit_rescale already holds a row, the DO block returns
--   before touching data, so applying this file twice multiplies nothing.
--   Proof sketch for the reviewer: run the file twice against any database;
--   the second run reports 'already applied' and changes zero rows (the
--   UPDATE statements never execute). Corollary: NEVER delete or truncate
--   public.credit_unit_rescale. With the marker gone, a replay would double
--   every balance, which is the one way this migration can corrupt data.
--
-- RESIDUAL RACE, STATED RATHER THAN HIDDEN
--   The deploy pipeline applies migrations while the PREVIOUS control-plane
--   and edge-api binaries are still serving. A request that commits an
--   OLD-unit ledger row between this transaction's snapshot and its COMMIT
--   would miss the scan and stay unscaled. The window is the duration of this
--   transaction (all tables together are a few tens of thousands of small
--   rows), and the post-deploy verification below detects it. On a quiet box
--   the expected count of stragglers is zero.
--
-- POST-DEPLOY VERIFICATION (orchestrator runs these against the box; they are
-- checks, not writes):
--   -- owner account balance should read ~99,997,990,000 (was 9,999,799):
--   SELECT sum(credits_delta) FROM public.credit_ledger_entries
--    WHERE account_id = '<owner-account-uuid>';
--   -- no unflagged nonzero row may predate the marker (straggler detector):
--   SELECT count(*) FROM public.credit_ledger_entries e
--    WHERE e.credits_delta <> 0
--      AND e.created_at < (SELECT applied_at FROM public.credit_unit_rescale)
--      AND NOT (e.metadata ? 'credit_unit');
--   -- catalog spot check (hive-default was 5250 / 21000):
--   SELECT input_price_credits, output_price_credits FROM public.model_aliases
--    WHERE alias_id = 'hive-default';   -- expect 52500000 / 210000000
-- =============================================================================

BEGIN;

SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS public.credit_unit_rescale (
    applied_at timestamptz NOT NULL
);

COMMENT ON TABLE public.credit_unit_rescale IS
    'One-row marker for migration 20260823_40 (credit unit rescale, factor 10000). applied_at is the old-unit/new-unit boundary instant for every credit-denominated column. Never truncate: the rescale DO block skips when this table is non-empty, so emptying it makes a replay double every balance.';

DO $rescale$
DECLARE
    factor CONSTANT bigint := 10000;  -- 1,000,000,000 / 100,000
BEGIN
    IF EXISTS (SELECT 1 FROM public.credit_unit_rescale) THEN
        RAISE NOTICE 'credit unit rescale already applied; skipping';
        RETURN;
    END IF;

    -- 1. Catalog prices: credits per million metered units. NULL (the
    --    upstream_actual alias) passes through unchanged.
    UPDATE public.model_aliases
       SET input_price_credits       = input_price_credits       * factor,
           output_price_credits      = output_price_credits      * factor,
           cache_read_price_credits  = cache_read_price_credits  * factor,
           cache_write_price_credits = cache_write_price_credits * factor,
           updated_at                = now();

    -- 2. The variable-price alias's up-front hold (openrouter-auto only).
    UPDATE public.model_aliases
       SET reservation_estimate_credits = reservation_estimate_credits * factor
     WHERE reservation_estimate_credits IS NOT NULL;

    -- 3. Ledger history. Flagged per-row so an auditor can tell rescaled
    --    old-unit rows from native new-unit ones.
    UPDATE public.credit_ledger_entries
       SET credits_delta = credits_delta * factor,
           metadata = metadata || jsonb_build_object(
               'credit_unit', 'legacy-1usd-100k-credits')
     WHERE credits_delta <> 0;

    -- 4. Live and historical reservations. reserved_credits carries a > 0
    --    CHECK; scaling positives keeps them positive.
    UPDATE public.credit_reservations
       SET reserved_credits = reserved_credits * factor,
           consumed_credits = consumed_credits * factor,
           released_credits = released_credits * factor;

    -- 5. Reservation lifecycle events, flagged like the ledger.
    UPDATE public.credit_reservation_events
       SET credits_delta = credits_delta * factor,
           metadata = metadata || jsonb_build_object(
               'credit_unit', 'legacy-1usd-100k-credits')
     WHERE credits_delta <> 0;

    -- 6. Payment intents. Must precede the first settlement the new binary
    --    performs on a pre-deploy intent (see header).
    UPDATE public.payment_intents
       SET credits = credits * factor;

    -- 7. Per-key budget limits and thresholds, both user-set credit figures.
    UPDATE public.api_key_policies
       SET budget_limit_credits = budget_limit_credits * factor
     WHERE budget_limit_credits IS NOT NULL AND budget_limit_credits > 0;

    UPDATE public.account_budget_thresholds
       SET threshold_credits = threshold_credits * factor
     WHERE threshold_credits > 0;

    -- 8. Usage accounting counters.
    UPDATE public.api_key_usage_rollups
       SET consumed_credits = consumed_credits * factor
     WHERE consumed_credits <> 0;

    UPDATE public.api_key_budget_windows
       SET consumed_credits = consumed_credits * factor,
           reserved_credits = reserved_credits * factor
     WHERE consumed_credits <> 0 OR reserved_credits <> 0;

    -- 9. Batch accounting.
    UPDATE public.batches
       SET estimated_credits = estimated_credits * factor,
           actual_credits    = actual_credits    * factor
     WHERE estimated_credits <> 0 OR actual_credits <> 0;

    UPDATE public.batch_lines
       SET consumed_credits = consumed_credits * factor
     WHERE consumed_credits <> 0;

    -- 10. Trace cost telemetry.
    UPDATE public.llm_traces
       SET cost_credits = cost_credits * factor
     WHERE cost_credits <> 0;

    -- 11. Arm the guard LAST, so the boundary instant postdates every write
    --     this transaction could possibly have rescaled.
    INSERT INTO public.credit_unit_rescale (applied_at) VALUES (now());
END
$rescale$;

COMMIT;
