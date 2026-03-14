# Guest Session Attribution Implementation Plan

## APPROVED

- Approver: repository maintainer (chat approval)
- Approval date: 2026-03-13
- Approval artifact: maintainer approved the design and implementation sequence in chat before execution

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a server-trusted guest session with a mirrored browser-visible guest object, persist guest attribution data, and link guest usage to later authenticated accounts for conversion analysis.

**Architecture:** Introduce a dual guest-session model: a signed `httpOnly` guest cookie issued by `apps/web`, plus a browser-visible guest session object for UI state and analytics. Keep guest chat behind the web proxy, forward validated `guestId` and client IP to the internal API guest route, and persist guest attribution plus guest-to-user linkage in dedicated Supabase tables rather than authenticated `usage_events`.

**Tech Stack:** Next.js app router, Fastify API, TypeScript, Supabase/Postgres, Redis, Vitest.

---

## Tasks

## Task 1: Define failing tests for guest-session bootstrap and proxy requirements

**Files:**
- Modify: `apps/web/test/guest-chat-route.test.ts`
- Create: `apps/web/test/guest-session.test.ts`
- Test: `apps/web/test/guest-chat-route.test.ts`
- Test: `apps/web/test/guest-session.test.ts`

**Step 1: Add a failing test for guest-session bootstrap**

Require a web route that issues a guest session and returns a browser-visible guest object.

**Step 2: Add a failing test for guest chat requiring a guest session**

Guest chat route should reject when the signed guest session is missing or invalid.

**Step 3: Run targeted web tests to confirm failure**

Verify: `pnpm --filter @hive/web exec vitest run test/guest-chat-route.test.ts test/guest-session.test.ts`

## Task 2: Define failing API tests and persistence expectations for trusted guest identity

**Files:**
- Modify: `apps/api/test/routes/guest-chat-route.test.ts`
- Create: `apps/api/test/domain/guest-attribution.test.ts`
- Test: `apps/api/test/routes/guest-chat-route.test.ts`
- Test: `apps/api/test/domain/guest-attribution.test.ts`

**Step 1: Add a failing test for internal guest route requiring forwarded `guestId`**

The internal API route should reject requests that have the shared token but no validated forwarded guest identity.

**Step 2: Add a failing test for guest attribution persistence**

Require dedicated Supabase-backed guest attribution storage rather than storing guest activity as authenticated usage.

**Step 3: Run targeted API tests to confirm failure**

Verify: `pnpm --filter @hive/api exec vitest run test/routes/guest-chat-route.test.ts test/domain/guest-attribution.test.ts`

## Task 3: Add Supabase schema and runtime support for guest attribution

**Files:**
- Create: `supabase/migrations/<timestamp>_guest_attribution.sql`
- Create/Modify: guest attribution runtime store under `apps/api/src/runtime/`
- Test: `apps/api/test/domain/guest-attribution.test.ts`

**Step 1: Add guest attribution tables**

Create:

- `guest_sessions`
- `guest_usage_events`
- `guest_user_links`

with the minimum indexes and service-role access needed for the API runtime.

**Step 2: Add a runtime store for guest attribution**

Implement reads/writes for:

- session create/refresh
- guest usage writes
- guest-to-user link writes

**Step 3: Run targeted API tests**

Verify: `pnpm --filter @hive/api exec vitest run test/domain/guest-attribution.test.ts`

## Task 4: Implement guest-session bootstrap in the web app

**Files:**
- Create: `apps/web/src/app/api/guest-session/route.ts`
- Create/Modify: guest-session browser store under `apps/web/src/features/auth/` or `apps/web/src/features/chat/`
- Test: `apps/web/test/guest-session.test.ts`

**Step 1: Add guest-session issue/refresh route**

It should:

- mint or refresh a guest session
- set an `httpOnly` signed cookie
- return a browser-visible guest session object

**Step 2: Add guest-session browser store**

Mirror non-sensitive guest session data into browser storage for UI and analytics use.

**Step 3: Run targeted web tests**

Verify: `pnpm --filter @hive/web exec vitest run test/guest-session.test.ts`

## Task 5: Require guest session on the web guest chat route

**Files:**
- Modify: `apps/web/src/app/api/chat/guest/route.ts`
- Modify: `apps/web/src/features/chat/use-chat-session.ts`
- Test: `apps/web/test/guest-chat-route.test.ts`
- Test: `apps/web/test/chat-guest-mode.test.tsx`

**Step 1: Validate guest session before forwarding**

Guest chat should require the signed guest cookie and same-origin browser request.

**Step 2: Forward validated guest identity and client IP**

Forward:

- internal web token
- validated `guestId`
- caller IP

**Step 3: Bootstrap guest session when needed**

The guest-facing home flow should ensure a guest session exists before first guest chat submission.

**Step 4: Run targeted web tests**

Verify: `pnpm --filter @hive/web exec vitest run test/guest-chat-route.test.ts test/chat-guest-mode.test.tsx`

## Task 6: Enforce trusted forwarded guest identity in the API and record guest usage

**Files:**
- Create/Modify: guest attribution runtime/persistence under `apps/api/src/runtime/`
- Modify: `apps/api/src/routes/guest-chat.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/routes/guest-chat-route.test.ts`
- Test: `apps/api/test/domain/guest-attribution.test.ts`

**Step 1: Enforce forwarded guest identity on internal guest chat**

The internal API guest route should reject requests lacking validated forwarded `guestId`.

**Step 2: Record guest usage against guest attribution storage**

Write guest usage into `guest_usage_events`, not authenticated `usage_events`.

**Step 3: Run targeted API tests**

Verify: `pnpm --filter @hive/api exec vitest run test/routes/guest-chat-route.test.ts test/domain/guest-attribution.test.ts`

## Task 7: Link guest identity to authenticated users

**Files:**
- Modify: relevant auth/user route or runtime wiring in `apps/api/src/routes/` and `apps/api/src/runtime/`
- Test: `apps/api/test/domain/guest-attribution.test.ts`

**Step 1: Persist `guestId -> userId` linkage on signup/login or first authenticated handoff**

Make the linkage explicit and durable.

**Step 2: Add regression coverage for later payment-attribution readiness**

Ensure the linkage is queryable and stable.

**Step 3: Run targeted API tests**

Verify: `pnpm --filter @hive/api exec vitest run test/domain/guest-attribution.test.ts`

## Task 8: Update docs and run full verification

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`
- Modify: `docs/architecture/system-architecture.md`
- Modify: any relevant runbook/design references

**Step 1: Document guest session and attribution behavior**

Update docs to reflect:

- signed guest cookie
- browser-visible guest session object
- guest attribution persistence
- guest-to-user conversion linkage

**Step 2: Run full verification**

Verify:

- `pnpm --filter @hive/api test`
- `pnpm --filter @hive/api build`
- `pnpm --filter @hive/web test`
- `NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 NEXT_PUBLIC_SUPABASE_ANON_KEY=test-supabase-anon-key WEB_INTERNAL_GUEST_TOKEN=test-web-token pnpm --filter @hive/web build`

**Step 3: Extend smoke coverage if auth/guest conversion flow changes enough**

If needed:

- `pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`
