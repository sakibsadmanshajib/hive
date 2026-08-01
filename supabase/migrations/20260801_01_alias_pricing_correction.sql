-- =============================================================================
-- Issue #617: correct the catalog credit prices, and reduce every chat alias
-- to exactly one enabled route.
--
-- WHY
--   The prices seeded by 20260331_01_model_catalog.sql were arbitrary
--   placeholders (8/24, 12/36, 10/30 credits per million). Measured against
--   real provider rates they sat between roughly 1300x and 7500x too low,
--   and the shortfall varied per row, so no single global multiplier could
--   repair them. Every figure below is re-derived from the provider's own
--   published rate instead.
--
--   hive-fast additionally carried TWO enabled routes, an OpenRouter route
--   and a Groq route whose real costs differ by roughly ten times. One
--   alias must map to exactly one route: an alias whose cost depends on
--   which route happened to win is not priceable at the alias level. The
--   ambiguity is removed here rather than modelled.
--
-- FORMULA (mechanical, so regeneration needs no judgment)
--   credits_per_million = ceil(provider_list_usd_per_million * MARGIN * CREDITS_PER_USD)
--     MARGIN         = 1.4
--     CREDITS_PER_USD = 100000   (apps/control-plane/internal/payments/types.go)
--   Input and output are derived separately. The credit unit is per MILLION
--   tokens, per decision D-031; the charge formula lives in
--   apps/edge-api/internal/metering/precedence.go and is not touched here.
--
-- PROVIDER RATES, fetched 2026-08-01
--   Groq openai/gpt-oss-20b          0.075 in / 0.30 out USD per million
--     source: https://groq.com/pricing and https://console.groq.com/docs/models
--     (Groq's models API does not expose pricing, so the published pricing
--     page and the model docs table are the sources; both agree.)
--   OpenRouter openai/gpt-4o-mini    0.15  in / 0.60 out USD per million
--   OpenRouter openai/gpt-4.1-mini   0.40  in / 1.60 out USD per million
--     source: https://openrouter.ai/api/v1/models, which quotes USD per
--     token; the figures above are those values multiplied by 1e6.
--
-- WORKED DERIVATION
--   hive-fast     in  0.075 * 1.4 * 100000 =  10500
--   hive-fast     out 0.300 * 1.4 * 100000 =  42000
--   hive-default  in  0.150 * 1.4 * 100000 =  21000
--   hive-default  out 0.600 * 1.4 * 100000 =  84000
--   hive-auto     in  0.400 * 1.4 * 100000 =  56000
--   hive-auto     out 1.600 * 1.4 * 100000 = 224000
--   Every product is a whole number, so the ceiling is a no-op here. It
--   still governs any future rate that is not.
--
-- OUT OF SCOPE (deliberately unchanged)
--   * hive-stt, hive-tts, hive-embedding-default prices. apps/edge-api's
--     internal/audio never reads this catalog and charges flat literals
--     (issue #627), so repricing those rows would move a console display
--     without moving a charge. No per-token price is invented for them.
--   * cache_read_price_credits / cache_write_price_credits. These remain at
--     their placeholder values. precedence.go never reads them, so they are
--     display-only today; correcting them needs its own ruling on whether
--     cached input is billed at all.
--   * hive-embedding-default's three enabled routes. Reported for a
--     decision rather than changed unilaterally.
--
-- RE-RUNNABILITY
--   This migration contains ZERO INSERT statements, so there is no row on
--   which an ON CONFLICT clause could be needed or omitted. Every statement
--   is an UPDATE keyed on a primary key, assigning constants, which makes
--   re-application a no-op. Checked per statement, not per file.
-- =============================================================================

-- 1. Correct the chat alias prices.
UPDATE public.model_aliases
   SET input_price_credits  = 10500,
       output_price_credits = 42000,
       updated_at           = now()
 WHERE alias_id = 'hive-fast';

UPDATE public.model_aliases
   SET input_price_credits  = 21000,
       output_price_credits = 84000,
       updated_at           = now()
 WHERE alias_id = 'hive-default';

UPDATE public.model_aliases
   SET input_price_credits  = 56000,
       output_price_credits = 224000,
       updated_at           = now()
 WHERE alias_id = 'hive-auto';

-- 2. Point hive-fast's single route at the model its price is derived from.
--    The owner's instruction was the Groq gpt-oss family; the fast tier takes
--    the smaller and faster of the two Groq serves (20b rather than 120b).
--    litellm_model_name stays the route id: that column is the LiteLLM alias
--    name, not the upstream model. provider_model is the upstream identifier.
UPDATE public.provider_routes
   SET provider_model = 'groq/openai/gpt-oss-20b'
 WHERE route_id = 'route-groq-fast';

-- 3. Retire hive-fast's second route so the alias has exactly one enabled
--    route. Disabled rather than deleted: SelectRoute filters health_state
--    'disabled', litellmconfig's sync excludes it from the gateway config,
--    and keeping the row preserves the history and stays reversible.
UPDATE public.provider_routes
   SET health_state = 'disabled'
 WHERE route_id = 'route-openrouter-fast-fallback';

-- 4. Drop the retired route from hive-fast's fallback order so the policy
--    row cannot name a route that is no longer selectable.
UPDATE public.alias_route_policies
   SET fallback_order = '["route-groq-fast"]'::jsonb
 WHERE alias_id = 'hive-fast';
