# Web Flow Critical Review (2026-02-24)

## Objective

Review the implemented web user journey across auth, chat, and billing and identify issues that block or degrade a production-safe flow.

GitHub tracking issue: https://github.com/sakibsadmanshajib/hive/issues/14

## Scope

- `apps/web/src/app/page.tsx`
- `apps/web/src/app/auth/page.tsx`
- `apps/web/src/app/chat/page.tsx`
- `apps/web/src/app/billing/page.tsx`
- `apps/web/src/features/auth/*`
- `apps/web/src/features/chat/*`
- `apps/web/src/features/billing/*`
- `apps/web/src/features/settings/user-settings-panel.tsx`
- `apps/api/src/routes/google-auth.ts`

## Critical Findings

### 1) Google OAuth entrypoint is wired as a JSON navigation, not a redirect flow

- `apps/web/src/features/auth/google-login-button.tsx:14` navigates browser to `/v1/auth/google/start`.
- `apps/api/src/routes/google-auth.ts:11` returns JSON containing `authorization_url`.
- Result: users can land on a JSON payload instead of being redirected through OAuth.

### 2) OAuth callback contract does not match frontend authenticated state model

- Callback returns `session_token` from `apps/api/src/routes/google-auth.ts:30`.
- Frontend session storage expects `apiKey` (`apps/web/src/features/auth/auth-session.ts:1`) and chat requests use `x-api-key` (`apps/web/src/features/chat/use-chat-session.ts:74`).
- Result: no complete Google login path to a usable authenticated web session.

### 3) Billing requires manual API key even after successful auth

- Billing state starts with empty `apiKey` and no hydration from auth session (`apps/web/src/app/billing/page.tsx:22`).
- Result: auth -> billing is not continuous; users must copy credentials manually.

### 4) Chat request lifecycle can show both error and success for the same request

- On non-OK responses, error is set (`apps/web/src/features/chat/use-chat-session.ts:82`).
- The same code path still appends an assistant message and always emits success toast (`apps/web/src/features/chat/use-chat-session.ts:99`).
- Result: contradictory signals and reduced trust in system state.

## High/Medium Findings

### 5) Message timestamps are unstable

- Timestamp is generated at render time for each message (`apps/web/src/features/chat/components/message-list.tsx:27`).
- Result: timestamps can shift on rerender and do not represent actual message creation time.

### 6) Billing actions have incomplete network-failure handling

- `fetchSnapshot`, `topUpDemo`, and `createExtraKey` use `try/finally` without `catch` (`apps/web/src/app/billing/page.tsx:30`, `apps/web/src/app/billing/page.tsx:57`, `apps/web/src/app/billing/page.tsx:103`).
- Result: thrown network/runtime errors can surface poor or stale user status feedback.

## UI/UX Findings (Additional)

### 7) Visual language is inconsistent across auth, chat, and billing

- Chat shell uses a dark panel aesthetic while global shell and other pages use a warm/light visual tone.
- Card density, emphasis, and spacing hierarchy vary by page.
- Result: the app feels fragmented and less trustworthy for auth and billing actions.

### 8) Navigation lacks active-state orientation

- Primary nav links in `apps/web/src/components/layout/app-sidebar.tsx` do not reflect current route.
- Mobile sheet navigation also lacks strong location cues.
- Result: users can lose context during auth -> chat -> billing traversal.

### 9) Main task progression is not visually guided

- Credential input and card layout are emphasized over journey progression.
- Billing starts with manual API key entry instead of session-driven continuation.
- Result: users need extra cognitive effort to understand what to do next.

### 10) Chat ergonomics and metadata quality are weak

- Message viewport uses fixed heights (`apps/web/src/features/chat/components/message-list.tsx:19`) that can feel cramped on smaller screens.
- Composer + list balance is not adaptive enough for long conversations.
- Timestamps are not persistent message metadata (`apps/web/src/features/chat/components/message-list.tsx:27`).

### 11) Status and feedback copy is overly operational

- Repeated statuses such as "Set API key first" and generic "Working..." style messaging focus on system state, not user guidance.
- Combined with contradictory chat success/error signaling, this reduces user confidence.

## Product Direction Update (Owner-Specified)

The following are explicit required changes for the next web-flow redesign scope:

1. `/` must be the primary chat screen (ChatGPT-style entry).
2. `/` must be auth-guarded; unauthenticated users are redirected to `/auth`.
3. `/auth` remains the login/signup entry point for unauthenticated users.
4. Chat layout requirements:
   - left rail for previous chat navigation
   - top-right profile avatar/menu
   - profile menu includes `Settings` and `Developer Panel`
5. UI quality bar: cohesive modern design language; current look/flow is not release-ready.

Owner-provided references:

- Current flow recording: `~/hive-1.mp4`
- Visual reference: `~/Screenshot_24-2-2026_121358_chatgpt.com.jpeg`

## Recommended Direction

1. Select one canonical frontend auth model (API key session vs bearer session token) and align OAuth + local auth to it.
2. Add explicit frontend OAuth callback handling that persists canonical credentials.
3. Hydrate chat and billing from shared authenticated state; remove manual key entry from the primary user path.
4. Make chat request states mutually exclusive (`success` OR `error`) per request.
5. Store message `createdAt` when messages are added, not while rendering.
6. Add explicit `catch` handling for billing network failures.
7. Add end-to-end web smoke tests covering auth -> chat -> billing.
8. Introduce a single page-level visual system for auth/chat/billing (type scale, spacing rhythm, component emphasis).
9. Add active route indicators and stronger orientation cues in desktop + mobile navigation.
10. Rework key screens around guided progression rather than raw controls.
11. Standardize status microcopy for clarity, confidence, and actionability.
12. Make `/` the guarded chat home and preserve `/auth` as the only unauthenticated gateway.
13. Add profile avatar menu at chat top-right with clear access to `Settings` and `Developer Panel`.

## Status

- Audit completed.
- Detailed remediation tracked in issue #14.
