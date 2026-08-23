-- =============================================================================
-- hive-small, hive-medium and hive-fast: move the remaining Groq TEXT routes to
-- the OpenRouter free model. PRICES DELIBERATELY UNCHANGED (owner directive,
-- 2026-08-23, second half of the same instruction as
-- 20260823_20_free_route_aliases_half_price.sql).
--
-- WHAT CHANGES
--   * hive-small   REPOINTED from Groq openai/gpt-oss-20b  to OpenRouter
--                  dots-studio/dots-3-note-preview:free.
--   * hive-medium  REPOINTED from Groq openai/gpt-oss-120b to the same model.
--   * hive-fast    REPOINTED from Groq (GROQ_FAST_MODEL, openai/gpt-oss-20b) to
--                  the same model. Deprecated alias, kept invocable for
--                  back-compat, moved so no Groq text route is left behind.
--
-- WHY: to stop the Groq free-tier allowance being consumed. The Groq API key
-- stays configured and stays in use, for audio only (see below).
--
-- PRICES DO NOT MOVE, AND THAT IS NOT AN OVERSIGHT
--   This migration writes to NO price column at all. Not to
--   input_price_credits, not to output_price_credits, not to either cache
--   column, on any alias. The owner's 50 percent instruction applies to
--   hive-default and hive-auto only, and 20260823_20 is where it is carried
--   out. Serving a same-priced alias from a free upstream simply widens margin,
--   and that is the intended outcome here.
--
--   Stated this plainly because a reader who arrives from 20260823_20 will
--   reasonably expect a price change and its absence needs to read as
--   deliberate. It is also machine-checked:
--   TestGroqFreeRepointTouchesNoPrice in
--   apps/control-plane/internal/routing/free_alias_pricing_test.go fails if this
--   file assigns any price column.
--
--   Consequence worth naming: after both migrations, five aliases resolve to one
--   upstream model at three different price points (hive-small and hive-fast at
--   10500/42000, hive-medium at 21000/84000, hive-default at 5250/21000 and
--   hive-auto at 10500/42000). One model, five prices. That is the honest
--   description of what the two directives together produce, it is flagged to
--   the owner in the pull request rather than smoothed over here, and it is not
--   this file's decision to make.
--
-- AUDIO IS EXPLICITLY OUT OF SCOPE
--   route-groq-stt (groq/whisper-large-v3) and route-groq-tts
--   (groq/canopylabs/orpheus-v1-english) are NOT touched and must not be. Groq
--   STT/TTS is what serves Bengali voice dictation, wired to the gateway in
--   PR #1079, so moving it would remove voice from the product rather than
--   migrate it.
--
--   The survey behind that, done properly rather than asserted, because "no
--   audio on OpenRouter" would have been an overstatement. Across all 422
--   models on /api/v1/models (2026-08-23):
--     * TEXT TO SPEECH: nothing. Not one model, free or paid, advertises
--       `supported_voices`, and the only entries with audio in their output
--       modality are google/lyria-3-pro-preview and google/lyria-3-clip-preview
--       (free, MUSIC generation, no voice selection) and openai/gpt-audio and
--       openai/gpt-audio-mini (PAID, speech-to-speech chat). None of them is an
--       OpenAI-compatible /v1/audio/speech endpoint, which is what
--       internal/audio speaks. There is no replacement for Orpheus here at any
--       price.
--     * SPEECH TO TEXT: three FREE models accept audio as chat INPUT
--       (thinkingmachines/inkling:free, thinkingmachines/inkling-small:free,
--       nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free). They are NOT a
--       transcription endpoint: they are chat-completions models, while
--       route-groq-stt is a LiteLLM `mode: audio_transcription` route that
--       edge-api's audio handler forwards multipart audio to. Using one would
--       be a new integration, not a repoint. All three are also served by
--       providers whose published policy is training on prompts, which is a
--       poor destination for dictated speech in particular.
--   Reported rather than acted on, exactly as the directive asked.
--
--   Therefore GROQ_API_KEY remains required and the 'groq' provider row stays
--   enabled. What changes is that Groq no longer serves any CHAT traffic.
--
-- THE FREE TARGET IS THE SAME ONE, FOR THE SAME REASONS
--   Full derivation, the live capability probes and the provider data-policy
--   evidence are in 20260823_20's header and are not duplicated here. The short
--   version: of the 22 zero-priced models on
--   https://openrouter.ai/api/v1/models (fetched 2026-08-23), only five support
--   all of tools, tool_choice, response_format and structured_outputs; of those
--   five, three are served by providers that train on prompts, and the one
--   remaining zero-retention candidate (z-ai/glm-5.2:free via Decart) returned
--   429 on four of four live attempts from an upstream shared pool. That leaves
--   dots-studio/dots-3-note-preview:free as the only free model that is
--   simultaneously capability-complete, live-verified working, and served by a
--   provider that does not train on prompts.
--
--   CONCENTRATION RISK, accepted by the owner and recorded rather than buried.
--   After this file, every customer-reachable chat alias in the catalog resolves
--   to ONE model at ONE provider on ONE endpoint, capped by OpenRouter at 20
--   requests per minute for free variants. There are no gateway fallbacks, by
--   deliberate design (20260822_02). The failure mode therefore MOVES rather
--   than disappearing: a Groq daily allowance becomes a free per-minute
--   ceiling, which is tighter. It also removes the mitigation 20260823_20 could
--   still point at, namely "a rate-limited customer can select hive-small,
--   which is on a different provider". After this migration there is no such
--   alias. The only non-free chat routes left are the two paid DeepSeek ones,
--   and they are separate aliases a customer must choose deliberately.
--
--   What a customer sees at the cap is unchanged and is not fixed here: a
--   roughly 60 second wait and then a 502 reading "context canceled", never the
--   provider's own 429 with its retry hint. That is issue #1089. The candidate
--   fix lives in the litellm_settings block that the config sync preserves
--   verbatim on a live volume, so a file edit is inert on the box and
--   unverifiable from a developer machine; #1089 says the same about itself.
--   This migration raises that issue's priority from latent to likely.
--
--   This does NOT close issue #1088. That is CI consuming the live demo's
--   provider allowance, a different cause with a different owner.
--
-- CAPABILITY PARITY, PROBED LIVE RATHER THAN READ OFF A TABLE
--   dots-3-note-preview lists tools, tool_choice, response_format,
--   structured_outputs, reasoning, include_reasoning, max_tokens, temperature
--   and top_p, and NOT reasoning_effort, stop, frequency_penalty,
--   presence_penalty, seed, top_k or logprobs. The Groq gpt-oss models it
--   replaces do accept several of those, so the question is whether an
--   unlisted parameter FAILS or is ignored. Twelve request shapes were sent
--   live on 2026-08-23:
--
--     baseline, reasoning_effort=low, reasoning_effort=high, stop,
--     frequency_penalty, presence_penalty, seed, top_k, logprobs +
--     top_logprobs, n=2, response_format json_schema with strict: true, and
--     reasoning_effort together with provider.require_parameters
--
--   ALL TWELVE returned 200 with a correct answer. Nothing 400s. So there is no
--   request shape that works today and fails after this change, which is the
--   regression this section exists to rule out.
--
--   Two behavioural differences that are NOT failures and are recorded so
--   nobody reports them as new bugs:
--     * reasoning_effort is accepted and never rejected, but does not reliably
--       modulate effort on this model: 'low' produced more reasoning tokens
--       than 'high' in the same probe run. Requests keep working, the knob
--       stops being meaningful.
--     * n=2 returns one choice. That is OpenRouter's existing behaviour for a
--       provider that does not implement n, not something this change
--       introduces, and it is the same on the routes being replaced.
--
--   Capability FLAGS are therefore carried across unchanged, per route:
--     route-free-small, route-free-medium  identical to their Groq originals,
--                                          including supports_reasoning = true.
--     route-free-fast                      supports_reasoning stays FALSE.
--
--   That last one is deliberate status-quo preservation, not an oversight.
--   route-groq-fast has carried supports_reasoning = false since its original
--   20260331_02 seed; 20260822_02 examined it, called it a pre-existing
--   under-claim on a deprecated alias, and left it alone rather than widen it
--   in an unrelated migration. The same reasoning applies here. Widening it
--   would be safe (on a pinned single-route alias an under-claim is a 422 and a
--   widening cannot break a request), but a routing migration is not where a
--   deprecated alias should quietly gain a feature.
--
-- NO ROUTE HERE CARRIES A SOLE-CARRIER FLAG
--   Unlike route-groq-auto, none of route-groq-small, route-groq-medium or
--   route-groq-fast carries supports_batch, supports_image_generation,
--   supports_image_edit, supports_tts or supports_stt. All three are
--   false-across-the-media-columns rows, so disabling them removes no endpoint
--   from the catalog. The batch and image flags live on route-free-auto, handed
--   there by 20260823_20; supports_stt and supports_tts live on route-groq-stt
--   and route-groq-tts, which this file does not touch.
--
-- WHY NEW ROUTE IDS, AGAIN
--   Identical reasoning to 20260823_20 and 20260822_02: the LiteLLM config sync
--   merges field by field and the database owns only model, api_base and
--   api_key, so anything else already on a config entry survives forever.
--   Retiring the route id makes the merge drop the whole stale entry.
--
--   It also removes an env-var indirection. route-groq-fast's config entry read
--   `os.environ/GROQ_FAST_MODEL`; route-free-fast writes the slug literally, the
--   same shape 20260822_02 chose for exactly the drift issues #689 and #965
--   were. GROQ_FAST_MODEL becomes unused as a result, and is left in
--   .env.example rather than deleted because CI workflows still pass it
--   through.
--
-- price_class STAYS 'standard' on all three, matching the rows they replace.
-- It feeds alias_route_policies.allow_price_class_widening, so holding it
-- constant keeps this repoint from changing selection behaviour by a second
-- mechanism.
--
-- policy_mode IS NOT TOUCHED. hive-small and hive-medium are 'pinned';
-- hive-fast is 'latency' from its 20260331_02 seed and stays 'latency' with a
-- single-entry fallback_order, exactly as 20260801_01 left it. Only the route
-- named inside fallback_order moves.
--
-- RE-RUNNABILITY
--   Every INSERT carries ON CONFLICT DO NOTHING and every UPDATE carries a WHERE
--   guard excluding rows already at the target value, so a second run affects
--   zero rows and errors on nothing.
-- =============================================================================

BEGIN;

SET LOCAL lock_timeout = '5s';

-- 1. One new route per alias.
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
        'route-free-small',
        'hive-small',
        'openrouter',
        'openrouter/dots-studio/dots-3-note-preview:free',
        'route-free-small',
        'standard',
        'healthy',
        10
    ),
    (
        'route-free-medium',
        'hive-medium',
        'openrouter',
        'openrouter/dots-studio/dots-3-note-preview:free',
        'route-free-medium',
        'standard',
        'healthy',
        10
    ),
    (
        'route-free-fast',
        'hive-fast',
        'openrouter',
        'openrouter/dots-studio/dots-3-note-preview:free',
        'route-free-fast',
        'standard',
        'healthy',
        10
    )
on conflict (route_id) do nothing;

-- The doubled `openrouter/` prefix and the trailing `:free` are both
-- load-bearing; see 20260823_20's note. Dropping the prefix makes LiteLLM read
-- `dots-studio` as a provider it does not have, and dropping `:free` selects a
-- PAID endpoint of the same model, which would reintroduce out-of-pocket spend
-- on exactly the routes this migration exists to take off a metered allowance.

-- 2. Capabilities, carried across per route. supports_reasoning is the only
--    column that differs between them, and it differs the same way it does
--    today: false on the deprecated fast route, true on the other two.
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
    ('route-free-small',  true, true, true, false, true, true,  false, false, true, false, false, false),
    ('route-free-medium', true, true, true, false, true, true,  false, false, true, false, false, false),
    ('route-free-fast',   true, true, true, false, true, false, false, false, true, false, false, false)
on conflict (route_id) do nothing;

-- 3. Retire the three Groq text routes. Disabled, not deleted, so the change is
--    reversible and SelectRoute filters them out. route-groq-stt and
--    route-groq-tts are deliberately absent from this list: audio stays on Groq.
UPDATE public.provider_routes
   SET health_state = 'disabled'
 WHERE route_id IN ('route-groq-small', 'route-groq-medium', 'route-groq-fast')
   AND health_state <> 'disabled';

-- 4. Point each alias's policy at its new route, so no policy row names a route
--    this migration disabled.
UPDATE public.alias_route_policies
   SET fallback_order = '["route-free-small"]'::jsonb
 WHERE alias_id = 'hive-small'
   AND fallback_order <> '["route-free-small"]'::jsonb;

UPDATE public.alias_route_policies
   SET fallback_order = '["route-free-medium"]'::jsonb
 WHERE alias_id = 'hive-medium'
   AND fallback_order <> '["route-free-medium"]'::jsonb;

UPDATE public.alias_route_policies
   SET fallback_order = '["route-free-fast"]'::jsonb
 WHERE alias_id = 'hive-fast'
   AND fallback_order <> '["route-free-fast"]'::jsonb;

-- 5. There is deliberately no step 5. No summary text is rewritten and no price
--    column is written, on any alias. hive-small's and hive-medium's summaries
--    describe capability tiers rather than an upstream vendor, and the aliases
--    are provider-blind by design, so nothing customer-visible becomes untrue by
--    the repoint alone. Rewriting them would be the only way this file could
--    touch model_aliases, and TestGroqFreeRepointTouchesNoPrice is stricter and
--    simpler with that table left alone entirely.

COMMIT;
