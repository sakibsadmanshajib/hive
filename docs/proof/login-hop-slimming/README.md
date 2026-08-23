# Proof: fix/login-hop-slimming (PR #1078)

No URL in this capture carries a credential, code, state, or token value in
its query string (the screenshots below never show a browser address bar at
all, and the only cookie value used is a locally-minted throwaway JWT signed
with a fixed local test secret that exists nowhere outside this harness), so
no redaction step applied. Confirmed before writing this file, not assumed.

## Substrate

Not the live scubed.co demo box: no path to it was available in this session.
Instead, two Docker images built from this repo's own
`deploy/docker/Dockerfile.open-webui`, the same Dockerfile the demo box
builds from:

- **AFTER** = `hive-open-webui:login-hop-slimming`, built from this PR's head
  commit `603a343f71068a4f7aee7c598bb6a874af6f616b`. Confirmed live: the
  running container's own `/app/backend/open_webui/utils/oauth.py` contains
  `redirect_url = f'{redirect_base_url}/'` on the success leg.
- **BEFORE** = `hive-open-webui:mergebase-baseline`, built from `369c07c0`,
  the merge-base of `fix/login-hop-slimming` and `origin/main`. Built from the
  merge-base rather than current `main` HEAD because current `main` fails
  this exact Docker build with a pre-existing, unrelated Svelte error
  (`<svelte:window>` placement in `src/lib/hive/AgentSchedules.svelte`,
  introduced by PR #1081 after this branch diverged). Not fixed here, out of
  this PR's scope; the merge-base predates that commit
  (`git merge-base --is-ancestor` confirms it), so it is a clean pre-fix
  baseline. Frontend unit tests passed on both builds during the image build
  (60/60, including `sso-redirect.test.ts`).

Harness: a real Postgres, a real OWUI backend, and a stub OIDC discovery
document (a static JSON file, not a real external IdP — none was reachable
from this environment), driven by real Playwright/Chromium with a
`history.pushState`/`replaceState` hook injected at document-start so
SvelteKit's client-side route changes are visible even when they never touch
the network. No mocking of any OWUI backend endpoint.

## Fix 1: anonymous visitor no longer mounts /auth before the provider redirect

3 runs each, fresh browser context per run:

| run | visited `/auth` (client-side route) | time to `GET /oauth/oidc/login` |
|---|---|---|
| BEFORE 1 | yes | 6649ms |
| BEFORE 2 | yes | 2723ms |
| BEFORE 3 | yes | 3131ms |
| AFTER 1 | no | 4291ms |
| AFTER 2 | no | 3564ms |
| AFTER 3 | no | 3803ms |

BEFORE visited `/auth` as a client-side route in 3/3 runs before ever
requesting `/oauth/oidc/login`; AFTER did so in 0/3. This machine runs several
dozen other concurrent containers during this session, so the absolute-time
column is noisy (BEFORE's own range spans 2.7s-6.6s); the route-visited column
is the clean, 100%-consistent signal and is exactly the hop this fix removes.

## Fix 2: OAuth callback lands on / directly instead of bouncing through /auth

Reproduced the exact browser state the instant after a real callback exchange
(a real, valid `token` cookie set, no `localStorage` token, no prior page
load), then loaded each build's actual real landing target:

- **BEFORE**, landing on its real target `/auth`: 1 full HTML document load
  (of `/auth`, a page whose only remaining job here is converting the
  cookie), then a client-side route change to `/` at t=3400ms once the
  conversion finishes. See `before-landing-auth-intermediate.png`.
- **AFTER**, landing on its real target `/`: 1 full HTML document load (of
  `/`, the actual destination), session recovered from the cookie in place,
  0 further navigation. See `after-landing-root-signed-in.png`.

Both end up signed in on `/`; BEFORE pays for a full mount-and-render cycle of
a page that is not the destination, AFTER does not.

Also checked, for completeness, not because it happens in production: what
BEFORE's *root layout* does if handed a cookie-only session on `/` directly
(proving the two fixes are correctly yoked together). It does not recognize
the cookie at all — no cookie-recovery code exists there pre-fix — so it
takes the anonymous branch, client-routes to `/auth` at t=2964ms, and only
there does the pre-existing (unrelated to this PR) cookie recovery fire,
landing back on `/` at t=3187ms. Confirms the callback-target change alone,
without the frontend cookie-recovery half this PR also ships, would have
stranded users on an anonymous `/`.

## Fix 3: dead terminals probe

Same authenticated session (a real JWT minted via the real
`/api/v1/auths/signin` against a real bootstrapped admin user in this
harness's own Postgres), same backend, only the frontend image swapped:

- BEFORE: `GET /api/v1/terminals/` observed once on chat-layout mount.
- AFTER: `GET /api/v1/terminals/` observed zero times.

## What this does not cover

This harness never completed a real external IdP code exchange (state/PKCE
round trip against a live GoTrue instance); none was reachable from this
environment. That code path (`oauth.py` state validation around line 282,
PKCE/`code_challenge_method` around line 805) was reviewed directly and
confirmed both untouched and physically distant from this patch's anchor line
at ~1966, not exercised end-to-end here. The
`apply_oauth_callback_landing_patch.py` self-check
(`scripts/test_owui_oauth_callback_landing.py`) already exercises the real
patch against the real vendored file and is green in this PR's CI.

## Screenshots

- `before-landing-auth-intermediate.png`: the pre-fix `/auth` page rendered
  mid-conversion (the "You're now logged in" toast visible, page not yet
  navigated to `/`) — the wasted document this PR removes from the return
  leg.
- `after-landing-root-signed-in.png`: the post-fix landing directly on `/`,
  already signed in, no intermediate page ever rendered.
