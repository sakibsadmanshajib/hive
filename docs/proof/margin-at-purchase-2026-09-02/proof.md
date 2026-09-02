# Proof: margin moves from burn to purchase (issues #1692 and #1693)

Captured 2026-09-02 on branch feat/1692-margin-at-purchase.

Substrate: a throwaway Postgres (pgvector/pgvector:pg17) brought up by
scripts/ci-throwaway-db.sh, which runs the real applier
(scripts/apply-migrations.sh) over the whole supabase/migrations chain. 132
migrations executed on the fresh database, then the new file applied on top as
migration 133. Every figure below was read back with psql, not taken from an
application log line.

The demo box was not used and no live provider key was spent: the catalog half
is a database read, and the settlement half is arithmetic pinned by tests that
compare magnitudes against independently recomputed figures.


## 1. Catalog prices, before and after the migration

Read with:

    select alias_id, input_price_credits, output_price_credits,
           cache_read_price_credits, cache_write_price_credits
      from public.model_aliases order by alias_id;

BEFORE (chain applied through 20260902_01_web_tool_call_pricing.sql)

    alias_id           in            out           cache_read   cache_write
    deepseek-v4-flash  89460000      178920000     2982000      89460000
    deepseek-v4-pro    1570800000    4712400000    52360000     1570800000
    hive-auto          (null)        (null)        (null)       (null)
    hive-default       89460000      178920000     2982000      0
    hive-embedding-default 10000     0             (null)       (null)
    hive-fast          105000000     420000000     10000        40000
    hive-free          1000000       4000000       0            0
    hive-free-tools    1000000       4000000       0            0
    hive-medium        210000000     840000000     0            0
    hive-small         105000000     420000000     0            0
    hive-stt           0             43166670000   (null)       (null)
    hive-tts           0             30800000000   (null)       (null)
    hive-web-fetch     0             200000000000  (null)       (null)
    hive-web-search    0             100000000000  (null)       (null)
    openrouter-auto    (null)        (null)        (null)       (null)

AFTER (20260902_02_retire_alias_margin_factor.sql applied)

    alias_id           in            out           cache_read   cache_write
    deepseek-v4-flash  63900000      127800000     2130000      63900000
    deepseek-v4-pro    1122000000    3366000000    37400000     1122000000
    hive-auto          (null)        (null)        (null)       (null)
    hive-default       63900000      127800000     2130000      0
    hive-embedding-default 10000     0             (null)       (null)
    hive-fast          105000000     420000000     10000        40000
    hive-free          1000000       4000000       0            0
    hive-free-tools    1000000       4000000       0            0
    hive-medium        210000000     840000000     0            0
    hive-small         105000000     420000000     0            0
    hive-stt           0             30833333334   (null)       (null)
    hive-tts           0             22000000000   (null)       (null)
    hive-web-fetch     0             200000000000  (null)       (null)
    hive-web-search    0             100000000000  (null)       (null)
    openrouter-auto    (null)        (null)        (null)       (null)

Magnitude check, every repriced figure: before / after = 1.4 exactly.

    89460000   / 63900000   = 1.4
    178920000  / 127800000  = 1.4
    2982000    / 2130000    = 1.4
    1570800000 / 1122000000 = 1.4
    4712400000 / 3366000000 = 1.4
    52360000   / 37400000   = 1.4
    30800000000 / 22000000000 = 1.4

hive-small, hive-medium and hive-fast are unchanged above, deliberately. They
carry a 1.4-derived figure but they left Groq for a free OpenRouter model on
2026-08-23 and were not repriced when they did, so their price is no longer the
list rate of anything. The migration header carries the reasoning; section 5
below records how the first cut of this change got it wrong.

hive-stt is the one row whose ratio is not exactly 1.4 (43166670000 /
30833333334 = 1.39999999...), because the old figure was ceiled at the
pre-rescale unit and the new one is ceiled at the current unit. It is
re-derived from the published rate rather than divided out:
(0.111 / 3600 x 1e6) x 1e9 = 30833333333.33, ceiling 30833333334.

Rows deliberately unchanged, and why, are listed in the migration header:
hive-free and hive-free-tools (owner-set service prices on zero-cost
upstreams), hive-embedding-default (a placeholder predating the margin
convention), the two web-tool aliases (authored yesterday with no margin), and
hive-fast's two stale cache columns (not margin-derived, wrong on another
axis).


## 2. FX fee default

    select column_default from information_schema.columns
     where table_name='fx_snapshots' and column_name='fee_rate';

    '0.025'::text

Was '0.05'. Existing fx_snapshots rows are untouched: a historical row records
the fee that was actually applied to it.


## 3. A credit purchase, written through the production path and read back

An account and an FX snapshot were created through payments.FXService
(admin override standing in for the XE call) and payments.NewPgxRepository, and
two payment intents inserted with amounts from payments.PriceForCredits. Read
back with:

    select rail, credits, amount_usd, amount_local, local_currency,
           metadata->>'purchase_markup_rate' as markup,
           fx_snapshot_id is not null as has_fx
      from public.payment_intents order by rail;

    rail   | credits    | amount_usd | amount_local | local_currency | markup | has_fx
    bkash  | 1000000000 | 106        | 13798        | BDT            | 0.06   | t
    stripe | 1000000000 | 106        | 0            |                | 0.06   | f

    select mid_rate, fee_rate, effective_rate from public.fx_snapshots;

    mid_rate | fee_rate | effective_rate
    127.00   | 0.025    | 130.175000

Reproduced from the stored fields alone, with no constant from the build:

    USD  1000000000 credits / 10000000 credits per cent = 100 cents at the peg
         100 x (1 + 0.06) = 106 cents                        -> amount_usd 106
         no fx_snapshot_id, so no rate was applied at all    -> amount_local 0

    BDT  the same exact 106 cents
         106 x 130.175 = 13798.55 paisa
         truncated toward zero at the paisa boundary         -> amount_local 13798
                                                                (137.98 BDT)
    and the rate itself reproduces from its own row:
         127.00 x (1 + 0.025) = 130.175000                   -> effective_rate

Before this change the same two purchases were 100 cents and, at the same mid
rate under the old 5 percent fee, 133.35 BDT.


## 4. Settlement magnitude, with no multiplier

Pinned by tests rather than by a live burn, because a live burn needs a paid
provider key and would prove less: the assertions below recompute the expected
figure independently and fail on any factor.

    $1.00 of provider cost      -> 1,000,000,000 credits   (was 1,400,000,000)
    $0.0123456 reported cost    ->    12,345,600 credits   (was    17,283,840)
    $0.0004 reported cost       ->       400,000 credits   (was       560,000)
    batch line, $0.0001         ->       100,000 credits   (was       140,000)
    hive-auto hold, empty body  ->   245,910,000 credits   (was   344,274,000)

Every ratio is 1.4. The last row is the authorization hold, which shrank with
the charge because both go through one function.

Guards: TestCreditsForUpstreamCostCarriesNoMultiplier and
TestOneDollarOfProviderCostBurnsOneBillionCredits (edge-api inference),
TestBatchSettlementCarriesNoMultiplier and
TestOneDollarOfBatchCostSettlesAtOneBillionCredits (batch executor).


## 5. Review findings fixed before merge

Five, from this branch's own database and security passes and from the
independent database and money-path review that followed. All fixed and
re-verified:

1. The first cut repriced `hive-small`, `hive-medium` and `hive-fast` at
   Groq's list rate for gpt-oss-20b and gpt-oss-120b. All three left Groq for
   `openrouter/dots-studio/dots-3-note-preview:free` on 2026-08-23
   (20260823_21) and were deliberately not repriced when they did; their Groq
   routes are `health_state = 'disabled'` today. Removing 1.4 from a price with
   no cost basis is not the retirement of an inference margin, it is a 29
   percent price cut on three customer-facing aliases whose upstream costs
   zero, and the DERIVE rows would have documented a derivation against a
   disabled route. They are out of scope now, on the hive-free precedent.
   Caught by `TestHiveFastIsPinnedToOneRouteAtItsUnchangedPrice`, a DB-backed
   guard that runs only in CI, which is exactly what it was written for.

2. The migration's UPDATE guards used `<>` on two nullable cache columns.
   `NULL <> 2130000` evaluates to NULL, an OR chain that evaluates to NULL is
   not true, and the row would have been skipped in silence while keeping its
   old price. Now `IS DISTINCT FROM` throughout.
3. The migration header justified the TTS and STT reprice as display-only,
   citing the clause of D-033 that says `internal/audio` charges flat literals
   and never reads the catalog. That clause was corrected on 2026-09-02, the
   same day, and it is false: issue #627 closed on 2026-08-01, and
   `audio/pricing.go` `creditsForQuantity` bills `route.UnitPriceCredits`,
   which `audio/routing_adapter.go` sets from `model_aliases.output_price_credits`,
   the exact column this migration rewrites. The reprice stands and is correct
   under D-064, because both rates are provider list rates with the margin
   baked in. What changed is the header, which now says plainly that this cuts
   every speech and transcription charge by 28.57 percent at deploy. Verified
   against the code rather than taken on the reviewer's word.

4. `PriceForCredits` recorded `PurchaseMarkupRate` on the returned value
   unconditionally, including on the branch that skips the markup. Latent
   today, because `PurchaseMarkupAppliesToLocalCurrency` is true, and armed the
   moment anyone takes the one-line flip the file itself invites: every
   local-currency intent would then store a 6 percent markup beside an amount
   that never carried one, which is the #1682 shape the field exists to
   prevent. Fixed, and pinned by
   `TestReturnedMarkupRateReproducesTheAmountItIsRecordedBeside`, which never
   mentions the constant: it takes the rate the call reports and requires it to
   reproduce the amount the same call returned.

   Mutation checked in both directions rather than asserted. With the fix and
   the flip taken, the test passes. With the flip taken and the recorded rate
   put back to the constant, it fails on the local-currency case with
   `USDCents = 100, but the recorded markup "0.06" reproduces 106`.

5. `usdCentsToLocalPaisa` accepted any parseable rate, including zero and
   negative ones. It now refuses a non-positive rate rather than pricing a
   purchase at nothing or at a negative amount, neither of which looks like a
   failure once it is a row.

Re-verified: the whole chain re-applied from an empty database (133 of 133
migrations executed), the catalog read back at the figures above, and the
DB-backed routing suite run against that database with ROUTING_TEST_DB_URL set,
which is the suite CI failed on. It passes.


## 6. Test runs

    go test ./apps/edge-api/... -count=1 -short          all packages ok
    go test ./apps/control-plane/... -count=1 -short     all packages ok
    go vet on payments, inference, batchstore, routing   clean

    scripts/apply-migrations.sh --check                  baseline valid
    scripts/apply-migrations.sh                          applied 1 migration
