-- openrouter-auto: an alias billed at ACTUAL upstream cost, not a catalog price.
--
-- Wrapped in a single transaction because this file drops NOT NULL from two
-- columns several statements before the CHECK that re-imposes the invariant.
-- Under `supabase db push` that window never exists, but this repo also applies
-- migrations by hand with psql, which autocommits per statement, and an abort
-- in between would leave the table accepting a `fixed` row with NULL prices,
-- which is the exact state this design exists to prevent.
--
-- lock_timeout because model_aliases is read once per route selection, so once
-- per request. Each statement here is microseconds against a single-digit-row
-- table, but an ACCESS EXCLUSIVE request that queues behind one long-running
-- reader blocks every reader that arrives after it. Failing fast is better than
-- stalling the gateway.
begin;
set local lock_timeout = '5s';
--
-- `openrouter/auto-beta` is a router. It picks a different upstream model per
-- request, so OpenRouter's models endpoint reports `prompt: -1, completion: -1`
-- for it: the price is variable by construction. Every other alias in this
-- catalog carries one fixed input/output price pair and both the credit hold
-- and the settled charge are derived from it, so any single number written into
-- those columns for this alias would be wrong on nearly every request. The
-- owner chose to bill the real per-request cost rather than a fixed ceiling.
--
-- Why the price columns go NULL rather than 0
-- ───────────────────────────────────────────
-- 0 is not a free slot. It is already meaningful: routing.Service refuses an
-- alias whose input and output prices are both zero (ErrAliasNotPriced, issue
-- #617), and metering's RouteInfo.HasCostBasis reads the same condition. Worse,
-- a price that is silently 0 is exactly the shape that billed nothing for three
-- days in July. NULL is chosen because the Go readers scan these columns into
-- NON-POINTER int64, so a NULL is a hard pgx scan error: any reader that was
-- not taught about variable pricing fails loudly and closed instead of quietly
-- charging zero. That property is the point of the choice, not a side effect.
--
-- The shape CHECK below is what stops the two halves drifting apart, so a row
-- cannot claim a fixed price and carry none, or claim variable pricing and
-- carry a stale number nobody reads.
--
-- Related decisions: D-031 (credits per million, math/big, round half up),
-- D-032 (one model one price at the alias level -- this adds one explicitly
-- discriminated exception rather than revoking it), D-034 (money path fails
-- closed).

-- ─── 1. Pricing mode discriminator ──────────────────────────────────────────

alter table public.model_aliases
    add column if not exists pricing_mode text not null default 'fixed';

alter table public.model_aliases
    add column if not exists reservation_estimate_credits bigint;

comment on column public.model_aliases.pricing_mode is
    'How a request against this alias is priced. fixed: input_price_credits / output_price_credits are authoritative, the historical and default behaviour. upstream_actual: the price is variable per request and the charge is derived from the cost the upstream reports for that generation, so the price columns are NULL and must not be read.';

comment on column public.model_aliases.reservation_estimate_credits is
    'Credit hold taken up front for an upstream_actual alias, where no catalog price exists to derive one from. NULL for fixed aliases, which size their hold from the endpoint default.';

-- Idempotent: a re-applied migration must not fail on an existing constraint.
alter table public.model_aliases
    drop constraint if exists model_aliases_pricing_mode_allowed;

alter table public.model_aliases
    add constraint model_aliases_pricing_mode_allowed
    check (pricing_mode in ('fixed', 'upstream_actual'));

-- ─── 2. Prices become nullable, but only for the variable mode ──────────────

alter table public.model_aliases
    alter column input_price_credits drop not null;

alter table public.model_aliases
    alter column output_price_credits drop not null;

-- The NOT NULL constraints just dropped are re-imposed by this CHECK for every
-- row that is still priced the ordinary way, so relaxing the column does NOT
-- relax the invariant for the 'fixed' aliases. It only carves out the one mode
-- that genuinely has no price to record.
alter table public.model_aliases
    drop constraint if exists model_aliases_pricing_mode_shape;

alter table public.model_aliases
    add constraint model_aliases_pricing_mode_shape
    check (
        (
            pricing_mode = 'fixed'
            and input_price_credits is not null
            and output_price_credits is not null
            and reservation_estimate_credits is null
        )
        or (
            pricing_mode = 'upstream_actual'
            and input_price_credits is null
            and output_price_credits is null
            -- The cache columns are covered too. They are serialized onto the
            -- catalog surface, so a stale number left in either would be shown
            -- as this alias's price even though nothing charges from it.
            and cache_read_price_credits is null
            and cache_write_price_credits is null
            and reservation_estimate_credits is not null
            and reservation_estimate_credits > 0
        )
    );

-- ─── 3. The alias ───────────────────────────────────────────────────────────
--
-- Reservation hold: 200000 credits, i.e. 2.00 USD-equivalent at
-- payments.CreditsPerUSD = 100000. Derivation, so a later reader can argue with
-- the number instead of guessing at it:
--
--   provider.max_price bounds the RATE and nothing else, so it alone does not
--   bound one request. Both sides of the request are therefore bounded in Go
--   before dispatch (EnforceVariablePriceBounds): 256 KiB of body, which is a
--   rigorous upper bound of 262144 prompt tokens because a token can never be
--   fewer than one UTF-8 byte, and 16384 completion tokens pinned onto the
--   outbound request where a client cannot raise it.
--
--   At the configured 3.00 / 15.00 USD per million ceiling that worst bounded
--   request is 144507 credits after the 1.4 margin, against this 200000 hold,
--   leaving about 55000 credits of headroom. Those figures are not restated
--   here as prose that can drift: TestTheHoldProvablyCoversTheWorstBoundedRequest
--   recomputes them from this file, the LiteLLM config and the Go constants, and
--   fails if the hold stops covering the bound. The hold is also a real
--   solvency gate, because control-plane refuses a reservation that exceeds
--   available credits (enforcePolicy, PolicyModeStrict).
--
--   The hold is NOT a price and is never charged as one. Settlement releases
--   the whole hold and posts the real cost as the charge.
--
-- display_name is the owner's verbatim string. alias_id is a slug because
-- alias_id is what a client sends in the OpenAI-compatible `model` field, where
-- spaces and parentheses are hostile.

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
    price_unit,
    pricing_mode,
    reservation_estimate_credits
) values (
    'openrouter-auto',
    'hive',
    'Openrouter Auto (Task Aware)',
    'Task-aware routing that selects a model per request. Billed at actual usage.',
    -- `internal`, NOT `public`, and this is deliberate on two counts.
    --
    -- Deployment ordering: deploy-demo-box.yml has `deploy: needs: migrate`, so
    -- this row lands while the PREVIOUS control-plane binary is still serving,
    -- and that binary scans the price columns into non-pointer int64. The
    -- readers are list queries, so one NULL-priced row would fail the scan for
    -- the whole list and take /v1/models down for every model until the new
    -- image arrived. ListPublicAliases filters `visibility IN ('public',
    -- 'preview')`, so an `internal` row is simply not selected and the window
    -- closes.
    --
    -- Correctness: this alias cannot bill correctly until the LiteLLM pin moves
    -- off v1.77.7-stable, which destroys the reported cost on the streaming
    -- path. Until then every streamed request would take the fail-closed branch
    -- and be charged the hold. Flipping this to 'public' is a one-line
    -- follow-up migration, to be applied only after the pin bump has landed and
    -- been validated on the box.
    'internal',
    'stable',
    '["chat","responses","tools","reasoning","vision","task-aware"]'::jsonb,
    null,
    null,
    'tokens',
    'upstream_actual',
    200000
)
on conflict (alias_id) do update set
    -- `do nothing` is the right instinct for idempotency and the wrong one for
    -- the money columns. If any concurrent same-day migration seeded
    -- `openrouter-auto` first, in the default `fixed` mode with some price,
    -- `do nothing` would leave a variable-cost router billed at a fixed price,
    -- with the route row, the LiteLLM entry and the settlement branch all still
    -- treating it as variable, and no error anywhere. These four columns are
    -- the ones a wrong value silently misprices, so they converge on re-run
    -- instead. Display name and summary are deliberately NOT updated: those may
    -- have been retuned since and this migration has no business reverting
    -- them, which is the reason the other seed migrations use `do nothing`.
    pricing_mode                 = excluded.pricing_mode,
    input_price_credits          = excluded.input_price_credits,
    output_price_credits         = excluded.output_price_credits,
    reservation_estimate_credits = excluded.reservation_estimate_credits;

-- ─── 4. Route ───────────────────────────────────────────────────────────────
--
-- litellm_model_name is deliberately NOT 'route-openrouter-auto': that name is
-- already taken in deploy/litellm/config.yaml by the vision route for the
-- hive-auto alias, which resolves to a completely different model.
--
-- provider_model is the literal slug, not an env var. Every other OpenRouter
-- route in config.yaml resolves its model from `os.environ/..._MODEL`, and
-- issue #689 is what that costs: the catalog priced one model while the
-- deployment silently called another. For an alias billed at actual cost the
-- slug IS the product decision, and an env var would let a deployment repoint a
-- variable-cost alias at some other model with nothing to notice.

insert into public.provider_routes (
    route_id,
    alias_id,
    provider,
    provider_model,
    litellm_model_name,
    price_class,
    health_state,
    priority
) values (
    'route-openrouter-auto-beta',
    'openrouter-auto',
    'openrouter',
    -- Doubled prefix on purpose, matching every other OpenRouter row here
    -- (compare 'openrouter/openai/gpt-4o-mini'): LiteLLM strips the leading
    -- 'openrouter/' as its provider selector and forwards 'openrouter/auto-beta'
    -- upstream. This column is compared verbatim against LiteLLM's resolved
    -- litellm_params.model by the deploy-box price assertion, so it has to be
    -- the LiteLLM string rather than the bare OpenRouter slug.
    'openrouter/openrouter/auto-beta',
    'route-openrouter-auto-beta',
    'premium',
    'healthy',
    10
)
on conflict (route_id) do nothing;

-- Capabilities as reported by OpenRouter for openrouter/auto-beta, verified
-- against https://openrouter.ai/api/v1/models on 2026-08-22: 2000000 context,
-- text/image/audio/file/video input, text and image output, and support for
-- tools, tool_choice, response_format, structured_outputs, reasoning,
-- reasoning_effort and web_search_options.
--
-- supports_embeddings is false: the auto router is a chat router and OpenRouter
-- exposes no embedding models through it. Cache read/write are left false
-- because caching behaviour depends on whichever model the router picks, and
-- claiming a capability the request may not get is worse than not claiming it.
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
) values (
    'route-openrouter-auto-beta',
    true,
    true,
    true,
    false,
    true,
    true,
    false,
    false,
    true
)
on conflict (route_id) do nothing;

-- Pinned to the single route. There is deliberately no fallback: a fallback to
-- a fixed-price route would settle an upstream_actual alias against a provider
-- that never reports a per-request cost, which is precisely the free-serve
-- shape this alias must not have.
insert into public.alias_route_policies (
    alias_id,
    policy_mode,
    allow_price_class_widening,
    fallback_order
) values (
    'openrouter-auto',
    'pinned',
    false,
    '["route-openrouter-auto-beta"]'::jsonb
)
on conflict (alias_id) do nothing;

-- allowed_group_names defaults to ["default"], so a default-tier API key never
-- sees an alias unless it is in the `default` group. Same gap the voice
-- migration (20260717_02) documented.
insert into public.model_policy_group_members (group_name, alias_id)
values ('default', 'openrouter-auto')
on conflict (group_name, alias_id) do nothing;

commit;
