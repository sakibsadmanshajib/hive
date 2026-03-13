# Chat-First Guarded Home (Superseded)

## Outcome

This design was implemented initially, but the home-route policy has since been superseded by guest-first chat on `/`.

## Implemented IA

- Historical state:
  - `/` was the primary chat workspace and required an authenticated session.
  - Unauthenticated users on `/` were redirected to `/auth`.
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

- `pnpm --filter @hive/web test`
- `pnpm --filter @hive/web build`

## Superseded By

- `docs/plans/2026-03-13-issue-19-guest-home-free-models-design.md`
