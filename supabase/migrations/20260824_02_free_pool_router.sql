-- =============================================================================
-- The Free Pool Router and the last two catalog repoints (owner directive,
-- 2026-08-24, plus the coordinator scope extension of the same day).
--
-- WHAT CHANGES, IN THREE PARTS
--
--   PART A. THE FREE POOL
--   * hive-free          NEW. The product's free-tier serving alias. Pinned to
--                        a ROUTE GROUP: four provider_routes rows that share
--                        one litellm_model_name ('route-free-pool'), which the
--                        config sync emits as four deployments under ONE
--                        model_name in deploy/litellm/config.yaml. LiteLLM's
--                        router load-balances across them and cools a failing
--                        deployment down (litellm_settings.cooldown_time 30 /
--                        allowed_fails 3 / router_settings.retry_policy, all
--                        already shipped in the seed config and preserved
--                        verbatim by the merge), so one exhausted key fails
--                        over to the others instead of taking the alias down.
--                        This is the OpenRouter-style free router, built from
--                        our own free provider keys.
--   * Members, in emission order (ORDER BY route_id):
--       route-free-pool-free    openrouter dots-studio/dots-3-note-preview:free
--                               (OPENROUTER_API_KEY)
--       route-free-pool-gemini  gemini-flash-latest through LiteLLM's generic
--                               openai/ adapter against Google's official
--                               OpenAI-compatible endpoint (GEMINI_API_KEY).
--                               The owner verified this exact path live on
--                               2026-08-23 (gemini-2.0-flash is retired
--                               upstream; gemini-flash-latest answers 200).
--       route-free-pool-groq    groq/openai/gpt-oss-20b (GROQ_API_KEY)
--       route-free-pool-groq-2  groq/openai/gpt-oss-20b (GROQ_API_KEY_2) --
--                               the second Groq key arrived and was verified
--                               live (gpt-oss-20b answers 200) before this
--                               file shipped, so it is ACTIVE from birth, not
--                               a commented placeholder.
--
--   PART B. THE TWO REPOINTS (coordinator directive)
--   * hive-auto     leaves the dots free route and becomes the REAL OpenRouter
--                   Auto Router: openrouter/openrouter/auto, billed at ACTUAL
--                   upstream cost exactly like the internal openrouter-auto
--                   alias (pricing_mode 'upstream_actual', prices NULL,
--                   reservation hold 2000000000 credits = 2.00 USD at the
--                   current 1e9 unit). Slug verified live against
--                   https://openrouter.ai/api/v1/models on 2026-08-24:
--                   both 'openrouter/auto' and 'openrouter/auto-beta' exist,
--                   each reports pricing prompt/completion -1 (variable by
--                   construction) and lists tools, tool_choice,
--                   response_format, structured_outputs, reasoning and
--                   reasoning_effort among supported_parameters.
--   * hive-default  leaves the dots free route for deepseek-v4-flash, the
--                   paid quality tier, keeping its tool-parity story: the
--                   source route-deepseek-v4-flash carries tools_supported =
--                   true (20260822_02 step 3), so the claim survives honestly.
--
--   PART C. CI AND DAILY AUTOMATED CONSUMPTION MOVE TO hive-free
--   Handled outside this file (.github/workflows/ci.yml default,
--   deploy-demo-box.yml sdk-replay, packages/sdk-tests fallbacks,
--   scripts/post-deploy-verify.py, scripts/verify-control-plane.py); guarded
--   by TestCITestDefaultsPointAtTheFreeAlias in
--   apps/control-plane/internal/routing/free_pool_router_test.go.
--
-- WHY THE POOL IS FOUR DB ROWS RATHER THAN A GENERATOR FEATURE
--   provider_routes.litellm_model_name is the column that actually names the
--   LiteLLM gateway alias ("model_name:" in config.yaml); route_id is only
--   the table's primary key (deploy-demo-box.yml's price assertion says
--   exactly this at its lookup). Every seeded row so far kept the two equal,
--   and the sync emitted route_id. The generator change is therefore one
--   column: SyncService now emits pr.litellm_model_name as the entry's
--   model_name. Rows that keep the columns equal are byte-for-byte unchanged;
--   rows that diverge become additional deployments of a shared name. Adding
--   the third Groq key later is one more row plus one env var, nothing else.
--
--   All four member rows carry alias_id 'hive-free'. provider_routes.alias_id
--   is NOT NULL with a FK to model_aliases (20260331_02), so there was no
--   NULL-member option, and multi-route-per-alias is an established shape
--   here (hive-fast served two routes before 20260801_01). It is also
--   behaviorally inert which member selection picks: every member dispatches
--   the same litellm_model_name, so LiteLLM's router sees the same pool
--   regardless, and all four carry identical capability flags, so no flag
--   filter can split them. The pinned policy still names route-free-pool-free
--   as the single fallback_order entry, matching the one-alias-one-route
--   convention for anything that reads it.
--
--   STATEMENT ORDER: the alias row must exist before any route referencing it
--   (non-deferrable FK), so step 2 inserts hive-free ahead of the routes.
--
-- PRICES (current credit unit: 1 USD = 1,000,000,000 credits,
-- migration 20260823_40_credit_unit_rescale_billion.sql)
--   * hive-free   input 1000000 / output 4000000 credits per million tokens
--                 ($0.001 / $0.004). This is a SERVICE PRICE, not a derived
--                 margin: every upstream in the pool costs us zero, so the
--                 DERIVE formula cannot apply and parseRate would reject a
--                 $0.00 rate outright. It exists to cover gateway serving
--                 cost, per the owner's "a credit or a few per request"
--                 framing.
--   * hive-default repriced to the deepseek-v4-flash rates ALREADY in the
--                 catalog (20260822_02 DERIVE table), carried through the
--                 rescale factor 10,000:
--                   in         8946   x 10000 =    89460000
--                   out       17892   x 10000 =   178920000
--                   cache_read 1790   x 10000 =    17900000
--                   cache_write   0            =           0
--                 No new rate fetch, no new snapshot: same upstream model the
--                 deepseek-v4-flash alias serves today.
--   * hive-auto   fixed price columns go NULL under pricing_mode
--                 'upstream_actual'. The shape CHECK added by
--                 20260822_30_openrouter_auto_variable_pricing.sql enforces
--                 exactly this shape; this UPDATE satisfies it within one
--                 statement.
--   * Net catalog after this file (for the PR body):
--       hive-free     free pool, $0.001/$0.004      (CI + daily consumption)
--       hive-default  deepseek-v4-flash, paid       (quality + tool parity)
--       hive-auto     OpenRouter Auto Router, passthrough cost
--       deepseek-v4-pro, hive-small/medium/fast: untouched.
--
-- TOOLS AND STRUCTURED OUTPUT ARE HONESTLY FALSE ON THE POOL
--   tools_supported gates tools, tool_choice and response_format (PR #206).
--   The four pool members are three different providers whose tool behaviour
--   was NOT probed for cross-member parity, so the flag stays FALSE rather
--   than claiming parity nobody verified. Consequence: a tool-bearing request
--   to hive-free is rejected at selection time instead of failing
--   mid-request, and tool traffic lives where it is verified: hive-default
--   (deepseek-v4-flash, tools true) for the paid tier and hive-auto (the Auto
--   Router, whose lineage has always carried tools true). This split is the
--   owner's stated intent and can be collapsed later once cross-pool parity
--   is probed for real.
--   supports_reasoning stays TRUE on the pool: Groq documents gpt-oss as
--   reasoning models (20260822_02 header), gemini-flash-latest is a thinking-
--   capable flash model through an endpoint that maps reasoning_effort, and
--   the dots model accepted every reasoning_effort probe without failing
--   (20260823_21's twelve-shape live run). On a pinned single-route alias an
--   under-claim is a 422, not a withheld feature, so false would BREAK
--   reasoning requests that work today.
--
-- THE SOLE-CARRIER FLAGS MOVE WITH THEIR ALIAS
--   route-free-auto is today's ONLY carrier of supports_batch,
--   supports_image_generation and supports_image_edit. Disabling it without a
--   successor deletes /v1/batches, /v1/images/generations and
--   /v1/images/edits catalog-wide (SelectRoute hard-filters on these flags;
--   batchstore sends NeedBatch for EVERY batch). Its successor for hive-auto
--   is route-openrouter-auto-live, which inherits all three as STATUS QUO
--   PRESERVATION, not as fresh claims about openrouter/auto doing images --
--   the same honest framing 20260822_02 used when it handed them to
--   route-groq-auto. Guarded by
--   TestFreePoolDisablingRouteFreeAutoHandsOnTheSoleCarrierFlags.
--
-- WHY NEW ROUTE IDS, AGAIN
--   Identical to 20260822_02 / 20260823_20 / 20260823_21: the config sync
--   merges field by field and hand-tuned keys survive forever on a reused
--   route_id; retiring the id makes the merge drop the whole stale entry.
--   route-openrouter-auto (disabled since 20260822_02) and
--   route-openrouter-auto-beta (the internal openrouter-auto alias's own
--   route) both stay untouched for that reason too.
--
-- RE-RUNNABILITY
--   Every INSERT carries ON CONFLICT DO NOTHING and every UPDATE carries a
--   WHERE guard excluding rows already at the target value, so a second run
--   affects zero rows and errors on nothing.
-- =============================================================================

BEGIN;

SET LOCAL lock_timeout = '5s';

-- ─── 1. Provider rows for the two new key slots ─────────────────────────────
--
-- api_key_env lives on custom_providers (one per provider), so a SECOND Groq
-- key needs its own provider row pointing at the same Groq endpoint. That is
-- also what makes "add key three" cheap: one row, one env var, one member
-- route. The gemini row's base_url is the OpenAI-compatible surface of the
-- Generative Language API, which is what makes the generic openai/ adapter
-- mean Gemini; the generator refuses any openai/-prefixed model without a
-- non-empty api_base for exactly this reason.

insert into public.custom_providers (
    slug, display_name, base_url, api_key_env, litellm_prefix, enabled
) values
    ('groq-2', 'Groq (key slot 2)', 'https://api.groq.com/openai/v1',
     'GROQ_API_KEY_2', 'groq/', true),
    ('gemini', 'Google Gemini', 'https://generativelanguage.googleapis.com/v1beta/openai',
     'GEMINI_API_KEY', 'openai/', true)
on conflict (slug) do nothing;

-- ─── 2. The hive-free alias, FIRST: every pool member row references it via
--        provider_routes.alias_id's non-deferrable FK, so the alias must exist
--        before any route does. ─────────────────────────────────────────────

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
        'hive-free',
        'hive',
        'Hive Free',
        'Free-tier alias served from a load-balanced pool of our free provider keys; requests fail over automatically when one key is exhausted. Tool calling and structured output are not offered on this alias.',
        'public',
        'stable',
        '["stable","chat","responses"]'::jsonb,
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
    ('hive-free', 'pinned', false, '["route-free-pool-free"]'::jsonb)
on conflict (alias_id) do nothing;

-- Group membership: a default-tier key sees only aliases in its groups, so
-- without these rows the new alias is invisible however correct its price.
insert into public.model_policy_group_members (group_name, alias_id) values
    ('default', 'hive-free'),
    ('closed',  'hive-free')
on conflict (group_name, alias_id) do nothing;

-- ─── 3. The free pool: four routes, one shared litellm_model_name ───────────
--
-- Every member carries alias_id 'hive-free': the column is NOT NULL with a
-- non-deferrable FK (20260331_02), and multi-route-per-alias is an established
-- shape here. It is behaviorally inert which member selection picks -- all
-- four dispatch the same litellm_model_name under identical capability flags.

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
    ('route-free-pool-free',   'hive-free', 'openrouter', 'openrouter/dots-studio/dots-3-note-preview:free', 'route-free-pool', 'standard', 'healthy', 10),
    ('route-free-pool-gemini', 'hive-free', 'gemini',     'openai/gemini-flash-latest',                      'route-free-pool', 'standard', 'healthy', 10),
    ('route-free-pool-groq',   'hive-free', 'groq',       'groq/openai/gpt-oss-20b',                         'route-free-pool', 'standard', 'healthy', 10),
    ('route-free-pool-groq-2', 'hive-free', 'groq-2',     'groq/openai/gpt-oss-20b',                         'route-free-pool', 'standard', 'healthy', 10),
    ('route-openrouter-auto-live', 'hive-auto', 'openrouter', 'openrouter/openrouter/auto',              'route-openrouter-auto-live', 'premium', 'healthy', 10),
    ('route-deepseek-v4-flash-default', 'hive-default', 'openrouter', 'openrouter/~deepseek/deepseek-v4-flash-latest', 'route-deepseek-v4-flash-default', 'budget', 'healthy', 10)
on conflict (route_id) do nothing;

-- The doubled openrouter/ prefix and trailing :free carry the same meaning
-- they do on every sibling row (see 20260823_20's note): LiteLLM strips the
-- leading prefix as its provider selector, and :free selects the zero-priced
-- variant. The tilde in ~deepseek/deepseek-v4-flash-latest is part of the real
-- model id (20260822_02).

-- ─── 4. Capabilities ────────────────────────────────────────────────────────
--
-- Pool members: tools_supported FALSE (cross-provider tool parity unverified;
-- see the header). Everything chat-shaped TRUE, embeddings and cache FALSE.
-- Media flags FALSE everywhere in the pool; their succession line runs through
-- route-openrouter-auto-live below.

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
    ('route-free-pool-free',   true, true, true, false, true, true, false, false, false, false, false, false),
    ('route-free-pool-gemini', true, true, true, false, true, true, false, false, false, false, false, false),
    ('route-free-pool-groq',   true, true, true, false, true, true, false, false, false, false, false, false),
    ('route-free-pool-groq-2', true, true, true, false, true, true, false, false, false, false, false, false),
    ('route-openrouter-auto-live', true, true, true, false, true, true, false, false, true, true, true, true),
    ('route-deepseek-v4-flash-default', true, true, true, false, true, true, true, false, true, false, false, false)
on conflict (route_id) do nothing;

-- route-deepseek-v4-flash-default mirrors its source row
-- (route-deepseek-v4-flash, 20260822_02 step 3): cache_read TRUE because
-- DeepSeek publishes a cache-read rate, tools TRUE because that is the parity
-- claim hive-default is being moved here to keep.

-- ─── 5. hive-auto: the real Auto Router at actual cost ──────────────────────
--
-- One statement flips the whole pricing shape so the mode CHECK holds at its
-- end. NULL prices are deliberate and load-bearing: the Go readers scan them
-- into non-pointer int64 and fail loudly until taught about variable pricing
-- (20260822_30). The hold matches the internal openrouter-auto alias's value
-- after the rescale: 200000 pre-rescale x 10000 = 2000000000.

UPDATE public.model_aliases
   SET pricing_mode                 = 'upstream_actual',
       input_price_credits          = null,
       output_price_credits         = null,
       cache_read_price_credits     = null,
       cache_write_price_credits    = null,
       reservation_estimate_credits = 2000000000
 WHERE alias_id = 'hive-auto'
   AND pricing_mode <> 'upstream_actual';

-- Display metadata gets its OWN guarded statement, deliberately not gated on
-- the pricing mode: on a database where the mode already flipped, a re-run of
-- this file must still be able to land a corrected summary or badge set.
-- provider-blind wording per the catalog convention (the deepseek-v4-*
-- ALIAS IDS carry their vendor name by explicit owner decision; summaries do
-- not name vendors).
UPDATE public.model_aliases
   SET summary           = 'Automatic routing: each request gets a per-request model choice and is billed at actual usage.',
       capability_badges = '["stable","chat","responses","task-aware"]'::jsonb,
       updated_at        = now()
 WHERE alias_id = 'hive-auto'
   AND (summary IS DISTINCT FROM 'Automatic routing: each request gets a per-request model choice and is billed at actual usage.'
        OR capability_badges <> '["stable","chat","responses","task-aware"]'::jsonb);

UPDATE public.alias_route_policies
   SET fallback_order = '["route-openrouter-auto-live"]'::jsonb
 WHERE alias_id = 'hive-auto'
   AND fallback_order <> '["route-openrouter-auto-live"]'::jsonb;

-- ─── 6. hive-default: paid quality on deepseek-v4-flash ─────────────────────

UPDATE public.model_aliases
   SET input_price_credits       = 89460000,
       output_price_credits      = 178920000,
       cache_read_price_credits  = 17900000,
       cache_write_price_credits = 0,
       summary                   = 'Default alias for requests that name no model. Full tool-calling parity on the paid quality tier.',
       updated_at                = now()
 WHERE alias_id = 'hive-default'
   AND (input_price_credits <> 89460000 OR output_price_credits <> 178920000
        OR cache_read_price_credits <> 17900000 OR cache_write_price_credits <> 0);

UPDATE public.alias_route_policies
   SET fallback_order = '["route-deepseek-v4-flash-default"]'::jsonb
 WHERE alias_id = 'hive-default'
   AND fallback_order <> '["route-deepseek-v4-flash-default"]'::jsonb;

-- ─── 7. Retire the two dots free routes these aliases leave behind ──────────

UPDATE public.provider_routes
   SET health_state = 'disabled'
 WHERE route_id IN ('route-free-auto', 'route-free-default')
   AND health_state <> 'disabled';

COMMIT;
