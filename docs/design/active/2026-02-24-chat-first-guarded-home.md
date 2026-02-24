# Chat-First Guarded Home (Implemented)

## Outcome

The web app now uses a chat-first flow with authentication gating on `/`.

## Implemented IA

- `/` is the primary chat workspace and requires an authenticated session.
- Unauthenticated users on `/` are redirected to `/auth`.
- `/chat` now redirects to `/`.
- `/billing` is preserved as a dedicated route.
- New authenticated utility routes:
  - `/settings`
  - `/developer`

## Chat Workspace Structure

- Left conversation rail (desktop) + conversation sheet trigger (mobile).
- Message timeline + composer in main workspace.
- Top-right profile avatar menu with:
  - `Settings`
  - `Developer Panel`
  - `Billing`
  - `Log out`

## Session and Flow Improvements

- Billing page hydrates API key from stored auth session.
- Billing route redirects to `/auth` when session is missing.
- Chat request lifecycle no longer emits success signal on failed responses.
- Chat messages now store `createdAt`; timestamps render from message metadata.

## Visual Direction

- Unified dark-first shell across auth/chat/billing surfaces.
- Removed old global left nav shell in favor of chat-first workspace behavior.

## Verification

- `pnpm --filter @bd-ai-gateway/web test`
- `pnpm --filter @bd-ai-gateway/web build`
