-- =============================================================================
-- Retire the 1.4 margin factor from every authored alias price, and move the
-- FX fee default to 2.5 percent (issues #1692 and #1693, owner ruling
-- 2026-09-02, D-064 and D-066).
--
-- WHY
--   Margin used to be taken twice on the way to a customer: once as a 1.4
--   multiplier applied by hand when a price was authored into model_aliases,
--   and once more at settlement on the upstream_actual path. It is now taken
--   once, at the point of sale, as a 6 percent markup on the price of credits
--   (D-065, apps/control-plane/internal/payments/purchase_price.go). The two
--   changes deploy together: the removal alone gives inference away, and the
--   markup alone charges the margin twice.
--
--   The Go half of this change removes the runtime 7/5 factor from
--   apps/edge-api/internal/inference/upstream_cost.go and
--   apps/control-plane/internal/batchstore/executor/pricing.go. This file is
--   the data half: every `fixed` price below was authored as
--   list x 1.4 x CREDITS_PER_USD and is re-authored here as
--   list x CREDITS_PER_USD.
--
-- FORMULA
--   credits_per_million = ceil(provider_list_usd_per_million * CREDITS_PER_USD)
--     CREDITS_PER_USD = 1000000000   (D-046; apps/control-plane/internal/payments/types.go)
--     MARGIN          = none. That is the change.
--
--   The provider list rates are unchanged and are NOT re-fetched here. They are
--   the same figures 20260822_02_catalog_alias_restructure.sql recorded against
--   its committed snapshot (testdata/provider_rates_2026-08-22.json), and
--   dividing the stored figure by 1.4 recovers each one exactly. Re-fetching
--   would fold a rate change into a margin change and make it impossible to
--   tell afterwards which of the two moved a price.
--
-- WORKED DERIVATION
--   Machine-checked, by four tests in
--   apps/control-plane/internal/routing/margin_retirement_pricing_test.go that
--   parse the DERIVE rows below:
--
--     TestMarginRetirementPricesCarryNoMarginFactor
--       recomputes every credit figure from the rate beside it in exact
--       math/big rational arithmetic, and separately refuses a figure that is
--       still the rate times 1.4.
--     TestMarginRetirementRatesMatchTheProviderSnapshot
--       cross-checks each rate against the committed provider snapshot, so a
--       rate change cannot be folded into a margin change unnoticed.
--     TestMarginRetirementFiguresLandOnTheirOwnAliasRow
--       asserts the SQL below writes that figure into that column of that
--       alias row, rather than merely containing the digits somewhere.
--     TestMarginRetirementCoversEveryRepricedAlias
--       fails on a price this file writes with no DERIVE row behind it.
--
--   Editing a price without editing its rate turns the first two red.
--
-- DERIVE| alias_id | route_id | provider_model | field | usd_per_million | credits
-- DERIVE| hive-default | route-deepseek-v4-flash-default | openrouter/~deepseek/deepseek-v4-flash-latest | in | 0.0639 | 63900000
-- DERIVE| hive-default | route-deepseek-v4-flash-default | openrouter/~deepseek/deepseek-v4-flash-latest | out | 0.1278 | 127800000
-- DERIVE| hive-default | route-deepseek-v4-flash-default | openrouter/~deepseek/deepseek-v4-flash-latest | cache_read | 0.00213 | 2130000
-- DERIVE| deepseek-v4-flash | route-deepseek-v4-flash | openrouter/~deepseek/deepseek-v4-flash-latest | in | 0.0639 | 63900000
-- DERIVE| deepseek-v4-flash | route-deepseek-v4-flash | openrouter/~deepseek/deepseek-v4-flash-latest | out | 0.1278 | 127800000
-- DERIVE| deepseek-v4-flash | route-deepseek-v4-flash | openrouter/~deepseek/deepseek-v4-flash-latest | cache_read | 0.00213 | 2130000
-- DERIVE| deepseek-v4-flash | route-deepseek-v4-flash | openrouter/~deepseek/deepseek-v4-flash-latest | cache_write | 0.0639 | 63900000
-- DERIVE| deepseek-v4-pro | route-deepseek-v4-pro | openrouter/deepseek/deepseek-v4-pro-0813 | in | 1.122 | 1122000000
-- DERIVE| deepseek-v4-pro | route-deepseek-v4-pro | openrouter/deepseek/deepseek-v4-pro-0813 | out | 3.366 | 3366000000
-- DERIVE| deepseek-v4-pro | route-deepseek-v4-pro | openrouter/deepseek/deepseek-v4-pro-0813 | cache_read | 0.0374 | 37400000
-- DERIVE| deepseek-v4-pro | route-deepseek-v4-pro | openrouter/deepseek/deepseek-v4-pro-0813 | cache_write | 1.122 | 1122000000
--
--   Longhand, for a reader rather than the parser. Every product below is
--   exact; nothing here needs the ceiling.
--     hive-default      in  0.0639  x 1e9 =    63900000     (was    89460000)
--     hive-default      out 0.1278  x 1e9 =   127800000     (was   178920000)
--     hive-default      cr  0.00213 x 1e9 =     2130000     (was     2982000)
--     deepseek-v4-flash in  0.0639  x 1e9 =    63900000     (was    89460000)
--     deepseek-v4-flash out 0.1278  x 1e9 =   127800000     (was   178920000)
--     deepseek-v4-flash cr  0.00213 x 1e9 =     2130000     (was     2982000)
--     deepseek-v4-flash cw  0.0639  x 1e9 =    63900000     (was    89460000)
--     deepseek-v4-pro   in  1.122   x 1e9 =  1122000000     (was  1570800000)
--     deepseek-v4-pro   out 3.366   x 1e9 =  3366000000     (was  4712400000)
--     deepseek-v4-pro   cr  0.0374  x 1e9 =    37400000     (was    52360000)
--     deepseek-v4-pro   cw  1.122   x 1e9 =  1122000000     (was  1570800000)
--
--   The cache-read rate is not an independently published figure. Both DeepSeek
--   aliases price cache reads at one thirtieth of their input rate, which is the
--   published ratio 20260825_02_deepseek_cache_read_price_correction.sql
--   established, and 0.0639/30 = 0.00213 and 1.122/30 = 0.0374 both divide
--   without remainder. Cache WRITE is priced at the input rate, which is what
--   20260825_01_deepseek_cache_write_price.sql established. Both relations are
--   preserved exactly by this change: removing a common factor from a ratio
--   leaves the ratio alone.
--
-- THE TWO NON-TOKEN ALIASES
--   hive-tts and hive-stt carry the same baked-in 1.4 and are re-authored with
--   it removed, so the catalog stops displaying a price 40 percent above the
--   provider rate. They are NOT in the DERIVE table because their rates are not
--   in the provider snapshot, which covers text models only, and inventing a
--   snapshot entry to satisfy a parser would be worse than stating the
--   derivation here:
--
--     hive-tts  characters  22.00 x 1e9                    = 22000000000
--                                                            (was 30800000000)
--     hive-stt  seconds     (0.111 / 3600 x 1e6) x 1e9     = 30833333333.33,
--                                                            ceiling
--                                                            30833333334
--                                                            (was 43166670000)
--
--   Both rates are the ones 20260801_13_alias_price_unit.sql recorded. Per
--   D-033 these two prices are display figures: internal/audio charges flat
--   literals and never reads the catalog (issue #627), so this moves what a
--   customer is quoted and not yet what they are charged. That is a reason to
--   fix #627, not a reason to leave a retired margin on a published price.
--
-- WHAT THIS FILE DELIBERATELY DOES NOT TOUCH, and why. Stated in full because
-- "sweep for any other place a margin factor is applied" is half of issue
-- #1692, and a reader has to be able to check the sweep rather than trust it.
--
--   hive-free, hive-free-tools   Owner-set service prices ($0.001 / $0.004),
--                                not cost-derived. Every upstream in the free
--                                pool costs zero, so there is no list price to
--                                remove a margin from, and the halving relation
--                                20260823_20 established is machine-checked by
--                                free_alias_pricing_test.go. Dividing these by
--                                1.4 would break that guard and answer a
--                                question nobody asked.
--   hive-small, hive-medium, hive-fast
--                                These three carry a 1.4-derived figure and are
--                                still NOT repriced, which is the one call in
--                                this file that needed making rather than
--                                following. All three left Groq for a free
--                                OpenRouter model on 2026-08-23
--                                (20260823_21_groq_text_routes_to_openrouter_free.sql)
--                                and were deliberately not repriced when they
--                                did: their Groq routes are health_state
--                                'disabled' today and every one of them serves
--                                openrouter/dots-studio/dots-3-note-preview:free.
--                                So their price is no longer the list rate of
--                                anything. Dividing it by 1.4 would not remove
--                                an inference margin, because there is no
--                                inference cost under it to take a margin on;
--                                it would cut the price of three customer-facing
--                                aliases by 29 percent on an upstream that costs
--                                zero, which is a product decision nobody made.
--                                The hive-free precedent governs instead: a price
--                                with no cost basis is an owner-set service
--                                price and is left alone. Repricing them at
--                                Groq's list rate would also have documented a
--                                derivation against a route that is disabled,
--                                which is a claim this file cannot support.
--                                TestHiveFastIsPinnedToOneRouteAtItsUnchangedPrice
--                                is the DB-level guard that says so, and it is
--                                the reason this call was made deliberately
--                                rather than by omission.
--   hive-embedding-default       10000 credits per million is a placeholder
--                                that predates the margin convention entirely
--                                and is roughly four orders of magnitude below
--                                any real embedding rate. Repricing it is
--                                issue #627's neighbour, not this change.
--   hive-web-search, hive-web-fetch
--                                Authored yesterday by
--                                20260902_01_web_tool_call_pricing.sql, whose
--                                header says in terms that it applies no margin
--                                multiplier. Already correct.
--   hive-fast cache_read (10000) and cache_write (40000)
--                                Stale seed values from 20260331_01 that
--                                20260822_02 explicitly left alone. They are
--                                not 1.4-derived, they are wrong on a different
--                                axis (a Groq gpt-oss route declares no cache
--                                support at all, so the correct value is zero),
--                                and correcting them here would mix a margin
--                                change with an unrelated one on a deprecated
--                                alias.
--   reservation_estimate_credits Not a price. It is the ceiling a hold may
--                                take, and the sized holds it caps already
--                                shrank by the same 1.4 in the Go change,
--                                because the hold and the charge go through
--                                one function. A cap that is now more generous
--                                relative to the charge is not a mispricing.
--   fx_snapshots rows already written
--                                Historical rates record the fee that was
--                                actually applied at the time. Rewriting them
--                                would destroy exactly the reproducibility
--                                D-072 was learned for. Only the column DEFAULT
--                                moves, for rows written from here on.
--
-- SHAPE
--   Every UPDATE is guarded on the values it writes, so re-running this file is
--   a no-op rather than a second reprice, and applying it to a database that
--   somehow already holds the new figures records the migration without
--   touching a row.
--
--   The guards use IS DISTINCT FROM rather than <>, which matters on the two
--   cache columns: they are nullable, `NULL <> 2130000` evaluates to NULL, and
--   an OR chain that evaluates to NULL is not true, so a row holding a NULL
--   cache price would be skipped in silence and keep whatever it had. The
--   guarded columns are non-null on every row this file touches today, which is
--   exactly the sort of fact that stops being true without anyone noticing.
--
--   The guards compare against the values being WRITTEN, not against the values
--   expected to be there. That is deliberate and it is the convergence property
--   D-072 was learned for: this file reaches the same end state whatever it
--   finds, rather than recognising one particular starting state and silently
--   doing nothing on any other.
-- =============================================================================

begin;

-- ─── 1. DeepSeek aliases, and hive-default which serves the flash model ─────

update public.model_aliases
   set input_price_credits      = 63900000,
       output_price_credits     = 127800000,
       cache_read_price_credits = 2130000,
       updated_at               = now()
 where alias_id = 'hive-default'
   and (input_price_credits is distinct from 63900000
        or output_price_credits is distinct from 127800000
        or cache_read_price_credits is distinct from 2130000);

update public.model_aliases
   set input_price_credits       = 63900000,
       output_price_credits      = 127800000,
       cache_read_price_credits  = 2130000,
       cache_write_price_credits = 63900000,
       updated_at                = now()
 where alias_id = 'deepseek-v4-flash'
   and (input_price_credits is distinct from 63900000
        or output_price_credits is distinct from 127800000
        or cache_read_price_credits is distinct from 2130000
        or cache_write_price_credits is distinct from 63900000);

update public.model_aliases
   set input_price_credits       = 1122000000,
       output_price_credits      = 3366000000,
       cache_read_price_credits  = 37400000,
       cache_write_price_credits = 1122000000,
       updated_at                = now()
 where alias_id = 'deepseek-v4-pro'
   and (input_price_credits is distinct from 1122000000
        or output_price_credits is distinct from 3366000000
        or cache_read_price_credits is distinct from 37400000
        or cache_write_price_credits is distinct from 1122000000);

-- ─── 2. The two non-token aliases (display prices, per D-033) ───────────────

update public.model_aliases
   set output_price_credits = 22000000000,
       updated_at           = now()
 where alias_id = 'hive-tts'
   and output_price_credits is distinct from 22000000000;

update public.model_aliases
   set output_price_credits = 30833333334,
       updated_at           = now()
 where alias_id = 'hive-stt'
   and output_price_credits is distinct from 30833333334;

-- ─── 3. The FX fee default drops to 2.5 percent (D-066) ─────────────────────
--
-- The applied fee lives in Go (payments.FXFeeRate, read by fx.go), and this
-- default is what a row written by anything else would carry. They are moved
-- together so the two cannot disagree, which is the disagreement issue #1682
-- was filed about.

alter table public.fx_snapshots
  alter column fee_rate set default '0.025';

comment on column public.fx_snapshots.fee_rate is
  'Markup folded into the mid rate to produce effective_rate, as a decimal string. 0.025 since the 2026-09-02 ruling (D-066), 0.05 before it. Never shown to a customer as a line item: the customer sees one rate and one local price. Historical rows keep the fee that was actually applied to them.';

comment on column public.model_aliases.input_price_credits is
  'Credits per million prompt tokens (D-031). Authored as provider_list_usd_per_million x CREDITS_PER_USD with NO margin multiplier since the 2026-09-02 ruling (D-064): margin is taken once, at purchase, as a markup on the price of credits (D-065).';

commit;
