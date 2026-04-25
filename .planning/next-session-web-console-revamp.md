# Next session: Web-console revamp — OpenNext + Claude-grade design + auth fix

## How to use this prompt

Open a fresh session and run:

```
/frontend-design:frontend-design
```

Then paste this entire document as the user message. The skill is the
**driver** for the design + implementation pass. Treat the rest of this
document as the brief.

> **Why frontend-design skill, not freeform?** The skill enforces a
> design-first loop (style guide → component library → page-by-page
> build → screenshot review with `openwolf designqc`) and prevents the
> common failure mode of jumping to component code before the visual
> system is settled. Read the skill description before starting if it's
> a new tool to you.

---

## TL;DR

1. **Migrate** `apps/web-console` from `@cloudflare/next-on-pages` →
   **OpenNext for Cloudflare Workers** (`@opennextjs/cloudflare`). Drop
   the Pages adapter.
2. **Fix** the staging auth flow that's blocking 7 Playwright specs —
   server-side Supabase session is unreadable after client-side
   `signInWithPassword`, so every `page.goto("/console/...")` bounces
   to `/auth/sign-in`.
3. **Redesign** the entire console with a Claude.com-grade visual
   system (typography, restraint, density, motion). Target parity with
   `claude.ai`'s console aesthetic — not a clone.

All three ship together because they're entangled: the auth bug needs
SSR-aware cookies, OpenNext changes the runtime that hosts those
cookies, and the redesign rebuilds the surfaces that auth gates.

---

## Current state (read before any edits)

### Stack

| Concern | Today | After this PR |
|---------|-------|---------------|
| Framework | Next 15.1 (App Router) | Next 15.1 (App Router) — unchanged |
| Cloudflare adapter | `@cloudflare/next-on-pages@^1.13.12` | `@opennextjs/cloudflare` (latest) |
| Hosting | Cloudflare Pages | Cloudflare Workers (OpenNext) |
| Auth | `@supabase/ssr@^0.6.1` + `@supabase/supabase-js@^2.48.1` | unchanged libs, fixed wiring |
| Styling | (audit needed — likely Tailwind v3 + ad-hoc) | **Tailwind v4 + shadcn/ui (canary)** unless skill chooses otherwise |
| Test runner | Vitest (unit) + Playwright (e2e) | unchanged |
| CI E2E | Runs against `next dev` on `localhost:3000` | **Add second job** — runs against deployed staging URL |

### Routes inventory

```
app/
├── api/budget/                          # internal API route
├── auth/{sign-in,sign-up,callback,forgot-password,reset-password}/
├── invitations/accept/
└── console/
    ├── (dashboard root)
    ├── account-switch/
    ├── analytics/
    ├── api-keys/
    ├── billing/
    ├── catalog/
    ├── members/
    ├── setup/
    └── settings/{billing,profile}/
```

### Failing-on-staging tests (7)

All in `apps/web-console/tests/e2e/`:

- `auth-shell.spec.ts:42` — unverified members → profile redirect
- `auth-shell.spec.ts:55` — invitation accept keeps workspace
- `profile-completion.spec.ts:42` — setup saves profile
- `profile-completion.spec.ts:62` — dashboard reminder after completion
- `profile-completion.spec.ts:87` — profile settings reachable while unverified
- `profile-completion.spec.ts:103` — billing settings save partial business profile
- `profile-completion.spec.ts:124` — unverified billing → profile redirect

**Symptom**: after `signIn(page, ...)` succeeds in the helper, the next
`page.goto("/console/...")` redirects to `/auth/sign-in`. Direct
Supabase REST sign-in works (verified 2026-04-24), so users + passwords
are valid. The bug is in how server-rendered routes read the Supabase
session cookie. Likely root cause: cookie is set by client-side JS only
and the Pages Functions middleware doesn't re-issue it as an SSR cookie
on the server response. Confirm via `getServerSession()` style probe in
a debug route before fixing.

### Passing tests that must stay green (9)

- `unauth.spec.ts` — all 4
- `openai-sdk.spec.ts` — all 3
- `profile-completion.spec.ts:138` — dashboard no billing reminder
- `profile-completion.spec.ts:???` — 1 more passing (verify in run log)

---

## Goal

Ship a single PR that delivers all three of:

1. **OpenNext migration** — `npm run build` produces an OpenNext bundle,
   `wrangler deploy` (Workers, not Pages) runs it, the deploy step in
   `.github/workflows/deploy-staging.yml` is updated, and
   `console-hive.scubed.co` (or a new staging hostname) serves the new
   runtime.
2. **Green Playwright on deployed staging** — all 16 specs pass against
   the OpenNext-served URL. Fix surfaces in this order:
   - Supabase SSR cookie writeback in `app/auth/callback/route.ts` (or
     equivalent) so the session lands in `cookies()` before the response.
   - `lib/supabase/server.ts` reads the cookie consistently across
     server components, route handlers, and middleware.
   - `middleware.ts` updates the auth cookie on every request that
     receives a refresh.
3. **Claude-grade visual system** — every route in `app/` looks
   intentional, dense, calm. No leftover unstyled tables. Typography +
   spacing + color come from a single token file the skill produces.

---

## Visual reference — what "Claude-grade" means

Anchor visual references (study before implementing):

- `claude.ai` console (sidebar layout, dense settings, generous letter-
  spacing on headings, restrained accent color, monochrome data tables)
- `vercel.com/dashboard` (negative-space discipline, hover affordances,
  tabular numerals on usage charts)
- `linear.app` (motion timing — 120-180ms ease-out for menus + lists)

Operating principles for this build:

- **Restraint over flair** — one accent color, one scale, no gradients,
  no glassmorphism, no decorative illustrations on internal pages
- **Tabular numerics everywhere usage/billing renders** — credit
  balances, token counts, latency numbers
- **Density without clutter** — 14px body, 13px small, 12-step spacing
  scale, never above 32px on internal page elements
- **Single-column thinking** — no 3-column splits except true settings
  pages where a left index aids navigation
- **Motion is functional** — only on hover, focus, and page transitions
  (no decorative loops). 120-180ms ease-out (`cubic-bezier(0.16, 1, 0.3, 1)`)

The frontend-design skill will produce a `STYLE_GUIDE.md` and a token
file — defer to its output for exact tokens.

---

## Step-by-step

### Phase 1 — Audit + plan (no code yet)

1. Read `apps/web-console/package.json`, `app/layout.tsx`, `middleware.ts`,
   `lib/supabase/*`, and any auth callback route. Build a one-page map
   of how the session moves: client signs in → cookie → next request →
   server reads cookie. Identify the exact missing link.
2. Read `next.config.*` and `wrangler.jsonc`. List what `@cloudflare/next-on-pages` does that OpenNext replaces.
3. Skim every page under `app/console/`. Note which ones currently use
   inline JSX vs reusable components — the redesign will consolidate.
4. Record findings in `.planning/phases/<next>/RESEARCH.md`.

### Phase 2 — OpenNext migration

1. Branch: `feat/web-console-opennext-revamp`.
2. Install `@opennextjs/cloudflare` (latest stable). Use Context7
   (`resolve-library-id` → `query-docs`) for the **current** init
   command and config — the API has changed multiple times.
3. Replace `@cloudflare/next-on-pages` build script with the OpenNext
   build script. Drop the Pages adapter from `package.json`.
4. Update `wrangler.jsonc` for **Workers** (not Pages):
   - `name: hive-console`
   - `main: .open-next/worker.js` (or current OpenNext output path)
   - `compatibility_flags: ["nodejs_compat"]`
   - `compatibility_date: <today>`
   - `assets: { directory: ".open-next/assets", binding: "ASSETS" }`
   - Carry over Supabase env bindings + secrets via `vars` and
     `wrangler secret`.
5. Local smoke: `npm run build && npx wrangler dev`. Hit `/auth/sign-in`,
   verify it renders without 500.
6. Update `.github/workflows/deploy-staging.yml`:
   - Replace Pages deploy step with `wrangler deploy` (Workers).
   - Update DNS / route in Cloudflare to point
     `console-hive.scubed.co` at the Worker.
   - Keep `hive-console.pages.dev` working during the cutover (or
     explicitly retire it in the PR description).

### Phase 3 — Auth fix

1. Confirm root cause: add a `/api/whoami` route that returns
   `cookies().toString()` length and `supabase.auth.getUser()`. Hit it
   from a logged-in browser and from a logged-in Playwright fixture. If
   the latter returns `user: null` while cookies exist, the SSR client
   isn't reading them — fix that wiring.
2. Common fixes:
   - In `lib/supabase/server.ts`, ensure the `createServerClient`
     `cookies` adapter reads **and writes** to `cookies()` from
     `next/headers` so refreshed sessions are persisted on the response.
   - In `middleware.ts`, call `supabase.auth.getUser()` before any
     redirect logic so the refresh round-trip lands.
   - In `app/auth/callback/route.ts`, after `exchangeCodeForSession`
     return a `NextResponse.redirect` that has the new cookies set on
     `response.cookies.set(...)`.
3. Re-run Playwright suite (`apps/web-console/tests/e2e/`) against
   `wrangler dev`. Iterate until all 16 specs pass.
4. Re-run against the deployed staging Worker. They must also pass —
   if not, the cookie issue is environment-specific (Workers vs local).

### Phase 4 — Design system

1. Invoke the frontend-design skill's discovery loop:
   - Pick a stack — recommend Tailwind v4 + shadcn/ui canary.
   - Produce `apps/web-console/docs/STYLE_GUIDE.md` with tokens (color,
     type scale, spacing, radius, shadow, motion).
   - Build a primitives layer in `components/ui/` (Button, Input,
     Select, Switch, Card, Sheet, Dialog, Toast, Sidebar, Tabs,
     DataTable, Badge, EmptyState, KeyboardShortcut).
2. Build a `components/app-shell/` layout: sidebar + topbar + content,
   with workspace switcher in the sidebar header and account menu in
   the topbar. Mirror Claude.com's split.
3. Per-page redesign — order matters; ship in this sequence so each
   page has primitives ready when it's its turn:
   1. `auth/sign-in`, `auth/sign-up` — minimal centered card,
      tabular focus on form fields, inline validation
   2. `console/setup` — guided 3-step form, no skip, can save partial
   3. `console/` (dashboard) — overview tiles (credit balance, today's
      usage, recent errors), each a Card primitive
   4. `console/api-keys` — DataTable with copy-to-clipboard, masked
      keys, create/revoke modal
   5. `console/billing` + `console/settings/billing` — invoice table,
      payment method, top-up form, BD-aware (NEVER show FX rates per
      `feedback_bdt_no_fx_display`)
   6. `console/analytics` — usage charts, dense tabular layout, no
      decorative gradients
   7. `console/catalog` — model alias table with capability badges
   8. `console/members` + `account-switch` — workspace + member list
   9. `console/settings/profile` — owner profile, account type
4. Visual sign-off via `openwolf designqc` after each page lands.
   Iterate until the snapshot looks Claude-grade.

### Phase 5 — Test + ship

1. **All 16 Playwright specs green** against deployed staging.
2. Add a new visual regression snapshot per page (Playwright
   `expect(page).toHaveScreenshot()` with masked dynamic regions).
   Defer the regression *gating* — for this PR, generate baselines and
   land them; gating is the job of `next-session-visual-regression-coverage.md`.
3. Run `npm run build` → confirm OpenNext bundle size acceptable
   (<1MB Worker, <5MB total assets — Workers limit is 10MB compressed).
4. Run `wrangler deploy --dry-run` to confirm bindings.
5. Update `.planning/next-sessions-INDEX.md` — move ui-styling +
   visual-regression entries to **Done** with this PR's number.
6. Update `CLAUDE.md` (project instructions) Tech Stack table — replace
   `@cloudflare/next-on-pages` with `@opennextjs/cloudflare`.

---

## Constraints

- **Never push directly to main** — `feat/web-console-opennext-revamp`
  → PR.
- **No FX rate display to BD customers** — surfaces under
  `console/billing` must not render exchange rates or "≈ USD" hints.
- **No `as`, `any`, `unknown` casts** — strict TypeScript stays.
- **Storage backend is Supabase Storage only** — if any new asset
  handling is added, route through the existing storage helpers.
- **Auth flow must respect `feedback_no_human_verification`** — run
  Playwright against deployed staging autonomously, do not ask the user
  to manually log in.
- **Existing 9 passing Playwright tests must stay green** — run them
  often during the build; do not let a redesign break unauth flows or
  the OpenAI SDK consumer test.
- **Preserve current routes** — no URL changes; the redesign is visual
  + structural (component swap), not navigational.

---

## Out of scope

- Backend changes (`apps/edge-api`, `apps/control-plane`) — this PR is
  console-only.
- New product features (analytics charts, billing forms beyond what
  exists today).
- Mobile app or PWA-specific affordances — desktop responsive is enough.
- Replacing Supabase Auth with anything else.
- Renaming the `console-hive.scubed.co` domain.

---

## Acceptance checklist

- [ ] `apps/web-console/package.json` no longer depends on
      `@cloudflare/next-on-pages`; depends on `@opennextjs/cloudflare`
      at the latest stable.
- [ ] `wrangler.jsonc` is a Workers config (not Pages); `wrangler deploy`
      succeeds locally.
- [ ] `.github/workflows/deploy-staging.yml` deploys via `wrangler deploy`,
      not Pages.
- [ ] `npx playwright test` against deployed staging URL: **16 pass / 0
      fail / 0 skip** (the openai-sdk skip survives only if HIVE_API_KEY
      is unset, which it shouldn't be in the staging job).
- [ ] Screenshot snapshot baselines committed under
      `apps/web-console/tests/e2e/__screenshots__/` (or wherever
      Playwright lays them out).
- [ ] `apps/web-console/docs/STYLE_GUIDE.md` exists and matches the
      tokens used in `components/ui/`.
- [ ] `openwolf designqc` snapshot review for at least the dashboard,
      api-keys, billing, and sign-in pages — included in the PR
      description as inline reference.
- [ ] No FX rate displayed anywhere in `console/billing` or
      `console/settings/billing` for BD users.
- [ ] `CLAUDE.md` Tech Stack table updated.

---

## Why this is one PR

Splitting fails:

- **Auth fix without OpenNext** — fix needs SSR cookie behavior that
  may be Pages-Functions-specific; fixing it on Pages then re-fixing
  on Workers is double work.
- **Design without auth** — the redesign loop needs to walk through
  authed pages; if `page.goto("/console/...")` bounces, the skill
  can't visually iterate on those pages.
- **OpenNext alone** — a runtime swap with no test coverage of the
  authed surfaces is risky; pair it with the redesign so visual review
  catches regressions the test suite misses.

Land them together, revert together if needed.

---

## References to check before starting

- OpenNext for Cloudflare current docs (Context7:
  `resolve-library-id` → `query-docs` for `OpenNext` /
  `@opennextjs/cloudflare`)
- Cloudflare Workers + Next.js examples
- Supabase SSR `@supabase/ssr` cookie adapter docs (Context7)
- The 9 passing Playwright specs — they tell you what the unauth flow
  contract is; don't break it
- `feedback_bdt_no_fx_display` memory entry
- `feedback_no_human_verification` memory entry
- `feedback_strict_typescript` memory entry
- `.wolf/cerebrum.md` Do-Not-Repeat list
