---
phase: 02-identity-account-foundation
plan: "06"
subsystem: web-console
tags:
  - setup-flow
  - profile-settings
  - supabase
  - nextjs
  - onboarding
dependency_graph:
  requires:
    - 02-04
    - 02-05
  provides:
    - short setup flow for the minimal account profile
    - profile settings screen with email maintenance controls
    - dashboard reminder based on profile_setup_complete
  affects:
    - apps/web-console
tech_stack:
  added: []
  patterns:
    - Reusable account profile form shared between setup and settings via server actions
    - Control-plane profile fetches and updates stay server-side while Supabase email maintenance stays browser-side
key_files:
  created:
    - apps/web-console/app/console/setup/page.tsx
    - apps/web-console/app/console/settings/profile/page.tsx
    - apps/web-console/components/profile/account-profile-form.tsx
    - apps/web-console/components/email-settings-card.tsx
    - apps/web-console/lib/profile-schemas.ts
    - apps/web-console/tests/e2e/profile-completion.spec.ts (RED commit)
    - apps/web-console/tests/unit/profile-schemas.test.ts (RED commit)
  modified:
    - apps/web-console/app/console/page.tsx
    - apps/web-console/lib/control-plane/client.ts
    - apps/web-console/app/layout.tsx
    - apps/web-console/app/auth/callback/route.ts
    - apps/web-console/lib/supabase/server.ts
    - apps/web-console/middleware.ts
decisions:
  - "The setup flow submits the existing login email as a hidden value so onboarding stays limited to the five visible core fields."
  - "Profile editing uses shared server-action form handling, while email verification and email-change controls remain browser-side Supabase actions."
  - "Dashboard setup guidance is a reminder card, not a redirect gate, so /console remains the landing route after setup."
metrics:
  duration: "29min"
  completed_date: "2026-03-29"
  tasks_completed: 2
  files_created: 7
requirements:
  - AUTH-04
---

# Phase 02 Plan 06: Setup Flow and Profile Settings Summary

**Minimal setup and profile-maintenance flow that reuses the current-account profile API while keeping billing details out of onboarding**

## What Was Built

### Task 1: Create the core profile form, setup route, and shared schema

- **`apps/web-console/lib/profile-schemas.ts`** — Exposes `accountProfileSchema.safeParse()` for the minimal owner/account/location fields.
- **`apps/web-console/lib/control-plane/client.ts`** — Adds server-side `getAccountProfile()` and `updateAccountProfile()` helpers, and normalizes the viewer contract for current-account profile work.
- **`apps/web-console/components/profile/account-profile-form.tsx`** — Shared form component for owner name, account name, account type, country, and state/province, with server-action error handling.
- **`apps/web-console/app/console/setup/page.tsx`** — Minimal setup screen that saves the core profile and redirects back to `/console` on success.
- **Unit coverage** — `profile-schemas.test.ts` verifies the schema accepts the required shape and rejects missing core fields.

### Task 2: Expand profile settings and keep dashboard landing behavior intact

- **`apps/web-console/app/console/page.tsx`** — Adds a non-blocking setup reminder card linking to `/console/setup` when `profile_setup_complete` is false.
- **`apps/web-console/components/email-settings-card.tsx`** — Shows the current login email, `Resend verification email`, and `Change email` controls using the browser Supabase client.
- **`apps/web-console/app/console/settings/profile/page.tsx`** — Keeps profile maintenance reachable for unverified users and renders both the email settings card and shared account profile form.
- **E2E coverage** — `profile-completion.spec.ts` captures `setup saves profile`, `dashboard shows setup reminder instead of forcing setup after completion`, and `profile settings stay reachable while unverified`.

## Task Commits

1. **Task 1 RED: add failing setup schema coverage** — `0892fd7` (`test`)
2. **Task 1 GREEN: add setup flow and core profile form** — `85aa72a` (`feat`)
3. **Task 2 RED: add profile completion e2e coverage** — `4935b8c` (`test`)
4. **Task 2 GREEN: add profile settings and setup reminders** — `72fe028` (`feat`)

## Decisions Made

1. **Setup keeps login email hidden, not editable** — users already established login email during auth; setup only asks for the minimal extra account fields.
2. **Shared server-action form for setup and settings** — one validation/submission path keeps profile editing consistent across onboarding and later maintenance.
3. **Dashboard reminder instead of redirect loop** — the console stays the landing page after setup, and incomplete profiles only trigger a CTA card.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added the missing root Next.js app layout**
- **Found during:** Task 2 verification
- **Issue:** `next build` failed immediately because `apps/web-console/app/layout.tsx` did not exist.
- **Fix:** Added the minimal root layout wrapping `children` in `<html>` and `<body>`.
- **Files modified:** `apps/web-console/app/layout.tsx`
- **Verification:** Build advanced past the previous root-layout failure.
- **Committed in:** `72fe028`

**2. [Rule 3 - Blocking] Strictly typed existing Supabase cookie adapters**
- **Found during:** Task 2 verification
- **Issue:** Existing `setAll` callbacks in the callback route, server helper, and middleware relied on implicit `any` / assertion-style typing that breaks strict TypeScript policy.
- **Fix:** Typed those cookie adapter payloads with `CookieOptions` from `@supabase/ssr` and removed assertion-based cookie writes.
- **Files modified:** `apps/web-console/app/auth/callback/route.ts`, `apps/web-console/lib/supabase/server.ts`, `apps/web-console/middleware.ts`
- **Verification:** Build advanced through type-checking for those files under strict TypeScript.
- **Committed in:** `72fe028`

---

**Total deviations:** 2 auto-fixed (2 blocking)
**Impact on plan:** Both fixes were necessary to keep the web-console build/type baseline usable. They did not expand the product scope beyond the planned profile work.

## Issues Encountered

- The filtered Playwright coverage is auth-gated in this environment, so the task-2 e2e run executed and skipped all three scenarios because no E2E credentials were configured.
- Production build verification now reaches prerendering but stops on missing Supabase env configuration (`NEXT_PUBLIC_SUPABASE_URL` / `NEXT_PUBLIC_SUPABASE_ANON_KEY`) for auth pages.

## Next Phase Readiness

- `02-07` can reuse the profile-settings route structure and schema pattern for optional billing identity storage.
- The dashboard now has a dedicated non-blocking reminder pattern, so billing settings can remain optional without creating a second onboarding gate.

## Self-Check

- [x] `apps/web-console/app/console/settings/profile/page.tsx` contains `Resend verification email`
- [x] `apps/web-console/app/console/page.tsx` contains `/console/setup`
- [x] `apps/web-console/tests/e2e/profile-completion.spec.ts` contains `setup saves profile`
- [x] `apps/web-console/tests/e2e/profile-completion.spec.ts` contains `profile settings stay reachable while unverified`
- [x] Focused unit coverage passed for `tests/unit/profile-schemas.test.ts`
- [x] Filtered Playwright coverage executed and skipped because E2E credentials are not configured
- [x] Production build advanced through compilation and type-checking; remaining failure is missing Supabase env during prerender
