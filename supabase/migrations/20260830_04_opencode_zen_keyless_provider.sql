-- =============================================================================
-- OpenCode Zen: the catalog's first KEYLESS upstream (owner directive,
-- 2026-08-30).
--
-- WHAT THIS ADDS
--   * custom_providers  'opencode-zen'      https://opencode.ai/zen/v1
--   * model_aliases     'hive-free-tools'   fixed price, public
--   * provider_routes   'route-free-opencode-zen'  its OWN litellm_model_name
--   * provider_capabilities for that route, with values measured against the
--     live endpoint rather than copied from the existing free pool.
--
-- ACCEPTED RISK
--   On 2026-08-30 the owner accepted, as his own, all terms-of-service and
--   account-termination exposure arising from using this keyless upstream path.
--
-- WHY THERE IS NO API KEY, AND HOW THE SCHEMA EXPRESSES THAT
--   This endpoint takes no credential at all. Sending one is worse than
--   sending none: measured 2026-08-30, `Authorization: Bearer <anything
--   non-empty>` answers 401 "Invalid API key", while no Authorization header,
--   or an empty one, answers 200. What gates access instead is the request's
--   User-Agent: `opencode` answers 200, and a browser UA, curl's default and
--   the OpenAI client's own UA all answer 429 FreeUsageLimitError.
--
--   custom_providers.api_key_env is NOT NULL (20260611_01), so "keyless" is
--   written as the EMPTY STRING, and the config generator reads an empty
--   api_key_env as exactly that: it emits the literal `keyless` rather than
--   `os.environ/` with no variable name after it, which LiteLLM resolves to
--   nothing and which would fail the deployment at request time with an error
--   naming no route (litellmconfig.KeylessAPIKey, apps/control-plane/internal/
--   litellmconfig/generator.go). Naming some OTHER provider's variable here
--   would have been the alternative, and it would silently ship that
--   provider's real key to a third party.
--
--   The header itself is NOT in this file, because a header is not a column
--   this schema has. It lives on the deployment in deploy/litellm/config.yaml,
--   which is the seam the generator deliberately leaves open: it owns exactly
--   model, api_base and api_key and merges every other litellm_params key from
--   that file field by field (litellmconfig.mergeParams). route-openrouter-
--   auto-live's extra_body uses the same seam for the same reason. Guarded by
--   TestOpenCodeZenSeedConfigCarriesTheAccessHeaders.
--
-- WHY ITS OWN litellm_model_name, AND ITS OWN ALIAS
--   Hive filters candidate routes on provider_capabilities, but LiteLLM
--   dispatches by model_name group and load-balances across every deployment
--   sharing one. The four members of the existing free pool all carry
--   tools_supported FALSE and share one group name, which is what makes their
--   comment "it is behaviorally inert which member selection picks" true.
--   This route's tools_supported is TRUE. Adding it to that shared group would
--   let a tool-bearing request that Hive correctly routed here be dispatched
--   to a sibling that rejects tools, which is a coin-flip failure rather than
--   a clean refusal. So it gets its own group and its own alias, and this file
--   touches no existing route, capability or alias row at all. A separate
--   agent is concurrently correcting those four capability rows; collapsing
--   the two groups is a later decision, once cross-member parity is probed.
--   Guarded by TestOpenCodeZenRouteKeepsItsOwnLitellmGroup and
--   TestOpenCodeZenMigrationLeavesTheExistingPoolAlone.
--
-- CAPABILITIES ARE MEASURED, NOT INHERITED (all 2026-08-30, direct to the
-- endpoint and again through the pinned LiteLLM image v1.98.0)
--   supports_chat_completions  TRUE   POST /v1/chat/completions -> 200
--   supports_streaming         TRUE   stream:true -> chat.completion.chunk SSE
--   tools_supported            TRUE   a tools request returned finish_reason
--                                     tool_calls with a well-formed native
--                                     tool_calls array and valid JSON
--                                     arguments, and a json_schema strict
--                                     response_format returned schema-valid
--                                     JSON. This flag gates tools, tool_choice
--                                     AND response_format (PR #206), and it is
--                                     the entire reason this alias exists
--                                     rather than another pool member.
--   supports_reasoning         TRUE   reasoning_effort accepted, 200, and the
--                                     response schema carries
--                                     reasoning_content. On a pinned
--                                     single-route alias an under-claim is a
--                                     422 rather than a withheld feature.
--   supports_responses         TRUE   describes the HIVE surface: /v1/responses
--                                     is translated into a chat-completions
--                                     body before dispatch (apps/edge-api/
--                                     internal/inference/responses.go), so a
--                                     chat-capable upstream satisfies it. The
--                                     upstream's own /v1/responses path
--                                     answers 500 and is never called.
--   supports_completions       FALSE  POST /v1/completions -> 404 (an HTML
--                                     page, not an API error).
--   supports_embeddings        FALSE  POST /v1/embeddings -> 404.
--   supports_cache_read/write  FALSE  two identical calls both reported
--                                     cached_tokens 0 and cache_write_tokens
--                                     null.
--   batch, image generation, image edit  FALSE. Not probed, so not claimed.
--   The sole-carrier succession for those three runs through
--   route-openrouter-auto-live and is untouched here.
--
-- THE MODEL ID, AND THE DRIFT BEHIND IT
--   Six catalogued free ids were probed. Three are dead upstream right now:
--   deepseek-v4-flash-free answers 400 "Model is unavailable", hy3-free and
--   north-mini-code-free answer 401 "not supported". Of the three that answer,
--   nemotron-3-ultra-free took 59 s on a cold call (outside any sane request
--   timeout) and mimo-v2.5-free spends a small output budget entirely on
--   hidden reasoning and returns null content. big-pickle is the one that
--   behaves: 200 on every call measured, roughly 1.3 s to 1.5 s, native tools,
--   working json_schema. It is the only id wired here.
--
-- PRICING: FIXED, NEVER upstream_actual
--   Every response from this upstream reports "cost": "0". Under
--   pricing_mode 'upstream_actual' the settlement would therefore be zero on a
--   SERVED request, which D-055 forbids outright ("no served request bills
--   zero") and which is the shape of issue #689. It also cannot take the DERIVE
--   margin formula, because a margin over zero is zero and parseRate rejects a
--   $0.00 rate. So it carries the same SERVICE price hive-free carries
--   (20260824_02): 1,000,000 input / 4,000,000 output credits per million
--   tokens, which is $0.001 / $0.004 at the current unit (1 USD =
--   1,000,000,000 credits, 20260823_40). That price covers gateway serving
--   cost, which is the only cost this route actually incurs. Cache prices are
--   zero and both cache capability flags are false, so no previously unbilled
--   token class starts billing here (D-055 again).
--
-- FAILURE HANDLING
--   Exhaustion answers HTTP 429 FreeUsageLimitError with no Retry-After, and
--   that response is INDISTINGUISHABLE from a rejected client identity. Both
--   mean this route cannot serve right now, and neither is fixed by trying
--   again in three hundred milliseconds. Two things already make that a fast
--   failure rather than a retry storm, and this file deliberately adds no
--   third: router_settings.retry_policy.RateLimitErrorRetries is 0 in
--   deploy/litellm/config.yaml, so LiteLLM gives a rate limit zero retries;
--   and the alias is single-member by construction, so there is no sibling
--   deployment for the router to churn through. The edge's own retry ladder
--   (apps/edge-api/internal/inference/retry.go) still treats 429 as
--   retryable for every route; teaching it to tell a transient limit from a
--   hard wall is a separate change owned by another agent, and is deliberately
--   not touched here.
--
-- RE-RUNNABILITY
--   Every statement is an INSERT with ON CONFLICT DO NOTHING. A second run
--   affects zero rows and errors on nothing. There are no UPDATEs at all,
--   which is also what keeps this file off every row it does not own.
--
-- STATEMENT ORDER
--   provider_routes.alias_id and provider_routes.provider are both NOT NULL
--   with non-deferrable FKs, so the alias row and the provider row must both
--   exist before the route does; provider_capabilities.route_id likewise
--   follows the route.
-- =============================================================================

BEGIN;

SET LOCAL lock_timeout = '5s';

-- ─── 1. The keyless provider ────────────────────────────────────────────────
--
-- api_key_env is the empty string on purpose. See the header: it is how this
-- schema says "no credential", and the config generator reads it that way.

insert into public.custom_providers (
    slug, display_name, base_url, api_key_env, litellm_prefix, enabled
) values
    ('opencode-zen', 'OpenCode Zen (keyless)', 'https://opencode.ai/zen/v1',
     '', 'openai/', true)
on conflict (slug) do nothing;

-- ─── 2. The alias, before any route references it ───────────────────────────

insert into public.model_aliases (
    alias_id,
    owned_by,
    display_name,
    summary,
    visibility,
    lifecycle,
    pricing_mode,
    capability_badges,
    input_price_credits,
    output_price_credits,
    cache_read_price_credits,
    cache_write_price_credits
) values
    (
        'hive-free-tools',
        'hive',
        'Hive Free Tools',
        'Free-tier alias that also offers tool calling and structured output. Served from a single verified route, so a request that needs tools or a JSON schema is answered rather than refused.',
        'public',
        'stable',
        'fixed',
        '["stable","chat","responses","tools"]'::jsonb,
        1000000,
        4000000,
        0,
        0
    )
on conflict (alias_id) do nothing;

insert into public.alias_route_policies (
    alias_id,
    policy_mode,
    allow_price_class_widening,
    fallback_order
) values
    ('hive-free-tools', 'pinned', false, '["route-free-opencode-zen"]'::jsonb)
on conflict (alias_id) do nothing;

-- A default-tier key sees only aliases in its groups, so without these rows
-- the alias is invisible however correct its price.
insert into public.model_policy_group_members (group_name, alias_id) values
    ('default', 'hive-free-tools'),
    ('closed',  'hive-free-tools')
on conflict (group_name, alias_id) do nothing;

-- ─── 3. The route, in a group of its own ────────────────────────────────────
--
-- litellm_model_name equals route_id here, which is the ordinary shape for
-- every route in this catalog that is not a pool member. It is what keeps this
-- deployment out of any load-balanced group whose members carry different
-- capability flags.
--
-- provider_model carries the `openai/` prefix because LiteLLM reaches this
-- endpoint through the generic OpenAI adapter, exactly as the gemini row does:
-- the prefix selects the adapter and custom_providers.base_url is what makes
-- it mean this endpoint rather than api.openai.com.

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
    ('route-free-opencode-zen', 'hive-free-tools', 'opencode-zen',
     'openai/big-pickle', 'route-free-opencode-zen', 'standard', 'healthy', 10)
on conflict (route_id) do nothing;

-- ─── 4. Capabilities, as measured ───────────────────────────────────────────
--
-- provider_capabilities is INNER JOINed by the routing repository, so a route
-- with no row here is invisible to selection and every endpoint 422s.

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
    ('route-free-opencode-zen',
     true,   -- supports_responses: translated to chat completions before dispatch
     true,   -- supports_chat_completions: measured 200
     false,  -- supports_completions: measured 404
     false,  -- supports_embeddings: measured 404
     true,   -- supports_streaming: measured SSE chunks
     true,   -- supports_reasoning: reasoning_effort accepted, 200
     false,  -- supports_cache_read: cached_tokens 0 on repeat calls
     false,  -- supports_cache_write: cache_write_tokens null
     true,   -- tools_supported: native tool_calls and json_schema both measured
     false,  -- supports_batch: not probed, not claimed
     false,  -- supports_image_generation: not probed, not claimed
     false)  -- supports_image_edit: not probed, not claimed
on conflict (route_id) do nothing;

COMMIT;
