-- =============================================================================
-- Issue #627: give every alias price an explicit unit, and price the two voice
-- aliases in the unit their provider actually bills.
--
-- WHY
--   apps/edge-api/internal/audio charged flat literals (1000 credits for any
--   speech request, 500 for any transcription) and never read the catalog at
--   all, so a one second clip and a ten minute clip cost the same. The handler
--   now derives the charge from the alias row, which forces the question this
--   column answers: text-to-speech is billed per CHARACTER and transcription
--   per unit of audio DURATION. Neither is a token. A per-token price for
--   either would be a made up number, so the unit is recorded explicitly and
--   edge-api refuses a request whose alias unit does not match the quantity
--   the endpoint meters, rather than converting between units silently.
--
--   20260801_01_alias_pricing_correction.sql deliberately left hive-tts and
--   hive-stt at their placeholder prices, because at that point no charge read
--   them. It does now, so they are re-derived here from the provider's own
--   published rates.
--
-- FORMULA (same mechanical shape as 20260801_01, only the unit changes)
--   credits_per_million_units = ceil(provider_list_usd_per_million_units
--                                    * MARGIN * CREDITS_PER_USD)
--     MARGIN          = 1.4
--     CREDITS_PER_USD = 100000   (apps/control-plane/internal/payments/types.go)
--
-- PROVIDER RATES, fetched 2026-08-01 from https://groq.com/pricing
--   Text-to-Speech table:  Canopy Labs Orpheus English  $22.00 per 1M characters
--     (the model route-groq-tts serves, per deploy/litellm/config.yaml)
--   ASR table:             Whisper V3 Large             $0.111 per hour transcribed
--     (the model route-groq-stt serves), with the table footnote "Audio is
--     billed at a minimum of 10s per request", which edge-api mirrors as a
--     minimum billable duration rather than encoding it in this row.
--
-- WORKED DERIVATION
--   hive-tts  characters  22.00 * 1.4 * 100000                    = 3080000
--   hive-stt  seconds     (0.111 / 3600 * 1000000) * 1.4 * 100000 = 4316667
--                          ^ USD per million seconds = 30.833333...
--                            30.833333... * 1.4 = 43.166666... USD
--                            * 100000 = 4316666.67, ceiling 4316667
--
--   Sanity, at the charge formula edge-api applies
--   (quantity * credits_per_million / 1000000, round half up):
--     5000 characters of speech -> 15400 credits (was a flat 1000)
--     1800 seconds (30 min) of audio -> 7770 credits (was a flat 500)
--
-- WHICH COLUMN HOLDS THE PRICE
--   A character-priced or second-priced alias meters exactly ONE quantity, so
--   only one of the two price columns can apply. output_price_credits is that
--   column (both voice rows were already seeded that way by
--   20260717_02_voice_groq_stt_tts.sql), and the CHECK below makes it
--   unambiguous by forbidding a nonzero input price on any non-token row. A
--   reader never has to guess which side of a single-quantity price to use.
--
-- RE-RUNNABILITY
--   ADD COLUMN IF NOT EXISTS, a named constraint added only when absent, and
--   UPDATEs keyed on a primary key assigning constants. No INSERTs, so no
--   ON CONFLICT clause is needed anywhere. Re-application is a no-op.
-- =============================================================================

-- 1. The unit itself. Default 'tokens' so every existing text alias keeps its
--    current meaning without being touched, and any future row is a token row
--    unless it says otherwise.
ALTER TABLE public.model_aliases
    ADD COLUMN IF NOT EXISTS price_unit text NOT NULL DEFAULT 'tokens';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'model_aliases_price_unit_allowed'
    ) THEN
        ALTER TABLE public.model_aliases
            ADD CONSTRAINT model_aliases_price_unit_allowed
            CHECK (price_unit IN ('tokens', 'characters', 'seconds'));
    END IF;
END $$;

-- 2. Re-derive the two voice prices in their real units, before the CHECK in
--    step 3 starts enforcing the zero input price.
UPDATE public.model_aliases
   SET price_unit           = 'characters',
       input_price_credits  = 0,
       output_price_credits = 3080000,
       updated_at           = now()
 WHERE alias_id = 'hive-tts';

UPDATE public.model_aliases
   SET price_unit           = 'seconds',
       input_price_credits  = 0,
       output_price_credits = 4316667,
       updated_at           = now()
 WHERE alias_id = 'hive-stt';

-- 3. A single-quantity modality has exactly one price. Enforced in the schema
--    so a future admin edit cannot recreate the "which column applies?"
--    ambiguity that a per-unit price with two columns invites.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'model_aliases_single_unit_price'
    ) THEN
        ALTER TABLE public.model_aliases
            ADD CONSTRAINT model_aliases_single_unit_price
            CHECK (price_unit = 'tokens' OR input_price_credits = 0);
    END IF;
END $$;

COMMENT ON COLUMN public.model_aliases.price_unit IS
    'Unit the price columns are quoted in, per million: tokens (text), characters (text-to-speech), seconds (transcription). edge-api refuses a request whose alias unit does not match what the endpoint meters (issue #627). For any non-token unit the price is output_price_credits and input_price_credits must be 0.';
