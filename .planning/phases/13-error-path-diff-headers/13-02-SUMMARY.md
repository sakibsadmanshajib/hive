---
phase: 13-error-path-diff-headers
plan: 02
subsystem: api
tags: [fastify, headers, validation, vitest]

requires:
  - phase: 13-error-path-diff-headers
    provides: route and stub DIFF-header seeding baseline from 13-01
provides:
  - plugin-level no-dispatch DIFF headers for Fastify validation and scoped not-found responses
  - live v1Plugin validation regressions covering chat, embeddings, images, and responses 400 paths
  - fresh full API test and Docker-container API build evidence for DIFF-01 closure
affects: [DIFF-01, v1-plugin, validation]

tech-stack:
  added: []
  patterns:
    - plugin-scoped no-dispatch DIFF-header seeding for pre-dispatch Fastify errors
    - auth-agnostic live validation regressions on the real v1Plugin surface

key-files:
  created: []
  modified:
    - apps/api/src/routes/v1-plugin.ts
    - apps/api/test/routes/typebox-validation.test.ts

key-decisions:
  - "Seed no-dispatch DIFF headers in v1Plugin error and not-found handlers instead of broadening sendApiError beyond the /v1 plugin scope"
  - "Keep validation coverage on the real plugin registration with lightweight mock services so 400-path DIFF headers are proven without changing auth behavior"

patterns-established:
  - "Plugin-level Fastify error boundaries for /v1 routes should call setNoDispatchDiffHeaders(reply) before sending pre-handler JSON errors"
  - "Validation regressions should assert both the OpenAI error body shape and the four DIFF headers on live plugin responses"

requirements-completed: [DIFF-01]

duration: 4 min
completed: 2026-03-22
---

# Phase 13 Plan 02: Plugin-Level Validation DIFF Headers Summary

**Shared no-dispatch DIFF headers now cover plugin-generated `/v1/*` validation and not-found errors, with live regression coverage across chat, embeddings, images, and responses**

## Performance

- **Duration:** 4 min
- **Started:** 2026-03-22T07:12:00Z
- **Completed:** 2026-03-22T07:16:02Z
- **Tasks:** 3
- **Files modified:** 2

## Accomplishments
- Seeded the shared `setNoDispatchDiffHeaders(reply)` helper in `v1Plugin` so Fastify validation and scoped not-found responses keep the same no-dispatch contract as earlier route and stub fixes
- Expanded the live validation suite to assert DIFF headers on 400 schema failures for chat completions, embeddings, image generations, responses, and nested chat message validation
- Re-ran focused regressions, the full API suite, and the required Docker-container API build to confirm DIFF-01 is fully closed without changing shared error-body behavior

## Task Commits

Each task was committed atomically where code changed:

1. **Task 1: Seed no-dispatch DIFF headers inside v1Plugin before plugin-generated error responses** - `14ce89b` (feat)
2. **Task 2: Add live validation regressions that assert DIFF headers on 400 request-schema failures** - `7f26be4` (test)
3. **Task 3: Run full API verification and Docker-only API build for the plugin-level header fix** - verification-only task, no code changes to commit

## Files Created/Modified
- `apps/api/src/routes/v1-plugin.ts` - seeds static no-dispatch DIFF headers in the plugin error and not-found handlers without changing the existing OpenAI-style payloads
- `apps/api/test/routes/typebox-validation.test.ts` - adds `expectNoDispatchHeaders`, embeddings validation coverage, and live 400-path header assertions on the real plugin surface

## Decisions Made
- Reused the shared no-dispatch helper in `v1Plugin` rather than duplicating header literals or widening `sendApiError()` to non-`/v1/*` callers
- Kept the validation suite auth-agnostic so valid payload tests still prove schema acceptance by returning something other than `400`, while invalid payloads now prove both error-shape and DIFF-header contracts

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
- The Docker build command initially hit sandbox denial on the Docker daemon socket; rerunning the same `docker compose exec api ... build` command with approved escalation resolved it and the build passed

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- Phase 13 no longer relies only on representative route-level coverage; plugin-generated validation failures now carry the DIFF-01 headers and are locked by regression tests
- The API surface remains scoped to the documented `/v1/*` plugin boundary, and the phase is ready for roadmap/state closure

## Self-Check: PASSED

Files exist:
- `.planning/phases/13-error-path-diff-headers/13-02-SUMMARY.md` - FOUND

Commits exist:
- `14ce89b` - FOUND (Task 1)
- `7f26be4` - FOUND (Task 2)
