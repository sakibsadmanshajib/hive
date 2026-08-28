# Console overview cards + try-in-chat: capture log

Date: 2026-08-28. Branch `fix/console-overview-cards`.

## Environment blocker (read before questioning why this isn't a live-stack capture)

`docker compose --profile local --profile chat up` could not reach a
database: `SUPABASE_URL=https://yimgflllgdsbcibnaxqe.supabase.co` in the
checked-out `.env` no longer resolves (`Could not resolve host`), consistent
with the self-hosted Supabase cutover (`project_self_hosted_supabase_migration`
memory) having deleted that Cloud project. The self-hosted replacement lives
only on the demo box's internal Docker network (`caddy-supabase`, `GET
/auth/v1/user` proxied through `console-hive.scubed.co`'s own origin per
`Caddyfile.console`) and is not reachable from this sandbox at all: no public
Supabase hostname exists (`deploy/cloudflare/tunnel-ingress.json` lists only
`api-hive`, `control-hive`, `chat-hive`, `artifacts-hive`, `console-hive`).
`control-plane` itself came up but logged `database unreachable after 6
attempt(s)`, so no full-stack sign-in was possible from here at all right
now, on any branch. This is a real, repo-wide local-dev breakage independent
of this change; not something this PR fixes.

## Method

Real React rendering, real compiled CSS, real browser — only the network
boundary to `control-plane` is mocked, at the exact same seam the passing
unit tests (`tests/unit/console-overview-empty-states.test.tsx`,
`tests/unit/model-catalog-table.test.tsx`) already mock via `vi.mock("@/lib/
control-plane/client")`.

1. `docker compose run --build web-console npm run build` produced the real
   compiled Tailwind v4 CSS chunk (`.next/static/chunks/3075ok53ftila.css`),
   copied out via a mounted volume.
2. A throwaway vitest file (`tests/unit/__visual_proof__.test.tsx`, deleted
   before commit, never part of this PR's diff) called the real
   `ConsolePage()` and `ModelCatalogTable` component code with mocked
   `getViewer`/`getAccountProfile`/`getBalance`/`getAnalyticsUsage`/
   `getAnalyticsErrors` — same technique, same mock shapes as the committed
   unit tests — and used `react-dom/server`'s `renderToStaticMarkup` to
   produce real static HTML, written out through the same mounted volume.
3. That HTML was linked against the real compiled CSS chunk and opened in a
   real Chromium instance (chrome-devtools MCP), where full-page screenshots
   were taken.
4. "Before" screenshots used the identical technique against `origin/main`'s
   pre-fix `app/console/page.tsx` and `components/catalog/model-catalog-
   table.tsx`, temporarily swapped into place from `git show origin/main:...`
   and restored (`git status` confirmed clean restoration) immediately after
   capture, before any commit.

## Item 1 — console overview cards

**Before** (`shot-before-emdash.png`): "Today's activity" renders a bare
`—` em-dash with no distinction from a broken page. "Recent errors" renders
the same `—` plus "Error telemetry not yet wired up."

**After, empty state** (`shot-empty.png`, `getAnalyticsUsage`/
`getAnalyticsErrors` both return `[]`): "No requests in the last 24 hours."
and "No errors recorded." — an honest, wired empty state, distinguishable
from a broken one, not a hardcoded string (proven below).

**After, populated state** (`shot-populated.png`, two `getAnalyticsUsage`
rows summing to 272 requests / 312,670 tokens, one `getAnalyticsErrors` row
with `error_count: 6`): renders "272" as the real request count, "312,670
tokens in the last 24h", and "6" with a red error glyph and a link to
`/console/logs?errors=true&window=24h`. This is the proof the cards are
genuinely wired to `getAnalyticsUsage`/`getAnalyticsErrors` and not a second
hardcoded string: `tests/unit/console-overview-empty-states.test.tsx` asserts
the same two branches (`renders the real request and token counts...` /
`renders the real error count...`), and the screenshot shows the real
component rendering real mocked-network data end to end.

## Item 2 — try-in-chat affordance

**Before** (`shot-before-catalog.png`): the model catalogue table has no way
to reach chat from a row.

**After** (`shot-catalog.png`): every row carries a "Try in chat ↗" link
(`chatModelUrl(row.id)`, opens `chat-hive.scubed.co/?model=<id>` in a new
tab). Catalogue is DeepSeek + Groq only per the 2026-08-26 owner ruling, no
Anthropic model shown. `components/catalog/model-catalog-table.test.tsx`
covers the href, `target="_blank"`, `rel="noopener noreferrer"`. The model
detail page (`app/console/catalog/[id]/page.tsx`) got the matching "Try in
chat" primary action button; not separately screenshotted here since it is
the same `chatModelUrl` call proven by `tests/unit/chat-link.test.ts`, kept
out of this capture to control scope.

## Files

- `shot-before-emdash.png`, `shot-empty.png`, `shot-populated.png` — item 1
- `shot-before-catalog.png`, `shot-catalog.png` — item 2

Posted to the PR as permanent release assets via
`scripts/post-pr-visual-proof.sh`, not committed to git (only this log is,
per `npm run lint:proof-tokens`'s scan scope).
