---
phase: 02-identity-account-foundation
plan: "03"
subsystem: web-console
tags:
  - next.js
  - supabase
  - auth
  - docker
  - ssr
dependency_graph:
  requires:
    - 02-01 (control-plane service and Docker wiring)
  provides:
    - apps/web-console Next.js app with Docker service on port 3000
    - Hosted Supabase auth pages (sign-in, sign-up, forgot-password, reset-password)
    - SSR session middleware for /console route protection
    - Auth callback route with constrained redirect targets
  affects:
    - 02-04 and beyond (console shell layers onto this foundation)
tech_stack:
  added:
    - next@15.1.0
    - react@19.0.0
    - "@supabase/ssr@^0.6.1"
    - "@supabase/supabase-js@^2.48.1"
    - vitest@^3.1.1
    - "@vitejs/plugin-react@^4.4.1"
    - "@playwright/test@^1.51.1"
  patterns:
    - Next.js App Router with SSR session refresh via Supabase SSR helpers
    - Constrained callback redirects (allowlist of /console, /auth/reset-password)
    - Docker Compose develop.watch for hot-reload in containers
key_files:
  created:
    - deploy/docker/Dockerfile.web-console
    - apps/web-console/package.json
    - apps/web-console/tsconfig.json
    - apps/web-console/vitest.config.ts
    - apps/web-console/.gitignore
    - apps/web-console/lib/supabase/browser.ts
    - apps/web-console/lib/supabase/server.ts
    - apps/web-console/middleware.ts
    - apps/web-console/app/page.tsx
    - apps/web-console/app/auth/sign-in/page.tsx
    - apps/web-console/app/auth/sign-up/page.tsx
    - apps/web-console/app/auth/forgot-password/page.tsx
    - apps/web-console/app/auth/reset-password/page.tsx
    - apps/web-console/app/auth/callback/route.ts
    - apps/web-console/__tests__/supabase-helpers.test.ts
    - apps/web-console/__tests__/auth-routes.test.ts
  modified:
    - deploy/docker/docker-compose.yml
    - deploy/docker/docker-compose.override.yml
decisions:
  - Middleware uses named export `middleware` (not default export) per Next.js App Router convention
  - Callback route uses an explicit allowlist (Set) for next= redirect targets rather than regex — simpler and less error-prone
  - apps/web-console/.gitignore negates root-level `lib/` Python gitignore entry so the Next.js lib/ source directory can be committed
  - Server helper accepts ReadonlyRequestCookies parameter rather than calling cookies() internally — keeps the helper testable without mocking Next.js internals
metrics:
  duration: "8min"
  completed_date: "2026-03-29"
  tasks_completed: 2
  files_created: 16
  files_modified: 2
  tests_added: 15
---

# Phase 02 Plan 03: Web Console Foundation Summary

**One-liner:** Next.js web-console app with Supabase SSR session helpers, hosted auth pages (sign-in/sign-up/forgot-password/reset-password/callback), Docker service on port 3000, and route-protecting middleware.

## What Was Built

A new `apps/web-console` Next.js App Router application that serves as the authenticated developer console entrypoint. The app uses hosted Supabase auth for all password flows and Supabase SSR helpers for persistent server-side session management.

### Task 1: web-console package, Docker service, and Supabase client helpers

Created the full package scaffold: `package.json` with `@supabase/ssr`, `@supabase/supabase-js`, `next`, `react`, `vitest`, and `@playwright/test`. Added `Dockerfile.web-console` (node:22-alpine, EXPOSE 3000) and wired a `web-console` service into `docker-compose.yml` (port 3000:3000, depends on control-plane). Added docker-compose.override.yml watch rules for hot reload. Implemented `lib/supabase/browser.ts` (createBrowserClient) and `lib/supabase/server.ts` (createServerClient with full SSR cookie handling including setAll try/catch for Server Component contexts).

**TDD:** 4 tests validating helper exports and env var wiring.

### Task 2: Auth routes, root redirect, and SSR session middleware

Implemented all hosted auth flows:
- `middleware.ts`: refreshes Supabase session on every non-static request, redirects unauthenticated `/console` requests to `/auth/sign-in`, redirects authenticated `/` visits to `/console`
- `app/auth/sign-in/page.tsx`: `signInWithPassword` form
- `app/auth/sign-up/page.tsx`: `signUp` with `emailRedirectTo: ${NEXT_PUBLIC_APP_URL}/auth/callback`
- `app/auth/forgot-password/page.tsx`: `resetPasswordForEmail` with `redirectTo: ${NEXT_PUBLIC_APP_URL}/auth/callback?next=/auth/reset-password`
- `app/auth/reset-password/page.tsx`: `updateUser({ password })` form
- `app/auth/callback/route.ts`: `exchangeCodeForSession` with allowlisted redirect targets (`/console`, `/auth/reset-password`); falls back to `/console` when `next` is missing or invalid
- `app/page.tsx`: SSR root redirect based on session state

**TDD:** 11 tests covering middleware exports, callback redirect logic (valid/invalid/missing next targets), and component exports.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Root .gitignore Python section blocks apps/web-console/lib/**

- **Found during:** Task 1 commit
- **Issue:** The root `.gitignore` contains `lib/` from a Python gitignore template (line 246), which caused `git add` to reject `apps/web-console/lib/supabase/*.ts`
- **Fix:** Created `apps/web-console/.gitignore` with `!lib/` to override the root-level exclusion for this subdirectory
- **Files modified:** `apps/web-console/.gitignore` (created)
- **Commit:** 3c5b77e

**2. [Rule 1 - Bug] Middleware test assumed default export but Next.js uses named export**

- **Found during:** Task 2 TDD GREEN phase
- **Issue:** Test asserted `mod.default` but Next.js App Router middleware must use a named export `middleware` — `mod.default` was undefined
- **Fix:** Updated test to check `mod.middleware` which matches Next.js convention
- **Files modified:** `apps/web-console/__tests__/auth-routes.test.ts`
- **Commit:** 8366629

## Commits

| Hash | Message |
|------|---------|
| 3c5b77e | feat(02-03): create web-console package, Docker service, and Supabase client helpers |
| 8366629 | feat(02-03): add auth routes, root redirect, and SSR session middleware |

## Self-Check: PASSED

All 12 key files confirmed present. Both commits (3c5b77e, 8366629) found in git log. 15/15 tests passing.
