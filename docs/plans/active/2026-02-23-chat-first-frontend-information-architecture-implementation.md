# Chat-First Frontend Information Architecture Implementation

Date: 2026-02-23
Status: Active

## Implemented Changes

1. Default route moved to chat by mapping `apps/web/src/app/page.tsx` to `apps/web/src/app/chat/page.tsx`.
2. Header now exposes peer actions for `Developer Panel` (`/developer`) and `Settings` (`/settings`).
3. Added dedicated `apps/web/src/app/developer/page.tsx` for API key and usage workflows.
4. Added dedicated `apps/web/src/app/settings/page.tsx` for profile, payment, and account settings workflows.
5. Converted `apps/web/src/app/billing/page.tsx` to a compatibility route that points users to the new surfaces.
6. Updated auth success navigation to redirect to `/`.
7. Applied visual polish updates in global tokens and chat shell/composer/list layouts.

## Verification

- `pnpm --filter @bd-ai-gateway/web test`
- `pnpm --filter @bd-ai-gateway/web build`

## Follow-ups

- Add first-class profile editing APIs and persist profile updates from `/settings`.
- Consider deprecating `/billing` after downstream links are migrated.
