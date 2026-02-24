# Chat-First Frontend Information Architecture Design (Option A)

Date: 2026-02-23
Status: Proposed for implementation
Owner: Web platform

## Context and Problem

The current web experience spreads core workflows across too many top-level pages and feels visually inconsistent:

- chat is not the default destination
- developer workflows (API keys, usage) are mixed into non-developer surfaces
- account workflows (profile, billing, preferences) are not clearly separated
- layout density and spacing make pages feel boxed and unfinished

This creates navigation friction and lowers perceived product quality.

## Goals

1. Make chat the default product experience.
2. Keep developer tools and account settings as peer-level top-right actions.
3. Separate responsibilities between Chat, Developer Panel, and Settings.
4. Apply a cohesive "modern fintech editorial" visual system across the app.
5. Preserve existing backend/API contracts and minimize implementation blast radius.

## Non-Goals

- Changing backend API contracts or billing formulas.
- Reworking provider routing behavior.
- Introducing new auth methods or data models.

## User Experience and Information Architecture

### Top-level routes

- `/` -> Chat workspace (default landing page after auth)
- `/developer` -> Developer Panel
- `/settings` -> Settings
- `/auth` -> Authentication gateway only

### Header model

Top-right actions are peers and always visible in authenticated app surfaces:

- `Developer Panel`
- `Settings` (styled as avatar + label entry)
- theme toggle remains available but visually secondary

### Chat workspace (`/`)

- left rail: conversation history + new chat action
- main: message list + composer + model selector
- no key-management or billing forms in this route

### Developer Panel (`/developer`)

- API key management (primary + secondary key actions)
- usage and credit snapshot cards
- developer-oriented status and integration helpers

### Settings (`/settings`)

- profile and account identity data
- payment/billing preferences and personal account controls
- security and access preference toggles where applicable

## Visual Direction (Option A: Modern Fintech Editorial)

### Foundations

- warm neutral base with atmospheric gradient backgrounds
- premium card surfaces with subtle depth and soft borders
- stronger typography hierarchy and better legibility
- improved responsive spacing rhythm to remove empty-canvas feel

### Component language

- consistent button, input, and card heights
- clearer focus/hover states and active nav states
- refined chat bubble contrast and message spacing
- subtle, purposeful motion only (entry fade/slide, light stagger)

## Architecture and Component Changes

### Shared layout

- update app shell and header to support peer actions (`Developer Panel`, `Settings`)
- preserve mobile navigation via sheet/drawer
- keep route guards and auth redirects intact

### Route-level refactor

- move chat page content to `/`
- create `/developer` page and migrate developer-specific billing/key/usage widgets
- create/refine `/settings` page and migrate profile/account/billing preference controls
- keep `/auth` focused on login/register and redirect to `/` on success

### Reused feature modules

- retain existing chat hooks/components for message flow
- retain existing billing and settings panels, remapped to new route ownership
- avoid API shape changes unless strictly required by UI correctness

## Data Flow and API Usage

- Chat route continues using current chat session state and send flow.
- Developer Panel continues using existing endpoints for:
  - `/v1/users/me`
  - `/v1/users/api-keys`
  - `/v1/usage`
  - `/v1/credits/balance` (if already surfaced through current data flow)
- Settings continues using existing user settings/account endpoints.
- Auth route continues using existing login/register flow and session persistence.

No endpoint contract changes are required for this redesign.

## Error Handling and UX States

- Keep explicit loading states for all async panels.
- Keep inline status messaging for API-key-required actions.
- Keep user-safe error messages and avoid exposing sensitive internals.
- Preserve empty states for conversation history and usage lists.

## Accessibility and Responsiveness

- maintain keyboard-friendly nav/actions
- preserve sufficient text contrast and focus visibility
- ensure mobile drawer parity for conversation and app navigation
- validate layout behavior at mobile, tablet, and desktop breakpoints

## Verification Plan

1. Build verification:
   - `pnpm --filter @bd-ai-gateway/web build`
2. Route verification:
   - unauthenticated access redirects to `/auth`
   - successful auth redirects to `/`
   - header shows `Developer Panel` and `Settings` as peer actions
3. Workflow verification:
   - chat send and conversation selection on `/`
   - API key creation and usage snapshot on `/developer`
   - profile/settings and billing controls on `/settings`
4. Responsive verification:
   - mobile and desktop behavior for chat rail/drawer and header actions

## Risks and Mitigations

- Risk: route migration introduces broken links.
  - Mitigation: update shared nav in one place and add route smoke checks.
- Risk: mixed responsibilities remain after migration.
  - Mitigation: enforce ownership checklist per route during refactor.
- Risk: visual churn reduces readability.
  - Mitigation: keep token-driven styles and test contrast/spacing before completion.

## Rollout Strategy

Implement in small, reviewable steps:

1. shell/header visual and IA updates
2. chat-first route switch
3. developer panel route and migration
4. settings route and migration
5. final polish and responsive QA
