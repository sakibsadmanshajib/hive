# Web E2E Smoke Coverage Design (Auth -> Chat -> Billing)

Date: 2026-02-24
Issue: https://github.com/sakibsadmanshajib/hive/issues/16
Owner: Platform/Web

## Context

Issue #16 requests end-to-end smoke coverage for the chat-first guarded-home flow in `apps/web`, including happy and failure paths across auth, chat, and billing. Current verification leans on unit tests and build checks, leaving a coverage gap for cross-page browser behavior and integration wiring.

This design follows repository constraints in `AGENTS.md` and keeps endpoint behavior and security boundaries unchanged.

## Goals

1. Add repeatable browser-level smoke coverage for `apps/web`.
2. Validate the primary user path: unauth -> auth -> chat -> billing.
3. Validate failure messaging for chat and billing.
4. Provide a CI-friendly command and documentation.

## Non-Goals

1. Replacing existing Vitest unit/component coverage.
2. Exhaustive visual regression or cross-browser matrix expansion.
3. Changing API contracts or billing formulas.

## Recommended Approach

Use Playwright for smoke e2e in `apps/web`, executed against Docker full stack.

Why this approach:

- High confidence because tests run against real runtime wiring.
- Minimal blast radius because tests are additive and smoke-scoped.
- Strong CI portability via deterministic setup and explicit commands.

## Architecture and Components

### 1) E2E Runner Setup

- Add Playwright dependencies and config in `apps/web`.
- Add a dedicated e2e script, e.g. `pnpm --filter @bd-ai-gateway/web test:e2e`.
- Configure base URL and runtime timeouts for CI stability.

### 2) Test Data and Session Strategy

- Use API-assisted setup inside tests/fixtures to create or authenticate users quickly.
- Keep generated accounts unique per run (timestamp/random suffix) to avoid collisions.
- Persist authenticated browser state where useful to avoid repetitive login UI steps in every spec.

### 3) Smoke Spec Coverage

Single smoke suite validates:

1. Unauthenticated access to `/` reaches auth path and does not expose chat-only view.
2. Auth happy path (register/login) leads to chat-ready state.
3. Chat success path renders assistant response.
4. Chat failure path shows explicit failure messaging without contradictory success-only signal.
5. Billing is reachable from app navigation and renders expected billing shell.
6. Billing failure-state messaging appears for failed top-up/payment interactions.

## Data Flow

1. Test opens web route.
2. Web invokes existing API endpoints (`/v1/users/*`, `/v1/chat/completions`, `/v1/payments/*`, `/v1/usage`).
3. Assertions validate UI behavior and state transitions only (no API contract rewrites).

This preserves current OpenAI-compatible API behavior and status endpoint boundaries.

## Error Handling and Flake Control

- Prefer robust role/text/test-id selectors over fragile CSS selectors.
- Keep smoke assertions outcome-focused, not style/animation-focused.
- Use deterministic waiting on visible UI state changes instead of arbitrary sleeps.
- Keep retries low and scoped to CI transient behavior.

## CI and Local Execution

- Add docs for prerequisites (Docker stack + env).
- Add e2e command to run locally and in CI.
- Document expected artifacts (e.g., Playwright report/traces when failures occur).

## Documentation Updates

Update docs to include:

- e2e runner command for `apps/web`
- required runtime baseline (Docker full stack)
- smoke suite scope and intended usage

## Acceptance Mapping

- Repeatable e2e command: provided via `apps/web` script.
- Auth -> chat -> billing smoke path: covered in smoke suite.
- Chat and billing failure states: explicitly asserted in suite.

## Risks and Mitigations

1. **Runtime dependency instability**
   - Mitigation: strict baseline docs and health checks before test run.
2. **Test flakiness from async UI/network timing**
   - Mitigation: resilient selectors and deterministic waits.
3. **Credential/session brittleness**
   - Mitigation: fixture-managed auth and isolated test accounts.

## Implementation Readiness

Design is ready for implementation planning and execution in incremental commits with targeted validation.
