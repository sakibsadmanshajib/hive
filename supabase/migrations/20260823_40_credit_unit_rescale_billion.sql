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
--   public.payment_invoices.credits
--     absolute credit quantity on each issued invoice; scaled so historical
--     invoices keep stating the same real-money purchase after the cutover.
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
--   Three mechanisms:
--   1. Every rescaled nonzero ledger entry and reservation event gains a
--      metadata key: {"credit_unit": "legacy-1usd-100k-credits"}.
--   2. The NEW binary stamps every ledger entry it writes and every payment
--      intent it creates with {"credit_unit": "v2-1usd-1e9"} (ledger
--      repository PostEntry and payments.InitiateCheckout, same pull
--      request). Together with 1 this makes every writer identifiable PER
--      ROW on both sides of the deploy, which closes the detection gap: an
--      OLD-unit row written between this migration's COMMIT and the
--      container recreate carries NO credit_unit key at all, because the old
--      binaries predate stamping. Zero-delta rows carry no flag because zero
--      means the same thing in both units.
--   3. public.credit_unit_rescale holds exactly one row whose applied_at is
--      clock_timestamp() taken at the END of the work (now() would be
--      transaction START): the wall-clock upper bound of everything this
--      file could have rescaled. Boundary for tables without a metadata
--      column. This file itself, in the migration history, is the durable
--      declaration of when the unit changed.
--
-- IDEMPOTENCY / REPLAY PROOF
--   The whole body is guarded by the marker table: if
--   public.credit_unit_rescale already holds a row, the DO block returns
--   before touching data, so applying this file twice multiplies nothing.
--   Single-rowness is enforced STRUCTURALLY (PRIMARY KEY pinned to id = 1),
--   so even two concurrent first applications serialize: the loser's INSERT
--   violates the PK and its ENTIRE transaction rolls back, undoing every
--   UPDATE it made inside that transaction. Proof sketch for the reviewer:
--   run the file twice against any database; the second run reports 'already
--   applied' and changes zero rows. Corollary: NEVER delete or truncate
--   public.credit_unit_rescale. With the marker gone, a replay would double
--   every balance, the one way this migration can corrupt data; the RLS
--   lockdown below removes the anon/authenticated paths to exactly that.
--
-- RESIDUAL RACE, STATED RATHER THAN HIDDEN
--   The deploy pipeline applies migrations while the PREVIOUS control-plane
--   and edge-api binaries are still serving. A request committing an
--   OLD-unit ledger row after this transaction's COMMIT lands UNSCALED and
--   UNFLAGGED (old binaries do not stamp), until the containers are
--   recreated seconds to minutes later. That is why detection keys on the
--   metadata marker rather than created_at: see STRAGGLER DETECTOR below.
--   On a quiet box the expected count is zero.
--
-- POST-DEPLOY VERIFICATION (orchestrator runs these against the box; they are
-- checks, not writes):
--   -- owner account balance should read ~99,997,990,000 (was 9,999,799):
--   SELECT sum(credits_delta) FROM public.credit_ledger_entries
--    WHERE account_id = '<owner-account-uuid>';
--   -- STRAGGLER DETECTOR, covers BOTH window halves (rows the scan missed
--   -- pre-boundary AND old-binary rows written during the recreate window):
--   -- every writer stamps its rows EXCEPT the unscaled ones, so ANY hit is
--   -- an unscaled row needing one flagged UPDATE x10000. Expected: 0 rows.
--   SELECT account_id, count(*), sum(credits_delta)
--     FROM public.credit_ledger_entries
--    WHERE credits_delta <> 0 AND NOT (metadata ? 'credit_unit')
--    GROUP BY account_id;
--   SELECT id, status, credits FROM public.payment_intents
--    WHERE credits <> 0 AND NOT (metadata ? 'credit_unit');
--   -- catalog spot check (hive-default was 5250 / 21000):
--   SELECT input_price_credits, output_price_credits FROM public.model_aliases
--    WHERE alias_id = 'hive-default';   -- expect 52500000 / 210000000
-- =============================================================================

BEGIN;

SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS public.credit_unit_rescale (
    id          integer     PRIMARY KEY CHECK (id = 1),
    applied_at  timestamptz NOT NULL
);

COMMENT ON TABLE public.credit_unit_rescale IS
    'One-row marker for migration 20260823_40 (credit unit rescale, factor 10000). applied_at is the clock_timestamp() upper bound of the work this file did; the id=1 PRIMARY KEY makes a concurrent replay fail loudly instead of double-scaling. NEVER delete or truncate: emptying this table lets a replay multiply every balance again.';

-- Lockdown, same shape as the 20260529_01 ledger family and
-- 20260801_01_payment_webhook_deliveries: force RLS with no policy for the
-- published-key roles, so PostgREST can neither read nor mutate the marker.
-- DELETE here is the named path to doubling every balance on replay; it must
-- not be reachable by anon or authenticated under any circumstance. The role
-- statements are guarded on role existence so this file also applies to a
-- plain Postgres (CI throwaway) that has no Supabase-managed roles.
ALTER TABLE public.credit_unit_rescale ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.credit_unit_rescale FORCE ROW LEVEL SECURITY;
do $lockdown$
begin
  if exists (select 1 from pg_roles where rolname = 'anon') then
    execute 'revoke all on public.credit_unit_rescale from anon';
  end if;
  if exists (select 1 from pg_roles where rolname = 'authenticated') then
    execute 'revoke all on public.credit_unit_rescale from authenticated';
  end if;
end $lockdown$;

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

    -- 10b. Issued invoices state purchased credit quantities.
    UPDATE public.payment_invoices
       SET credits = credits * factor
     WHERE credits <> 0;

    -- 11. Arm the guard LAST. clock_timestamp(), not now(): now() is this
    --     transaction's start, and a straggler row written by another
    --     transaction DURING our window carries a created_at between those
    --     two instants, so the post-deploy detector below must compare
    --     against the LATEST possible wall-clock boundary to catch it.
    INSERT INTO public.credit_unit_rescale (id, applied_at) VALUES (1, clock_timestamp());
END
$rescale$;

COMMIT;
