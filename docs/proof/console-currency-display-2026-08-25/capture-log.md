# Console currency display — visual proof capture log

Captured 2026-08-25 for the branch `feat/console-currency-display`.

The screenshots themselves are attached to the pull request through
`scripts/post-pr-visual-proof.sh`, which uploads them to the permanent
`visual-proof-assets` release. Only this text log is committed, because
`npm run lint:proof-tokens` scans `docs/proof/` and nothing else.

## What was running

Not the demo box: the change is not deployed yet, and a before/after pair has
to render both versions against the same data. The stack was stood up locally
from this branch's own tree, using the same scripts the `Web E2E (full stack)`
CI job uses, so the catalog rows are the real seeded catalog rather than
fixtures invented for the screenshot.

| Component | How it was started |
| --- | --- |
| Postgres | `pgvector/pgvector:pg17`, throwaway, published on a free host port |
| GoTrue + PostgREST + gateway | `scripts/ci-supabase-stack.sh --port 9010` |
| Schema | the full migration chain, 106 of 106 applied by `scripts/ci-throwaway-db.sh --gotrue` |
| control-plane | `docker compose --profile local up control-plane`, published on a free host port |
| Next.js console | `npm run build && npm start` from `apps/web-console`, port 3100 |
| Account | a run-scoped fixture user created by `tests/e2e/support/e2e-auth-fixtures.mjs` in the throwaway GoTrue, destroyed with the stack. No shared account's password was set, reset or rotated. |

The before frames were captured from the same stack with the working tree
stashed back to `origin/main`, so the only difference between each pair is
this branch's diff. The database was untouched between the two runs.

## URLs captured

| Frame | URL |
| --- | --- |
| `before-catalog.png`, `after-catalog.png` | `http://localhost:3100/console/catalog` |
| `before-model-detail.png`, `after-model-detail.png` | `http://localhost:3100/console/catalog/deepseek-v4-flash` |
| `before-analytics.png`, `after-analytics.png` | `http://localhost:3100/console/analytics?tab=overview&window=24h` |

No URL here carries a credential in its query string or fragment, so there was
nothing to redact from the address bar. The signed-in account's email address
is masked in every frame by Playwright's screenshot `mask` option, applied
before the file is written rather than after.

## Console transcript

Every frame was captured with a `page.on("console")` listener attached. The
listener recorded no message of any type on any of the six page loads: the
"--- console messages ---" section of each capture run is empty. No errors, no
warnings, no React hydration complaints.

## What the frames show

`before-catalog.png` — the four price columns render raw credit integers:
`89,460,000`, `178,920,000`, `2,982,000`, `1,570,800,000`, `43,166,670,000`.

`after-catalog.png` — the same eleven rows, same data, rendered as dollars per
million tokens: `$0.0895`, `$0.179`, `$0.00298`, `$1.57`, `$43.17`. The three
non-numeric states are unchanged and still distinguishable from each other and
from a real zero:

* `hive-auto` still reads `Variable` in all four columns (an `upstream_actual`
  alias publishes no per-million rate).
* `hive-embedding-default` and both voice aliases still show `—` in the two
  cache columns (a fixed alias with no cache rate is not the same as a free
  one).
* `hive-default`'s cache write, `hive-free`'s cache columns and both voice
  aliases' input still read `$0`, because a published zero is a decision and
  has to stay visible as one.

The smallest real rates in the catalog are the ones this change could most
easily have destroyed. `hive-embedding-default` input is 10,000 credits per
million, one hundred-thousandth of a dollar, and `hive-fast` cache write is
40,000. Both render as `$0.00001` and `$0.00004` rather than collapsing to
`$0.00`, which would have read as free.

`before-model-detail.png` / `after-model-detail.png` — the header tiles and the
Pricing table on `deepseek-v4-flash`. Before: a single `Credits per 1M tokens`
column. After: `Price / 1M` leads and `Credits / 1M` sits beside it, keeping
the exact integer an account needs to reconcile a bill against the ledger.

`before-analytics.png` / `after-analytics.png` — the blended effective price
tile, over six seeded usage rows on the throwaway database (3,438,000 credits
across 31,200 tokens). Before: `Blended credits / 1M`, `110,192,307`. After:
`Blended price / 1M`, `$0.11`, with `110,192,307 credits` kept in the tile's
derivation note. The figures on that page are synthetic rows inserted into the
throwaway database for this capture; they are not any real account's balance
or spend.
