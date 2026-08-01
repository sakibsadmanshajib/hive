-- D-032: per-route pricing (bigint credits per million tokens), replacing
-- alias-level model_aliases.input_price_credits/output_price_credits as
-- billing's source of truth. Issue #617 found the seeded model_aliases
-- values were placeholders 333x-7,375x too low against real provider rates
-- (non-uniform ratio, ruling out a single scale-factor bug), and one alias
-- (hive-fast) routes to providers whose real cost differs by an order of
-- magnitude, so no single alias-wide price could ever be correct for both
-- routes at once. model_aliases columns are left in place unchanged:
-- catalog.Service still reads them for the customer-facing /v1/models
-- listing, which is out of scope for this migration.
--
-- Margin: 1.4x provider list cost (owner ruling, D-032), applied uniformly,
-- no per-row exceptions. All four repriced routes land on whole credit
-- values with no rounding needed. Live prices fetched 2026-07-31:
--   OpenRouter: GET https://openrouter.ai/api/v1/models (public, no auth),
--   pricing.prompt / pricing.completion, USD per token.
--   Groq: https://groq.com/pricing, USD per million tokens.
-- CreditsPerUSD = 100,000 (apps/control-plane/internal/payments/types.go:37-38).
--
-- OUT OF SCOPE for this repricing (filed as a separate follow-up to #617):
-- hive-stt, hive-tts, and hive-embedding-default's three routes.
--   - apps/edge-api/internal/audio/handler.go bills TTS/STT with hardcoded
--     flat credit literals (1000 / 500 per request) and never reads this
--     column at all, so repricing these rows would change only the console
--     catalog display, not any actual charge -- a different, larger fix.
--   - hive-embedding-default's two OpenRouter-routed candidates have no
--     discoverable live price: OpenRouter's public /v1/models endpoint does
--     not cover the generic openai/ adapter path embeddings ride (see
--     deploy/litellm/config.yaml's comments on this).
-- Their existing model_aliases values are carried forward unchanged below so
-- the NOT NULL constraint is satisfiable without inventing a number for a
-- row this ruling explicitly excluded.
--
-- Re-verified live 2026-08-01 against the same two sources: OpenRouter's
-- gpt-4.1-mini, gpt-4o-mini, and llama-3.1-8b-instruct prices, and Groq's
-- llama-3.3-70b-versatile price, are unchanged from the 2026-07-31 figures
-- below.
--
-- pricing_unit (D-032 as amended, CodeRabbit thread 2 on PR #632): every
-- price row carries an explicit unit so a row can be rejected on a unit
-- mismatch instead of silently mispriced. Every route this migration prices
-- is a chat-completions route, hence 'tokens' for all of them, including the
-- carried-forward STT/TTS/embedding rows above -- their real unit (D-033:
-- characters, audio duration) is a separate migration for when #627 actually
-- wires per-request pricing for those handlers, not invented here.

alter table public.provider_routes
    add column input_price_credits bigint,
    add column output_price_credits bigint,
    add column pricing_unit text;

-- hive-auto: OpenRouter openai/gpt-4.1-mini, $0.40 / $1.60 per M tokens ->
-- 40,000 / 160,000 credits per M at cost, x1.4 margin.
update public.provider_routes set
    input_price_credits = 56000,
    output_price_credits = 224000
where route_id = 'route-openrouter-auto';

-- hive-default: OpenRouter openai/gpt-4o-mini, $0.15 / $0.60 per M -> 15,000
-- / 60,000 at cost, x1.4.
update public.provider_routes set
    input_price_credits = 21000,
    output_price_credits = 84000
where route_id = 'route-openrouter-default';

-- hive-fast, OpenRouter route: meta-llama/3.1-8b-instruct, $0.05 / $0.08 per
-- M -> 5,000 / 8,000 at cost, x1.4. The cheaper of hive-fast's two routes --
-- see the Groq route immediately below for why one alias-wide price could
-- never cover both (this pair is D-032's motivating case).
update public.provider_routes set
    input_price_credits = 7000,
    output_price_credits = 11200
where route_id = 'route-openrouter-fast-fallback';

-- hive-fast, Groq route: llama-3.3-70b-versatile, $0.59 / $0.79 per M ->
-- 59,000 / 79,000 at cost, x1.4. Roughly 8x-12x the OpenRouter route's
-- per-token cost under the same alias.
update public.provider_routes set
    input_price_credits = 82600,
    output_price_credits = 110600
where route_id = 'route-groq-fast';

-- Out of scope (see header comment): carried forward unchanged from
-- model_aliases so no route is left without a price.
update public.provider_routes r set
    input_price_credits = m.input_price_credits,
    output_price_credits = m.output_price_credits
from public.model_aliases m
where r.alias_id = m.alias_id
  and r.route_id in (
    'route-groq-stt',
    'route-groq-tts',
    'route-nvidia-embedding',
    'route-openrouter-embedding',
    'route-openrouter-embedding-fallback'
  );

-- Every route this migration prices meters tokens.
update public.provider_routes set pricing_unit = 'tokens';

-- Fail-closed (D-032): every route seeded to date now carries a real price
-- and a real unit, so this makes "a route with no price" and "a route with
-- no unit" both unreachable for current data rather than merely
-- discouraged. A future route inserted without a price or unit fails at
-- insert time instead of silently billing as free or under the wrong unit.
-- pricing_unit is constrained to 'tokens' only: D-033 widens this check when
-- a non-token modality actually gets real per-request pricing, not before.
alter table public.provider_routes
    alter column input_price_credits set not null,
    alter column output_price_credits set not null,
    alter column pricing_unit set not null,
    add constraint provider_routes_pricing_unit_check check (pricing_unit = 'tokens');
