# Request-log instrument — visual proof capture log

Captured 2026-08-28 for the branch `feat/reqlog-instrument`.

The screenshots themselves are attached to the pull request through
`scripts/post-pr-visual-proof.sh`, which uploads them to the permanent
`visual-proof-assets` release. Only this text log is committed, because
`npm run lint:proof-tokens` scans `docs/proof/` and nothing else.

## What was running, and why this is a fixture capture rather than a live-login one

The change touches two things: the `usage_events`/`request_attempts` read
path in control-plane (Go), verified separately with the live-DB test suite
(`TestListEvents_LatencyMsPresentAfterAttemptCompletes`,
`TestListEvents_LatencyMsNilWhileAttemptInFlight`, run in CI against a
throwaway Postgres), and the `/console/logs` React components (histogram,
latency column, drill-in, column controls), which is what needed a rendered
screenshot.

A full local stack was attempted first: `docker compose --profile local
--profile dev up control-plane edge-api redis web-console` against this
worktree's own `.env` (the same shared, self-hosted Supabase project every
other local/demo/CI environment currently points at, per `SUPABASE_DB_URL` in
`.env`). `control-plane` could not open a database connection ("database
unreachable after 6 attempt(s) over 1m15s") under the concurrent load this
session's many parallel worktree agents were putting on that same
session-mode pooler — the exact failure mode `project_supabase_pool_ceiling.md`
warns about. Continuing to retry against a pool already reported as a shared
bottleneck was not worth the risk to other agents' live sessions and to
chat-hive, so this was abandoned rather than forced.

Instead, `apps/web-console` was built and run standalone (`docker compose
--profile dev up web-console`, no `control-plane`/`redis`/DB dependency
reachable, own bridge network, host port 3000), with a temporary route
(`app/dev-proof-reqlog/page.tsx`, deleted before this PR was opened, never
part of the diff) that renders the real, unmodified
`UsageLogsHistogram`/`UsageLogsTable` components against eight fixture
`UsageEventRow` objects covering both models in the current catalogue
(`deepseek-chat`, `deepseek-reasoner`, `groq-llama-fast`), a spread of
latencies across every histogram bucket, one cache-bearing row, one failed
row with no measured latency (the em-dash case), and one row with an API key
name. No login, no cookies, no Supabase call on this route at all.

Captured with Playwright (chromium) against `http://localhost:3000/...`.
Note for anyone repeating this: `127.0.0.1:3000` and `localhost:3000` are NOT
interchangeable against this dev server — Next 16's dev-mode origin allowlist
403s `_next/*` asset and RSC requests from `127.0.0.1`, which silently kept
the recharts-based histogram's client chunk from loading (zero `<svg>`
elements, no thrown error) until the capture was pointed at `localhost`
instead.

## Frames captured

| Frame | What it shows |
| --- | --- |
| `after-full.png` | Full page: latency histogram (7 buckets, real bar heights) above the table with the new Latency column, showing `620ms`, `1.5s`, `8.2s`, and an em-dash on the one row with no measured latency (failed, no `completed_at`) |
| `after-columns.png` | The `Columns` control open, a checklist of every column including the new Latency entry |
| `after-drilldown.png` | A row's per-row detail expanded, showing `Latency: 620ms` alongside Request ID, Attempt, Endpoint, Event, and Error |

No URL captured here carries any credential, session token, or query-string
secret (the route takes no session at all), so there was nothing to redact.

## Provider column

Not shipped, and not screenshotted: see the PR body for why (no per-request
provider identity is stored on `usage_events`/`request_attempts` for the
general gateway path today, and the repo's provider-blind invariant is a
second, independent reason it would need a product decision before shipping
even if the data existed).

## Re-capture after the review fixes

Captured again on 2026-08-28 after the review round on this pull request,
because the review changed what the page renders: the histogram now counts one
request per `request_attempt_id` instead of one per usage event, it carries a
caption naming how many requests it covers and that the number is this page
rather than the account, the first bucket label reads as the closed range the
inclusive comparison actually implements, and the last remaining column's
checkbox is disabled rather than silently springing back.

Same method as above: the console built and run standalone from a private image
tag (`hive-web-console:fix1293-proof`, never the shared `hive-web-console:ci`)
on host port 3100, with a temporary route (`app/dev-proof-reqlog/page.tsx`,
deleted before this commit, never part of the diff) rendering the real
`UsageLogsHistogram` and `UsageLogsTable` against twelve fixture
`UsageEventRow` objects spread across eight `request_attempt_id` values. Three
of the twelve events share one attempt at 62ms and two more share another at
1.5s, which is exactly the shape that used to be triple and double counted.
One row carries no latency at all. No login, no cookies, no Supabase call.

Captured with Playwright (chromium) against `http://localhost:3100/...`.

### Frames captured

| Frame | What it shows |
| --- | --- |
| `01-histogram-and-latency-column.png` | Seven bars of height one across seven buckets from twelve events, so the three events sharing one attempt count once; the caption `7 measured requests on this page only, not the whole account.`; the first bucket labelled `0-100ms`; the Latency column showing `62ms`, `340ms`, `1.5s`, `14.5s` and an em-dash on the one row with no measured latency |
| `03-column-floor-disabled.png` | Every column but Time hidden through the checklist: the table keeps one header, and the Time checkbox is checked and disabled rather than clickable |

### Console transcript

```
URL: http://localhost:3100/dev-proof-reqlog (no credential, no query string, no session)
console.info: %cDownload the React DevTools for a better development experience: https://react.dev/link/react-devtools font-weight:bold
console.log: [HMR] connected
histogram caption: 7 measured requests on this page only, not the whole account.
table headers: ["Time","Model","Tokens in","Tokens out","Cached in","Cache write","Credits","Latency","Status","API key"]
latency column cells: ["62ms","62ms","62ms","340ms","340ms","780ms","1.5s","1.5s","3.2s","8.2s","14.5s","—"]
last remaining column checkbox disabled: true
header count at the floor: 1
```

The bucket-label probe in that run returned an empty array because the selector
did not match how recharts renders its axis text. The labels are legible in the
screenshot itself, which is what the frame is for, so the probe was left as it
came out rather than tuned until it agreed.

No URL captured here carries any credential, session token, or query-string
secret (the route takes no session at all), so there was nothing to redact in
either the text or the pixels.
