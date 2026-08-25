# Demo readiness live verification, 2026-08-25

Live verification of DEMO.md's claims against the deployed demo box, run
against https://chat-hive.scubed.co, https://console-hive.scubed.co and
https://api-hive.scubed.co after the 2026-08-25 Cloudflare regional-edge
false alarm had cleared. Session obtained via the standard admin
one-time-token mint (`apps/web-console/tests/e2e/support/live-auth.mjs`,
read-only against `demo@hive-demo.invalid`, no password touched), run inside
the box's own `hive_default` docker network so the internal Kong-equivalent
auth proxy (`http://caddy-supabase`) could serve `/auth/v1/admin/generate_link`,
then a follow-up cookie-encoding pass using the same public
`NEXT_PUBLIC_SUPABASE_URL`/`NEXT_PUBLIC_SUPABASE_ANON_KEY` pair the deployed
web-console frontend actually uses, so the minted cookie name matches what
the browser bundle reads. Chat's own session was obtained by letting the real
OIDC auto-redirect run once a valid console-hive session existed, not by
forging a chat-side cookie (chat does not proxy `/auth/v1` at all).

## Surface reachability (curl, this session)

```
chat-hive.scubed.co/api/config   -> 200 {"status":true,"name":"Hive",...,"oauth":{"providers":{"oidc":"Hive"}}}
console-hive.scubed.co/          -> 307 (redirect to sign-in, expected)
api-hive.scubed.co/health        -> 200 {"status":"ok"}
control-hive.scubed.co/          -> 404 (API only, no root route, expected)
```

All four live and healthy at verification time.

## Capability matrix

| Capability | DEMO.md claim | Verdict | Evidence |
|---|---|---|---|
| Artifacts (#1110 / PR #1141) | stale: "spins forever" | WORKS | `/artifacts` renders a real empty-state index ("No artifacts yet..."), not a spinner. Screenshot 04. |
| Credits in chat (#1063 / PR #1119) | stale: "no visibility" | WORKS | Composer shows "You've used 24,696 credits today, 99,997,301,021 remaining"; matches console Billing overview exactly (99,997,301,021 available). Screenshots 01, 02. |
| Knowledge nav (#1109) | broken, open | STILL BROKEN | Clicking "Knowledge" in the sidebar highlights the row but leaves the home composer showing (URL unchanged). Screenshot 03. Direct `/knowledge` now answers an honest 404 ("Nothing is served at /knowledge") instead of the originally-reported silent bounce to home; symptom shifted slightly, underlying bug (no working surface) unchanged. Screenshot 07. |
| Cache-aware billing (PR #1157) | not previously in DEMO.md | SHIPPED, UNEXERCISED | `usage_events` query for the 6 hours before/after deploy: 143 requests, 0 with `cache_read_tokens>0` or `cache_write_tokens>0`. Code and tests are real; no live cache-bearing request has hit it yet. |
| `cache_control` passthrough (PR #1152) | not previously in DEMO.md | SHIPPED, NOTHING TO ACT ON | Live model catalog (console Model catalog page, screenshot 08) has no Anthropic/Claude model at all: Deepseek V4 Flash/Pro, hive-auto, hive-default, hive-embedding-default, hive-fast, hive-free, hive-medium, hive-small, hive-stt, hive-tts. `cache_control` is an Anthropic-native request field; there is no live route it can affect today. |
| Free pool failover (PR #1155) | already in DEMO.md as working | STILL WORKS, NO REGRESSION | `hive-free` served 52 completed + 26 in-flight requests in the trailing 24h with zero error-status `usage_events` rows. No member has actually failed during the observation window, so the specific failover trigger was not caught in the act, but nothing broke either. |
| Null-content coercion (PR #1169) | not previously in DEMO.md | SHIPPED, NO REGRESSION OBSERVED | Zero `error`/`upstream_error`/`finalize_failed` rows anywhere in `usage_events` in the trailing 24h (356 total rows, all `completed`/`dispatching`/`streaming`). The specific reasoning-model-exhausts-max-tokens scenario that originally triggered this fix has not recurred live to re-test directly. |
| External uptime probe (PR #1166) | not previously in DEMO.md | WORKS, RUNNING ON SCHEDULE | `gh run list --workflow=external-uptime-probe.yml`: three runs, all `success`, most recent scheduled run 2026-08-25T19:43:46Z (9s). Cron confirmed `*/15 * * * *` in the workflow file. |
| OIDC sign-in round trip | "works end to end" | CONFIRMED LIVE | Console session -> chat-hive auto-redirect landed signed in on chat with no manual click needed (`auto_redirect: true`). |
| Multi-user isolation (#947/#948/#949 family) | "not demoable" | PARTIALLY FIXED, NOT FULLY | `gh issue view`: #947, #948 and #949 are all CLOSED, fixed 2026-08-23 by PRs #960, #1067, #1091, #1096. One residual is still OPEN: #1056, an acknowledged partial fix of #947 covering two Knowledge by-id/files routes that still short-circuit on `role == admin` with no ownership check. |

## Screenshots (uploaded to the visual-proof GitHub release, linked from the PR)

1. `01-console-landing.png` - console dashboard, Available credits 99,997,301,021
2. `02-chat-landing.png` - chat composer, credits strip above composer, sidebar with Knowledge/Agents/no-Artifacts-entry visible
3. `03-chat-after-knowledge-click.png` - Knowledge row highlighted, content unchanged (dead nav)
4. `04-chat-artifacts.png` - `/artifacts` real empty-state index
5. `06-console-providers.png` - Providers page correctly gated behind platform-admin ("Managed by your administrator") for this tenant-OWNER account
6. `07-chat-knowledge-direct.png` - `/knowledge` honest 404
7. `08-console-model-catalog-real.png` - full live model catalog, no Anthropic/Claude model present

No credential, token or account address beyond the already-public fixture
address `demo@hive-demo.invalid` (documented in `docs/live-test-auth.md`)
appears in any of these captures. No screenshot shows a browser URL bar
(full-page DOM captures only), so no query-string token exposure is
possible from this capture method.

## What this pass did not attempt

No real chat message was sent, no API key was minted, no agent task was
submitted, and no checkout flow was exercised, per
`docs/live-test-auth.md`'s "never point a write-capable suite at the demo
account" rule: everything above is a sign-in plus page loads, all read-only.
Streaming chat itself, voice dictation, read-aloud, code interpreter
execution and file uploads were not re-exercised this pass (would require
real spend and write actions); no evidence either way beyond what DEMO.md
already stated.

## New issues filed

None. Every genuinely broken thing found (dead Knowledge nav, residual
cross-tenant Knowledge-by-id read, missing Artifacts sidebar entry) already
has an open tracking issue (#1109, #1056, #943 item 4 respectively).
