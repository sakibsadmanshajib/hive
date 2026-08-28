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
