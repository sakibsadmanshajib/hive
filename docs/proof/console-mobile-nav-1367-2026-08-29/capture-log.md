# Visual proof: console navigation below 1024px, and the console defect pass

Date: 2026-08-29
Branch: fix/1367-console-mobile-nav
Issue: #1367
Base: origin/main at 5818cd268

No credential appears anywhere in this capture. Nothing in this flow carries a
token in a query string (no invitation accept, password reset, magic link or
OAuth callback), no sign-in happens at all, and every URL below is a local
`localhost:13100` path with no query parameter other than `?page=`.

## Substrate note, read this before judging the capture

This repository's shared dev `.env` has `NEXT_PUBLIC_SUPABASE_URL` and
`NEXT_PUBLIC_SUPABASE_ANON_KEY` present but EMPTY, and `SUPABASE_DB_URL`
points at a Supabase Cloud project that was deleted during the self-hosted
cutover. Verified in this session:

```
$ grep "^NEXT_PUBLIC_SUPABASE_URL=" .env | awk -F= '{print "len=" length($2)}'
len=0
```

The self-hosted Supabase that replaced it runs on the owner's physical demo
box, which this sandboxed worktree has no network path to. So `getViewer()`
cannot resolve a signed-in session anywhere in this console right now, on any
branch, and `/console/*` is unreachable from here. That is a pre-existing
environment gap, not something this change introduced.

## What was actually run

A throwaway, never-committed harness route (`app/proofharness/page.tsx`, plus
`app/proofharness/before/page.tsx`) rendered the REAL `ConsoleShell`, the real
`ApiKeyList`, the real `ModelCatalogBrowser`, the real `BillingOverview` and
the real `ObservabilityTiles` with mock props. It sits outside `app/console/`,
so `app/console/layout.tsx`'s `getViewer()` gate never runs. The before route
imports origin/main's own `console-shell.tsx`, taken verbatim with
`git show origin/main:apps/web-console/components/app-shell/console-shell.tsx`
and renamed only so both shells could be served by one dev server.

Served by a real `next dev` from this worktree's own freshly built Docker image
(`hive-web-console:ci`, rebuilt with `--build` after every source edit, since
that service mounts no volume and COPYs source at image build time), in a
worktree-scoped compose project
(`COMPOSE_PROJECT_NAME=hive-agent-a26f0beb8a41352ca-50a7a312`) published on
host port 13100 so it could not collide with another agent's stack.

Placeholder Supabase values were passed to the container purely so
`middleware.ts` could construct its client: `NEXT_PUBLIC_SUPABASE_URL` was set
to `http://supabase.invalid` and `NEXT_PUBLIC_SUPABASE_ANON_KEY` to the literal
string `proof-harness-placeholder-not-a-credential`. Neither is a credential,
neither reaches any real service, and no session was minted.

Capture tool: Playwright 1.56 Chromium, `deviceScaleFactor: 2`, viewport
375x812 for the phone shots and 1280x900 for the desktop ones.

## 1. Issue #1367: there is now navigation below 1024px

Tabbable set on `/console/api-keys` at 375px, which is what the original audit
measured. Before, on origin/main's shell:

```
["Documentation", "Switch to EN", "Switch to বাংলা", "Sign out", "Revoke"]
```

Five elements, and not one of them is a route. Billing, Members, Analytics,
Logs, API keys and Settings had no path from this page or any other.

After, drawer closed: the same set plus `Open navigation`. After, drawer open:

```
["Hive", "Overview", "API keys", "Model catalog", "Logs", "Analytics",
 "Billing", "Members", "Privacy", "Settings", "Providers", "Feature gates",
 "Marketplace", "Sign out", "Close navigation", "Documentation",
 "Switch to EN", "Switch to বাংলা", "Sign out", "Revoke"]
```

All thirteen nav entries reachable, workspace switcher and account footer with
them. Screenshots `01-before-375-no-nav.png`, `02-after-375-closed.png`,
`03-after-375-drawer-open.png`.

Closed, the rail is still `hidden`, so those thirteen links are out of the tab
order rather than parked off-screen: the closed tabbable set above contains
none of them.

## 2. Issue #1367, second half: horizontal overflow at 375px

A/B measured in one browser session, by putting origin/main's own table wrapper
(`overflow-hidden`, no containing block) back on the element at runtime and
then restoring this branch's:

| Page | origin/main wrapper | this branch |
|---|---|---|
| `/console/api-keys` | documentScrollWidth 803, page scrolls right by 428px | 375, 0 |
| `/console/catalog` | documentScrollWidth 818, page scrolls right by 443px | 375, 0 |

`pageScrollsRightBy` is `window.scrollTo(9999, 0)` then `window.scrollX`, which
is the only measurement that answers the question a reader cares about: how far
can someone actually drag the page sideways.

Root cause, isolated experimentally before the fix was written. The table was
already inside a scroller that was the right width (wrapper `offsetWidth` 343,
`scrollWidth` 802, `body.scrollWidth` 375), and yet the document still scrolled
428px. Chromium propagates an over-wide `<table>`'s layout overflow past a
non-positioned `overflow:auto` ancestor to the viewport. Four probes, same page,
same session:

```
baseline                    htmlScrollWidth 803  canScrollRight 428
table display:none          htmlScrollWidth 375  canScrollRight 0
table-layout:fixed          htmlScrollWidth 375  canScrollRight 0
wrapper contain:paint       htmlScrollWidth 375  canScrollRight 0
wrapper max-width:100%      htmlScrollWidth 803  canScrollRight 428
wrapper position:relative   htmlScrollWidth 375  canScrollRight 0
```

Hence `relative overflow-x-auto` on the `DataTable` wrapper: `relative` stops
the propagation, `overflow-x-auto` replaces `overflow-hidden` so the columns
past the fold stay reachable instead of being clipped away.

## 3. Buy credits contrast, measured in the browser in both themes

Measured by round-tripping the computed colour through a 1x1 canvas, because
Chromium hands `getComputedStyle` back untouched `lab()` here and a parser that
only understands `rgb()` would have silently skipped it. The raw values it
returned are recorded to show that trap is real:

| Theme | raw background | resolved | label | ratio |
|---|---|---|---|---|
| light | `lab(43.1662 40.7486 40.2976)` | rgb(169,69,36) | rgb(250,250,250) | **5.65:1** |
| dark | `lab(66.4258 39.8986 37.0067)` | rgb(238,131,97) | rgb(14,13,11) | **7.45:1** |

Reported before this change: 4.45:1 light and 2.61:1 dark (white label). Both
now clear the 4.5:1 AA threshold. Screenshots `05-billing-light.png`,
`05-billing-dark.png`.

## 4. Branded 404 that discloses nothing

`GET /definitely-not-a-route` renders the branded page. Its complete tabbable
set is one element:

```
["Back to the console"]
```

No rail, no workspace name, no signed-in identity, no Providers, Feature gates
or Marketplace entry. Screenshot `06-404-light.png`.

## 5. Grafana environment variable no longer in customer copy

On the analytics surface with `GRAFANA_BASE_URL` unset:

```
mentionsEnvVar: false
disabledTileCopy: ["Platform overview", "Not available on this deployment.",
                   "Rate limits",       "Not available on this deployment."]
```

Screenshot `07-analytics-tiles.png`.

## 6. Ledger ordering

`05-billing-dark.png` shows Recent transactions in 26, 24, 16, 11 August order.
The console renders whatever order the API returns; the ordering itself is
fixed in `apps/control-plane/internal/ledger/repository.go` and proven by three
live Postgres tests (see the pull request body), not by this screenshot.

## 7. Re-capture after review, covering the accessibility fixes

The adversarial review round added focus management, `inert` on `<main>`,
a background scroll lock, and closing the drawer when the viewport crosses up
past `lg`. The drawer screenshots above were retaken against that code, and the
new behaviour was measured in the same browser session:

```
closed:               pageScrollsRightBy 0  mainInert false  bodyOverflow (unset)
                      focusedIsDrawer false  toggleExpanded false
open:                 pageScrollsRightBy 0  mainInert true   bodyOverflow hidden
                      focusedIsDrawer true   toggleExpanded true
after Escape:         mainInert false  bodyOverflow (unset)  focus back on "Open navigation"
after resize to 1280: mainInert false  bodyOverflow (unset)  toggleExpanded false
```

Tab order from the open drawer, 24 presses, checking at every stop whether
focus landed inside `<main>`:

```
Hive, Overview, API keys, Model catalog, Logs, Analytics, Billing, Members,
Privacy, Settings, Providers, Feature gates, Marketplace, Sign out,
Close navigation, Documentation, Switch to EN, Switch to বাংলা, Sign out,
NEXTJS-PORTAL, (body), Hive, Overview, API keys

reached page content behind scrim: false
```

Focus cycles through the drawer and the header, then wraps. It never reaches
the page sitting behind the scrim. (`NEXTJS-PORTAL` is the `next dev` error
overlay and exists only in development.)

## Cleanup performed

- `rm -rf apps/web-console/app/proofharness` (never committed; `git status`
  clean before the pull request).
- `.env`, copied read-only from the shared checkout to run this stack, deleted
  from the worktree.
- Throwaway `pgvector/pgvector:pg17` container and the `hivetest` database used
  for the Go tests removed, along with the `next dev` container.
