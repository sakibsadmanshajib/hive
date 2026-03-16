# Persisted Chat History Across Guest → User Link

## Context

Hive’s web chat supports a guest-first experience on `/`, where users can chat without logging in. Guest chat sessions are:

- Backed by a server-trusted guest session (cookie + internal API).
- Persisted in Supabase under a `guest_id` via `SupabaseChatHistoryStore`.
- Later, when a guest signs up or logs in, an internal link flow (`/v1/internal/guest/link`) associates the guest identity with a `userId` and calls `claimGuestSessionsForUser`.

Today:

- Guest sessions are persisted, and `claimGuestSessionsForUser` re-keys them from `guest_id` to `user_id`.
- The web UI loads:
  - Guest sessions via guest routes and presents them as “guest previous chats”.
  - User sessions via `/v1/chat/sessions` and `/v1/chat/sessions/:id` into the chat reducer.
- However, after link:
  - The backend correctly associates sessions to the user.
  - The frontend does not reliably reload those now-user-owned sessions, so prior guest history often does not appear in the logged-in user’s chat list.

We also want to preserve `guest_id` as an attribution identity for analytics and funnel analysis, even after sessions are claimed.

## Goals

1. Persist guest chat sessions in DB for all guest conversations.
2. On link, associate existing guest sessions with the authenticated user without losing attribution:
   - Sessions that started as guest should be visible in the user’s persisted chat history.
   - Linked sessions should no longer appear in the “guest previous chats” UI.
   - `guest_id` must remain on those rows for analytics and attribution.
3. Frontend should:
   - After a successful link, show all now-user-owned sessions (including those that began as guest) in the authenticated user’s conversation list.
   - Avoid duplicating sessions across guest and user views.
4. Keep behavior compatible with existing auth + guest session design, and avoid regressions in the smoke-auth-chat-billing flow.

## Current Behavior (Observed)

### Data model / store

- `SupabaseChatHistoryStore` uses the `chat_sessions` table:

  - `createSession` inserts rows with:
    - `user_id` set for authenticated sessions.
    - `guest_id` set for guest sessions.
  - `listSessionsForUser(userId)`:
    - `select ... from chat_sessions where user_id = userId order by updated_at desc`.
  - `listSessionsForGuest(guestId)`:
    - `select ... from chat_sessions where guest_id = guestId order by updated_at desc`.
  - `getSessionForUser` and `getSessionForGuest` enforce ownership via `user_id` or `guest_id`.
  - `claimGuestSessionsForUser(guestId, userId)` currently:
    - `update chat_sessions set user_id = userId, guest_id = null where guest_id = guestId`.

- `PersistentChatHistoryService` delegates:
  - `listSessions`, `getSession` → `listSessionsForUser`, `getSessionForUser`.
  - Guest equivalents for guest flows.
  - `claimGuestSessionsForUser(guestId, userId)` forwards into the store.

### API routes

- Guest attribution (`/v1/internal/guest/session`, `/v1/internal/guest/link`):
  - `/v1/internal/guest/session` persists/refreshes guest session metadata.
  - `/v1/internal/guest/link`:
    - Validates internal web guest token + guest headers.
    - Authenticates the principal user.
    - Calls:
      - `services.users.linkGuest(guestId, principal.userId, "auth_session")`
      - `services.chatHistory.claimGuestSessionsForUser(guestId, principal.userId)`.

- Guest chat sessions routes use `listSessionsForGuest` / `getSessionForGuest` to provide guest history.
- Authenticated chat sessions routes use `listSessionsForUser` / `getSessionForUser`.

### Web client

- `useChatSession` manages chat state and model options.
- Guest session loading:
  - Uses guest-specific routes and guest session headers.
  - On success, dispatches `sessionsLoaded` to populate guest conversations.
- Authenticated session loading:

  - `useEffect` with deps `[authReady, guestMode, accessToken]`:
    - If not ready, guest, or missing token, or if `sessionsLoadedRef.current` is already `true`, it returns early.
    - Otherwise:
      - Fetches `/v1/chat/sessions` (list).
      - For up to 20 sessions, fetches `/v1/chat/sessions/:id` to get messages.
      - Builds `ChatConversation[]` and dispatches `sessionsLoaded`.

- `sessionsLoadedRef` is a single boolean per page lifecycle, not keyed by auth scope. Once it is set, the authenticated sessions effect will not run again, even if the guest is later linked to a user.

### Net effect

- Guest chats are persisted and retrievable by API.
- Linking re-keys DB rows from `guest_id` to `user_id`, but:
  - `guest_id` is lost.
  - The web client may not reload `/v1/chat/sessions` for the now-authenticated user after link, so the user does not see those sessions in their history.

## Proposed Behavior

### Data model and persistence

1. **Guest chat persistence (unchanged core)**

   - All guest chat sessions continue to be stored in `chat_sessions` with:
     - `guest_id = <guestId>`
     - `user_id = null`

   - Guest chat messages remain in `chat_messages` with `session_id` pointing to the corresponding session row.

2. **On link, keep `guest_id` and add `user_id`**

   - Adjust `SupabaseChatHistoryStore.claimGuestSessionsForUser(guestId, userId)` to:
     - `update chat_sessions set user_id = userId where guest_id = guestId;`

   - Do not null out `guest_id`. After link:
     - `guest_id = <guestId>`
     - `user_id = <userId>`

   - This preserves the original guest identity for analytics and attribution, while clearly indicating ownership by the user.

3. **Session classification**

   - Semantics:
     - Guest-only chats: `guest_id` set, `user_id` null.
     - User chats (including originally guest sessions): `user_id` set.
       - Some user chats will also have `guest_id` — those are “guest-originated” sessions.

4. **API listing semantics**

   - Authenticated user listing (`/v1/chat/sessions`):
     - Continue to use `listSessionsForUser(userId)`:
       - `where user_id = userId`.
     - This includes:
       - Sessions the user started while logged in.
       - Sessions that started as guest and were later linked (now have `user_id` as well).

   - Guest listing for “previous guest chats”:
     - Define guest listing semantics as:
       - Only sessions with `guest_id = guestId AND user_id IS NULL`.
     - This means:
       - Once a session is linked to a user, it disappears from the guest listing and is considered user-owned from the UI’s point of view.

### Web client behavior

1. **Guest phase**

   - Guest sessions continue to load via existing guest APIs and are shown in the UI as guest previous chats.
   - These represent rows with `guest_id` set and `user_id` null.

2. **Login + link phase**

   - User signs up or logs in (Supabase/auth flow).
   - Browser calls `/api/guest-session/link`:
     - Ensures:
       - Valid `WEB_INTERNAL_GUEST_TOKEN`.
       - Same-origin browser request.
       - `Authorization` header present.
       - Guest cookie is parsed into `guestId`.
     - Forwards to API `/v1/internal/guest/link` with:
       - `authorization`.
       - `x-web-guest-token`.
       - `x-guest-id`.

   - API route:
     - Authenticates user.
     - Links guest → user.
     - Calls `claimGuestSessionsForUser`, which now adds `user_id` and keeps `guest_id`.
     - Returns a success response.

3. **Post-link frontend refresh**

   - Once link succeeds, the client performs a single refresh of authenticated chat sessions, and guest listing naturally stops returning linked sessions.

   - `useChatSession` changes:

     - Introduce an auth-scope-aware loading mechanism:
       - Keep using `authScopeKey` (`guest`, `session:<identity>`, `booting`).
       - Track `lastLoadedAuthScope` in a ref.

     - Authenticated sessions effect:
       - Runs when:
         - `authReady` is true.
         - `guestMode` is false.
         - `accessToken` is present.
       - Only loads if `lastLoadedAuthScope !== authScopeKey`, then sets `lastLoadedAuthScope = authScopeKey`.
       - Fetches `/v1/chat/sessions` + details exactly as today.
       - Dispatches `sessionsLoaded` with the resulting conversations, replacing the current conversation list.

     - Expose or internalize a helper that can be invoked after link success to:
       - Ensure we treat the current auth scope as “not yet loaded”.
       - Trigger one run of the authenticated session loading sequence.

   - Link-success behavior:
     - When `/api/guest-session/link` resolves successfully:
       - Ensure we are in authenticated mode (`authReady`, `guestMode === false`, token present).
       - Trigger a reload of sessions for the current user:
         - Either by resetting `lastLoadedAuthScope` (for the current scope) or directly invoking the loading logic.
       - This re-fetches `/v1/chat/sessions`, whose result now includes all sessions with `user_id = userId`, including sessions just linked from guest.

   - UI effect:
     - Guest previous chats:
       - Based on guest listing that only returns `user_id IS NULL` rows.
       - Previously guest-only sessions that have been linked now drop out of this list.
     - Authenticated user history:
       - Shows all sessions with `user_id = userId`, including linked sessions.
       - Guest-originated sessions are now treated as normal user sessions in the UI.

4. **In-memory conversation behavior**

   - On the post-link reload:
     - The client replaces the in-memory conversations list with the server-backed authenticated conversations.
   - Implication:
     - If the user had unsent or just-created messages in a purely in-memory guest conversation that were not yet persisted, those may disappear at the moment of link.
     - Given the link typically happens around signup/login and sessions are persisted at send-time, this is acceptable and simpler than trying to merge partial state.

## Error Handling and Edge Cases

- Link failure:
  - If `/api/guest-session/link` fails (network error, 4xx/5xx from API):
    - Do not trigger a reload of authenticated sessions.
    - Leave guest sessions and UI as-is.
    - Optionally surface a non-blocking error message (“We couldn’t attach your previous guest chats yet”).

- Multiple logins or account switching:
  - Because authenticated sessions loading is keyed to `authScopeKey`:
    - When a user logs out and in as someone else, the scope changes from `session:<old>` to `session:<new>`.
    - The authenticated load effect runs once for the new scope and fetches only that user’s sessions.

- Performance:
  - Preserve:
    - The cap of 20 sessions when fetching details.
    - A single additional `/v1/chat/sessions` reload at link time.
  - No change to guest chat message flow or provider calls.

- Analytics and attribution:
  - Because `guest_id` remains on linked sessions:
    - Later reporting can:
      - Join guest usage events and payment or user lifecycle data.
      - Understand the guest journey up to signup and beyond.

## Testing Strategy

1. Unit and integration (API):
   - Tests for `SupabaseChatHistoryStore.claimGuestSessionsForUser`:
     - After linking, sessions retain `guest_id` and gain `user_id = userId`.
   - Guest listing:
     - Returns only `guest_id = guestId AND user_id IS NULL`.
   - Authenticated listing:
     - Returns sessions with `user_id = userId`, including those that had `guest_id` before link.

2. Web behavior tests:
   - Guest → login + link → post-link:
     - Before link: guest previous chats show N sessions.
     - After link:
       - Guest previous chats no longer show those linked sessions.
       - Authenticated conversation list shows those sessions.

3. Regression checks:
   - Run:
     - `pnpm --filter @hive/api test`
     - `pnpm --filter @hive/web lint`
     - `pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`
   - Ensure no regressions in:
     - Existing authenticated chat history.
     - Guest chat bootstrap and messaging.
     - Auth flows and redirect behavior.

## Summary

- Guest chats are persisted under `guest_id` and remain so.
- Linking adds `user_id` while keeping `guest_id`, enabling durable attribution.
- Guest UI shows only unlinked guest sessions; linked sessions move exclusively into the user history UI.
- The web client explicitly reloads authenticated sessions once after a successful link, ensuring the user sees their prior guest conversations merged into their normal persisted chat history.

