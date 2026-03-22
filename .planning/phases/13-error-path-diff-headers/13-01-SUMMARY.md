---
phase: 13
plan: 01
subsystem: api
tags: [diff-headers, error-paths, fastify, vitest, docker]
dependency_graph:
  requires:
    - Phase 8 differentiator header contract
    - Phase 9 stub route coverage
    - Phase 10 no-dispatch header precedent on models routes
    - Phase 11 live v1 Fastify test harness
  provides:
    - route-level no-dispatch DIFF header seeding for v1 AI error paths
    - stub-route DIFF header seeding for unsupported v1 endpoints
    - live regression coverage for auth and service-error DIFF headers
    - DIFF-01 closure for v1 error and stub responses
  affects:
    - apps/api/src/routes/diff-headers.ts
    - apps/api/src/routes/chat-completions.ts
    - apps/api/src/routes/embeddings.ts
    - apps/api/src/routes/images-generations.ts
    - apps/api/src/routes/responses.ts
    - apps/api/src/routes/v1-stubs.ts
    - apps/api/test/routes/v1-error-diff-headers.test.ts
    - apps/api/test/routes/v1-stubs.test.ts
tech_stack:
  added: []
  patterns:
    - route-layer-no-dispatch-header-seeding
    - live-v1-error-path-regression-tests
key_files:
  created:
    - apps/api/src/routes/diff-headers.ts
    - apps/api/test/routes/v1-error-diff-headers.test.ts
  modified:
    - apps/api/src/routes/chat-completions.ts
    - apps/api/src/routes/embeddings.ts
    - apps/api/src/routes/images-generations.ts
    - apps/api/src/routes/responses.ts
    - apps/api/src/routes/v1-stubs.ts
    - apps/api/test/routes/v1-stubs.test.ts
decisions:
  - Route handlers and stub routes seed static no-dispatch DIFF headers before shared auth and error helpers can terminate the response
  - Shared reply header helpers in this repo should set headers sequentially instead of assuming reply.header chaining in lightweight tests
requirements_completed: [DIFF-01]
metrics:
  duration: 9 minutes
  completed: "2026-03-22T05:20:06Z"
  tasks_completed: 3
  files_modified: 8
---

# Phase 13 Plan 01: Error-Path DIFF Headers Summary

Shared no-dispatch DIFF header seeding now covers v1 AI-route auth/service failures and unsupported stub 404s, with live regressions and a passing Docker API build.

## Performance

- **Duration:** 9 minutes
- **Started:** 2026-03-22T05:11:00Z
- **Completed:** 2026-03-22T05:20:06Z
- **Tasks:** 3
- **Files modified:** 8

## Accomplishments

- Added `setNoDispatchDiffHeaders(reply)` and seeded it before auth, validation, rate-limit, and stub error exits on the in-scope `/v1/*` routes.
- Added a live `v1Plugin` regression suite covering invalid-auth and service-error DIFF headers for chat, embeddings, images, and responses.
- Extended stub-route assertions to require the same static empty-string and zero-credit DIFF header contract, then passed full API tests plus Docker `api` build.

## Task Commits

1. **Task 1: Add a shared no-dispatch DIFF-header helper and seed it in all in-scope production error paths** - `9875441` (`fix`)
2. **Task 2: Add live DIFF-header regressions for AI-route error paths and extend stub assertions** - `d2e8bc1` (`test`)
3. **Task 3: Run full API verification and Docker-only API build** - `3c801a4` (`fix`)

## Files Created/Modified

- `apps/api/src/routes/diff-headers.ts` - Shared static DIFF-header helper for no-dispatch responses.
- `apps/api/src/routes/chat-completions.ts` - Seeds fallback DIFF headers before auth and stream/non-stream error exits.
- `apps/api/src/routes/embeddings.ts` - Seeds fallback DIFF headers before auth, rate-limit, and service errors.
- `apps/api/src/routes/images-generations.ts` - Seeds fallback DIFF headers before auth, prompt validation, and error handling while preserving richer service headers.
- `apps/api/src/routes/responses.ts` - Seeds fallback DIFF headers before auth, rate-limit, and service errors.
- `apps/api/src/routes/v1-stubs.ts` - Seeds static DIFF headers before unsupported endpoint 404 responses.
- `apps/api/test/routes/v1-error-diff-headers.test.ts` - Live route regressions for invalid-auth and service-error DIFF headers across the four POST endpoints.
- `apps/api/test/routes/v1-stubs.test.ts` - Stub-route header assertions for the static no-dispatch contract.

## Decisions Made

- Kept DIFF-header seeding in the route and stub layer instead of broadening `sendApiError()`, so non-`/v1/*` routes stay untouched.
- Preserved the existing image-route pattern where service-provided error headers can override the static fallback when richer metadata exists.
- Standardized the shared helper on non-chainable `reply.header()` calls because this repo already has lightweight route tests that do not emulate Fastify chaining.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Made the shared DIFF-header helper compatible with lightweight mock replies**
- **Found during:** Task 3
- **Issue:** The first helper implementation chained `reply.header(...)`, which broke `test/routes/rbac-settings-enforcement.test.ts` because its mock reply returns `undefined` from `header()`.
- **Fix:** Rewrote `setNoDispatchDiffHeaders()` to set each header sequentially without assuming chaining.
- **Files modified:** `apps/api/src/routes/diff-headers.ts`
- **Verification:** `pnpm --filter @hive/api test`; `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`
- **Committed in:** `3c801a4`

---

**Total deviations:** 1 auto-fixed (1 Rule 1 bug)
**Impact on plan:** The auto-fix was necessary to keep existing route handler tests compatible with the new shared helper. No scope creep.

## Issues Encountered

- The Task 1 verification command in the plan used repo-root-prefixed test paths under `pnpm --filter @hive/api exec vitest`, which resolved to no files inside the package. I reran the equivalent package-local command with `test/routes/...` paths and kept the intended verification scope unchanged.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `DIFF-01` is now closed for the remaining v1 error and stub response paths.
- The remaining milestone audit gap is `DIFF-03` in Phase 12 (`/v1/embeddings` real-runtime alias compliance).

## Self-Check: PASSED

- FOUND: `.planning/phases/13-error-path-diff-headers/13-01-SUMMARY.md`
- FOUND: `9875441`
- FOUND: `d2e8bc1`
- FOUND: `3c801a4`
