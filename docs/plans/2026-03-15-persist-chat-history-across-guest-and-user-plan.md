# Persisted Chat History Across Guest → User Link Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure guest chat sessions are always persisted, keep `guest_id` for attribution when sessions are linked to a user, and make previously guest-only conversations appear correctly in the authenticated user’s persisted chat history while disappearing from the guest view.

**Architecture:** Keep the existing Supabase-backed chat history model, update the claim/link semantics so `guest_id` is retained while `user_id` is added, and adjust the web client’s session loading to be auth-scope-aware and to reload authenticated sessions after a successful guest→user link. All behavior change is done with focused, minimal deltas to the current store, API, and `useChatSession` hook.

**Tech Stack:** TypeScript, Supabase (PostgreSQL) via `@supabase/supabase-js`, Fastify API (`apps/api`), Next.js App Router web (`apps/web`), Playwright for E2E.

---

## File Map

- **API / persistence**
  - Modify: `apps/api/src/runtime/supabase-chat-history-store.ts`
    - Update `claimGuestSessionsForUser` so it sets `user_id` but does not clear `guest_id`.
    - (If needed) adjust guest listing query to only show unlinked guest sessions (`guest_id` set, `user_id` null).
  - Read/verify only (no structural changes expected):
    - `apps/api/src/runtime/chat-history-service.ts`
    - `apps/api/src/routes/guest-attribution.ts`
    - `apps/api/src/routes/guest-chat-sessions.ts` (or equivalent guest sessions route).
  - Tests:
    - Add/modify tests under `apps/api/test/**` for chat history store and linking behavior.

- **Web / client**
  - Modify: `apps/web/src/features/chat/use-chat-session.ts`
    - Make authenticated session loading auth-scope-aware.
    - Add a small mechanism to reload authenticated sessions after a successful guest→user link.
  - Read/verify only:
    - `apps/web/src/app/api/guest-session/link/route.ts`
    - Any helper where guest link is invoked from the UI (sign-up / login flow).
  - Tests:
    - Add/extend tests for `useChatSession` (location depends on existing test layout).
    - E2E: `apps/web/e2e/smoke-auth-chat-billing.spec.ts`.

- **Docs**
  - Already added design:
    - `docs/design/active/2026-03-15-persist-chat-history-across-guest-and-user-design.md`
  - This implementation plan:
    - `docs/plans/2026-03-15-persist-chat-history-across-guest-and-user-plan.md`

---

## Chunk 1: API Store Semantics for Guest → User Claim

### Task 1: Add/Extend Tests for Claiming Guest Sessions

**Files:**
- Test: `apps/api/test/**` (choose or create a focused test file for `SupabaseChatHistoryStore`, e.g. `apps/api/test/runtime/supabase-chat-history-store.test.ts`)

- [ ] **Step 1: Add a unit/integration test for `claimGuestSessionsForUser` retaining `guest_id`**

  - Write a test that:
    - Inserts a `chat_sessions` row with:
      - `id = 'test_session_1'`
      - `guest_id = 'guest-123'`
      - `user_id = null`
    - Calls `store.claimGuestSessionsForUser('guest-123', 'user-abc')`.
    - Asserts that:
      - `chat_sessions` row now has `user_id = 'user-abc'`.
      - `guest_id` is still `'guest-123'`.

- [ ] **Step 2: Add a test for guest listing semantics (guest-only sessions)**

  - In the same or nearby test:
    - Insert two sessions:
      - `session_guest_only`: `guest_id = 'guest-123'`, `user_id = null`.
      - `session_linked`: `guest_id = 'guest-123'`, `user_id = 'user-abc'`.
    - Call the store method used for guest listing (e.g. `listSessionsForGuest('guest-123')`).
    - Assert that **only** `session_guest_only` appears in the returned list.

- [ ] **Step 3: Run the focused API tests**

  - Run:
    - `pnpm --filter @hive/api test -- supabase-chat-history-store`  
      (Adjust test file path/name as needed.)
  - Expected: new tests **fail** because current implementation clears `guest_id` and/or includes linked sessions in guest listing.

### Task 2: Implement Store Changes for Claim and Guest Listing

**Files:**
- Modify: `apps/api/src/runtime/supabase-chat-history-store.ts`

- [ ] **Step 1: Update `claimGuestSessionsForUser` implementation**

  - Change the Supabase update so that:
    - It sets `user_id = userId`.
    - It **does not** set `guest_id` to `null`.
  - The final query should conceptually correspond to:
    - `update chat_sessions set user_id = userId where guest_id = guestId;`

- [ ] **Step 2: Ensure guest listing excludes linked sessions**

  - In `listSessionsForGuest(guestId)`:
    - Make sure the query filters on:
      - `.eq("guest_id", guestId)`
      - `.is("user_id", null)` (or equivalent).
  - This ensures that once a session has `user_id` set, it will not be returned to the guest listing API.

- [ ] **Step 3: Re-run the focused API tests**

  - Same command as above:
    - `pnpm --filter @hive/api test -- supabase-chat-history-store`
  - Expected: tests now **pass**, confirming:
    - `guest_id` retention on claim.
    - Guest listing excludes linked sessions.

- [ ] **Step 4: Run full API test suite for safety**

  - Run:
    - `pnpm --filter @hive/api test`
  - Expected: all tests pass.

- [ ] **Step 5: Commit API-side changes**

  - Suggested message:
    - `git add apps/api/src/runtime/supabase-chat-history-store.ts apps/api/test/...`
    - `git commit --no-gpg-sign -m "fix(api): retain guest attribution when claiming chat sessions"`

---

## Chunk 2: Web Auth-Scope-Aware Session Loading and Link Refresh

### Task 3: Add/Extend Tests for Web Session Loading Behavior

**Files:**
- Test: existing or new tests around `useChatSession`, e.g. under `apps/web/src/features/chat/__tests__/use-chat-session.test.ts` (adjust to match repo layout).

- [ ] **Step 1: Add a test for authenticated session load per auth scope**

  - Write a test that simulates:
    - First render with `authReady = true`, `guestMode = false`, `accessToken` set, `authScopeKey = "session:user@example.com"`.
    - Confirms that the logic to fetch `/v1/chat/sessions` runs once for that scope.
    - Subsequent renders with the same scope **do not** trigger another load.

- [ ] **Step 2: Add a test for guest→user transition triggering reload**

  - In the same test file:
    - Start in guest mode (no access token, `guestMode = true`), load guest sessions.
    - Then simulate:
      - `authReady = true`, `guestMode = false`, `accessToken` set, `authScopeKey = "session:user@example.com"`.
      - A successful call to `/api/guest-session/link`.
    - Assert that:
      - A reload of `/v1/chat/sessions` is triggered once after link.
      - The resulting conversations replace the existing in-memory list.

- [ ] **Step 3: Run focused web tests**

  - Run:
    - `pnpm --filter @hive/web test -- use-chat-session`  
      (Adjust to the actual test file.)
  - Expected: tests fail until implementation is updated.

### Task 4: Implement Auth-Scope-Aware Loading and Link Reload

**Files:**
- Modify: `apps/web/src/features/chat/use-chat-session.ts`
- Read/verify: `apps/web/src/app/api/guest-session/link/route.ts` and any UI hook/call site that invokes it.

- [ ] **Step 1: Introduce `lastLoadedAuthScope` tracking**

  - In `useChatSession`, add a `useRef<string | null>` that tracks the last auth scope for which authenticated sessions were loaded (e.g. `lastLoadedAuthScopeRef`).

- [ ] **Step 2: Update authenticated sessions `useEffect` to use auth scope**

  - In the authenticated sessions effect (currently gated by `sessionsLoadedRef.current`):
    - Replace the global boolean gate with logic that:
      - Early-returns if:
        - `!authReady`, or `guestMode`, or missing `accessToken`.
      - Otherwise:
        - If `lastLoadedAuthScopeRef.current === authScopeKey`, return (already loaded for this scope).
        - Else:
          - Set `lastLoadedAuthScopeRef.current = authScopeKey`.
          - Run the existing load logic for `/v1/chat/sessions` and details.
    - Ensure this effect still dispatches `sessionsLoaded` with the constructed `ChatConversation[]` and replaces the conversations list.

- [ ] **Step 3: Add a helper to force reload after link**

  - In `useChatSession`, add a small helper (e.g. `reloadAuthenticatedSessions`) that:
    - Sets `lastLoadedAuthScopeRef.current = null` (or a distinct value) when in authenticated mode.
    - Causes the authenticated sessions effect to run once on the next render and reload `/v1/chat/sessions`.
  - Wire this to the place where the client handles a successful `/api/guest-session/link` response:
    - If that handling is in `useChatSession` already, call the helper directly.
    - If it’s in another hook or component, expose a function from `useChatSession` that consumers can call after link success.

- [ ] **Step 4: Ensure guest listing hides linked sessions**

  - Confirm that the guest-side fetching logic in `useChatSession` (guest sessions effect) consumes an API that now returns only `guest_id` rows with `user_id` null.
  - No extra client changes should be necessary beyond trusting the API contract; just verify that behavior lined up with Chunk 1.

- [ ] **Step 5: Re-run focused web tests**

  - Run:
    - `pnpm --filter @hive/web test -- use-chat-session`
  - Expected: newly added tests now pass.

- [ ] **Step 6: Run web lint and targeted E2E smoke**

  - Run:
    - `pnpm --filter @hive/web lint`
    - `pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`
  - Expected:
    - Lint passes.
    - Smoke spec passes, and:
      - Guest chat still works.
      - After signup/login, prior guest conversation appears in user history.

- [ ] **Step 7: Commit web-side changes**

  - Suggested message:
    - `git add apps/web/src/features/chat/use-chat-session.ts apps/web/**/__tests__/**`
    - `git commit --no-gpg-sign -m "fix(web): reload persisted chat after guest link"`

---

## Chunk 3: Final Verification and Cleanup

### Task 5: Full-Stack Verification

**Files/Commands:**
- No new file edits; commands only.

- [ ] **Step 1: Run full API and web tests together**

  - Run:
    - `pnpm --filter @hive/api test`
    - `pnpm --filter @hive/web lint`
    - `pnpm --filter @hive/web test`

- [ ] **Step 2: Run Docker-local smoke if appropriate**

  - If this change affects anything around guest/auth flows (it does), follow the repo’s Docker-local smoke guidance:
    - Start stack:
      - `pnpm stack:dev` (or equivalent documented command).
    - Verify readiness:
      - `curl -s http://127.0.0.1:8080/health`
      - `curl -sI http://127.0.0.1:3000/auth`
    - Run:
      - `pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`

- [ ] **Step 3: Manual sanity check in browser (optional but recommended)**

  - With the dev stack running:
    - Open `http://127.0.0.1:3000/`.
    - As a guest, send at least one chat message, confirm it appears in “previous guest chats”.
    - Sign up or log in, ensuring the guest→user link is triggered.
    - Confirm:
      - The prior guest conversation disappears from any guest-only view.
      - The same conversation now appears in the user’s persisted chat list.

- [ ] **Step 4: Final commit if additional tweaks were needed**

  - If any final adjustments were made during verification:
    - `git add ...`
    - `git commit --no-gpg-sign -m "chore: finalize guest-to-user chat history linking"`

---

## Completion Criteria

- Guest chat sessions are always stored in DB with `guest_id`.
- After calling the link endpoint:
  - `chat_sessions` rows for that guest gain `user_id` but retain `guest_id`.
  - Guest listing returns only unlinked guest sessions (`guest_id` set, `user_id` null).
  - Authenticated listing returns all sessions for the user, including those that began as guest.
- `useChatSession`:
  - Loads authenticated sessions once per auth scope.
  - Reloads authenticated sessions after a successful guest→user link.
  - Replaces the in-memory conversations list with the server-backed authenticated sessions on reload.
- All targeted unit and integration tests pass.
- Web lint and smoke E2E (`smoke-auth-chat-billing`) pass, confirming guest-first and auth flows are intact.

