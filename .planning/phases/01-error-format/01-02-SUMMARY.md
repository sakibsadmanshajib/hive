---
phase: 01-error-format
plan: 02
subsystem: api
tags: [openai, error-format, fastify, routes, sendApiError]

requires:
  - phase: 01-error-format/01
    provides: sendApiError helper, v1Plugin scope, api-error.ts, STATUS_TO_TYPE mapping
provides:
  - All v1 route errors use OpenAI nested error format via sendApiError
  - v1 routes registered inside v1Plugin scope with scoped error/notFound handlers
  - Web pipeline routes remain outside plugin with flat error format
affects: [02-chat-completions, 03-auth-hardening]

tech-stack:
  added: []
  patterns:
    - "sendApiError(reply, status, message, opts?) for all v1 route errors"
    - "v1Plugin registers v1 routes internally; index.ts delegates via app.register(v1Plugin)"

key-files:
  created: []
  modified:
    - apps/api/src/routes/auth.ts
    - apps/api/src/routes/chat-completions.ts
    - apps/api/src/routes/images-generations.ts
    - apps/api/src/routes/responses.ts
    - apps/api/src/routes/v1-plugin.ts
    - apps/api/src/routes/index.ts

key-decisions:
  - "Test FakeApp mocks extended with register/setErrorHandler/setNotFoundHandler stubs rather than refactoring test infrastructure"
  - "Reply mock capture pattern updated to use sentPayload for void sendApiError calls"

patterns-established:
  - "v1 route error pattern: sendApiError(reply, status, message, { code?, param? })"
  - "Route registration: v1 routes inside v1Plugin, web pipeline routes directly in registerRoutes"

requirements-completed: [FOUND-01]

duration: 5min
completed: 2026-03-17
---

# Phase 1 Plan 2: Route Error Migration Summary

**All v1 route errors migrated to OpenAI nested format via sendApiError, with v1 routes registered inside v1Plugin scope**

## Performance

- **Duration:** 5 min
- **Started:** 2026-03-17T21:31:17Z
- **Completed:** 2026-03-17T21:36:18Z
- **Tasks:** 2
- **Files modified:** 11

## Accomplishments
- Migrated auth.ts 401/403 error sends to sendApiError with correct codes (invalid_api_key for 401)
- Migrated chat-completions, images-generations, and responses route errors to sendApiError with rate_limit_exceeded codes and prompt param
- Restructured index.ts to register v1 routes via v1Plugin scope, keeping web pipeline routes with flat error format

## Task Commits

Each task was committed atomically:

1. **Task 1: Migrate auth.ts error sends to sendApiError** - `b347fa2` (feat)
2. **Task 2: Migrate v1 route error sends and restructure route registration** - `c9d1a9f` (feat)

## Files Created/Modified
- `apps/api/src/routes/auth.ts` - Auth errors now use sendApiError (401 with invalid_api_key, 403 for forbidden/settings)
- `apps/api/src/routes/chat-completions.ts` - Rate limit and domain errors use sendApiError
- `apps/api/src/routes/images-generations.ts` - Rate limit, missing prompt, and domain errors use sendApiError
- `apps/api/src/routes/responses.ts` - Rate limit and domain errors use sendApiError
- `apps/api/src/routes/v1-plugin.ts` - Now imports and registers all four v1 route functions
- `apps/api/src/routes/index.ts` - Removed v1 route imports, registers v1Plugin scope instead
- `apps/api/test/routes/analytics-route.test.ts` - FakeApp register/handler stubs
- `apps/api/test/routes/guest-attribution-route.test.ts` - FakeApp register/handler stubs
- `apps/api/test/routes/guest-chat-route.test.ts` - FakeApp register/handler stubs
- `apps/api/test/routes/user-api-keys-route.test.ts` - FakeApp register/handler stubs
- `apps/api/test/routes/images-generations-route.test.ts` - Updated error assertions for nested format
- `apps/api/test/routes/rbac-settings-enforcement.test.ts` - Updated error assertion for nested format

## Decisions Made
- Extended test FakeApp mocks with register/setErrorHandler/setNotFoundHandler stubs rather than refactoring test infrastructure (minimal change approach)
- Updated reply mock capture pattern to use sentPayload variable for void sendApiError calls

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed test FakeApp missing register method**
- **Found during:** Task 2 (route restructuring)
- **Issue:** 4 test files using registerRoutes() had FakeApp without register() method, causing TypeError
- **Fix:** Added register(), setErrorHandler(), setNotFoundHandler() stubs to FakeApp in 4 test files
- **Files modified:** analytics-route.test.ts, guest-attribution-route.test.ts, guest-chat-route.test.ts, user-api-keys-route.test.ts
- **Verification:** All 222 tests pass
- **Committed in:** c9d1a9f (Task 2 commit)

**2. [Rule 1 - Bug] Fixed test assertions for new error format**
- **Found during:** Task 2 (error migration)
- **Issue:** images-generations and rbac-settings tests expected flat { error: "string" } but now get nested OpenAI format; also sendApiError is void so return value is undefined
- **Fix:** Updated assertions to expect nested { error: { message, type, param, code } } and capture payload via sentPayload mock variable
- **Files modified:** images-generations-route.test.ts, rbac-settings-enforcement.test.ts
- **Verification:** All 222 tests pass
- **Committed in:** c9d1a9f (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (2 bugs from changed error format)
**Impact on plan:** Both auto-fixes necessary for test correctness after planned error format migration. No scope creep.

## Issues Encountered
None beyond the test updates documented as deviations.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Phase 01 (error format standardization) is fully complete
- All v1 routes return OpenAI-compatible error envelopes
- Web pipeline routes preserved with flat format
- Ready for Phase 02 (chat completions hardening)

## Self-Check: PASSED

All 6 modified source files verified present. Both task commits (b347fa2, c9d1a9f) verified in git log.

---
*Phase: 01-error-format*
*Completed: 2026-03-17*
