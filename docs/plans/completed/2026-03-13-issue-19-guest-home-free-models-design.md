# Issue 19 Guest Home Free-Model Access Design

> APPROVED
> Approver: repository maintainer (chat approval)
> Approval date: 2026-03-13
> Approval artifact: maintainer replied `yes` to each design section and approved execution in the implementation thread before work began.

## Goal

Make `/` a guest-first chat entry in the web app while preserving authenticated API access rules and ensuring guest traffic can use only models that are explicitly free.

## Problem Statement

Hive currently treats `/` as an authenticated chat home and keeps direct API access authenticated. Issue `#19` is not a user-plan entitlement problem. It is a model-pricing and access-policy problem:

- models can be free, fixed-cost, or variable-cost
- guest users should be able to try Hive from the web app without logging in
- guest users must never reach credit-charging models
- direct API routes should remain authenticated and unchanged for programmatic clients

The model catalog also needs richer pricing metadata so policy can evolve beyond a single per-request credit number.

## Chosen Direction

Use a first-class model metadata catalog in the API, still code-defined and in-memory for now, with:

- `costType: "free" | "fixed" | "variable"`
- structured `pricing` metadata for billable units

Guest eligibility is determined from `costType`, not inferred solely from zero-valued pricing fields, though free models should also carry zero-valued pricing where relevant.

## Section 1: Product and Access Boundaries

1. The web app home route `/` becomes guest-accessible by default.
2. Guest users can open chat immediately and are encouraged to log in or sign up when they hit paid-model boundaries or account features.
3. Direct API routes such as `/v1/chat/completions` remain authenticated and are not opened for anonymous programmatic use.
4. Guest access exists as a web product capability, not as public unauthenticated API usage.
5. Authenticated users retain access to credit-backed paid models and existing top-up flows.

## Section 2: Model Metadata and Policy

Each chat model should expose richer metadata than the current `creditsPerRequest` shape:

- `costType`
- `pricing.creditsPerRequest` for fixed-cost models
- `pricing.inputTokensPer1m`
- `pricing.outputTokensPer1m`
- `pricing.cacheReadTokensPer1m`
- `pricing.cacheWriteTokensPer1m`
- room for future metadata such as provider-native rate limits or image-unit costs

Policy rules:

- `costType: "free"` models are guest-eligible
- `costType: "fixed"` and `costType: "variable"` models are not guest-eligible
- guest-safe model checks happen on the server, never only in the client
- guest requests fail closed; they never spill into non-free models

## Section 3: Runtime Flow

### API

- Keep `/v1/chat/completions` authenticated.
- Add an internal guest chat route for web-server use only.
- Validate requested model against the guest-safe policy before dispatch.
- Do not consume credits for guest requests.
- Keep authenticated chat credit charging and response-header behavior unchanged.

### Web

- Remove the current auth gate from `/`.
- Reuse the same main chat experience rather than splitting guest and auth UIs into separate products.
- When no auth session is present:
  - show guest-safe models only
  - browser sends through a Next.js server route, not directly to the API
  - present clear login/sign-up/top-up prompts for paid capabilities
- When an auth session is present:
  - show the full authenticated model set
  - use the normal authenticated chat path

## Section 4: Failure and Fallback Behavior

1. Guest requests can target only guest-safe models.
2. The web guest route should accept only same-origin browser traffic and forward the caller IP for guest rate limiting.
3. If a guest-safe model is unavailable, return a controlled failure from the guest route.
4. Guest requests must never fall through to fixed-cost or variable-cost models.
5. Authenticated requests keep current paid-model behavior.
6. Public/internal provider status and metrics boundaries remain unchanged.

## Section 5: Testing and Documentation

Required verification should cover:

- API tests for guest-route model rejection and fail-closed behavior
- API tests for authenticated routes remaining protected
- web tests for `/` rendering in guest mode without redirect
- web tests for model availability differences between guest and authenticated states
- builds for both API and web

Docs to update in the same change:

- `README.md`
- `CHANGELOG.md`
- `docs/architecture/system-architecture.md`
- existing chat-first guarded-home docs that currently describe `/` as authenticated-only

## Non-Goals

- no persistent provider intelligence sync layer in this issue
- no direct public anonymous OpenAI-compatible API access
- no account-plan entitlement system
- no full provider catalog storage or provenance workflow
