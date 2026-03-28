# Phase 13: Error-Path DIFF Headers - Research

**Researched:** 2026-03-22
**Domain:** `/v1/*` non-success DIFF-header gap closure
**Confidence:** HIGH

## Summary

Phase 13 is a surgical route-contract hardening pass, not a new endpoint phase. The milestone audit already identified the exact break:

1. `apps/api/src/routes/chat-completions.ts`, `embeddings.ts`, `images-generations.ts`, and `responses.ts` call `sendApiError()` on auth, rate-limit, validation, or service-error branches before the four DIFF headers are guaranteed.
2. `apps/api/src/routes/v1-stubs.ts` returns OpenAI-style 404 bodies but never seeds `x-model-routed`, `x-provider-used`, `x-provider-model`, or `x-actual-credits`.

The strongest low-blast-radius fix is to seed static "no dispatch happened" DIFF headers in the route/stub layer before any early error return. That satisfies the phase goal without broadening `sendApiError()` to non-`/v1/*` code paths that also use shared auth helpers.

**Primary recommendation:** Add a small route-level helper that sets:
- `x-model-routed: ""`
- `x-provider-used: ""`
- `x-provider-model: ""`
- `x-actual-credits: "0"`

Use it at the top of the four in-scope POST route handlers and inside the stub handler before `sendApiError()`. Keep the current success-path header forwarding intact, and keep the existing image-route pattern that forwards any service-provided error headers before sending the error.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Keep the fix in the `/v1/*` route/stub layer; do not globally change `sendApiError()`.
- Pre-dispatch failures should use honest static "no routing happened" DIFF values:
  - `x-model-routed: ""`
  - `x-provider-used: ""`
  - `x-provider-model: ""`
  - `x-actual-credits: "0"`
- If a service error already carries DIFF header metadata, preserve it before `sendApiError()`.
- Unsupported `/v1/*` stubs should mirror the models-route precedent for static DIFF headers.
- Regression proof must exercise live route/stub behavior, not only AI-service success-path tests.

### Claude's Discretion
- Whether the static-header helper is shared across route files or duplicated locally
- Exact representative error scenarios used in regression coverage
- Whether any service methods should also grow richer error headers if that remains scope-safe

### Deferred Ideas (OUT OF SCOPE)
- Global `sendApiError()` refactor for non-v1 routes
- Extending DIFF-header guarantees to guest/web routes
- Reworking generic unknown-route `setNotFoundHandler` behavior beyond explicit stubs
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| DIFF-01 | All `/v1/*` endpoints include `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits` headers | Phase 13 narrows this remaining gap to non-success AI-route and stub responses only; success paths and models-route responses already comply. |
</phase_requirements>

## Current State in Code

### What already exists
- `apps/api/src/routes/v1-plugin.ts`
  - already guarantees `x-request-id` for all `/v1/*` responses
- `apps/api/src/routes/models.ts`
  - already demonstrates the correct static "no dispatch" DIFF-header pattern via `setModelsRouteHeaders()`
- `apps/api/src/routes/images-generations.ts`
  - already preserves `result.headers` on one service-error path before calling `sendApiError()`
- `apps/api/test/helpers/test-app.ts`
  - already mounts the real `v1Plugin` with controllable mock services, which is ideal for representative live error-path tests

### Actual gaps confirmed locally
- `apps/api/src/routes/api-error.ts`
  - formats the body only; it never seeds DIFF headers
- `apps/api/src/routes/auth.ts`
  - `requireV1ApiPrincipal()` sends 401 immediately, so routes that do not pre-seed headers lose DIFF-01 on auth failures
- `apps/api/src/routes/chat-completions.ts`
  - missing DIFF-header seeding before auth, rate-limit, and non-stream/stream service errors
- `apps/api/src/routes/embeddings.ts`
  - missing DIFF-header seeding before auth, rate-limit, and service errors
- `apps/api/src/routes/images-generations.ts`
  - missing DIFF-header seeding before auth, rate-limit, and prompt validation
- `apps/api/src/routes/responses.ts`
  - missing DIFF-header seeding before auth, rate-limit, and service errors
- `apps/api/src/routes/v1-stubs.ts`
  - returns 404 stub bodies with no DIFF headers
- `apps/api/src/routes/__tests__/differentiators-headers.test.ts`
  - only covers success-path AI-service headers, not live route/stub error branches
- `apps/api/test/openai-sdk-regression.test.ts`
  - exercises error bodies but cannot prove the DIFF-header contract because the SDK does not expose those response headers

## Architecture Patterns

### Pattern 1: Route-level default DIFF-header helper
**What:** Add a small helper such as `setNoDispatchDiffHeaders(reply)` in a v1-routes module.
**Why:** Five files need the same static values, and the logic should stay outside `sendApiError()` so non-v1 routes are unaffected.
**Recommendation:** Add:

```typescript
export function setNoDispatchDiffHeaders(reply: FastifyReply): void {
  reply
    .header("x-model-routed", "")
    .header("x-provider-used", "")
    .header("x-provider-model", "")
    .header("x-actual-credits", "0");
}
```

Use it only from the in-scope v1 AI routes and `v1-stubs.ts`.

### Pattern 2: Seed before auth, validation, or service error branches
**What:** Call the helper at the top of each handler.
**Why:** `requireV1ApiPrincipal()` and `sendApiError()` terminate the response immediately; any header logic after them is too late.
**Recommendation:** Place the helper call before:
- `requireV1ApiPrincipal(...)`
- `services.rateLimiter.allow(...)`
- prompt/body validation returns
- service-error `sendApiError(...)` branches

### Pattern 3: Preserve richer service error headers when already available
**What:** Keep forwarding `result.headers` on error paths where the service already returns them.
**Why:** The static helper is the minimum DIFF-01 floor; richer metadata is useful when already known and cheap to preserve.
**Recommendation:** Do not invent provider metadata in this phase, but do keep the existing `images-generations.ts` preservation logic and apply the same pattern elsewhere only if the route/service types already support it cleanly.

### Pattern 4: Use live v1-plugin regression tests, not only direct handler tests
**What:** Add a dedicated integration-style test file using `createTestApp()` and `app.inject()`/HTTP requests.
**Why:** The audit finding is specifically about the live route/stub contract. Fake reply objects in unit tests are useful, but they are not enough evidence on their own.
**Recommendation:** Add one focused regression suite that covers:
- invalid-auth 401 on chat, embeddings, images, responses
- representative service-error responses on those same routes
- unsupported stub 404 header assertions

## Test Impact and Recommended Coverage

### New regression surface
- `apps/api/test/routes/v1-error-diff-headers.test.ts`
  - uses `createTestApp(createMockServices(...))`
  - proves DIFF headers on representative live error paths for:
    - `POST /v1/chat/completions`
    - `POST /v1/embeddings`
    - `POST /v1/images/generations`
    - `POST /v1/responses`

### Existing tests to extend
- `apps/api/test/routes/v1-stubs.test.ts`
  - add assertions that stub responses include:
    - `x-model-routed: ""`
    - `x-provider-used: ""`
    - `x-provider-model: ""`
    - `x-actual-credits: "0"`

### Existing tests that can remain unchanged
- `apps/api/src/routes/__tests__/differentiators-headers.test.ts`
  - still useful for success-path AI-service header guarantees
- `apps/api/test/openai-sdk-regression.test.ts`
  - still useful for body/status compatibility, but not the main DIFF-header proof surface

## Common Pitfalls

### Pitfall 1: Fixing `sendApiError()` globally
**What goes wrong:** Non-v1 routes that use shared auth helpers inherit DIFF headers they should not expose.
**How to avoid:** Keep DIFF-header seeding in the v1 route/stub layer only.

### Pitfall 2: Seeding after auth or validation
**What goes wrong:** 401/400/429 responses still miss the headers because the response was already sent.
**How to avoid:** Call the helper before `requireV1ApiPrincipal()`, before rate-limit checks, and before prompt validation.

### Pitfall 3: Forgetting the streaming chat error branch
**What goes wrong:** Non-stream chat errors pass, but `stream: true` failures still return no DIFF headers.
**How to avoid:** Seed the helper at the top of the chat handler so both streaming and non-streaming error branches inherit it.

### Pitfall 4: Treating success-path header tests as enough
**What goes wrong:** Phase 13 "passes" locally but the exact audit gap remains untested.
**How to avoid:** Add a dedicated live error-path suite plus stub assertions.

## Recommended Project Structure

Expected implementation surface:

```text
apps/api/src/routes/diff-headers.ts
apps/api/src/routes/chat-completions.ts
apps/api/src/routes/embeddings.ts
apps/api/src/routes/images-generations.ts
apps/api/src/routes/responses.ts
apps/api/src/routes/v1-stubs.ts
apps/api/test/routes/v1-error-diff-headers.test.ts
apps/api/test/routes/v1-stubs.test.ts
```

## Validation Architecture

### Existing infrastructure
- **Framework:** Vitest
- **Primary command:** `pnpm --filter @hive/api test`
- **Focused commands:**
  - `pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-error-diff-headers.test.ts`
  - `pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-stubs.test.ts`

### Sampling recommendation
- After route/stub production changes: run the new `v1-error-diff-headers` regression file
- After stub-test updates: run `v1-stubs.test.ts`
- Before phase completion: run full `pnpm --filter @hive/api test`
- Final build verification remains Docker-only:
  - `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`

### Manual verification
No manual-only verification is required if the live route/stub regression tests cover the required headers on representative 401/4xx/5xx/404 paths.

## Implementation Recommendation

Plan this as one autonomous plan with three task clusters:
1. Add the route-layer default DIFF-header helper and seed it in the five in-scope production files.
2. Add a live regression suite for AI-route error paths and extend stub tests for the static header contract.
3. Run the full API suite plus the Docker-container API build.

Do not split this into multiple plans. The phase is narrow, the blast radius is small, and a single plan keeps the requirement/test linkage obvious.
