# Guest Session Attribution Design

## Goal

Add a durable guest-session layer for the guest-first chat home so anonymous usage can be trusted server-side, analyzed over time, and linked to later account creation and payment conversion.

## Problem Statement

The current guest-chat boundary is stronger than a public anonymous API route, but it still relies on a same-origin web proxy plus internal token. That is enough for basic scoping, but it does not yet create a durable guest identity that supports:

- server-trusted guest access control
- guest-specific abuse controls
- funnel analysis from guest usage to signup to payment

The system needs a first-class guest session that is safe for enforcement and also visible enough in the browser to support UI state and analytics.

## Chosen Direction

Use a dual guest-session model:

- a trusted signed `httpOnly` guest-session cookie for server enforcement
- a mirrored browser-visible guest session object for UI state and analytics

The browser-visible object is not trusted for authorization. It exists to coordinate UX and analytics. The server trusts only the signed guest cookie and the web-server validation path.

## Section 1: Session Model

### Trusted server session

- Mint a guest session in the web app through a dedicated Next.js route.
- Store it in an `httpOnly` signed cookie.
- Include or reference a stable `guestId`.
- Use a bounded TTL with refresh on active use.

### Browser-visible guest session

- Mirror non-sensitive session data into browser-readable storage.
- Recommended fields:
  - `guestId`
  - `issuedAt`
  - `expiresAt`
  - optional acquisition metadata such as landing source or campaign tag

### Trust rule

- The browser-visible guest session is for UI and analytics only.
- The web server trusts only the signed guest cookie.
- The API trusts only the web server's internal handoff.

## Section 2: Analytics and Conversion Attribution

The guest session should function as a durable attribution identity.

### Primary attribution identity

- `guestId` is the primary anonymous funnel identity.

### Supporting signals

- client IP remains an abuse/rate-limit signal
- device-level identifiers should not become the primary analytics identity

### Conversion linkage

When a user later signs up or logs in:

- persist a mapping from `guestId` to `userId`
- use that link for later analysis of signup and payment conversion

This allows analysis such as:

- which guests used free chat
- which later created accounts
- which later purchased credits

## Section 3: Runtime Flow

### Web bootstrap

1. User lands on `/` without an auth session.
2. Web app checks for guest session.
3. If missing or expired, the web app calls a guest-session bootstrap route.
4. That route sets the signed guest cookie and returns the browser-visible guest session object.
5. Browser stores the mirrored guest session for UI state and analytics.

### Guest chat

1. Browser sends guest chat through the existing Next.js guest chat route.
2. Web route requires a valid guest session cookie and same-origin browser request.
3. Web route forwards:
   - internal web token
   - validated `guestId`
   - caller IP
4. API internal guest route executes only for guest-safe models.
5. Guest usage is recorded under `guestId`, not a fake user id.

### Account conversion

1. Guest later signs up or logs in.
2. Web/API persist `guestId -> userId` linkage.
3. Later payment events can be attributed through `userId`.

## Section 4: Persistence Shape

Add dedicated Supabase persistence for guest attribution rather than overloading authenticated usage tables.

Recommended tables:

- `guest_sessions`
  - `guest_id`
  - `created_at`
  - `updated_at`
  - `expires_at`
  - optional acquisition metadata
  - optional operational fields such as last-seen IP summary
- `guest_usage_events`
  - `id`
  - `guest_id`
  - `endpoint`
  - `model`
  - `credits`
  - `created_at`
  - optional lightweight metadata needed for analysis
- `guest_user_links`
  - `guest_id`
  - `user_id`
  - `linked_at`
  - optional `link_source`

This should remain separate from `usage_events`, which currently assumes authenticated `user_id` foreign keys.

## Section 5: Abuse Controls

- Rate limiting should consider both forwarded IP and guest identity.
- Guest session TTL should be refreshable but bounded.
- The web route should continue rejecting non-same-origin browser traffic.
- The API internal guest route should reject missing or invalid internal token.
- Replay resistance should come from server-issued session state, not just client-supplied guest identifiers.

## Section 6: Testing and Verification

Required verification should cover:

- web tests for guest-session bootstrap and persistence
- web tests for guest chat requiring a valid guest session
- API tests for internal guest route requiring trusted forwarded guest identity
- persistence tests for guest attribution and guest-to-user linking
- full API test suite
- full web test suite
- production web build with required envs, including `WEB_INTERNAL_GUEST_TOKEN`

If auth/chat flow changes materially, extend Playwright smoke coverage to exercise guest-first home behavior and later conversion into an authenticated session.

## Non-Goals

- no full device fingerprinting system
- no public anonymous API access
- no complete analytics warehouse implementation in this issue
- no payment attribution redesign beyond linking through `guestId -> userId`
