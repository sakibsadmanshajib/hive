-- =============================================================================
-- Issue #1695: charge web_search and web_fetch tool calls against Hive credits.
--
-- WHY
--   apps/edge-api/internal/webtools served both tools with no settlement path
--   at all: no hold, no charge, no usage row. They cost real money (a SearXNG
--   query on our own hosting for search, and an embedding burst on a large page
--   for fetch) and cost the customer nothing. This migration puts the price in
--   the catalog so the handler can read it, rather than carrying a literal in
--   Go, which is the antipattern issues #627 and #617 already moved this repo
--   away from.
--
-- UNIT: calls
--   Neither tool meters tokens, characters or seconds. A search bills per
--   QUERY upstream (results are free and the model chooses how many to ask
--   for), and a fetch's byte count and its embedded character count diverge by
--   orders of magnitude, so a per-byte price would make a small exfiltration
--   fetch the cheapest call on the surface and a legitimate research fetch the
--   most expensive. Per call is also the unit the surface already meters:
--   SearchBudgetPerTurn 2, FetchBudgetPerTurn 3, TenantCallsPerMinute 30.
--
--   'calls' is added to the existing model_aliases_price_unit_allowed CHECK
--   (20260801_13_alias_price_unit.sql), which already carries tokens,
--   characters and seconds. Like every other non-token unit it is quoted PER
--   MILLION units and holds the price in output_price_credits with
--   input_price_credits at 0, which model_aliases_single_unit_price enforces.
--
-- PRICES, decided by the owner on 2026-09-02 from live market research
-- (issue #1695 comment; full citations in the vault at
-- hive/spec-2026-09-02-web-tool-pricing.md, all rates read 2026-09-02)
--
--   web_search   0.0001 USD per call   =   0.10 USD per 1,000 calls
--   web_fetch    0.0002 USD per call   =   0.20 USD per 1,000 calls
--
--   Market anchors: Anthropic web search 10.00 USD per 1,000 searches, OpenAI
--   10.00 on reasoning models, Google Gemini grounding 14.00 per 1,000
--   requests, Microsoft Grounding with Bing 14.00 per 1,000 transactions. No
--   assistant vendor bills per fetch at all; the infrastructure vendors do
--   (Firecrawl 0.0008 per page, Exa 0.0010, Tavily 0.0016).
--
--   Hive prices search roughly one hundred times below the assistant vendors.
--   That is deliberate and not an error: these are COST PASS THROUGH prices
--   with no burn margin, because margin is taken at purchase (issue #1693), and
--   search here is one HTTP call to self hosted SearXNG rather than a metered
--   third party API. COROLLARY, register it: if Hive ever moves off self
--   hosted SearXNG onto a paid search API, this price must rise by about two
--   orders of magnitude, and these rows are where that change lands.
--
-- FORMULA
--   credits_per_million_calls = usd_per_call * CREDITS_PER_USD * 1000000
--     CREDITS_PER_USD = 1000000000   (D-046, 1 USD = 1e9 credits)
--     NO margin multiplier. The 1.4 inference multiplier earlier migrations
--     applied is being removed (issues #1692, #1693); applying it here would
--     be a second margin on top of the one taken at purchase.
--
-- WORKED DERIVATION
--   hive-web-search  0.0001 * 1000000000 =    100000 credits per call
--                    100000 * 1000000    = 100000000000 per million calls
--   hive-web-fetch   0.0002 * 1000000000 =    200000 credits per call
--                    200000 * 1000000    = 200000000000 per million calls
--
--   Sanity, at the charge formula edge-api applies (quantity * credits per
--   million / 1000000, round half up, metering.ChargeCredits):
--     1 search call -> 100000 credits (0.0001 USD)
--     1 fetch call  -> 200000 credits (0.0002 USD)
--     30 calls, the per tenant per minute ceiling, all fetches -> 6000000
--     credits (0.006 USD)
--
-- VISIBILITY: internal
--   These aliases are a price carrier, not a model. visibility 'internal'
--   keeps them out of ListPublicAliases (public/preview only) and out of every
--   tenant entitlement verdict (catalog.AliasVisibleToTenant returns false for
--   anything that is not public, preview or an explicitly granted restricted
--   row), so they can never appear in GET /v1/models or in a chat model
--   picker. edge-api reads them through the alias-price lookup, which is
--   deliberately not the entitlement-filtered catalog snapshot.
--
-- RE-RUNNABILITY
--   The CHECK is dropped and recreated by name, and both rows are inserted
--   with ON CONFLICT DO UPDATE assigning constants. Re-application is a no-op.
-- =============================================================================

-- 1. Allow the new unit. Dropped and recreated rather than added only when
--    absent: the constraint already exists on every deployment, so an
--    IF NOT EXISTS guard would leave the old three-value CHECK in place and
--    the INSERTs below would fail.
ALTER TABLE public.model_aliases
    DROP CONSTRAINT IF EXISTS model_aliases_price_unit_allowed;

ALTER TABLE public.model_aliases
    ADD CONSTRAINT model_aliases_price_unit_allowed
    CHECK (price_unit IN ('tokens', 'characters', 'seconds', 'calls'));

COMMENT ON COLUMN public.model_aliases.price_unit IS
    'Unit the price columns are quoted in, per million: tokens (text), characters (text-to-speech), seconds (transcription), calls (per-call tools such as web_search and web_fetch). edge-api refuses a request whose alias unit does not match what the endpoint meters (issues #627, #1695). For any non-token unit the price is output_price_credits and input_price_credits must be 0.';

-- 2. The two price carriers.
INSERT INTO public.model_aliases (
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
    cache_write_price_credits,
    price_unit,
    pricing_mode
) VALUES
    (
        'hive-web-search',
        'hive',
        'Hive Web Search',
        'Per-call price carrier for the web_search tool. Not a model and never chat-selectable.',
        'internal',
        'hidden',
        '["web-tool","search"]'::jsonb,
        0,
        100000000000,
        NULL,
        NULL,
        'calls',
        'fixed'
    ),
    (
        'hive-web-fetch',
        'hive',
        'Hive Web Fetch',
        'Per-call price carrier for the web_fetch tool. Not a model and never chat-selectable.',
        'internal',
        'hidden',
        '["web-tool","fetch"]'::jsonb,
        0,
        200000000000,
        NULL,
        NULL,
        'calls',
        'fixed'
    )
ON CONFLICT (alias_id) DO UPDATE
   SET owned_by                  = EXCLUDED.owned_by,
       display_name              = EXCLUDED.display_name,
       summary                   = EXCLUDED.summary,
       visibility                = EXCLUDED.visibility,
       lifecycle                 = EXCLUDED.lifecycle,
       capability_badges         = EXCLUDED.capability_badges,
       input_price_credits       = EXCLUDED.input_price_credits,
       output_price_credits      = EXCLUDED.output_price_credits,
       cache_read_price_credits  = EXCLUDED.cache_read_price_credits,
       cache_write_price_credits = EXCLUDED.cache_write_price_credits,
       price_unit                = EXCLUDED.price_unit,
       pricing_mode              = EXCLUDED.pricing_mode,
       updated_at                = now();
