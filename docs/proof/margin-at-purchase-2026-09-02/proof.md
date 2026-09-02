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
    hive-fast          75000000      300000000     10000        40000
    hive-free          1000000       4000000       0            0
    hive-free-tools    1000000       4000000       0            0
    hive-medium        150000000     600000000     0            0
    hive-small         75000000      300000000     0            0
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
    105000000  / 75000000   = 1.4
    420000000  / 300000000  = 1.4
    210000000  / 150000000  = 1.4
    840000000  / 600000000  = 1.4
    30800000000 / 22000000000 = 1.4

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


## 5. Review findings fixed before the first review round closed

Two defects found by the database and security passes over this branch's own
diff, both fixed in the second commit and re-verified on a fresh throwaway:

1. The migration's UPDATE guards used `<>` on two nullable cache columns.
   `NULL <> 2130000` evaluates to NULL, an OR chain that evaluates to NULL is
   not true, and the row would have been skipped in silence while keeping its
   old price. Now `IS DISTINCT FROM` throughout.
2. `usdCentsToLocalPaisa` accepted any parseable rate, including zero and
   negative ones. It now refuses a non-positive rate rather than pricing a
   purchase at nothing or at a negative amount, neither of which looks like a
   failure once it is a row.

Re-verified: the whole chain re-applied from an empty database (133 of 133
migrations executed) and the catalog read back at the same figures.


## 6. Test runs

    go test ./apps/edge-api/... -count=1 -short          all packages ok
    go test ./apps/control-plane/... -count=1 -short     all packages ok
    go vet on payments, inference, batchstore, routing   clean

    scripts/apply-migrations.sh --check                  baseline valid
    scripts/apply-migrations.sh                          applied 1 migration
