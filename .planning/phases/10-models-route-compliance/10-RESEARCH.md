# Phase 10: Models Route Compliance - Research

**Researched:** 2026-03-21
**Domain:** `/v1/models` auth and differentiator-header gap closure
**Confidence:** HIGH

## Summary

Phase 10 is a narrow route-hardening phase, not a new endpoint build. The current codebase already has OpenAI-compliant models payload serialization in `apps/api/src/routes/models.ts`: `GET /v1/models` returns `{ object: "list", data: services.models.list().map(serializeModel) }`, `GET /v1/models/:model` exists, and unknown models already return `404` via `sendApiError(..., { type: "invalid_request_error", code: "model_not_found" })`.

The remaining gaps are exactly the audit findings captured in `10-CONTEXT.md`:
1. Models routes are still public because they do not call `requireV1ApiPrincipal()`.
2. Models routes do not set `x-model-routed`, `x-provider-used`, `x-provider-model`, or `x-actual-credits`.

The important implementation nuance is that DIFF-01, as written in Phase 10 success criteria, applies to all route responses, not only happy-path 200s. That means the static models-route headers must be attached before any early return from auth or 404 handling. If headers are only set right before successful payload returns, 401 and 404 responses will still fail the phase.

**Primary recommendation:** Add a small models-route-local helper in `apps/api/src/routes/models.ts` that sets the static catalog headers, call it at the top of both handlers before auth/model lookup, and make `requireV1ApiPrincipal()` accept an optional scope so models routes can express the locked decision "any valid API key, no specific scope".

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Use `requireV1ApiPrincipal` for models routes.
- No scope restriction for models routes. Any valid API key may list or retrieve models.
- Missing or invalid API key returns 401 with `authentication_error`.
- Static header values for both models routes:
  - `x-model-routed: ""`
  - `x-provider-used: ""`
  - `x-provider-model: ""`
  - `x-actual-credits: "0"`
- Scope is limited to `GET /v1/models` and `GET /v1/models/:model`.
- No response-body changes and no cross-route audit outside models routes.

### Claude's Discretion
- Whether the static header helper stays local to `models.ts` or becomes a shared utility.
- How to encode "no specific scope" in the auth helper signature.

### Deferred Ideas (OUT OF SCOPE)
- Global differentiator-header refactor across all routes.
- Changing Bearer auth semantics on non-models endpoints.
- Extending `/v1/models` payload with pricing/capability metadata.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| FOUND-02 | Bearer token auth is OpenAI-SDK-compatible | `requireV1ApiPrincipal()` already produces the correct 401 body for missing/invalid Bearer tokens; models routes simply do not call it today. |
| DIFF-01 | All `/v1/*` endpoints include differentiator headers | Chat, embeddings, images, and responses routes already set these headers. Models routes need a static-route variant because there is no provider dispatch. |
</phase_requirements>

## Current State in Code

### What already exists
- `apps/api/src/routes/models.ts`
  - Lists models using `serializeModel()`
  - Retrieves a single model by id
  - Sends OpenAI-style `404 model_not_found`
- `apps/api/src/routes/auth.ts`
  - `requireV1ApiPrincipal()` enforces Bearer-only auth and emits:
    - `401 No API key provided`
    - `401 Incorrect API key provided`
- `apps/api/src/routes/chat-completions.ts`
  - Canonical pattern for route-level `requireV1ApiPrincipal()`
  - Canonical pattern for route-level differentiator headers
- `apps/api/src/routes/v1-plugin.ts`
  - Already adds `x-request-id` on all `/v1/*` responses
  - Already normalizes non-streaming `content-type`

### Actual gaps confirmed locally
- `apps/api/src/routes/models.ts` does not import or call `requireV1ApiPrincipal()`.
- `apps/api/src/routes/models.ts` never calls `reply.header()` for DIFF-01 headers.
- `apps/api/test/openai-sdk-regression.test.ts` still asserts the pre-phase behavior:
  - `models.list() with any key returns a model list (public endpoint)`
- `apps/api/test/routes/v1-auth-compliance.test.ts` still treats `GET /v1/models` as a no-auth success case for the content-type assertion.
- `apps/api/test/routes/typebox-validation.test.ts` still asserts `GET /v1/models` returns `200`, which will no longer be true after auth is added.

## Architecture Patterns

### Pattern 1: Pre-seed static headers before auth and lookup
**What:** Attach the four static DIFF-01 headers before any early-return path.
**Why:** `requireV1ApiPrincipal()` and `sendApiError()` send the response immediately; headers added after those calls never appear on 401/404 responses.
**Recommendation:** Add a tiny helper inside `models.ts`:

```typescript
function setModelsRouteHeaders(reply: FastifyReply): void {
  reply
    .header("x-model-routed", "")
    .header("x-provider-used", "")
    .header("x-provider-model", "")
    .header("x-actual-credits", "0");
}
```

Call it at the top of both handlers, before auth and before model lookup.

### Pattern 2: Optional-scope V1 auth helper
**What:** Update `requireV1ApiPrincipal()` so models routes can call it without passing a scope.
**Why:** The locked decision is "any valid key", and the current function signature requires a scope argument even though the implementation does not currently enforce scopes at all.
**Recommendation:** Change the signature to:

```typescript
export async function requireV1ApiPrincipal(
  request: FastifyRequest,
  reply: FastifyReply,
  services: RuntimeServices,
  requiredScope?: "chat" | "image" | "usage" | "billing",
): Promise<AuthPrincipal | undefined>
```

This is a semantic cleanup more than a behavior change. Existing call sites can keep passing a scope. Models routes should omit it.

### Pattern 3: Keep the models-header helper local
**What:** Do not extract a shared differentiator-header utility yet.
**Why:** This phase is intentionally surgical, and the models header values are unique because the routes are static catalog reads with no provider dispatch.
**Recommendation:** Local helper in `models.ts` keeps the blast radius to one route file.

## Test Impact and Recommended Coverage

### Route/unit tests to update
- `apps/api/test/routes/models-route.test.ts`
  - Add auth-aware request/reply fakes for both list and retrieve handlers.
  - Verify 401 body for missing/invalid Bearer token.
  - Verify all four DIFF-01 headers are present on:
    - successful list response
    - successful retrieve response
    - 404 retrieve response
    - 401 auth failure response

### SDK/integration tests to update
- `apps/api/test/openai-sdk-regression.test.ts`
  - Replace the old "public endpoint" assertion with:
    - invalid key -> `OpenAI.AuthenticationError`
    - valid key -> successful `models.list()`
- `apps/api/test/routes/v1-auth-compliance.test.ts`
  - Add/adjust models-route coverage for missing and invalid Bearer auth.
  - Fix the content-type test so it uses a valid Bearer token rather than unauthenticated `GET /v1/models`.

### Validation-only tests to update
- `apps/api/test/routes/typebox-validation.test.ts`
  - Stop asserting unauthenticated `GET /v1/models` returns `200`.
  - Best option: assert it does **not** return `400`, because this file is validating schema behavior, not auth behavior.

## Common Pitfalls

### Pitfall 1: Adding headers only on success responses
**What goes wrong:** 200 responses have DIFF-01 headers, but 401/404 responses do not.
**How to avoid:** Call `setModelsRouteHeaders(reply)` before `requireV1ApiPrincipal()` and before `sendApiError()` paths.

### Pitfall 2: Encoding a fake scope for models auth
**What goes wrong:** Passing `"chat"` or `"usage"` works today only because `requireV1ApiPrincipal()` does not enforce scopes yet.
**How to avoid:** Make the scope parameter optional and omit it in models routes so the code matches the locked decision and future scope enforcement cannot accidentally break models access.

### Pitfall 3: Forgetting non-route tests that document old behavior
**What goes wrong:** Implementation is correct, but regression/typebox/auth suites still assert that `GET /v1/models` is public.
**How to avoid:** Update all three affected test areas in the same plan:
- `models-route.test.ts`
- `openai-sdk-regression.test.ts`
- `v1-auth-compliance.test.ts`
- `typebox-validation.test.ts`

## Recommended Project Structure

No new production modules are required. Expected implementation surface:

```text
apps/api/src/routes/auth.ts
apps/api/src/routes/models.ts
apps/api/test/routes/models-route.test.ts
apps/api/test/openai-sdk-regression.test.ts
apps/api/test/routes/v1-auth-compliance.test.ts
apps/api/test/routes/typebox-validation.test.ts
```

## Validation Architecture

### Existing infrastructure
- **Framework:** Vitest
- **Primary command:** `pnpm --filter @hive/api test`
- **Focused commands:**
  - `pnpm --filter @hive/api exec vitest run apps/api/test/routes/models-route.test.ts`
  - `pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-auth-compliance.test.ts`
  - `pnpm --filter @hive/api exec vitest run apps/api/test/openai-sdk-regression.test.ts`
  - `pnpm --filter @hive/api exec vitest run apps/api/test/routes/typebox-validation.test.ts`

### Sampling recommendation
- After route/auth changes: run models-route + v1-auth-compliance tests.
- After regression/test updates: run openai-sdk-regression + typebox-validation tests.
- Before phase completion: run full `pnpm --filter @hive/api test`.

### Manual verification
No manual-only verification is required if the SDK regression and route tests cover:
- invalid/missing key => 401 on models endpoints
- valid key => 200 on models endpoints
- DIFF-01 headers present on success and error responses

## Implementation Recommendation

Plan this as one tightly scoped execution plan with two task clusters:
1. Production changes in `auth.ts` and `models.ts`.
2. Test updates across models-route, auth-compliance, regression, and validation suites.

Do not split production and test work into separate independent waves. The behavior change is small, and the critical risk is stale tests documenting the old public-route behavior.
