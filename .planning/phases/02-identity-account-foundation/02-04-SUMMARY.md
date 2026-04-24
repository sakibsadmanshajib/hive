---
phase: 02-identity-account-foundation
plan: "04"
subsystem: web-console
tags:
  - console-shell
  - viewer-gates
  - workspace-switching
  - invitation-acceptance
  - verification-banner
dependency_graph:
  requires:
    - 02-02
    - 02-03
  provides:
    - authenticated console shell with verification lock state
    - workspace switcher persisting hive_account_id
    - invitation acceptance without auto-switch
  affects:
    - apps/web-console
tech_stack:
  added: []
  patterns:
    - Server Component viewer fetcher forwarding Supabase access token + X-Hive-Account-ID
    - Gate functions over viewer.gates fields (canInviteMembers, canManageApiKeys)
    - Cookie-based account context (hive_account_id) separate from Supabase session
    - E2E tests using environment variables for verified/unverified credentials
key_files:
  created:
    - apps/web-console/lib/control-plane/client.ts
    - apps/web-console/lib/viewer-gates.ts
    - apps/web-console/app/console/layout.tsx
    - apps/web-console/app/console/page.tsx
    - apps/web-console/app/console/members/page.tsx
    - apps/web-console/components/nav-shell.tsx
    - apps/web-console/components/verification-banner.tsx
    - apps/web-console/components/workspace-switcher.tsx
    - apps/web-console/app/console/account-switch/route.ts
    - apps/web-console/app/invitations/accept/page.tsx
    - apps/web-console/playwright.config.ts
    - apps/web-console/tests/unit/viewer-gates.test.ts (pre-existing RED phase, GREEN implemented)
    - apps/web-console/tests/e2e/auth-shell.spec.ts
  modified:
    - apps/web-console/vitest.config.ts (already existed, no changes needed)
decisions:
  - "WorkspaceSwitcher uses an HTML form POST to /console/account-switch so it works without client-side JS and avoids direct cookie mutation in a component."
  - "account-switch route validates account_id against viewer.memberships before persisting — prevents users from switching into unauthorized accounts."
  - "invitations/accept page does not set hive_account_id after joining — the new membership appears in the switcher and requires explicit selection per the plan spec."
  - "VerificationBanner renders based on viewer.user.email_verified === false in layout so it appears on every console route without per-page logic."
metrics:
  duration: "15min"
  completed_date: "2026-03-29"
  tasks_completed: 2
  files_created: 13
requirements:
  - AUTH-01
  - AUTH-02
  - AUTH-03
---

# Phase 02 Plan 04: Console Shell, Viewer Gates, and Workspace Switching Summary

**One-liner:** Verification-aware Next.js console shell with hive_account_id cookie persistence for explicit workspace switching and invitation acceptance without auto-switching.

## What Was Built

### Task 1: Verification-aware shell, viewer gates, and read-only members view

- **`lib/control-plane/client.ts`** — `getViewer()` fetches from `GET /api/v1/viewer` forwarding `Authorization: Bearer <token>` and `X-Hive-Account-ID: <cookie>` when the `hive_account_id` cookie is set. Also exposes `getMembers()` for the members roster.
- **`lib/viewer-gates.ts`** — `canInviteMembers(viewer)`, `canManageApiKeys(viewer)`, and `allowedUnverifiedRoutes` constant with exactly 4 entries.
- **`app/console/layout.tsx`** — Async Server Component that calls `getViewer()`, renders `VerificationBanner` (shown when `email_verified === false`), `WorkspaceSwitcher`, and nav links.
- **`app/console/page.tsx`** — Dashboard page showing workspace `display_name` and reminder copy for unverified users.
- **`app/console/members/page.tsx`** — Read-only roster by default; shows disabled invite button with helper text "Email verification is required before you can invite teammates." when `canInviteMembers(viewer)` is false.
- **`components/nav-shell.tsx`** — Navigation sidebar component.
- **`components/verification-banner.tsx`** — Warning banner rendered in layout when email is unverified.
- **`playwright.config.ts`** — Playwright E2E config targeting `localhost:3000` by default, configurable via `PLAYWRIGHT_BASE_URL`.
- **Unit tests** (9/9 passing): `canInviteMembers`, `canManageApiKeys`, `allowedUnverifiedRoutes` coverage.

### Task 2: Workspace switching and invitation acceptance

- **`components/workspace-switcher.tsx`** — Renders all `viewer.memberships` as `<option>` elements in a `<form method="POST" action="/console/account-switch">`. Marks current account. Auto-submits on change.
- **`app/console/account-switch/route.ts`** — POST handler that validates `account_id` exists in `viewer.memberships` before setting `hive_account_id` cookie and redirecting to `/console`. Does not touch Supabase session.
- **`app/invitations/accept/page.tsx`** — Reads `?token=` from search params, requires Supabase session, POSTs to `/api/v1/invitations/accept`, redirects to `/console/members?joined=1`. Does **not** set `hive_account_id`.
- **`tests/e2e/auth-shell.spec.ts`** — Three E2E test suites (all conditional on env vars): unverified members page locked, invitation acceptance keeps current workspace, workspace switcher persists selected account.

## Decisions Made

1. **WorkspaceSwitcher uses HTML form POST** (not fetch/XHR) — works without JS and keeps cookie mutation in the route handler where it belongs.
2. **account-switch validates against viewer.memberships** — prevents unauthorized account switching; falls back to `/console` on any error.
3. **Invitation acceptance does not change hive_account_id** — per spec, joining a workspace doesn't auto-switch context; the user chooses via the switcher.
4. **VerificationBanner lives in layout** — declared once, applies to all console routes without per-page repetition.

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check

- [x] `apps/web-console/lib/control-plane/client.ts` exists and contains `X-Hive-Account-ID`
- [x] `apps/web-console/lib/viewer-gates.ts` exists and contains `allowedUnverifiedRoutes`
- [x] `apps/web-console/app/console/page.tsx` contains `Dashboard`
- [x] `apps/web-console/app/console/members/page.tsx` contains `Email verification is required before you can invite teammates.`
- [x] `apps/web-console/components/workspace-switcher.tsx` contains `account_id`
- [x] `apps/web-console/app/console/account-switch/route.ts` contains `hive_account_id`
- [x] `apps/web-console/app/invitations/accept/page.tsx` contains `/api/v1/invitations/accept`
- [x] `apps/web-console/tests/e2e/auth-shell.spec.ts` contains `workspace switcher persists selected account`
- [x] `apps/web-console/tests/e2e/auth-shell.spec.ts` contains `accepting an invitation keeps current workspace until switcher changes it`
- [x] Unit tests: 9/9 passing
- [x] Task 1 commit: 6b62dae
- [x] Task 2 commit: 7721cda
