-- =============================================================================
-- Customer-facing catalog restructure (owner directive, 2026-08-22).
--
-- WHAT CHANGES
--   * hive-small        NEW. Same upstream model as hive-fast, and the same
--                       INPUT and OUTPUT price. This is the rename half of
--                       "hive-fast becomes hive-small", done as an add rather
--                       than an in-place rename; see BACK-COMPAT below.
--                       "Same price" is stated for input and output only, on
--                       purpose. hive-fast still carries cache_read 1 and
--                       cache_write 4 from the original 20260331_01 seed,
--                       while hive-small is inserted with 0 and 0. Those
--                       columns are display-only, precedence.go never reads
--                       them, but they ARE published through
--                       catalog.CatalogPricing, so the difference is visible.
--                       Zero is the correct value for a Groq gpt-oss route,
--                       which declares no cache support and has no published
--                       cache rate; hive-fast's non-zero pair is the stale
--                       one, and it is left alone rather than corrected here
--                       so that the deprecated alias is not repriced on any
--                       axis by this change.
--   * hive-medium       NEW. Groq openai/gpt-oss-120b, the larger sibling of
--                       the model hive-small serves.
--   * deepseek-v4-flash NEW. OpenRouter ~deepseek/deepseek-v4-flash-latest.
--   * deepseek-v4-pro   NEW. OpenRouter deepseek/deepseek-v4-pro-0813.
--   * hive-default      REPOINTED from OpenRouter openai/gpt-4o-mini to Groq
--                       openai/gpt-oss-20b, and repriced to that model's rate.
--   * hive-auto         REPOINTED from OpenRouter openai/gpt-4.1-mini to Groq
--                       openai/gpt-oss-120b, and repriced to that model's rate.
--   * hive-fast         Marked deprecated. Nothing else about it moves.
--
-- WHY THE TWO REPOINTS GO TO GROQ AND NOT TO COMPOUND
--   The original directive asked for hive-default and hive-auto to move to
--   Groq's Compound systems (groq/compound-mini and groq/compound). They do
--   not, because Compound has no per-token price to derive from. Groq's own
--   model table at https://console.groq.com/docs/models shows "--" in the
--   pricing column for both systems, and
--   https://console.groq.com/docs/compound/systems/compound-mini states
--   verbatim "Final pricing depends on which underlying models and tools are
--   used for your specific query", then itemises charges that are not
--   per-token at all: basic web search $5 per 1000 requests, advanced web
--   search $8 per 1000, visit website $1 per 1000, code execution $0.18 per
--   hour. model_aliases stores exactly one input/output credit pair and
--   apps/edge-api/internal/metering/precedence.go charges from it, so any pair
--   written here would be a fixed customer price against a supplier cost that
--   varies per request with tool use. On a representative 2000-in/500-out
--   request the token cost is about $0.0006 while two basic web searches cost
--   $0.010, so the tool fee is the dominant term, not a rounding error.
--   Compound therefore waits on variable-cost settlement, which does not exist
--   yet; hive-auto is its intended home once it does.
--
--   Both aliases still leave OpenRouter, because OpenRouter costs real money
--   out of pocket while Groq is free to us at present. They move instead to
--   the same two plain per-token Groq models this migration already adds, so
--   the repoint needs no new pricing machinery.
--
-- BOTH REPOINTED ALIASES GET CHEAPER
--   hive-default  21000 in / 84000  out  ->  10500 in / 42000 out
--   hive-auto     56000 in / 224000 out  ->  21000 in / 84000 out
--   A price reduction cannot overcharge anyone, so this is safe to apply, but
--   it does change revenue on the highest-volume path in the product.
--
--   Honesty about what the names now mean: hive-auto performs no automatic
--   routing any more, and hive-default is functionally identical to
--   hive-small. Both are kept as semantic aliases for back-compat and for the
--   meaning of their names, not as distinct capabilities, and their summary
--   text is rewritten below to say exactly that rather than imply routing
--   intelligence that is not there.
--
-- WHAT DELIBERATELY DOES NOT CHANGE
--   hive-embedding-default, hive-stt and hive-tts are untouched, and so is
--   route-doc-vlm, which has no provider_routes row at all and survives as an
--   operator-managed LiteLLM entry still reading OPENROUTER_AUTO_MODEL.
--
--   One loose end this migration cannot tidy from the database. The two
--   gateway-level chat fallbacks that named route-openrouter-default and
--   route-openrouter-auto have been REMOVED from deploy/litellm/config.yaml in
--   this same change, so a fresh boot is correct. They were not re-pointed at
--   the replacement routes on purpose: a fallback answers from a different
--   upstream model than the alias was priced against, which breaks the
--   one-alias-one-price rule one layer below the catalog, where the price
--   cannot see it. Do not reintroduce them.
--
--   What the seed file cannot reach is a box that is already running. The
--   config sync preserves litellm_settings verbatim, so a live volume keeps
--   whatever fallback lines it already has until someone rewrites that block
--   by hand. Those stale entries will name models no longer in model_list,
--   which is inert rather than fatal, so cleaning them up is deliberately out
--   of scope for a catalog change.
--
-- BACK-COMPAT (non-negotiable)
--   hive-fast is the model id persisted per-conversation in existing Open
--   WebUI chats and in live API clients. It is NOT renamed in place and NOT
--   deleted: hive-small is added alongside it, pointing at the same upstream
--   model at the same price through its own route, and hive-fast keeps
--   visibility 'public' so it stays resolvable. Only its lifecycle moves.
--
--   The deprecation marker is lifecycle = 'hidden'. It is not 'deprecated'
--   because model_aliases' CHECK constraint
--   (20260331_01_model_catalog.sql) permits only 'stable', 'preview' and
--   'hidden'; a literal 'deprecated' would abort this migration on apply.
--   Being honest about its reach. No Go code filters on lifecycle: it is
--   selected and returned by catalog/repository.go as a display field and
--   nothing gates on it, so hive-fast still appears in /v1/models and stays
--   invocable, which is the point. The front end DOES read it, though:
--   apps/web-console/components/catalog/model-catalog-table.tsx maps lifecycle
--   to a status badge and has an explicit 'hidden' entry rendering as
--   "Hidden". So the visible effect in the console catalog is that hive-fast
--   is listed, priced and fully callable while its status column reads
--   Hidden. That is a fair rendering of a deprecated alias, but it is not
--   nothing, and a reader should not have to discover it. The alternative,
--   moving its visibility to 'internal', would make catalog.AliasVisibleToTenant
--   fail closed and break every saved chat, because that one predicate governs
--   both the listing and the inference-time entitlement check.
--
-- ONE ALIAS, ONE ENABLED ROUTE (owner rule)
--   Every alias added here gets exactly one route and an alias_route_policies
--   row in 'pinned' mode whose fallback_order names only that route, matching
--   how 20260717_02 seeded the voice aliases. No fallbacks are added and the
--   existing disabled route-openrouter-fast-fallback is left disabled.
--
-- FORMULA (identical to 20260801_01, 20260801_13 and 20260818_01)
--   credits_per_million = ceil(provider_list_usd_per_million * MARGIN * CREDITS_PER_USD)
--     MARGIN          = 1.4
--     CREDITS_PER_USD = 100000   (apps/control-plane/internal/payments/types.go)
--
-- PROVIDER RATES, ALL FETCHED 2026-08-22
--   Groq openai/gpt-oss-20b    $0.075 in / $0.30 out per million
--   Groq openai/gpt-oss-120b   $0.15  in / $0.60 out per million
--     source: https://console.groq.com/docs/models. Note for future
--     re-derivations: https://groq.com/pricing, the source 20260801_01 and
--     20260818_01 both cite, now 301-redirects to the marketing homepage and
--     carries no rate card at all. The second agreeing Groq-owned source used
--     here is https://console.groq.com/docs/compound/systems/compound-mini,
--     whose underlying-model cost breakdown independently lists GPT-OSS 120B
--     at $0.15 input / $0.60 output. The 20b figures are unchanged from what
--     20260818_01 recorded.
--   OpenRouter ~deepseek/deepseek-v4-flash-latest
--     $0.0639 in / $0.1278 out / $0.01278 cache read per million
--   OpenRouter deepseek/deepseek-v4-pro-0813
--     $1.122 in / $3.366 out / $0.0374 cache read per million
--     source: https://openrouter.ai/api/v1/models, which quotes USD per token;
--     the figures above are those values multiplied by 1e6. Neither model
--     publishes a cache-WRITE rate, so both cache_write_price_credits are 0.
--
--   The leading tilde in ~deepseek/deepseek-v4-flash-latest is part of the
--   real OpenRouter model id and must not be "corrected" away. Verified live:
--   the API returns three distinct flash entries and they are not the same
--   model at the same price, so dropping the tilde silently reprices the
--   route: '~deepseek/deepseek-v4-flash-latest' (0.0639/0.1278, tokenizer
--   "Router"), 'deepseek/deepseek-v4-flash-0731' (0.08/0.18) and
--   'deepseek/deepseek-v4-flash' (0.05586/0.11172).
--
--   KNOWN RISK on that alias: it is a "-latest" router entry, so the model it
--   resolves to, and therefore its published rate, can change without a
--   migration. The price written here is correct as of the fetch date only.
--   deepseek-v4-pro is date-pinned (-0813) and does not carry this risk.
--
-- WORKED DERIVATION
--   Machine-checked. The DERIVE rows below are parsed by
--   TestCatalogAliasPricesMatchProviderRates in
--   apps/control-plane/internal/routing/catalog_alias_pricing_test.go, which
--   recomputes every credit figure from the rate beside it in exact math/big
--   rational arithmetic, cross-checks that rate against the committed snapshot
--   in that package's testdata/provider_rates_2026-08-22.json, and fails if
--   any figure here disagrees with the SQL below. Editing a price without
--   editing its rate, or either without re-snapshotting, turns that test red.
--
-- DERIVE| alias_id | route_id | provider_model | field | usd_per_million | credits
-- DERIVE| hive-small | route-groq-small | groq/openai/gpt-oss-20b | in | 0.075 | 10500
-- DERIVE| hive-small | route-groq-small | groq/openai/gpt-oss-20b | out | 0.30 | 42000
-- DERIVE| hive-medium | route-groq-medium | groq/openai/gpt-oss-120b | in | 0.15 | 21000
-- DERIVE| hive-medium | route-groq-medium | groq/openai/gpt-oss-120b | out | 0.60 | 84000
-- DERIVE| hive-default | route-groq-default | groq/openai/gpt-oss-20b | in | 0.075 | 10500
-- DERIVE| hive-default | route-groq-default | groq/openai/gpt-oss-20b | out | 0.30 | 42000
-- DERIVE| hive-auto | route-groq-auto | groq/openai/gpt-oss-120b | in | 0.15 | 21000
-- DERIVE| hive-auto | route-groq-auto | groq/openai/gpt-oss-120b | out | 0.60 | 84000
-- DERIVE| deepseek-v4-flash | route-deepseek-v4-flash | openrouter/~deepseek/deepseek-v4-flash-latest | in | 0.0639 | 8946
-- DERIVE| deepseek-v4-flash | route-deepseek-v4-flash | openrouter/~deepseek/deepseek-v4-flash-latest | out | 0.1278 | 17892
-- DERIVE| deepseek-v4-flash | route-deepseek-v4-flash | openrouter/~deepseek/deepseek-v4-flash-latest | cache_read | 0.01278 | 1790
-- DERIVE| deepseek-v4-pro | route-deepseek-v4-pro | openrouter/deepseek/deepseek-v4-pro-0813 | in | 1.122 | 157080
-- DERIVE| deepseek-v4-pro | route-deepseek-v4-pro | openrouter/deepseek/deepseek-v4-pro-0813 | out | 3.366 | 471240
-- DERIVE| deepseek-v4-pro | route-deepseek-v4-pro | openrouter/deepseek/deepseek-v4-pro-0813 | cache_read | 0.0374 | 5236
--
--   Longhand, for a reader rather than the parser:
--     hive-small        in  0.075   * 1.4 * 100000 =   10500      (exact)
--     hive-small        out 0.300   * 1.4 * 100000 =   42000      (exact)
--     hive-medium       in  0.150   * 1.4 * 100000 =   21000      (exact)
--     hive-medium       out 0.600   * 1.4 * 100000 =   84000      (exact)
--     hive-default      in  0.075   * 1.4 * 100000 =   10500      (exact)
--     hive-default      out 0.300   * 1.4 * 100000 =   42000      (exact)
--     hive-auto         in  0.150   * 1.4 * 100000 =   21000      (exact)
--     hive-auto         out 0.600   * 1.4 * 100000 =   84000      (exact)
--   hive-default and hive-auto are written out in full rather than as "same as
--   hive-small" and "same as hive-medium" on purpose: they are independent
--   rows, and a future reader repricing one of them must not silently reprice
--   the other by editing a shared line.
--     deepseek-v4-flash in  0.0639  * 1.4 * 100000 =    8946      (exact)
--     deepseek-v4-flash out 0.1278  * 1.4 * 100000 =   17892      (exact)
--     deepseek-v4-flash cr  0.01278 * 1.4 * 100000 =    1789.2 -> 1790 (CEILED)
--     deepseek-v4-pro   in  1.122   * 1.4 * 100000 =  157080      (exact)
--     deepseek-v4-pro   out 3.366   * 1.4 * 100000 =  471240      (exact)
--     deepseek-v4-pro   cr  0.0374  * 1.4 * 100000 =    5236      (exact)
--   Only deepseek-v4-flash's cache-read product is fractional, so it is the
--   one row where the ceiling actually does something.
--
-- ALIAS NAMING
--   deepseek-v4-flash and deepseek-v4-pro name their provider's model family,
--   unlike the provider-blind hive-* aliases. That is an explicit owner
--   decision taken after the provider-blind convention in CLAUDE.md was
--   raised, and it is scoped to these two alias names only. The
--   provider-blind rule continues to apply in full to error messages and to
--   routing internals. The display_name strings are the owner's own wording,
--   verbatim. alias_id is the value a client sends in the OpenAI-compatible
--   "model" field, so it is the slug form rather than the display string.
--
-- PRICE UNIT
--   All four aliases are per-token, so price_unit is left at its column
--   default of 'tokens' (20260801_13_alias_price_unit.sql).
--
-- CACHE COLUMNS
--   cache_read_price_credits is derived for the two DeepSeek aliases because
--   OpenRouter publishes a cache-read rate for both. It is 0 for the two Groq
--   aliases because Groq publishes no cache-read rate for the gpt-oss family;
--   0 here means "no rate published", and mirrors that both Groq routes below
--   declare supports_cache_read = false. These columns remain display-only
--   either way: precedence.go does not read them, exactly as recorded in
--   20260801_01.
--
-- RE-RUNNABILITY
--   Every INSERT carries ON CONFLICT DO NOTHING and every UPDATE carries a
--   WHERE guard that excludes rows already at the target value, so a second
--   run of this file affects zero rows and errors on nothing.
--
--   DO NOTHING rather than DO UPDATE on the INSERTS, for the reason 20260717_02
--   gives: a row already present may have been retuned since, and this
--   migration has no business reverting that. Scoped deliberately to the
--   INSERTs, because the UPDATEs below do exactly that reverting. Replaying
--   this file after someone corrects hive-default's or hive-auto's price, or
--   un-hides hive-fast, puts those rows back to the 2026-08-22 values. That is
--   what a repricing migration is for and 20260818_01 set the same precedent,
--   but it means this file is NOT safe to replay over hand-tuned state.
-- =============================================================================

-- One transaction, for the reason 20260818_01 introduced it: these statements
-- establish the alias, its single route, its capabilities, its policy and its
-- group membership together. A request landing on ListRouteCandidates or
-- LoadAliasPricing partway through must not see an alias that exists but has no
-- route, or a route with no price.
BEGIN;

-- 1. The four new customer-facing aliases.
insert into public.model_aliases (
    alias_id,
    owned_by,
    display_name,
    summary,
    visibility,
    lifecycle,
    capability_badges,
    input_price_credits,
    output_price_credits,
    cache_read_price_credits,
    cache_write_price_credits
) values
    (
        'hive-small',
        'hive',
        'Hive Small',
        'Fast, low-cost chat for everyday prompts. Replaces hive-fast, which is deprecated and now resolves to the same model at the same price.',
        'public',
        'stable',
        '["stable","chat","responses"]'::jsonb,
        10500,
        42000,
        0,
        0
    ),
    (
        'hive-medium',
        'hive',
        'Hive Medium',
        'Larger general-purpose chat model. Same family as Hive Small, more capacity per request.',
        'public',
        'stable',
        '["stable","chat","responses"]'::jsonb,
        21000,
        84000,
        0,
        0
    ),
    (
        'deepseek-v4-flash',
        'hive',
        'Deepseek V4 Flash',
        'Very low-cost long-context chat with tool use and reasoning. Largest context window in the catalog.',
        'public',
        'stable',
        '["stable","chat","responses","tools","reasoning"]'::jsonb,
        8946,
        17892,
        1790,
        0
    ),
    (
        'deepseek-v4-pro',
        'hive',
        'Deepseek V4 Pro',
        'Highest-capability long-context chat with tool use and reasoning, for harder work.',
        'public',
        'stable',
        '["stable","chat","responses","tools","reasoning"]'::jsonb,
        157080,
        471240,
        5236,
        0
    )
on conflict (alias_id) do nothing;

-- 2. Exactly one route per new alias.
--    route-groq-small deliberately points at the same upstream model as the
--    existing route-groq-fast rather than reusing that route: provider_routes
--    is keyed one route to one alias, so hive-small needs its own row for
--    hive-fast to keep working. Two routes naming one upstream model is a
--    shape this migration itself establishes three times over:
--    route-groq-fast, route-groq-small and route-groq-default all call
--    groq/openai/gpt-oss-20b, and route-groq-medium and route-groq-auto both
--    call groq/openai/gpt-oss-120b. (An earlier draft cited
--    route-openrouter-auto and route-doc-vlm sharing a model in
--    deploy/litellm/config.yaml; this change deletes the first of those, so
--    the citation would have sent a reader looking for something gone.)
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
        'route-groq-small',
        'hive-small',
        'groq',
        'groq/openai/gpt-oss-20b',
        'route-groq-small',
        'standard',
        'healthy',
        10
    ),
    (
        'route-groq-medium',
        'hive-medium',
        'groq',
        'groq/openai/gpt-oss-120b',
        'route-groq-medium',
        'standard',
        'healthy',
        10
    ),
    (
        'route-deepseek-v4-flash',
        'deepseek-v4-flash',
        'openrouter',
        'openrouter/~deepseek/deepseek-v4-flash-latest',
        'route-deepseek-v4-flash',
        'budget',
        'healthy',
        10
    ),
    (
        'route-deepseek-v4-pro',
        'deepseek-v4-pro',
        'openrouter',
        'openrouter/deepseek/deepseek-v4-pro-0813',
        'route-deepseek-v4-pro',
        'premium',
        'healthy',
        10
    )
on conflict (route_id) do nothing;

-- 3. Capabilities per route.
--
--    supports_reasoning is TRUE on both Groq rows, and this is worth stating
--    because an earlier revision had it false on the reasoning that an
--    over-claim breaks requests while an under-claim merely withholds a
--    feature. That reasoning is wrong for every alias in this migration.
--    It holds only for a multi-route alias, where a narrower route just loses
--    the ordering contest. Every alias here is pinned to exactly ONE route by
--    step 4, so with a single candidate matchesRequestedCapabilities
--    (routing/service.go) drops it, SelectRoute returns ErrRouteNotEligible
--    and writeRoutingError maps that to 422. An under-claim on a pinned alias
--    is not a withheld feature, it is a failed request.
--    The flag is also simply true: the gpt-oss family exposes reasoning
--    effort, and apps/edge-api/internal/inference/chat_completions.go sets
--    NeedReasoning whenever a request carries reasoning_effort.
--    route-groq-fast still carries false from its original seed. That is a
--    pre-existing under-claim on the deprecated alias, left alone here rather
--    than widened in a pricing migration.
--
--    supports_cache_read and supports_cache_write stay false. Groq publishes
--    no cache rate for the gpt-oss family, and unlike reasoning this costs
--    nothing at dispatch: no request path in edge-api ever sets NeedCacheRead
--    or NeedCacheWrite, they are plumbed through the routing API and never
--    populated, so a false flag here cannot turn into a 422.
--
--    tools_supported is true for all four. 20260612_01 already sets it true
--    for every openrouter and groq route by provider, but that migration ran
--    once over the rows that existed then; new rows would otherwise take the
--    column DEFAULT of false and silently reject tools, tool_choice and
--    response_format (PR #206 routes those on this column).
--
--    The DeepSeek flags follow the live API's supported_parameters and pricing
--    fields directly: both list tools, tool_choice, response_format,
--    structured_outputs, reasoning and reasoning_effort, and both publish a
--    cache-read but no cache-write rate. One real difference is not
--    representable here: flash lists parallel_tool_calls and pro does not, and
--    provider_capabilities has no parallel-tool-call column. No catalog claim
--    becomes untrue by that omission, the same conclusion 20260818_01 reached
--    about the identical gap.
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
    tools_supported
) values
    ('route-groq-small',        true, true, true, false, true, true, false, false, true),
    ('route-groq-medium',       true, true, true, false, true, true, false, false, true),
    ('route-deepseek-v4-flash', true, true, true, false, true, true, true,  false, true),
    ('route-deepseek-v4-pro',   true, true, true, false, true, true, true,  false, true)
on conflict (route_id) do nothing;

-- 4. Pin each alias to its single route. 'pinned' with a one-entry
--    fallback_order is the shape 20260717_02 used for the voice aliases and is
--    what the one-alias-one-enabled-route rule requires.
insert into public.alias_route_policies (
    alias_id,
    policy_mode,
    allow_price_class_widening,
    fallback_order
) values
    ('hive-small', 'pinned', false, '["route-groq-small"]'::jsonb),
    ('hive-medium', 'pinned', false, '["route-groq-medium"]'::jsonb),
    ('deepseek-v4-flash', 'pinned', false, '["route-deepseek-v4-flash"]'::jsonb),
    ('deepseek-v4-pro', 'pinned', false, '["route-deepseek-v4-pro"]'::jsonb)
on conflict (alias_id) do nothing;

-- 5. Group membership. Without this the whole migration is inert for customers:
--    api_key_policies.allowed_group_names defaults to '["default"]', so a
--    default-tier key never sees an alias that is not in the 'default' group,
--    however correctly it is priced and routed. This gap has already had to be
--    patched by hand twice, by 20260717_01 for hive-auto and by 20260717_02 for
--    the voice aliases. 'closed' mirrors how 20260331_03 seeded every chat
--    alias into both groups.
--    deepseek-v4-pro is additionally placed in 'premium'. That group exists
--    for "Premium or higher-cost models" (20260331_03), and after this
--    migration deepseek-v4-pro is by a wide margin the most expensive alias in
--    the catalog at 157080 in and 471240 out, while hive-auto, the alias
--    'premium' was originally created around, has just been repriced DOWN to
--    21000 and 84000. Leaving the highest-cost model out of the cost-gating
--    group would quietly break that group's only purpose, and a key scoped to
--    ["premium"] could not reach the premium model at all. It stays in
--    'default' as well because the owner asked for these aliases to be
--    customer-facing, and prepaid credit balance, not group membership, is
--    what actually bounds spend.
insert into public.model_policy_group_members (group_name, alias_id) values
    ('default', 'hive-small'),
    ('default', 'hive-medium'),
    ('default', 'deepseek-v4-flash'),
    ('default', 'deepseek-v4-pro'),
    ('closed', 'hive-small'),
    ('closed', 'hive-medium'),
    ('closed', 'deepseek-v4-flash'),
    ('closed', 'deepseek-v4-pro'),
    ('premium', 'deepseek-v4-pro')
on conflict (group_name, alias_id) do nothing;

-- 6. Move hive-default and hive-auto off OpenRouter and onto the two Groq
--    models. Each alias gets a NEW route row and its old OpenRouter route is
--    disabled, so every alias still has exactly one enabled route: four
--    aliases resolving to two distinct upstream models through four separate
--    routes. No route row is shared between aliases.
--
--    WHY NEW ROUTES RATHER THAN REPOINTING THE EXISTING ROWS IN PLACE.
--    This was the tempting one-line change and it is wrong. LiteLLM's config
--    sync merges FIELD BY FIELD: the database owns only model, api_base and
--    api_key, and every other key already on the entry survives on purpose, so
--    that hand-tuning sticks across syncs (mergeParams in
--    apps/control-plane/internal/litellmconfig/generator.go, issue #707).
--    route-openrouter-default and route-openrouter-auto both carry an
--    OpenRouter-specific extra_body block in deploy/litellm/config.yaml
--    (provider.allow_fallbacks and provider.sort). Repointing those route_ids
--    at Groq would leave that block attached and send OpenRouter's vendor
--    routing object to Groq on every request to the DEFAULT model, and no
--    sync could ever remove it. Retiring the route id instead makes the merge
--    drop the whole stale entry, because a known route_id that is no longer
--    active is deleted from the config rather than updated. Disabling rather
--    than deleting the row follows 20260801_01, which retired
--    route-openrouter-fast-fallback exactly this way: SelectRoute filters
--    'disabled', the config sync excludes it, and the row stays reversible.
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
        'route-groq-default',
        'hive-default',
        'groq',
        'groq/openai/gpt-oss-20b',
        'route-groq-default',
        'standard',
        'healthy',
        10
    ),
    (
        'route-groq-auto',
        'hive-auto',
        'groq',
        'groq/openai/gpt-oss-120b',
        'route-groq-auto',
        'standard',
        'healthy',
        10
    )
on conflict (route_id) do nothing;

--    route-groq-auto additionally inherits three flags that live on NO other
--    route in the catalog: supports_batch, supports_image_generation and
--    supports_image_edit. 20260414_01_provider_capabilities_media_columns.sql
--    granted all five media flags to route-openrouter-auto and to no other
--    row, and 20260717_02 later corrected two of them (tts, stt) back to
--    false. Disabling route-openrouter-auto without carrying the remaining
--    three forward would delete three product surfaces catalog-wide, not just
--    for hive-auto: SelectRoute skips disabled candidates and then hard
--    filters on each flag (service.go), and both batchstore/submitter.go and
--    batchstore/local_executor_adapters.go send NeedBatch = true for EVERY
--    batch, so /v1/batches, /v1/images/generations and /v1/images/edits would
--    each find zero eligible routes for every alias in the system.
--
--    supports_batch is a true claim: Phase 15 ships a control-plane local
--    batch executor and the route carries executor_kind 'local', so batching
--    does not depend on Groq having a native batch API, which it does not.
--
--    The two image flags are carried forward as a STATUS QUO PRESERVATION and
--    are not a fresh assertion by this migration. They were already untrue of
--    route-openrouter-auto's previous model: gpt-4.1-mini cannot generate or
--    edit images either. Carrying them keeps this catalog change from
--    silently removing two endpoints as a side effect, which is not what a
--    repricing was asked to do. Correcting them properly needs a real image
--    route with a model that can actually serve one, and that is its own
--    change with its own decision behind it. Tracked separately; do not read
--    these two flags as a claim that gpt-oss-120b does images.
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
    ('route-groq-default', true, true, true, false, true, true, false, false, true, false, false, false),
    ('route-groq-auto',    true, true, true, false, true, true, false, false, true, true,  true,  true)
on conflict (route_id) do nothing;

-- 6b. Retire the two OpenRouter routes these aliases used to take. Disabled,
--     not deleted, so the change is reversible and the history survives.
UPDATE public.provider_routes
   SET health_state = 'disabled'
 WHERE route_id IN ('route-openrouter-default', 'route-openrouter-auto')
   AND health_state <> 'disabled';

-- 6c. Point each alias's policy at its new route, so no policy row names a
--     route that is no longer selectable. Same step 20260801_01 took after
--     disabling route-openrouter-fast-fallback.
UPDATE public.alias_route_policies
   SET fallback_order = '["route-groq-default"]'::jsonb
 WHERE alias_id = 'hive-default'
   AND fallback_order <> '["route-groq-default"]'::jsonb;

UPDATE public.alias_route_policies
   SET fallback_order = '["route-groq-auto"]'::jsonb
 WHERE alias_id = 'hive-auto'
   AND fallback_order <> '["route-groq-auto"]'::jsonb;

-- 7. Reprice the two moved aliases to the rate of the model they now call,
--    and rewrite their summaries so the names do not imply behaviour the
--    aliases no longer have. Both prices go DOWN; see the header table.
--    The cache columns are zeroed on the same pass. They still carry the
--    values the original seed gave them against OpenRouter models
--    (20260331_01: hive-default 2 and 6, hive-auto 1 and 5), and both aliases
--    now sit on Groq routes declaring supports_cache_read = false and
--    supports_cache_write = false. These columns are not internal:
--    catalog.CatalogPricing marshals them into the public catalog response, so
--    leaving them would advertise a cache-read price for two models that do no
--    caching. Zero here means "no rate published", exactly as it does for the
--    four aliases inserted above.
UPDATE public.model_aliases
   SET input_price_credits       = 10500,
       output_price_credits      = 42000,
       cache_read_price_credits  = 0,
       cache_write_price_credits = 0,
       summary                   = 'Default alias for requests that name no model. Now resolves to the same fast, low-cost model as Hive Small; kept as a distinct alias for back-compat.',
       updated_at                = now()
 WHERE alias_id = 'hive-default'
   AND (input_price_credits <> 10500 OR output_price_credits <> 42000
        OR cache_read_price_credits <> 0 OR cache_write_price_credits <> 0);

UPDATE public.model_aliases
   SET input_price_credits       = 21000,
       output_price_credits      = 84000,
       cache_read_price_credits  = 0,
       cache_write_price_credits = 0,
       summary                   = 'Larger-capacity alias. Performs no automatic routing or model selection; it resolves to the same model as Hive Medium and is kept as a distinct alias for back-compat.',
       updated_at                = now()
 WHERE alias_id = 'hive-auto'
   AND (input_price_credits <> 21000 OR output_price_credits <> 84000
        OR cache_read_price_credits <> 0 OR cache_write_price_credits <> 0);

-- 8. Deprecate hive-fast. Route, price, visibility and group membership are all
--    left exactly as they are, so every existing conversation and API client
--    keeps working unchanged; only the lifecycle marker moves.
UPDATE public.model_aliases
   SET lifecycle  = 'hidden',
       updated_at = now()
 WHERE alias_id = 'hive-fast'
   AND lifecycle <> 'hidden';

COMMIT;
