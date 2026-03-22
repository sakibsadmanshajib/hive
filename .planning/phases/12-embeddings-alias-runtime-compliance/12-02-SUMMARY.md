---
phase: 12-embeddings-alias-runtime-compliance
plan: 02
subsystem: api
tags: [embeddings, openai-sdk, runtime-services, vitest]

requires:
  - phase: 12-embeddings-alias-runtime-compliance
    provides: canonical public embeddings id and provider-boundary behavior from 12-01
provides:
  - exported RuntimeAiService constructor surface with narrow collaborator types for real test wiring
  - shared Fastify test-app helper that accepts real runtime services without masking the embeddings catalog
  - real-runtime OpenAI SDK embeddings regression proving the public id maps to the upstream provider id
  - fresh focused SDK verification plus full API test and Docker-container API build evidence for DIFF-03 closure
affects: [DIFF-03, openai-sdk-regression, runtime-services, test-app-helper]

tech-stack:
  added: []
  patterns:
    - real-runtime SDK regressions instantiate ModelService plus RuntimeAiService plus ProviderRegistry instead of relying on mock catalog shortcuts
    - shared v1 test helpers can accept either mock ai services or real runtime ai implementations

key-files:
  created: []
  modified:
    - apps/api/src/runtime/services.ts
    - apps/api/test/helpers/test-app.ts
    - apps/api/test/openai-sdk-regression.test.ts

key-decisions:
  - "Export RuntimeAiService with narrow credit, usage, and langfuse collaborator shapes so tests can instantiate the real runtime path without widening production behavior."
  - "Remove the helper's direct `text-embedding-3-small` mock catalog entry so the embeddings SDK regression must exercise the real ModelService lookup."
  - "Assert that the real-runtime SDK success path sends `openai/text-embedding-3-small` upstream while the SDK-visible response model stays `text-embedding-3-small`."

patterns-established:
  - "If an SDK regression is meant to guard runtime model resolution, it must wire the real catalog and provider registry instead of helper-only model lists."
  - "Shared Fastify test harnesses should expose a typed escape hatch for real runtime services rather than duplicating special-purpose app builders."

requirements-completed: [DIFF-03]

duration: artifact recovery after 3 task commits
completed: 2026-03-22
---

# Phase 12 Plan 02: Real-Runtime Embeddings SDK Regression Summary

**The OpenAI SDK embeddings success path now uses the real catalog and runtime wiring, proving `text-embedding-3-small` works publicly while `openai/text-embedding-3-small` stays an internal provider target**

## Performance

- **Duration:** artifact recovery after execution
- **Completed:** 2026-03-22T09:13:27Z
- **Tasks:** 3
- **Files modified:** 3

## Accomplishments
- Exported `RuntimeAiService` with narrow collaborator types so tests can instantiate the real embeddings runtime without changing production method behavior.
- Updated the shared Fastify test helper to accept either mock AI services or a real `RuntimeAiService`, and removed the helper's direct `text-embedding-3-small` catalog masking.
- Replaced the mock-only SDK embeddings success test with a dedicated real-runtime harness that uses `ModelService`, `ProviderRegistry`, and `RuntimeAiService` together.

## Task Commits

Each task was committed atomically where code changed:

1. **Task 1: Export a test-instantiable RuntimeAiService surface and remove the helper's direct embeddings catalog masking** - `71f7174` (fix)
2. **Task 2: Rewrite the embeddings SDK success case to use a real ModelService plus RuntimeAiService plus ProviderRegistry app** - `4c46250` (test)
3. **Task 3: Run focused SDK verification and leave the full API suite plus Docker-only build as explicit phase gates** - `66127b3` (test)

**Plan metadata:** summary/tracking recovered after execution; code commits already existed before this artifact pass

## Files Created/Modified
- `apps/api/src/runtime/services.ts` - exports `RuntimeAiService` and narrows constructor collaborators to the methods the class actually uses.
- `apps/api/test/helpers/test-app.ts` - adds `createTestAppWithServices()` and removes the helper's direct embeddings catalog entry so tests cannot hide alias gaps.
- `apps/api/test/openai-sdk-regression.test.ts` - replaces the helper-backed embeddings success case with a real-runtime harness that asserts the upstream provider receives `openai/text-embedding-3-small`.

## Decisions Made
- Kept the production runtime unchanged while exposing just enough constructor surface to instantiate `RuntimeAiService` directly in tests.
- Preserved the existing shared mock app for other SDK regressions, but required embeddings to go through the real model catalog and provider routing path.
- Used the fake provider client's `embeddings` mock as the proof point for upstream routing, rather than introducing extra route-only assertions.

## Verification Run
- `pnpm --filter @hive/api exec vitest run test/routes/models-route.test.ts test/routes/v1-auth-compliance.test.ts test/routes/v1-stubs.test.ts` - passed (`3` files, `32` tests)
- `pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts -t "real runtime catalog path"` - passed (`1` targeted test, `14` skipped)
- `pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts` - passed (`1` file, `15` tests)
- `pnpm --filter @hive/api test` - passed (`69` files, `368` tests)
- `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` - passed

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- The original executor completed the code commits but did not write the required summary/tracking artifacts, so this summary was recovered from the committed code and rerun verification evidence.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- Phase 12 plan execution is complete, and the remaining step is phase-level verification/closure.
- The real-runtime SDK harness now protects against the exact blind spot called out by the milestone audit: helper-only acceptance of `text-embedding-3-small`.

## Self-Check: PASSED

Files exist:
- `.planning/phases/12-embeddings-alias-runtime-compliance/12-02-SUMMARY.md` - FOUND

Commits exist:
- `71f7174` - FOUND (Task 1)
- `4c46250` - FOUND (Task 2)
- `66127b3` - FOUND (Task 3)
