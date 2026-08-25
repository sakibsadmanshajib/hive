# Visual proof: console cache visibility (PR #1172)

Captured 2026-08-25 against a running stack, after the change, per the
visual-proof rule in `.claude/rules/orchestrator.md`.

The screenshots themselves are attached to PR #1172 as GitHub Release assets
via `scripts/post-pr-visual-proof.sh`. They are deliberately not committed
here: a `raw.githubusercontent.com` URL pinned to this PR's branch 404s the
moment the branch is deleted, which is what squash-and-delete does on merge
(D-042). This directory carries the text half, because
`npm run lint:proof-tokens` scans this directory and nothing else.

## What was running

Real stack, not a mock. Both passes read the same database rows and the same
control-plane; the only thing swapped between them is the console image.

| Component | What it was |
| --- | --- |
| Postgres | `hiveverify-supabase-db-1`, self-hosted Supabase Postgres 16 with the full migration chain |
| Auth | `hiveverify-supabase-auth-1`, GoTrue v2.189.0, reached through a Caddy gateway on the `caddy-supabase` network alias |
| control-plane | Go binary built from this branch (`deploy/docker/Dockerfile.control-plane`), container `cachevis-cp` |
| Console under test (after) | `hive-web-console:cachevis-proof`, built from this branch |
| Console for the before shots | `hive-web-console:cachevis-before`, built from `origin/main` at 78c4b61a4 |
| Reached at | `http://127.0.0.1:13310`, a Caddy proxy sharing the console's network namespace |

The session was minted through the admin one-time-token flow in
`apps/web-console/tests/e2e/support/live-auth.mjs`. No password was set, read
or rotated, per `docs/live-test-auth.md`.

Two notes on the stack, neither caused by this change:

- The `caddy-supabase` gateway that control-plane validates bearer tokens
  against had exited two days earlier, so every token was being rejected with
  "invalid or expired token". A replacement gateway was started on the same
  network alias. This is stack repair, not a code change.
- The control-plane image already running in that project predated the
  `id` and `request_attempt_id` fields on the usage-events payload, so the
  request log could not decode a single row. A control-plane built from this
  branch was used instead. Nothing in this PR touches Go.

## Redaction

The signed-in account address is masked in the page DOM immediately before
each screenshot, in text nodes only. No number, column, control or layout is
altered. No URL in any capture carries a token, and no key or credential
appears in any frame.

## Fixture rows behind the numbers

`public.usage_events` was empty, so six rows were seeded for account
`bdf639ea…` (workspace "Verify 952") and removed again after the capture.
They are listed here in full so a reviewer can check every rendered number by
hand rather than taking the screenshot's word for it.

| Model | input_tokens | output_tokens | cache_read | cache_write | credits |
| --- | --- | --- | --- | --- | --- |
| deepseek-v4-pro | 52,000 | 1,900 | 47,000 | 0 | -1,927 |
| deepseek-v4-flash | 24,000 | 800 | 21,000 | 0 | -79 |
| deepseek-v4-flash | 18,500 | 640 | 15,200 | 0 | -68 |
| hive-fast | 3,200 | 410 | 0 | 2,800 | -21 |
| hive-default | 1,450 | 220 | 0 | 0 | -24 |
| hive-embedding-default | 900 | 0 | 0 | 0 | -1 |

Each credit figure was derived from that alias's own seeded per-million rates
in `public.model_aliases`, pricing the fresh input remainder, the cache-read
subset and the output at their separate rates, so the blended tile below is
checkable rather than decorative.

### The two tiles, checked by hand

```
cache hit rate = cache_read / prompt_tokens
               = (47,000 + 21,000 + 15,200) / (52,000 + 24,000 + 18,500 + 3,200 + 1,450 + 900)
               = 83,200 / 100,050
               = 0.83158  ->  83.2%          rendered: 83.2%

blended        = credits / (input + output) * 1,000,000
               = 2,120 / (100,050 + 3,970) * 1,000,000
               = 2,120 / 104,020 * 1,000,000
               = 20,380.7  ->  20,380        rendered: 20,380
```

Cache writes are excluded from the hit-rate numerator on purpose (D-056), so
the hive-fast row's 2,800 cache-write tokens move the log table and the CSV
but not the rate.

## Catalog pricing, all three absence cases from real seeded rows

The catalog capture is not a constructed case. The seeded `model_aliases`
already carry all three:

| Alias | cache_read_price_credits | pricing_mode | Renders |
| --- | --- | --- | --- |
| deepseek-v4-flash | 1790 | fixed | `1,790` |
| deepseek-v4-pro | 5236 | fixed | `5,236` |
| hive-default | 0 | fixed | `0` (a real stored zero, not an absence) |
| hive-embedding-default | NULL | fixed | `—` |
| hive-stt | NULL | fixed | `—` |
| hive-tts | NULL | fixed | `—` |

The `Variable` case (`pricing_mode = upstream_actual`, alias
`openrouter-auto`) is not visible in the capture because that alias is not
public to this tenant. It is covered by a unit test instead.

## Captures

Before is `origin/main`. After is this branch. Same data, same backend.

| File | Surface | URL |
| --- | --- | --- |
| `before-catalog.png` | Model catalog, five columns, no search, filter or sort | `/console/catalog` |
| `after-catalog.png` | Model catalog with Cache read / 1M and Cache write / 1M, plus search, capability filter, sort and a result count | `/console/catalog` |
| `before-logs.png` | Request logs, seven columns, no cache tokens | `/console/logs?window=24h` |
| `after-logs.png` | Request logs with Cached in and Cache write columns | `/console/logs?window=24h` |
| `before-analytics.png` | Overview, four tiles | `/console/analytics?tab=overview&window=24h` |
| `after-analytics.png` | Overview, six tiles including Cache hit rate and Blended credits / 1M, each with its derivation note | `/console/analytics?tab=overview&window=24h` |
| `after-catalog-search.png` | Search "deepseek" narrowing the table to 2 of 10 models | `/console/catalog` |
| `after-catalog-sort-desc.png` | Sort "Input price, high to low", with unpriced aliases still last | `/console/catalog` |

Browser console errors during both passes: none. The per-surface run log is in
`capture.run.log`.
