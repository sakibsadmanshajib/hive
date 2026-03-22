---
phase: 13
plan: 01
wave: 1
title: Seed DIFF headers on v1 error and stub paths, then lock with live regressions
depends_on: []
files_modified:
  - apps/api/src/routes/diff-headers.ts
  - apps/api/src/routes/chat-completions.ts
  - apps/api/src/routes/embeddings.ts
  - apps/api/src/routes/images-generations.ts
  - apps/api/src/routes/responses.ts
  - apps/api/src/routes/v1-stubs.ts
  - apps/api/test/routes/v1-error-diff-headers.test.ts
  - apps/api/test/routes/v1-stubs.test.ts
autonomous: true
requirements:
  - DIFF-01

must_haves:
  truths:
    - "Representative auth and service-error responses for POST /v1/chat/completions, /v1/embeddings, /v1/images/generations, and /v1/responses all include x-model-routed, x-provider-used, x-provider-model, and x-actual-credits"
    - "Unsupported /v1 stub routes return the same four DIFF headers on 404 responses"
    - "The four DIFF headers are seeded before requireV1ApiPrincipal() or sendApiError() can terminate the response"
    - "The full API suite and Docker-container API build both pass after the DIFF-header route changes"
  artifacts:
    - path: "apps/api/src/routes/diff-headers.ts"
      provides: "shared helper that seeds static no-dispatch DIFF headers for v1 error and stub paths"
      contains: "setNoDispatchDiffHeaders"
    - path: "apps/api/src/routes/chat-completions.ts"
      provides: "pre-dispatch DIFF header seeding for both streaming and non-streaming chat error paths"
      contains: "setNoDispatchDiffHeaders(reply)"
    - path: "apps/api/src/routes/v1-stubs.ts"
      provides: "stub 404 responses with the static DIFF-header contract"
      contains: "setNoDispatchDiffHeaders(reply)"
    - path: "apps/api/test/routes/v1-error-diff-headers.test.ts"
      provides: "live route-level regression coverage for auth and service-error DIFF headers across the four in-scope POST routes"
    - path: "apps/api/test/routes/v1-stubs.test.ts"
      provides: "stub-route assertions for static empty-string/zero-credit DIFF headers"
  key_links:
    - from: "apps/api/src/routes/diff-headers.ts"
      to: "apps/api/src/routes/chat-completions.ts"
      via: "route-level static DIFF-header seeding before auth/sendApiError"
      pattern: "setNoDispatchDiffHeaders"
    - from: "apps/api/src/routes/diff-headers.ts"
      to: "apps/api/src/routes/v1-stubs.ts"
      via: "stub 404 DIFF-header seeding"
      pattern: "setNoDispatchDiffHeaders"
    - from: "apps/api/test/routes/v1-error-diff-headers.test.ts"
      to: "apps/api/test/helpers/test-app.ts"
      via: "real v1Plugin wiring with controllable mock services"
      pattern: "createTestApp|createMockServices"
---

## Objective
Close the remaining Phase 13 DIFF-01 gap by seeding the four differentiator headers on v1 AI-route and stub error paths before any early error return, then prove the live contract with focused route/stub regressions and the standard full API verification/build gates.

## Context
- The milestone audit already isolated the defect: route error branches and `v1-stubs.ts` call `sendApiError()` before DIFF headers are guaranteed.
- `sendApiError()` is shared beyond the v1 public API surface, so the fix should stay in the route/stub layer rather than altering the global error helper.
- `models.ts` already demonstrates the correct static "no dispatch happened" DIFF-header values: `""`, `""`, `""`, `"0"`.
- `images-generations.ts` already shows the right preservation pattern when a service error includes headers: forward them first, then send the error.
- `apps/api/test/helpers/test-app.ts` already provides a live `v1Plugin` harness, which is the correct verification surface for this phase.

<task id='T1' wave='1'>
<title>Add a shared no-dispatch DIFF-header helper and seed it in all in-scope production error paths</title>
<files>apps/api/src/routes/diff-headers.ts, apps/api/src/routes/chat-completions.ts, apps/api/src/routes/embeddings.ts, apps/api/src/routes/images-generations.ts, apps/api/src/routes/responses.ts, apps/api/src/routes/v1-stubs.ts</files>
<read_first>
- AGENTS.md
- apps/api/src/routes/api-error.ts
- apps/api/src/routes/auth.ts
- apps/api/src/routes/models.ts
- apps/api/src/routes/chat-completions.ts
- apps/api/src/routes/embeddings.ts
- apps/api/src/routes/images-generations.ts
- apps/api/src/routes/responses.ts
- apps/api/src/routes/v1-stubs.ts
- .planning/phases/13-error-path-diff-headers/13-CONTEXT.md
- .planning/phases/13-error-path-diff-headers/13-RESEARCH.md
</read_first>
<action>
Create `apps/api/src/routes/diff-headers.ts` and export a function named `setNoDispatchDiffHeaders(reply)` that sets exactly:
- `x-model-routed` to `""`
- `x-provider-used` to `""`
- `x-provider-model` to `""`
- `x-actual-credits` to `"0"`

Update the in-scope route files to import and call `setNoDispatchDiffHeaders(reply)` at the top of each handler before `requireV1ApiPrincipal()`, before `services.rateLimiter.allow(...)`, before prompt validation, and before any `sendApiError(...)` branch:
- `apps/api/src/routes/chat-completions.ts`
- `apps/api/src/routes/embeddings.ts`
- `apps/api/src/routes/images-generations.ts`
- `apps/api/src/routes/responses.ts`

In `apps/api/src/routes/chat-completions.ts`, keep the existing streaming and success-path header writes intact so successful responses overwrite the static defaults with real routed values.

In `apps/api/src/routes/images-generations.ts`, keep the existing `if (result.headers) { ... }` error-branch forwarding so a service error can still override the static defaults when richer metadata exists.

Update `apps/api/src/routes/v1-stubs.ts` so `stubHandler()` calls `setNoDispatchDiffHeaders(reply)` immediately before `sendApiError(...)`.

Do not modify `apps/api/src/routes/api-error.ts` and do not change non-v1 auth helper behavior in `requirePrincipal()` / `requireApiPrincipal()`.
</action>
<done>
- Every in-scope route and stub handler seeds the static DIFF headers before early error returns can send the response.
- Success-path header propagation remains untouched.
- The global `sendApiError()` helper stays route-agnostic.
</done>
<verify>cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/chat-completions-route.test.ts apps/api/test/routes/images-generations-route.test.ts apps/api/test/routes/responses-route.test.ts</verify>
<acceptance_criteria>
- `apps/api/src/routes/diff-headers.ts` contains `function setNoDispatchDiffHeaders`
- `apps/api/src/routes/diff-headers.ts` contains `.header("x-model-routed", "")`
- `apps/api/src/routes/diff-headers.ts` contains `.header("x-provider-used", "")`
- `apps/api/src/routes/diff-headers.ts` contains `.header("x-provider-model", "")`
- `apps/api/src/routes/diff-headers.ts` contains `.header("x-actual-credits", "0")`
- `apps/api/src/routes/chat-completions.ts` contains `setNoDispatchDiffHeaders(reply)` before `requireV1ApiPrincipal(`
- `apps/api/src/routes/embeddings.ts` contains `setNoDispatchDiffHeaders(reply)` before `requireV1ApiPrincipal(`
- `apps/api/src/routes/images-generations.ts` contains `setNoDispatchDiffHeaders(reply)` before `requireV1ApiPrincipal(`
- `apps/api/src/routes/responses.ts` contains `setNoDispatchDiffHeaders(reply)` before `requireV1ApiPrincipal(`
- `apps/api/src/routes/v1-stubs.ts` contains `setNoDispatchDiffHeaders(reply)`
- `pnpm --filter @hive/api exec vitest run apps/api/test/routes/chat-completions-route.test.ts apps/api/test/routes/images-generations-route.test.ts apps/api/test/routes/responses-route.test.ts` exits 0
</acceptance_criteria>
</task>

<task id='T2' wave='2'>
<title>Add live DIFF-header regressions for AI-route error paths and extend stub assertions</title>
<files>apps/api/test/routes/v1-error-diff-headers.test.ts, apps/api/test/routes/v1-stubs.test.ts</files>
<read_first>
- apps/api/test/helpers/test-app.ts
- apps/api/test/routes/v1-stubs.test.ts
- apps/api/test/openai-sdk-regression.test.ts
- apps/api/src/routes/chat-completions.ts
- apps/api/src/routes/embeddings.ts
- apps/api/src/routes/images-generations.ts
- apps/api/src/routes/responses.ts
- apps/api/src/routes/v1-stubs.ts
- .planning/phases/13-error-path-diff-headers/13-CONTEXT.md
- .planning/phases/13-error-path-diff-headers/13-RESEARCH.md
</read_first>
<action>
Create `apps/api/test/routes/v1-error-diff-headers.test.ts` using `createTestApp(createMockServices(...))` from `apps/api/test/helpers/test-app.ts`.

Add representative live error-path coverage that asserts all four DIFF headers are present with the static no-dispatch values on:
1. Invalid-auth `401` requests to:
   - `POST /v1/chat/completions`
   - `POST /v1/embeddings`
   - `POST /v1/images/generations`
   - `POST /v1/responses`
2. Service-error responses with a valid API key for the same four routes by overriding the relevant mock AI method to return an error status.

Use request bodies that match each route's schema:
- chat: `{ "model": "mock-chat", "messages": [{ "role": "user", "content": "hi" }] }`
- embeddings: `{ "model": "text-embedding-3-small", "input": "hello" }`
- images: `{ "model": "dall-e-3", "prompt": "a lighthouse in fog" }`
- responses: `{ "model": "mock-chat", "input": "hello" }`

For every response asserted in this file, check:
- `x-model-routed === ""`
- `x-provider-used === ""`
- `x-provider-model === ""`
- `x-actual-credits === "0"`

Extend `apps/api/test/routes/v1-stubs.test.ts` so the stub endpoint matrix and parameterized-route test also assert the same four headers with the same static values.

Do not use only fake reply objects here; these tests must exercise the real `v1Plugin` registration surface.
</action>
<done>
- There is one dedicated live regression suite for Phase 13's AI-route error paths.
- Stub tests assert the DIFF-header contract, not only the error body.
- The route/stub header contract is proven on the same surface the milestone audit used.
</done>
<verify>cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-error-diff-headers.test.ts apps/api/test/routes/v1-stubs.test.ts</verify>
<acceptance_criteria>
- `apps/api/test/routes/v1-error-diff-headers.test.ts` contains `/v1/chat/completions`
- `apps/api/test/routes/v1-error-diff-headers.test.ts` contains `/v1/embeddings`
- `apps/api/test/routes/v1-error-diff-headers.test.ts` contains `/v1/images/generations`
- `apps/api/test/routes/v1-error-diff-headers.test.ts` contains `/v1/responses`
- `apps/api/test/routes/v1-error-diff-headers.test.ts` contains `x-actual-credits`
- `apps/api/test/routes/v1-stubs.test.ts` contains `x-model-routed`
- `apps/api/test/routes/v1-stubs.test.ts` contains `x-provider-used`
- `apps/api/test/routes/v1-stubs.test.ts` contains `x-provider-model`
- `apps/api/test/routes/v1-stubs.test.ts` contains `x-actual-credits`
- `pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-error-diff-headers.test.ts apps/api/test/routes/v1-stubs.test.ts` exits 0
</acceptance_criteria>
</task>

<task id='T3' wave='3'>
<title>Run full API verification and Docker-only API build</title>
<files>apps/api/src/routes/diff-headers.ts, apps/api/src/routes/chat-completions.ts, apps/api/src/routes/embeddings.ts, apps/api/src/routes/images-generations.ts, apps/api/src/routes/responses.ts, apps/api/src/routes/v1-stubs.ts, apps/api/test/routes/v1-error-diff-headers.test.ts, apps/api/test/routes/v1-stubs.test.ts</files>
<read_first>
- AGENTS.md
- apps/api/src/routes/diff-headers.ts
- apps/api/src/routes/chat-completions.ts
- apps/api/src/routes/embeddings.ts
- apps/api/src/routes/images-generations.ts
- apps/api/src/routes/responses.ts
- apps/api/src/routes/v1-stubs.ts
- apps/api/test/routes/v1-error-diff-headers.test.ts
- apps/api/test/routes/v1-stubs.test.ts
</read_first>
<action>
Run final verification in this order:
1. `cd /home/sakib/hive && pnpm --filter @hive/api test`
2. `cd /home/sakib/hive && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`

If the Docker build fails because the stack is not up, start or refresh the Docker-local stack using the repository's standard workflow, then re-run the build inside the container. Do not substitute a host build for the Docker build.

Record the exact failing suite or command if either verification step fails. Do not claim Phase 13 is ready until both commands pass.
</action>
<done>
- Full API regression coverage passes with the new Phase 13 tests included.
- The Docker-container API build passes against the same committed tree.
</done>
<verify>cd /home/sakib/hive && pnpm --filter @hive/api test && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"</verify>
<acceptance_criteria>
- `pnpm --filter @hive/api test` exits 0
- `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` exits 0
- `apps/api/src/routes/api-error.ts` remains unchanged with respect to DIFF-header seeding
</acceptance_criteria>
</task>

## Verification

Focused regressions:

```bash
pnpm --filter @hive/api exec vitest run apps/api/test/routes/chat-completions-route.test.ts apps/api/test/routes/images-generations-route.test.ts apps/api/test/routes/responses-route.test.ts
pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-error-diff-headers.test.ts apps/api/test/routes/v1-stubs.test.ts
```

Final phase verification:

```bash
pnpm --filter @hive/api test
docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"
```
