-- =============================================================================
-- Issue #965 (owner-ruled fix, 2026-08-18): hive-fast currently routes to
-- Groq's llama-3.1-8b-instant, which Groq has removed from its live catalog
-- since the 20260801_14 migration pinned it. Every hive-fast request 404s
-- with model_not_found. Confirmed live 2026-08-18 against
-- https://api.groq.com/openai/v1/models (Authorization: Bearer $GROQ_API_KEY):
-- llama-3.1-8b-instant is absent from the response entirely. Groq's current
-- catalog carries no small/fast Llama chat model at all; the closest fast,
-- cheap, currently-live alternative is openai/gpt-oss-20b, which is also
-- exactly what route-groq-fast pointed at before 20260801_14 repointed it
-- for a (now moot) cost saving, so this is a revert to a config already
-- proven live in production, not a fresh guess.
--
-- PROVIDER RATE, re-fetched 2026-08-18
--   Groq openai/gpt-oss-20b   $0.075 in / $0.30 out USD per million tokens
--     source: https://groq.com/pricing ("GPT OSS 20B" row) and
--     https://console.groq.com/docs/models, both agree, unchanged since the
--     20260801_01 migration derived hive-fast's price from the same figures.
--
-- FORMULA (same mechanical shape as 20260801_01 and 20260801_14)
--   credits_per_million = ceil(provider_list_usd_per_million * MARGIN * CREDITS_PER_USD)
--     MARGIN          = 1.4
--     CREDITS_PER_USD = 100000   (apps/control-plane/internal/payments/types.go)
--
-- WORKED DERIVATION
--   hive-fast  in  0.075 * 1.4 * 100000 = 10500
--   hive-fast  out 0.300 * 1.4 * 100000 = 42000
--   Both products are whole numbers, so the ceiling is a no-op here. These
--   are the exact figures 20260801_01_alias_pricing_correction.sql already
--   derived for this same model; 20260801_14 is the only migration that
--   ever moved hive-fast off them.
--
-- KNOWN TRADE-OFF, DELIBERATELY ACCEPTED
--   Per https://console.groq.com/docs/tool-use (checked 2026-08-18),
--   openai/gpt-oss-20b supports tool use but not PARALLEL tool calls;
--   llama-3.1-8b-instant did support parallel tool calls. This is a real
--   capability step-down from what 20260801_14 intended, but
--   llama-3.1-8b-instant no longer exists on Groq at any price, so there is
--   no currently-live Groq model in hive-fast's fast/cheap tier that
--   preserves parallel tool calls. provider_capabilities has no
--   parallel-tool-call column (checked: supports_responses,
--   supports_chat_completions, supports_completions, supports_embeddings,
--   supports_streaming, supports_reasoning, supports_cache_read,
--   supports_cache_write, plus the media flags added by
--   20260414_01_provider_capabilities_media_columns.sql), so no catalog
--   claim becomes untrue by this change, matching 20260801_14's own note
--   about supports_reasoning.
--
-- OUT OF SCOPE (deliberately unchanged)
--   * litellm_model_name: unchanged, same reasoning as 20260801_14 (it is
--     the LiteLLM gateway alias name, not the upstream model).
--   * alias_route_policies, provider_capabilities: untouched. hive-fast
--     keeps exactly one enabled route (route-groq-fast).
--   * The upstream model the running gateway actually calls is read from the
--     GROQ_FAST_MODEL environment variable, not this catalog (issue #684).
--     That variable is reverted separately in .env.example and the CI
--     workflows that reference it; this migration only corrects the credit
--     catalog. The demo box's own .env additionally needs
--     GROQ_FAST_MODEL=groq/openai/gpt-oss-20b applied and the litellm
--     container RECREATED (not restarted) for the live gateway to pick this
--     up, per the same operational step 20260801_14 already required and
--     per project_repo_is_not_the_running_system: this migration alone does
--     not make the box serve the new model.
--
-- RE-RUNNABILITY
--   Both statements are UPDATEs keyed on a primary key, each carrying its
--   own WHERE guard that excludes rows already at the target value, so a
--   second run of this file affects zero rows and errors on nothing.
-- =============================================================================

-- Wrapped in a transaction (database review finding on this PR): these two
-- UPDATEs enforce the "one route, one price" invariant together. Without
-- BEGIN/COMMIT, apply-migrations.sh commits each statement independently, and
-- a request landing on ListRouteCandidates/LoadAliasPricing between them
-- could read the new route with the old price, or vice versa. Same gap
-- pre-existed in 20260801_01/20260801_14; closed here since this migration
-- is specifically the one guarding that invariant.
BEGIN;

-- 1. Point route-groq-fast back at the model Groq currently serves.
UPDATE public.provider_routes
   SET provider_model = 'groq/openai/gpt-oss-20b'
 WHERE route_id = 'route-groq-fast'
   AND provider_model <> 'groq/openai/gpt-oss-20b';

-- 2. Re-derive hive-fast's price from that model's published rate.
UPDATE public.model_aliases
   SET input_price_credits  = 10500,
       output_price_credits = 42000,
       updated_at           = now()
 WHERE alias_id = 'hive-fast'
   AND (input_price_credits <> 10500 OR output_price_credits <> 42000);

COMMIT;
