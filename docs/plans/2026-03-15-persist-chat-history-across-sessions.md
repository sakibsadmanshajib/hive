## Goal
Persist guest and authenticated chat sessions/messages server-side so the web sidebar and active transcript survive reloads, guest-to-user linking, and later authenticated sessions without weakening the existing guest/web security boundary or current billing behavior.

## Assumptions
- This plan artifact was written directly to `docs/plans/2026-03-15-persist-chat-history-across-sessions.md` because `.agent/skills/superpowers-workflow/scripts/write_artifact.py` is unavailable in this repository.
- Keep `POST /v1/chat/completions` OpenAI-compatible for API-product callers; add dedicated session/history routes for the web product instead of overloading the current public completion payload.
- Server persistence in Supabase/Postgres becomes the source of truth for transcripts; the web reducer remains an optimistic/hydration layer, not the durable store.
- Guest-owned sessions are keyed by `guest_id`; successful guest linking claims those sessions for `user_id` without copying or dropping message rows.

## Plan
1. Files: `apps/api/test/domain/chat-history-store.test.ts`, `apps/api/test/routes/chat-sessions-route.test.ts`, `apps/api/test/routes/guest-chat-sessions-route.test.ts`, `apps/web/test/chat-history-persistence.test.tsx`, `apps/web/test/guest-chat-history-route.test.ts`
   Change: Add failing tests that define the required durable behavior for session creation, message persistence, session listing, guest reloads, authenticated reloads, and guest-to-user history claiming.
   Verify: `pnpm --filter @hive/api exec vitest run test/domain/chat-history-store.test.ts test/routes/chat-sessions-route.test.ts test/routes/guest-chat-sessions-route.test.ts`
2. Files: `supabase/migrations/<timestamp>_chat_history.sql`, `apps/api/src/runtime/supabase-chat-history-store.ts`, `apps/api/src/domain/types.ts`, `apps/api/test/domain/chat-history-store.test.ts`
   Change: Add `chat_sessions` and `chat_messages` tables plus a Supabase store for create/list/read/append/claim operations, with ownership columns/indexes that support guest sessions first and later authenticated claims.
   Verify: `pnpm --filter @hive/api exec vitest run test/domain/chat-history-store.test.ts`
3. Files: `apps/api/src/runtime/services.ts`, `apps/api/src/routes/chat-sessions.ts`, `apps/api/src/routes/index.ts`, `apps/api/test/routes/chat-sessions-route.test.ts`, `packages/openapi/openapi.yaml`
   Change: Add authenticated session-history endpoints for list/create/read/send so the web product can hydrate and continue prior conversations without routing transcript writes through the public API-product completion contract.
   Verify: `pnpm --filter @hive/api exec vitest run test/routes/chat-sessions-route.test.ts`
4. Files: `apps/api/src/routes/guest-chat-sessions.ts`, `apps/api/src/routes/guest-attribution.ts`, `apps/api/src/runtime/services.ts`, `apps/api/test/routes/guest-chat-sessions-route.test.ts`, `apps/api/test/routes/guest-attribution-route.test.ts`
   Change: Add internal guest session-history endpoints behind `WEB_INTERNAL_GUEST_TOKEN`, then extend guest linking so it claims guest-owned chat sessions into the authenticated user as part of the same server-side link flow.
   Verify: `pnpm --filter @hive/api exec vitest run test/routes/guest-chat-sessions-route.test.ts test/routes/guest-attribution-route.test.ts`
5. Files: `apps/api/src/runtime/services.ts`, `apps/api/test/domain/runtime-chat-billing.test.ts`, `apps/api/test/routes/chat-sessions-route.test.ts`, `apps/api/test/routes/guest-chat-sessions-route.test.ts`
   Change: Persist user and assistant messages, session titles, and `updated_at` during reply generation while preserving current credit charging, usage events, provider headers, and guest rate limiting semantics.
   Verify: `pnpm --filter @hive/api exec vitest run test/domain/runtime-chat-billing.test.ts test/routes/chat-sessions-route.test.ts test/routes/guest-chat-sessions-route.test.ts`
6. Files: `apps/web/src/app/api/chat/guest/sessions/route.ts`, `apps/web/src/app/api/chat/guest/sessions/[sessionId]/route.ts`, `apps/web/src/app/api/chat/guest/sessions/[sessionId]/messages/route.ts`, `apps/web/test/guest-chat-history-route.test.ts`, `apps/web/test/guest-chat-route.test.ts`
   Change: Add same-origin guest-history proxy routes so guest sidebar hydration and message sends keep the existing web-app boundary and server-only guest token handling.
   Verify: `pnpm --filter @hive/web exec vitest run test/guest-chat-history-route.test.ts test/guest-chat-route.test.ts`
7. Files: `apps/web/src/app/chat/chat-types.ts`, `apps/web/src/app/chat/chat-reducer.ts`, `apps/web/src/features/chat/use-chat-session.ts`, `apps/web/src/app/page.tsx`, `apps/web/test/chat-history-persistence.test.tsx`, `apps/web/test/chat-guest-mode.test.tsx`, `apps/web/test/chat-auth-gate.test.tsx`
   Change: Replace the local-only seeded conversation flow with persisted history hydration, server-backed session creation, persisted message sends, and refetch/reselection behavior across guest reloads and post-login auth transitions.
   Verify: `pnpm --filter @hive/web exec vitest run test/chat-history-persistence.test.tsx test/chat-guest-mode.test.tsx test/chat-auth-gate.test.tsx`
8. Files: `apps/web/e2e/smoke-auth-chat-billing.spec.ts`, `README.md`, `CHANGELOG.md`, `docs/architecture/system-architecture.md`, `docs/runbooks/active/web-e2e-smoke.md`
   Change: Update smoke coverage and documentation so Docker-local verification proves transcript persistence after reload and guest-to-user conversion, and the docs reflect the new durable chat-history architecture.
   Verify: `pnpm --filter @hive/api test && pnpm --filter @hive/api build && NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 NEXT_PUBLIC_SUPABASE_ANON_KEY=test-supabase-anon-key pnpm --filter @hive/web build && pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`

## Verification: built-in browser flow

Flow verification **must** use the built-in browser (Cursor IDE browser / MCP):

1. **Stack**: `docker compose up --build -d`; confirm API health and web `/auth` (or `/`) respond.
2. **Guest flow**: Navigate to the web app, ensure guest mode is active, send a chat message, confirm user + assistant messages appear in the UI.
3. **Persistence (after step 7)**: Reload the page and confirm the same conversation (and sidebar entry) appears from server-backed history.
4. **Guest-to-user (optional)**: Sign in after guest chat, confirm guest sessions are claimed and visible for the authenticated user.

Run this flow in the built-in browser as part of completion check for steps 6–8, not only unit/E2E tests.

## Debugging guest chat 404 (list returns sessions, get by id returns 404)

If after reload the list returns 200 with sessions but GET `/v1/internal/guest/chat/sessions/:sessionId` returns 404:

1. **Compare guest_id on list vs get**
   - The API logs a debug line for list: `guest chat list` with `guestId`, `sessionCount`, and `sessionIds`.
   - It logs for get: `guest chat get not found` or `guest chat get ok` with `guestId` and `sessionId`.
   - Run the API with a log level that shows debug (e.g. set `LOG_LEVEL=debug` in the API env if supported; Fastify default is often `info` so debug lines may not appear otherwise). Reload the page and inspect API logs. Confirm the `guestId` on the list request matches the `guestId` on the get request. If they differ, the web proxy or cookie is sending a different guest id for the detail request.

2. **Check the row in the database**
   - Connect to the same Postgres the API uses (e.g. Supabase local: `psql` to port 54322 or Supabase Studio).
   - Run: `SELECT id, guest_id, title, updated_at FROM public.chat_sessions ORDER BY updated_at DESC LIMIT 5;`
   - Confirm the session id from the list response exists and its `guest_id` matches the `guestId` you see in the API log for the get request. If the row’s `guest_id` differs from the get request’s `guestId`, that explains the 404 (e.g. cookie changed between create and reload).

3. **Check cookie vs header**
   - In the browser, after reload, open DevTools → Application → Cookies and note the guest-session cookie (name/value) if present.
   - In the API logs, note the `x-guest-id` (and optionally `x-web-guest-token`) received on the list and get requests. If the web proxy derives guest id from the cookie, a missing or changed cookie after reload would cause a different or empty guest id.

4. **Supabase embed (only if 1–3 match)**
   - If list and get use the same `guestId` and the DB row has that `guest_id`, but get still 404s, the store’s `getSessionForGuest` might be failing for another reason (e.g. PostgREST embed `messages:chat_messages(...)` or RLS). Temporarily change the store to a simple select without the messages embed; if get then returns 200, the embed or relation name is the cause.

## Risks & mitigations
- Risk: Transcript persistence accidentally changes the API-product `/v1/chat/completions` contract. Mitigation: keep session/history behavior on dedicated web endpoints and leave existing public completion payloads untouched.
- Risk: Guest-linking claims attribution metadata but misses chat transcripts. Mitigation: add explicit store-level and route-level tests that assert sessions become visible under the authenticated user immediately after link.
- Risk: Web hydration regresses into login redirects or blank chat state during auth bootstrap. Mitigation: preserve the current auth-ready gate and test guest reload, authenticated reload, and transition-from-guest flows in both unit and smoke coverage.
- Risk: Retried sends duplicate user messages. Mitigation: keep message creation inside server-managed session routes and add tests around append ordering/idempotent session updates before claiming completion.

## Rollback plan
- Revert the new chat-history routes, store wiring, and web hydration changes together so the app falls back to the current local-only reducer behavior.
- If the migration has not shipped, remove the `chat_sessions` and `chat_messages` migration before merge; if it has shipped, add a follow-up rollback migration instead of editing applied migration history.
- If smoke verification exposes auth or guest regression risk late, disable the persisted-history client path in the branch and keep the server schema/routes out of the release until the hydration path is corrected.
