# Chat-First Guarded Home Implementation Plan

## Goal

Deliver a ChatGPT-like chat-first IA with `/` as authenticated chat home and `/auth` as unauthenticated gateway.

## Delivered

1. Guarded root route and `/chat` redirect shim.
2. New chat workspace shell with left rail and top-right avatar menu.
3. Profile menu actions for Settings, Developer Panel, Billing, and Logout.
4. Billing session hydration and auth guard.
5. Stable message timestamps via `createdAt` metadata.
6. Non-contradictory chat request success/error handling.
7. Dark-first unified visual treatment for auth/chat/billing.

## Verification Commands

- `pnpm --filter @bd-ai-gateway/web test`
- `pnpm --filter @bd-ai-gateway/web build`
