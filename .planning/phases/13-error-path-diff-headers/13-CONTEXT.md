# Phase 13: Error-Path DIFF Headers - Context

**Gathered:** 2026-03-22
**Status:** Ready for planning

<domain>
## Phase Boundary

Close the remaining DIFF-01 gap by ensuring all in-scope `/v1/*` non-success responses preserve the four differentiator headers: `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits`.

This phase covers representative error paths for `POST /v1/chat/completions`, `/v1/embeddings`, `/v1/images/generations`, `/v1/responses`, plus unsupported `/v1/*` stub routes. It does not change success-path behavior, request/response bodies, model aliasing, or any non-`/v1/*` web routes.

</domain>

<decisions>
## Implementation Decisions

[auto] Selected all gray areas: pre-dispatch header seeding, error-value semantics, stub-route header behavior, regression coverage.

### Pre-dispatch header seeding

[auto] Selected: seed DIFF headers in the route/stub layer before any early `sendApiError()` return, rather than changing `sendApiError()` globally.

- Route handlers for `chat-completions`, `embeddings`, `images-generations`, and `responses` should seed DIFF headers before auth failures, rate-limit failures, validation failures, and other early-return error branches.
- Unsupported `/v1/*` stub handlers should seed the same headers before calling `sendApiError()`.
- Keep the fix scoped to the `/v1/*` routes in this phase. Do not broaden `sendApiError()` in a way that leaks DIFF headers onto non-`/v1/*` routes that also use shared auth helpers.

### Error header semantics

[auto] Selected: use a stable "no dispatch happened" contract for pre-dispatch failures, while preserving concrete metadata from service errors when it already exists.

- For auth, validation, rate-limit, and other route-level failures that happen before real model/provider dispatch, send all four DIFF headers with static values:
  - `x-model-routed: ""`
  - `x-provider-used: ""`
  - `x-provider-model: ""`
  - `x-actual-credits: "0"`
- When a service error already carries DIFF header metadata, preserve and forward it before `sendApiError()` rather than overwriting it with the static fallback.
- Phase 13 is about header presence and honest semantics, not inventing routing metadata that never existed.

### Stub-route contract

[auto] Selected: unsupported `/v1/*` stubs should mirror the models-route precedent for non-dispatching responses.

- Stub routes should return the same static empty-string/zero-credit DIFF header values used by models routes when no provider call occurs.
- Keep the current OpenAI-style 404 body shape and `unsupported_endpoint` code unchanged; this phase adds header completeness, not a new stub format.
- Unknown `/v1/*` routes handled by the scoped not-found handler are outside the explicit Phase 13 stub requirement unless they are already represented by a registered stub route.

### Regression coverage

[auto] Selected: add representative route-level error-path tests and stub tests, using real route wiring with per-test app instances.

- Add targeted regression coverage for representative non-success flows on the four in-scope POST routes and for unsupported stub routes.
- Cover at least these categories:
  - auth failure on an in-scope AI route
  - early route-level error such as rate limit or request validation
  - service/provider failure path where error metadata may already exist
  - unsupported stub 404
- Use the existing Fastify test-app harness and per-test app-instance pattern from Phase 11 for error-path isolation.
- Do not rely only on AI-service success-path header tests; Phase 13 must prove the live route/stub contract where the audit found the gap.

### Claude's Discretion

- Exact helper name/location for the static DIFF-header seeding logic
- Whether the static header helper is shared across multiple v1 route files or duplicated as small local functions
- The exact representative error scenarios chosen per route, as long as the success criteria coverage is satisfied
- Whether any service methods should also be upgraded to return richer error headers when that can be done without expanding scope

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase requirements and audit evidence
- `.planning/ROADMAP.md` §Phase 13 — Scope, success criteria, and dependency chain for the DIFF-header gap closure
- `.planning/REQUIREMENTS.md` §DIFF-01 and traceability table — Requirement definition and why Phase 13 owns the remaining closure
- `.planning/v1.0-MILESTONE-AUDIT.md` — Audit evidence showing the broken flow and the exact gap on non-success response headers
- `.planning/STATE.md` — Prior recorded implementation decisions, especially the models-route header precedent captured after Phase 10

### Prior phase decisions to carry forward
- `.planning/phases/08-differentiators/08-CONTEXT.md` — Original DIFF-01/02/04 intent, request-id hook, and existing header propagation assumptions
- `.planning/phases/09-operational-hardening/09-CONTEXT.md` — Stub-route format and explicit route registration decisions
- `.planning/phases/10-models-route-compliance/10-CONTEXT.md` — Static empty-string/`"0"` header values used when no provider dispatch occurs

### Current implementation targets
- `apps/api/src/routes/api-error.ts` — Canonical v1 error sender; this phase must work with it without broadening non-v1 behavior
- `apps/api/src/routes/auth.ts` — `requireV1ApiPrincipal()` currently sends 401s before DIFF headers are seeded on most POST routes
- `apps/api/src/routes/chat-completions.ts` — In-scope route with auth, rate-limit, streaming, and service-error branches
- `apps/api/src/routes/embeddings.ts` — In-scope route with auth, rate-limit, and service-error branches
- `apps/api/src/routes/images-generations.ts` — In-scope route with prompt validation and partial error-header forwarding already present
- `apps/api/src/routes/responses.ts` — In-scope route with auth, rate-limit, and service-error branches
- `apps/api/src/routes/v1-stubs.ts` — Stub handlers currently call `sendApiError()` without seeding DIFF headers
- `apps/api/src/routes/models.ts` — Existing precedent for static DIFF headers on non-dispatching responses
- `apps/api/src/routes/v1-plugin.ts` — Existing global `x-request-id` hook; useful boundary marker for what Phase 13 should not rework

### Existing regression harness and tests
- `apps/api/test/helpers/test-app.ts` — Fastify app/test-services harness for representative route-level regression coverage
- `apps/api/test/openai-sdk-regression.test.ts` — Existing live error-path suite that exposed remaining coverage gaps called out in the audit
- `apps/api/test/routes/v1-stubs.test.ts` — Stub-route regression tests to extend with DIFF-header assertions
- `apps/api/src/routes/__tests__/differentiators-headers.test.ts` — Current success-path DIFF-header coverage; useful contrast for what is still missing
- `apps/api/test/routes/models-route.test.ts` — Existing assertions for the static models-route DIFF-header values

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `apps/api/src/routes/models.ts:setModelsRouteHeaders()` — proven helper pattern for static empty-string/`"0"` DIFF headers when no provider dispatch occurs
- `apps/api/src/routes/api-error.ts:sendApiError()` — canonical error formatter once route/stub handlers have seeded any required headers
- `apps/api/src/routes/auth.ts:requireV1ApiPrincipal()` — central auth failure path for all in-scope v1 AI routes
- `apps/api/test/helpers/test-app.ts` — quickest way to stand up representative route-level error-path tests with isolated service overrides

### Established Patterns
- Success paths already forward `result.headers` or `streamResult.headers` from services; the remaining gap is almost entirely in early-return error branches
- `v1Plugin` already guarantees `x-request-id` for all `/v1/*` responses, so Phase 13 only needs to close the other four DIFF headers
- `images-generations.ts` already shows the preservation pattern for service errors that include headers: forward available headers first, then call `sendApiError()`
- Models routes already established the "no dispatch happened" header semantics: empty routing fields and `"0"` credits

### Integration Points
- Route-layer changes land in `chat-completions.ts`, `embeddings.ts`, `images-generations.ts`, `responses.ts`, and `v1-stubs.ts`
- Shared auth behavior in `requireV1ApiPrincipal()` must remain compatible with route-scoped DIFF-header seeding
- Optional service-layer refinement may be useful where provider failures already know routed model/provider metadata
- Regression proof should live beside the existing route and SDK regression suites, not only in domain-level header tests

</code_context>

<specifics>
## Specific Ideas

- This is a surgical gap-closure phase, not a redesign of the DIFF-header system.
- The milestone audit wording is the main product reference for this phase: preserve the header contract on non-success response paths that currently call `sendApiError()` too early.
- Models-route static header values are the canonical precedent for truthful "no provider dispatch happened" responses.

</specifics>

<deferred>
## Deferred Ideas

- Broader refactoring of `sendApiError()` or shared auth helpers for non-`/v1/*` routes
- Expanding DIFF-header guarantees beyond the explicit Phase 13 scope to guest/web pipeline routes
- Revisiting unknown-route `setNotFoundHandler` behavior beyond the explicitly registered unsupported stub endpoints

</deferred>

---

*Phase: 13-error-path-diff-headers*
*Context gathered: 2026-03-22*
