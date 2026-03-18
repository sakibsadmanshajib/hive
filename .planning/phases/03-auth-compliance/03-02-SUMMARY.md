---
phase: 03-auth-compliance
plan: 02
subsystem: testing
tags: [openai-sdk, integration-test, auth, content-type, vitest, fastify]

requires:
  - phase: 03-auth-compliance/01
    provides: requireV1ApiPrincipal, v1Plugin onSend hook, OpenAI error envelope
provides:
  - createTestApp helper for booting Fastify with mock services
  - SDK integration test suite proving FOUND-02 and FOUND-05 compliance
affects: [04-chat-completions, 05-models-images, 07-responses-api]

tech-stack:
  added: []
  patterns: [openai-sdk-as-test-client, ephemeral-fastify-server-in-tests, mock-services-pattern]

key-files:
  created:
    - apps/api/test/helpers/test-app.ts
    - apps/api/test/routes/v1-auth-compliance.test.ts
  modified: []

key-decisions:
  - "Mock services include models.list() and rateLimiter.allow() stubs so all registered routes can boot without real services"
  - "Used openai SDK as HTTP client for auth tests (not raw fetch) to prove SDK compatibility"
  - "Used raw fetch for edge cases SDK cannot reproduce (missing header, dual-header)"

patterns-established:
  - "createTestApp pattern: boot real Fastify with mock services on ephemeral port for integration tests"
  - "SDK-as-client pattern: use openai npm package to validate API compliance end-to-end"

requirements-completed: [FOUND-02, FOUND-05]

duration: 2min
completed: 2026-03-18
---

# Phase 3 Plan 2: SDK Auth Compliance Tests Summary

**OpenAI SDK integration tests proving bearer auth, 401 error parsing, and Content-Type enforcement on /v1/* routes**

## Performance

- **Duration:** 2 min
- **Started:** 2026-03-18T03:56:37Z
- **Completed:** 2026-03-18T03:58:33Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- Created reusable createTestApp helper that boots Fastify with mock services on ephemeral port
- 6 SDK integration tests covering all FOUND-02 and FOUND-05 behaviors
- Proved openai SDK parses 401s as AuthenticationError (not generic APIError)
- All 237 tests pass (58 test files, zero regressions)

## Task Commits

Each task was committed atomically:

1. **Task 1: Create test app helper with mock services** - `157da5e` (test)
2. **Task 2: Write SDK integration tests for auth compliance and Content-Type** - `122871b` (test)

## Files Created/Modified
- `apps/api/test/helpers/test-app.ts` - Reusable test helper: createTestApp + createMockServices factory
- `apps/api/test/routes/v1-auth-compliance.test.ts` - 6 integration tests using openai SDK as HTTP client

## Decisions Made
- Mock services extended with `models.list()` and `rateLimiter.allow()` stubs because all routes register at boot (not just auth-tested ones)
- Used openai SDK as primary HTTP client to prove real SDK compatibility; raw fetch only for cases the SDK cannot reproduce (missing Authorization header, dual x-api-key + Bearer)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added models and rateLimiter mocks to test helper**
- **Found during:** Task 2 (SDK integration tests)
- **Issue:** /v1/models returned 500 because mock services lacked `models.list()` — the models route accesses `services.models.list()` at request time
- **Fix:** Added `models: { list: () => [...] }` and `rateLimiter: { allow: async () => true }` to MockServices type and factory
- **Files modified:** apps/api/test/helpers/test-app.ts
- **Verification:** All 6 tests pass, /v1/models returns 200
- **Committed in:** 122871b (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** Essential for route functionality. No scope creep.

## Issues Encountered
None beyond the auto-fixed deviation above.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Auth compliance (FOUND-02, FOUND-05) fully validated with SDK integration tests
- createTestApp helper available for future integration test suites
- Phase 3 complete — ready for Phase 4 (chat completions)

---
*Phase: 03-auth-compliance*
*Completed: 2026-03-18*
