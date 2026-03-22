---
phase: 10-models-route-compliance
plan: 01
subsystem: api
tags: [models, auth, headers, openai-sdk, vitest]

requires:
  - phase: 03-auth-compliance
    provides: Bearer-token authentication_error behavior for /v1 routes
  - phase: 04-models-endpoint
    provides: OpenAI-compliant models list/retrieve serialization and model_not_found 404 handling
  - phase: 08-differentiators
    provides: DIFF-01 header contract for all /v1 responses
provides:
  - Protected GET /v1/models and GET /v1/models/:model routes
  - Static DIFF-01 headers on all models-route success and error responses
  - Regression coverage proving invalid-key OpenAI SDK models.list throws AuthenticationError
affects: [11-real-openai-sdk-regression-tests-ci-style-e2e]

tech-stack:
  added: []
  patterns:
    - pre-auth static headers for catalog routes
    - scope-optional V1 auth helper for routes that accept any valid API key

key-files:
  created: []
  modified:
    - apps/api/src/routes/auth.ts
    - apps/api/src/routes/models.ts
    - apps/api/test/routes/models-route.test.ts
    - apps/api/test/routes/v1-auth-compliance.test.ts
    - apps/api/test/openai-sdk-regression.test.ts
    - apps/api/test/routes/typebox-validation.test.ts

key-decisions:
  - "Models routes use requireV1ApiPrincipal with no scope restriction so any valid API key can list or retrieve models"
  - "Models routes set static empty routing headers and x-actual-credits: 0 before auth and lookup so 200/401/404 responses all satisfy DIFF-01"
  - "Models validation coverage stays auth-agnostic by asserting GET /v1/models does not return 400 rather than forcing a fake authenticated path"

patterns-established:
  - "Static catalog routes should seed differentiator headers before auth guards or sendApiError paths"
  - "OpenAI SDK auth regressions should assert the concrete SDK error class, not just raw 401 responses"

requirements-completed: [FOUND-02, DIFF-01]

duration: 1h 39m
completed: 2026-03-22
---

# Phase 10 Plan 01: Models Route Auth and Differentiator Header Gap Closure Summary

**Bearer-protected models routes with static DIFF-01 headers on every response path and SDK regressions that lock invalid-key behavior to AuthenticationError**

## Performance

- **Duration:** 1h 39m
- **Started:** 2026-03-22T02:05:25Z
- **Completed:** 2026-03-22T03:43:56Z
- **Tasks:** 3
- **Files modified:** 6

## Accomplishments
- Added Bearer auth enforcement to `GET /v1/models` and `GET /v1/models/:model` without changing the existing OpenAI-compliant success payloads or `model_not_found` 404 shape
- Attached static `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits` headers before auth and lookup so 200, 401, and 404 models responses all satisfy DIFF-01
- Updated route, auth, SDK, and validation suites so no test still documents unauthenticated models routes as public
- Verified the full API suite passes and the API builds successfully inside the Docker `api` container

## Task Commits

Each task was committed atomically where code changed:

1. **Task 1: Add optional-scope V1 auth and static models-route headers, with direct route coverage** - `7190e49` (feat)
2. **Task 2: Update auth, SDK regression, and validation suites to match protected models routes** - `c2fbc70` (test)
3. **Task 3: Run full API verification and Docker-only API build** - verification-only task, no code changes to commit

## Files Created/Modified
- `apps/api/src/routes/auth.ts` - makes `requireV1ApiPrincipal` scope-optional so models routes can require any valid API key
- `apps/api/src/routes/models.ts` - adds `setModelsRouteHeaders`, then enforces Bearer auth on list/retrieve before returning models data or 404s
- `apps/api/test/routes/models-route.test.ts` - covers models-route 200/401/404 behavior and static DIFF-01 headers on all paths
- `apps/api/test/routes/v1-auth-compliance.test.ts` - asserts missing and invalid Bearer auth on models routes return 401 and keeps the content-type check authenticated
- `apps/api/test/openai-sdk-regression.test.ts` - verifies invalid-key `client.models.list()` throws `OpenAI.AuthenticationError`
- `apps/api/test/routes/typebox-validation.test.ts` - keeps models validation coverage schema-focused by asserting the route does not fail with 400

## Decisions Made
- Used `requireV1ApiPrincipal()` with no scope argument for models routes so the code matches the intended "any valid key" semantics instead of smuggling in an unrelated scope
- Kept the static models-route header helper local to `models.ts` because catalog reads do not use provider dispatch and need route-specific zero-value headers
- Left the validation suite auth-agnostic so it still proves schema behavior instead of becoming another auth integration test

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Corrected vitest file paths for `pnpm --filter @hive/api exec`**
- **Found during:** Task 1 verification
- **Issue:** The plan's initial `pnpm --filter @hive/api exec vitest run apps/api/test/...` command resolves from `apps/api`, so Vitest reported `No test files found`
- **Fix:** Re-ran focused verifications with repository-relative paths from the filtered package root (`test/...`)
- **Files modified:** None
- **Verification:** `pnpm --filter @hive/api exec vitest run test/routes/models-route.test.ts` and `pnpm --filter @hive/api exec vitest run test/routes/v1-auth-compliance.test.ts test/openai-sdk-regression.test.ts test/routes/typebox-validation.test.ts`
- **Committed in:** None - verification path correction only

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** No scope creep. The only deviation corrected a package-root path mismatch in the verification command.

## Issues Encountered
- Docker build verification initially failed because the `api` service was not running and Docker access required escalation. Starting `docker compose up -d api` resolved the environment issue, and the required in-container build then passed.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Phase 10 goals are satisfied: models routes now enforce OpenAI-compatible Bearer auth and include DIFF-01 headers on success and error paths
- Phase 11 regression coverage already benefits from the new invalid-key models behavior and can remain the next verification surface
- Phase 10 is ready for phase-level verification and roadmap closure

## Self-Check: PASSED

Files exist:
- apps/api/src/routes/models.ts - FOUND
- apps/api/test/openai-sdk-regression.test.ts - FOUND

Commits exist:
- 7190e49 - FOUND (Task 1)
- c2fbc70 - FOUND (Task 2)
