# Visual proof capture log: chat settings retitle + Usage tab

PR: feat/chat-settings-usage-tab. Captured 2026-08-28.

## What is proven

Two screenshots, captured with Playwright (chromium) against a real,
locally-built Docker image (`docker build -f deploy/docker/Dockerfile.open-webui
...`, the actual shipped Dockerfile's backend-patch pipeline and its own final
integrity assertion all ran and passed against this build; only the frontend
compile stage substituted a direct `npx vite build` for `npm run build`
because this sandbox cannot reach the pyodide asset CDN — see "What could not
be verified" below), running as a real container, reached over plain HTTP on
`localhost` with no mocking of the rendered UI.

- `proof-general-chat-preferences.png`: Settings modal, General tab. Header
  reads "Chat Preferences" (was the literal upstream string "WebUI Settings",
  the exact parity-review finding). Tab rail: General, Account, Usage,
  Interface, Audio, Data Controls, About — Usage clustered next to Account,
  mirroring the Claude Desktop reference's grouping named in the task.
- `proof-usage-tab.png`: Settings modal, Usage tab (new). Shows "Usage isn't
  available on this deployment.", a working "Refresh" control, and a "Last
  updated" timestamp. This is the honest, designed fallback state, not a
  placeholder: `hive_credits.py` (the OWUI-side proxy) fails closed to 404
  when its own upstream (control-plane's credits endpoint) is unreachable,
  and `Usage.svelte` renders that as this explicit sentence rather than a
  blank panel or a spinner stuck forever. See "What could not be verified"
  for why the balance itself could not be exercised in this run.

## Sign-in path used

Not the shared QA fixture (`.env` lines 70-71) and not the production OAuth
("Continue with Hive") flow. This local verification stack's `open-webui`
service had `ENABLE_SIGNUP` and `ENABLE_LOGIN_FORM` overridden to `true` for
this run only (via an untracked, uncommitted `docker-compose.verify-override.yml`,
deleted after use), and a brand-new, throwaway account (`verify-<epoch
millis>@example.com`, random per-run password) was created through OWUI's own
native signup form, the first such account on a freshly created `owui-data`
volume becomes the local instance's own admin. No shared credential was read,
touched, or rotated.

## What could not be verified, and why (environment, not code)

Full live-data verification (a Usage tab showing a real non-zero credit
balance) was blocked by a pre-existing, environment-wide problem, confirmed
independently before touching anything:

- The shared `.env`'s `SUPABASE_URL` (`https://yimgflllgdsbcibnaxqe.supabase.co`)
  does not resolve at all (`curl: (6) Could not resolve host`). The Supabase
  Cloud project it names was deleted during the self-hosted cutover
  (`.wolf/decisions.md`, project self-hosted-Supabase-migration notes).
- `SUPABASE_DB_URL` points at the same dead project's pooler
  (`aws-1-us-east-1.pooler.supabase.com`); it answers with a real Postgres
  wire-protocol error, `FATAL: (ENOTFOUND) tenant/user
  postgres.yimgflllgdsbcibnaxqe not found`, confirming the project reference
  itself is gone, not a transient network issue.
- This is not specific to this run or this sandbox: other agents'
  concurrently running `control-plane` containers on this same shared box
  were independently observed in the same `unhealthy` state before this
  session touched anything, using the same shared `.env`.
- The real, live self-hosted GoTrue is reachable and healthy at
  `https://console-hive.scubed.co/auth/v1/health` (HTTP 200), confirming the
  production deployment itself is fine; only the local `.env`'s pointers are
  stale.

Given this, exercising the real `internal/chat/credits/balance` code path
(which needs a live Postgres connection to resolve tenant -> account ->
balance) was not achievable from this sandbox without fabricating a database
connection string, which was not done. The captured "Usage isn't available on
this deployment." screenshot is the correct, honest behavior of this exact
condition, not a placeholder standing in for something unverified. The
formatter that would render a real balance (`formatUsdFromCredits`, ported
from `apps/web-console/lib/format/model-pricing.ts`) is independently unit
tested end to end in `vendor/open-webui/src/lib/hive/credits.test.ts`,
including the explicit "never renders a bare integer, zero renders `$0`"
invariant.

Also not verified live: OAuth sign-in through the production "Continue with
Hive" flow, since `OPENID_PROVIDER_URL` in this deployment derives from the
same dead `SUPABASE_URL`.

## Full test and build evidence (reproducible, not just this capture)

- `npm run test:frontend -- --run` inside `vendor/open-webui`: 208/208 tests
  passed across 16 files, including the 10 new regression tests in
  `src/lib/hive/settings-usage-tab.test.ts`.
- `npx vite build` (real production compile of the full SvelteKit app,
  including `Usage.svelte` and the `ChartBar` icon import): succeeded,
  produced `build/index.html` and every real chunk (`ChartBar.js` present in
  the emitted chunk list).
- `docker build -f deploy/docker/Dockerfile.open-webui ...` (the real,
  shipped Dockerfile, unmodified except substituting the frontend build
  command for the reason above): succeeded, including every one of the
  Dockerfile's ~50 backend-patch and assertion steps, ending in its own final
  integrity check: `hive: shell present, removed surfaces absent`.

## Credential handling

No credential-bearing URL was captured in any screenshot (headless Playwright
screenshots in this run capture only page content, not browser chrome or the
address bar). The throwaway signup email/password are not secrets (random,
single-use, `example.com`/`example.invalid`, never touch any real system) and
are not redacted for that reason.
