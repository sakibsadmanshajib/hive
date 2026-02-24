# Auth-First Chat Entry Design

## Problem

The current `/chat` page mixes registration, login, API key setup, and chatting in one view. This creates a broken first-run flow and makes the chat UI feel crowded and low-trust.

## Goals

- Move authentication to a dedicated entry screen, similar to ChatGPT/Abacus-style onboarding.
- Redirect unauthenticated users away from `/chat` to the auth entry page.
- Keep Google sign-in available in the new auth entry.
- Remove the in-chat "Session setup" card and let chat focus on conversations/messages.

## Non-Goals

- No backend auth contract changes.
- No billing API behavior changes.
- No role/permissions redesign.

## UX Flow

1. User lands on `/auth`.
2. User chooses login/register (and can use Google sign-in on login panel).
3. On successful auth, client stores API key and basic user snapshot.
4. User is redirected to `/chat`.
5. `/chat` renders only chat workspace; if auth is missing, it redirects to `/auth`.

## Technical Design

### Auth state

- Introduce a small client-side auth store in `apps/web/src/features/auth`.
- Persist auth payload in `localStorage`.
- Expose utility functions/hook for reading, writing, and clearing auth state.

### New auth route

- Add `apps/web/src/app/auth/page.tsx`.
- Build two forms (register, login) with consistent card layout.
- Include existing `GoogleLoginButton` on the auth page.
- Reuse current API endpoints:
  - `POST /v1/users/register`
  - `POST /v1/users/login`

### Chat route guard

- Update `apps/web/src/app/chat/page.tsx` to:
  - read auth state from store,
  - redirect to `/auth` when missing,
  - remove session setup UI,
  - continue using same message/reducer/chat components.

### Styling polish

- Keep established visual language but simplify hierarchy:
  - focused auth surface,
  - fewer nested borders in chat top area,
  - maintain responsive behavior and current shells.

## Error Handling

- Keep toast-based feedback for auth failures and chat failures.
- Show concise inline auth status on auth page.
- Preserve existing chat error rendering in message area.

## Validation Plan

- `curl -i http://127.0.0.1:3000/auth` returns `200`.
- `curl -i http://127.0.0.1:3000/chat` redirects to `/auth` when no auth state is present (client-side check validated through tests).
- After login/register, browser navigates to `/chat` and can send messages without showing session setup.
- Run web tests + web build.
