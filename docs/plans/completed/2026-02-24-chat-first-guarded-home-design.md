# Chat-First Guarded Home Design (2026-02-24)

## Goal

Redesign web IA and UX so the product is chat-first, modern, and coherent: `/` becomes the guarded chat home, `/auth` is the unauthenticated gateway, and authenticated users get a ChatGPT-like workspace with clear account controls.

## Chosen Direction

- Chosen approach: **Approach B** (ChatGPT-like structure with controlled BD AI identity layer).
- Developer Panel exposure: authenticated users only.
- Billing placement: keep dedicated `/billing` route.
- Visual target: very close to ChatGPT interaction style.

## Section 1: Information Architecture and Routing

1. `/` is the primary chat workspace and is auth-guarded.
2. Unauthenticated users visiting `/` are redirected to `/auth`.
3. `/auth` is the single login/signup entry point.
4. `/billing` remains a dedicated route and is accessible from authenticated UI entry points.
5. `/chat` is deprecated as primary entry and should redirect to `/`.

## Section 2: Chat Layout Specification

### Desktop

- Left rail: new chat + previous conversations.
- Main panel: timeline + composer.
- Top-right: avatar profile menu.

### Avatar Menu

- Account identity block.
- `Settings`.
- `Developer Panel`.
- `Billing`.
- `Log out`.

### Mobile/Tablet

- Left rail collapses to slide-over.
- Avatar menu remains top-right.
- Composer stays bottom-safe and usable.

## Section 3: Auth and Session Model

1. Use one canonical frontend authenticated session shape across auth/chat/billing.
2. Email/password and Google OAuth must both resolve to this same client auth state.
3. Protected routes must redirect to `/auth` when session is missing/invalid.
4. Billing must hydrate from authenticated session and not require manual key paste as primary path.
5. Logout from avatar menu clears local session and re-enters `/auth` flow.

## Section 4: UI System Quality Bar

1. Cohesive dark-first visual language with restrained accents.
2. Consistent spacing, type scale, and card emphasis across auth/chat/billing.
3. Clear active states for navigation and context.
4. Mutually exclusive request states (`idle`, `loading`, `success`, `error`).
5. Stable message metadata (timestamps stored at insertion, not render-time).
6. Human-readable error and empty-state copy.
7. Keyboard and focus accessibility for rail, menu, and composer.

## Section 5: Delivery Phasing and Acceptance Criteria

### Phase 1 - IA + Routing Foundation

- `/` guarded chat home is live.
- `/chat` redirects to `/`.
- `/auth` remains unauthenticated entry.

### Phase 2 - Workspace Layout

- Left rail + main timeline + top-right avatar menu implemented.
- Avatar menu contains `Settings`, `Developer Panel`, `Billing`, and logout.

### Phase 3 - Session Continuity

- Auth session is shared across chat and billing.
- Billing no longer uses manual API key as default journey.

### Phase 4 - UI Polish and Reliability

- Unified visual language passes internal review.
- Chat/billing state and error feedback are clear and non-contradictory.
- Message timestamps are stable.

### Acceptance Criteria

- Unauthenticated `GET /` navigates to `/auth`.
- Authenticated `GET /` opens chat workspace directly.
- Left rail and avatar menu exist and behave correctly on desktop + mobile.
- Avatar menu exposes `Settings` and `Developer Panel` for signed-in users.
- Billing is reachable from avatar menu and works without manual API key as first step.
- UX matches ChatGPT-like interaction rhythm while keeping BD AI labels and product semantics.

## Inputs Used

- Owner directives from issue discussion.
- Current flow recording: `~/hive-1.mp4` (reviewed via ffmpeg frame extraction).
- Visual reference: `~/Screenshot_24-2-2026_121358_chatgpt.com.jpeg`.
- Audit issue: https://github.com/sakibsadmanshajib/hive/issues/14
