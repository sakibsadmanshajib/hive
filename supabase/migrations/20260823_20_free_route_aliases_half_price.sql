-- =============================================================================
-- hive-default and hive-auto: route to an OpenRouter free model, halve the
-- price (owner directive, 2026-08-23).
--
-- WHAT CHANGES
--   * hive-default  REPOINTED from Groq openai/gpt-oss-20b to OpenRouter
--                   dots-studio/dots-3-note-preview:free, and repriced to
--                   EXACTLY HALF its current rate.
--   * hive-auto     REPOINTED from Groq openai/gpt-oss-120b to the same free
--                   model, and repriced to EXACTLY HALF its current rate.
--
-- Nothing else in the catalog moves. hive-small, hive-medium, hive-fast,
-- deepseek-v4-flash, deepseek-v4-pro, openrouter-auto, hive-embedding-default,
-- hive-stt and hive-tts are untouched, so Groq remains reachable through
-- hive-small and hive-medium for a customer who needs a different upstream.
--
-- THE PRICES, AND WHERE THE "CURRENT" FIGURES COME FROM
--   Read out of 20260822_02_catalog_alias_restructure.sql step 7, which is the
--   statement that set them, not from an estimate and not from memory.
--
--     alias         unit                          old      new
--     hive-default  credits per million prompt      10500     5250
--     hive-default  credits per million completion  42000    21000
--     hive-default  cache read (display only)           0        0
--     hive-default  cache write (display only)          0        0
--     hive-auto     credits per million prompt      21000    10500
--     hive-auto     credits per million completion  84000    42000
--     hive-auto     cache read (display only)           0        0
--     hive-auto     cache write (display only)          0        0
--
--   Every figure halves with no remainder, so there is no rounding decision
--   here and no place for a float to enter. The cache columns are already 0 on
--   both rows (20260822_02 zeroed them when both aliases moved to Groq routes
--   that declare no cache support), and half of zero is zero, so they are
--   asserted rather than changed.
--
--   Both aliases stay pricing_mode 'fixed' and price_unit 'tokens'. Stated
--   because a plausible-sounding claim to the contrary was in circulation:
--   PR #1012 did NOT put hive-auto on upstream-actual pricing. It created a
--   SEPARATE alias, `openrouter-auto`, on route-openrouter-auto-beta, and its
--   own migration says so ("litellm_model_name is deliberately NOT
--   'route-openrouter-auto': that name is the retired route id of the
--   pre-existing hive-auto alias, which resolves to a completely different
--   model"). hive-auto has been a plain fixed-price row throughout.
--
-- WHY THE MARGIN FORMULA IS NOT USED HERE
--   Every previous pricing migration in this tree derives credits as
--   provider_list_usd_per_million * 1.4 * 100000 (D-032). A free upstream
--   costs zero, so that formula yields zero, and zero is not a price: routing
--   refuses an alias whose input and output prices are both zero
--   (routing.Service, RouteInfo.HasCostBasis) and it would be dead rather than
--   free. The existing guard test agrees, by construction: parseRate in
--   apps/control-plane/internal/routing/catalog_alias_pricing_test.go rejects a
--   zero rate as "a mispricing, not a rate".
--
--   So these two figures are owner-set, not cost-derived, and the invariant
--   that replaces the formula is the HALVING RELATION against the pinned old
--   values above. That relation is machine-checked by
--   apps/control-plane/internal/routing/free_alias_pricing_test.go, which reads
--   the four money columns positionally out of this file and fails if any one
--   of them is not exactly half of the figure recorded beside it.
--
-- WHY THIS MODEL, VERIFIED LIVE ON 2026-08-23 RATHER THAN ASSUMED
--   Requirement, from what the two aliases serve today: both current routes
--   declare tools_supported = true, which is the column PR #206 routes `tools`,
--   `tool_choice` and `response_format` on, plus supports_streaming and
--   supports_reasoning. A free target that cannot do those breaks requests
--   that work today.
--
--   Of the 22 zero-priced models on https://openrouter.ai/api/v1/models
--   (422 models total, fetched 2026-08-23), only five support all four of
--   tools, tool_choice, response_format and structured_outputs. Joining them to
--   the provider data policies at
--   https://openrouter.ai/api/frontend/v1/all-providers:
--
--     dots-studio/dots-3-note-preview:free   AtlasCloud  no training, retains
--     z-ai/glm-5.2:free                      Decart      no training, zero retention
--     nvidia/nemotron-3-super-120b-a12b:free NVIDIA      TRAINS ON PROMPTS
--     nvidia/nemotron-nano-9b-v2:free        NVIDIA      TRAINS ON PROMPTS
--     liquid/lfm-2.5-2.6b:free               Liquid      TRAINS ON PROMPTS
--
--   A trap for whoever re-derives this: the endpoints API reports NVIDIA's
--   provider name as `Nvidia` while the provider directory keys it under
--   displayName `NVIDIA`. A join on displayName misses it and reports every
--   NVIDIA free endpoint as no-training and zero-retention, the exact opposite
--   of the truth. Join on both fields.
--
--   Live probes with the project's real key, not documentation reading:
--     * z-ai/glm-5.2:free, the only zero-retention candidate, returned 429 on
--       4 of 4 attempts, provider_error_code upstream_429, limit_source
--       upstream_provider_shared_pool. That is Decart's shared pool, not our
--       account's limit, so buying credits cannot raise it.
--     * openrouter/free (the router id does exist) succeeded 5 of 5, but 4 of
--       those landed on NVIDIA endpoints and 2 of 5 landed on
--       nvidia/nemotron-3.5-content-safety:free, a moderation classifier which
--       answered a plain chat prompt with "User Safety: safe" and then with an
--       empty string. Unfit for a chat alias on output quality alone.
--     * provider {zdr: true} returned 404 "No endpoints found matching your
--       data policy (Zero data retention)". There is no zero-data-retention
--       free endpoint reachable at all today, so a free upstream cannot be
--       squared with a strict zero-retention posture. That contradiction is
--       recorded here on purpose, because this product sells data sovereignty.
--     * dots-studio/dots-3-note-preview:free returned 200 on a sync
--       completion, on a tools request (a real tool_call with correct
--       arguments, finish_reason tool_calls), on response_format
--       {"type":"json_object"} (valid JSON back) and on a streamed request.
--
--   Hence: the only free model that is simultaneously full-parity,
--   live-verified working, and served by a provider that does not train on
--   prompts. AtlasCloud publishes no retention period, which is the residual
--   and is not hidden.
--
-- BOTH ALIASES GET THE SAME UPSTREAM MODEL, AND THAT IS A KNOWN WART
--   After this migration hive-auto costs exactly twice hive-default for the
--   identical model, so the surviving 2x gap buys a customer nothing. The
--   directive is 50 percent of each alias's own current price, which is what
--   this file implements; equalising them is a separate owner decision and
--   would be a further reduction, so it cannot overcharge anyone. The
--   alternative, giving hive-auto a distinct larger free model, is blocked:
--   no second free model has full capability parity without prompt training.
--   Two aliases sharing one upstream model through their own route rows is a
--   shape already established three times over by 20260822_02.
--
-- WHY NEW ROUTE IDS RATHER THAN REPOINTING THE EXISTING ROWS
--   Same reason 20260822_02 gave, and it applies in this direction too. The
--   LiteLLM config sync merges FIELD BY FIELD: the database owns only model,
--   api_base and api_key, and every other key already on the entry survives so
--   that hand-tuning sticks (mergeParams in
--   apps/control-plane/internal/litellmconfig/generator.go, issue #707).
--   Retiring the route id makes the merge drop the whole stale entry, because a
--   known route_id that is no longer active is deleted from the config rather
--   than updated. Disabling rather than deleting the row keeps it reversible,
--   and SelectRoute filters 'disabled'.
--
--   The api_key follows automatically: it comes from providers.api_key_env for
--   the row's provider slug, so provider 'openrouter' resolves to
--   OPENROUTER_API_KEY without this migration naming a secret.
--
-- price_class STAYS 'standard'
--   'budget' would arguably describe a free upstream better, but price_class
--   feeds alias_route_policies.allow_price_class_widening, and these two
--   aliases are 'pinned' with a one-entry fallback_order. Keeping the same
--   value as the routes being replaced means the repoint cannot change
--   selection behaviour through a second mechanism. One variable, not two.
--
-- THE CAPABILITY LANDMINE, HANDLED
--   route-groq-auto is the SOLE carrier of supports_batch,
--   supports_image_generation and supports_image_edit in the whole catalog
--   (20260414_01 granted the media flags to route-openrouter-auto and to no
--   other row; 20260822_02 carried three of them onto route-groq-auto).
--   SelectRoute skips disabled candidates and then hard-filters on each flag,
--   and batchstore sends NeedBatch = true for EVERY batch, so disabling
--   route-groq-auto without carrying those three forward would make
--   /v1/batches, /v1/images/generations and /v1/images/edits find zero
--   eligible routes for EVERY alias in the system, not just for hive-auto.
--   route-free-auto therefore carries all three.
--
--   Restating what 20260822_02 already said about the two image flags, so this
--   file cannot be read as a fresh claim: they are STATUS QUO PRESERVATION.
--   Neither gpt-4.1-mini, nor gpt-oss-120b, nor this free model is an
--   image-generation model. Carrying them keeps a routing change from silently
--   deleting two endpoints as a side effect. Correcting them properly needs a
--   real image route and is its own change. Do not read them as a claim that
--   this model generates images. supports_batch is a true claim: Phase 15's
--   control-plane local batch executor does not depend on the provider having a
--   native batch API.
--
--   supports_reasoning is true on both rows and is verified rather than
--   inherited: the model's supported_parameters include `reasoning`,
--   `include_reasoning` and (on the router-visible entry) reasoning controls,
--   and edge-api sets NeedReasoning only when a request carries
--   reasoning_effort. An under-claim on a PINNED alias is not a withheld
--   feature, it is a 422, because with a single candidate
--   matchesRequestedCapabilities drops it and SelectRoute returns
--   ErrRouteNotEligible.
--
--   supports_cache_read and supports_cache_write stay false. This model
--   publishes no cache-read rate, and no edge-api request path ever sets
--   NeedCacheRead or NeedCacheWrite, so a false flag here cannot become a 422.
--
--   supports_embeddings stays false, matching both retired rows.
--
-- WHAT THIS MIGRATION CANNOT DO
--   It cannot make the running gateway serve the new route. The LiteLLM config
--   is seeded into a named volume only when absent, and the sync preserves
--   litellm_settings verbatim, so the companion edit to
--   deploy/litellm/config.yaml is a first-boot seed and the live change arrives
--   through POST /internal/litellm/sync reading provider_routes. Verify against
--   the running gateway, not the file.
--
--   It also does not fix issue #1089. A free endpoint is documented at 20
--   requests per minute, far tighter than Groq, and today a rate limit reaches
--   the customer as a roughly 60 second wait and then a 502 whose message is
--   "context canceled", never as the 429 with its retry hint. The candidate fix
--   lives in the litellm_settings block that the sync preserves verbatim, so a
--   file edit is inert on a live box and unverifiable from a dev machine. It
--   stays with #1089.
--
-- RE-RUNNABILITY
--   Every INSERT carries ON CONFLICT DO NOTHING and every UPDATE carries a
--   WHERE guard excluding rows already at the target value, so a second run
--   affects zero rows and errors on nothing. As with 20260822_02, replaying
--   this file after someone re-tunes hive-default's or hive-auto's price puts
--   those rows back to the 2026-08-23 values, which is what a repricing
--   migration is for but means it is NOT safe to replay over hand-tuned state.
-- =============================================================================

-- One transaction, for the reason 20260818_01 introduced it: a request landing
-- on ListRouteCandidates or LoadAliasPricing partway through must not see an
-- alias whose only enabled route has been disabled, or a route with no
-- capability row.
BEGIN;

SET LOCAL lock_timeout = '5s';

-- 1. One new route per alias. Two routes naming one upstream model is the same
--    shape 20260822_02 established for route-groq-small / route-groq-default
--    and route-groq-medium / route-groq-auto: provider_routes is keyed one
--    route to one alias, so each alias needs its own row.
insert into public.provider_routes (
    route_id,
    alias_id,
    provider,
    provider_model,
    litellm_model_name,
    price_class,
    health_state,
    priority
) values
    (
        'route-free-default',
        'hive-default',
        'openrouter',
        'openrouter/dots-studio/dots-3-note-preview:free',
        'route-free-default',
        'standard',
        'healthy',
        10
    ),
    (
        'route-free-auto',
        'hive-auto',
        'openrouter',
        'openrouter/dots-studio/dots-3-note-preview:free',
        'route-free-auto',
        'standard',
        'healthy',
        10
    )
on conflict (route_id) do nothing;

-- The doubled `openrouter/` prefix above is correct and deliberate. LiteLLM
-- strips the leading `openrouter/` as its provider selector and forwards the
-- rest, so this reaches OpenRouter as the slug
-- `dots-studio/dots-3-note-preview:free`. Every other OpenRouter row in this
-- catalog carries the same doubling (20260822_02's
-- `openrouter/~deepseek/deepseek-v4-flash-latest`, 20260822_30's
-- `openrouter/openrouter/auto-beta`). Removing it would send
-- `dots-studio/dots-3-note-preview:free` to LiteLLM as an unknown provider.
-- The trailing `:free` is part of the real OpenRouter model id and selects the
-- zero-priced variant; dropping it selects a PAID endpoint of the same model,
-- which is a silent repricing of our own cost, not a tidy-up.

-- 2. Capabilities per route. route-free-auto carries the three flags it is now
--    the sole catalog carrier of; see THE CAPABILITY LANDMINE above.
insert into public.provider_capabilities (
    route_id,
    supports_responses,
    supports_chat_completions,
    supports_completions,
    supports_embeddings,
    supports_streaming,
    supports_reasoning,
    supports_cache_read,
    supports_cache_write,
    tools_supported,
    supports_batch,
    supports_image_generation,
    supports_image_edit
) values
    ('route-free-default', true, true, true, false, true, true, false, false, true, false, false, false),
    ('route-free-auto',    true, true, true, false, true, true, false, false, true, true,  true,  true)
on conflict (route_id) do nothing;

-- 3. Retire the two Groq routes these aliases used to take. Disabled, not
--    deleted, so the change is reversible and the history survives. Groq
--    itself stays reachable: route-groq-small, route-groq-medium and
--    route-groq-fast are untouched and serve the same two upstream models.
UPDATE public.provider_routes
   SET health_state = 'disabled'
 WHERE route_id IN ('route-groq-default', 'route-groq-auto')
   AND health_state <> 'disabled';

-- 4. Point each alias's policy at its new route, so no policy row names a
--    route that is no longer selectable.
UPDATE public.alias_route_policies
   SET fallback_order = '["route-free-default"]'::jsonb
 WHERE alias_id = 'hive-default'
   AND fallback_order <> '["route-free-default"]'::jsonb;

UPDATE public.alias_route_policies
   SET fallback_order = '["route-free-auto"]'::jsonb
 WHERE alias_id = 'hive-auto'
   AND fallback_order <> '["route-free-auto"]'::jsonb;

-- 5. Halve both money columns on both aliases.
--
--    HALVE| alias | field | old | new
--    HALVE| hive-default | input_price_credits | 10500 | 5250
--    HALVE| hive-default | output_price_credits | 42000 | 21000
--    HALVE| hive-default | cache_read_price_credits | 0 | 0
--    HALVE| hive-default | cache_write_price_credits | 0 | 0
--    HALVE| hive-auto | input_price_credits | 21000 | 10500
--    HALVE| hive-auto | output_price_credits | 84000 | 42000
--    HALVE| hive-auto | cache_read_price_credits | 0 | 0
--    HALVE| hive-auto | cache_write_price_credits | 0 | 0
--
--    Those HALVE rows are parsed by
--    apps/control-plane/internal/routing/free_alias_pricing_test.go, which
--    recomputes new = old / 2 in exact integer arithmetic and cross-checks each
--    figure against the column it actually lands in below. Editing a price
--    without editing the row beside it turns that test red, which is the whole
--    point: the margin formula that guards every other pricing migration
--    cannot apply to a zero-cost upstream.
UPDATE public.model_aliases
   SET input_price_credits       = 5250,
       output_price_credits      = 21000,
       cache_read_price_credits  = 0,
       cache_write_price_credits = 0,
       summary                   = 'Default alias for requests that name no model. Now resolves to a free upstream model, at half the previous price.',
       updated_at                = now()
 WHERE alias_id = 'hive-default'
   AND (input_price_credits <> 5250 OR output_price_credits <> 21000
        OR cache_read_price_credits <> 0 OR cache_write_price_credits <> 0);

UPDATE public.model_aliases
   SET input_price_credits       = 10500,
       output_price_credits      = 42000,
       cache_read_price_credits  = 0,
       cache_write_price_credits = 0,
       summary                   = 'Larger-capacity alias. Performs no automatic routing or model selection; it resolves to the same free upstream model as the default alias, at half the previous price.',
       updated_at                = now()
 WHERE alias_id = 'hive-auto'
   AND (input_price_credits <> 10500 OR output_price_credits <> 42000
        OR cache_read_price_credits <> 0 OR cache_write_price_credits <> 0);

COMMIT;
