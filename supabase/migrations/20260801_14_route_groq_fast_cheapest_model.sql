-- =============================================================================
-- Owner instruction (2026-08-03): for the demo, route-groq-fast should run the
-- cheapest available Groq model. Move it off groq/openai/gpt-oss-20b onto
-- groq/llama-3.1-8b-instant, which is both cheaper and, per Groq's tool-use
-- docs, strictly better at tool calling: llama-3.1-8b-instant supports
-- parallel tool use, gpt-oss-20b does not. Both report a 131k context window.
-- provider_capabilities.supports_reasoning for route-groq-fast is already
-- false, so no catalog claim becomes untrue by this change.
--
-- FORMULA (same mechanical shape as 20260801_01 and PR #651)
--   credits_per_million = ceil(provider_list_usd_per_million * MARGIN * CREDITS_PER_USD)
--     MARGIN          = 1.4
--     CREDITS_PER_USD = 100000   (apps/control-plane/internal/payments/types.go)
--
-- PROVIDER RATES, re-fetched 2026-08-03
--   Groq llama-3.1-8b-instant   $0.05 in / $0.08 out USD per million tokens
--     source: https://groq.com/pricing ("Llama 3.1 8B Instant 128k" row) and
--     https://console.groq.com/docs/models (context_window: 131072, i.e. 131k)
--   Groq openai/gpt-oss-20b (outgoing) $0.075 in / $0.30 out USD per million,
--     confirmed unchanged against the same two sources, matching the rate
--     20260801_01_alias_pricing_correction.sql already derived hive-fast from.
--
-- WORKED DERIVATION
--   hive-fast  in  0.05 * 1.4 * 100000 =  7000
--   hive-fast  out 0.08 * 1.4 * 100000 = 11200
--   Both products are whole numbers, so the ceiling is a no-op here.
--
-- OUT OF SCOPE (deliberately unchanged)
--   * litellm_model_name: that column is the LiteLLM gateway alias name, not
--     the upstream model, and route-groq-fast's litellm_model_name already
--     equals the route id. Not touched.
--   * alias_route_policies, provider_capabilities: untouched. hive-fast keeps
--     exactly one enabled route (route-groq-fast); no route or fallback added.
--   * The upstream model the running gateway actually calls is read from the
--     GROQ_FAST_MODEL environment variable, not this catalog (issue #684:
--     deploy/litellm/config.yaml is only copied into the litellm named volume
--     on first boot, so a config-file edit alone would never reach a running
--     stack). That variable is updated separately in .env.example and the
--     compose/CI wiring that reference it; this migration only corrects the
--     credit catalog (control-plane's price and routing record of what the
--     alias is billed as and pointed at), which is a distinct concern from
--     the gateway's own model-string wiring.
--
-- RE-RUNNABILITY
--   Both statements are UPDATEs keyed on a primary key, each carrying its own
--   WHERE guard that excludes rows already at the target value, so a second
--   run of this file affects zero rows and errors on nothing. Guarded
--   per statement rather than relying on a single file-level check, because a
--   file-level guard has previously let unguarded statements ride along
--   beside a guarded one.
-- =============================================================================

-- 1. Point route-groq-fast at the cheaper upstream model.
UPDATE public.provider_routes
   SET provider_model = 'groq/llama-3.1-8b-instant'
 WHERE route_id = 'route-groq-fast'
   AND provider_model <> 'groq/llama-3.1-8b-instant';

-- 2. Re-derive hive-fast's price from the new model's published rate.
UPDATE public.model_aliases
   SET input_price_credits  = 7000,
       output_price_credits = 11200,
       updated_at           = now()
 WHERE alias_id = 'hive-fast'
   AND (input_price_credits <> 7000 OR output_price_credits <> 11200);
