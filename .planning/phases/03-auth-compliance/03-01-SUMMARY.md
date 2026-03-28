---
phase: 03-auth-compliance
plan: 01
subsystem: auth
tags: [bearer-token, openai-sdk, fastify-hooks, content-type]

# Dependency graph
requires:
  - phase: 01-error-format
    provides: sendApiError with OpenAI error envelope, STATUS_TO_TYPE mapping
  - phase: 02-type-infrastructure
    provides: TypeBox schemas for route validation
provides:
  - requireV1ApiPrincipal function for bearer-only auth on /v1/* routes
  - onSend hook enforcing Content-Type application/json on non-streaming responses
  - openai SDK devDependency for integration testing
affects: [03-auth-compliance plan 02, 04-chat-completions, 05-models-endpoint]

# Tech tracking
tech-stack:
  added: [openai (devDependency)]
  patterns: [bearer-only auth for v1 routes, onSend content-type enforcement]

key-files:
  created: []
  modified:
    - apps/api/src/routes/auth.ts
    - apps/api/src/routes/v1-plugin.ts
    - apps/api/src/routes/chat-completions.ts
    - apps/api/src/routes/images-generations.ts
    - apps/api/src/routes/responses.ts

key-decisions:
  - "requireV1ApiPrincipal is a standalone function, does not call resolvePrincipal — avoids JWT/x-api-key fallback paths"
  - "requiredScope parameter kept for API compatibility but unused in v1 path (scope enforcement deferred)"
  - "onSend hook skips Content-Type override when text/event-stream detected (preserves streaming)"

patterns-established:
  - "Bearer-only auth: v1 routes use requireV1ApiPrincipal exclusively, non-v1 routes keep existing auth"
  - "Content-Type enforcement: onSend hook in v1Plugin scope guarantees application/json; charset=utf-8"

requirements-completed: [FOUND-02, FOUND-05]

# Metrics
duration: 5min
completed: 2026-03-18
---

# Phase 3 Plan 1: Bearer-Only Auth and Content-Type Enforcement Summary

**Bearer-only requireV1ApiPrincipal auth for /v1/* routes with onSend Content-Type enforcement hook**

## Performance

- **Duration:** 5 min
- **Started:** 2026-03-18T03:49:24Z
- **Completed:** 2026-03-18T03:54:26Z
- **Tasks:** 2
- **Files modified:** 15

## Accomplishments
- New `requireV1ApiPrincipal` function: bearer-only auth returning 401 with distinct messages for missing vs invalid tokens
- onSend hook in v1-plugin.ts enforcing `Content-Type: application/json; charset=utf-8` on non-streaming responses
- All three v1 route handlers (chat-completions, images-generations, responses) switched to new auth function
- openai SDK installed as devDependency for upcoming integration tests

## Task Commits

Each task was committed atomically:

1. **Task 1: Create requireV1ApiPrincipal and add Content-Type onSend hook** - `64001b1` (feat)
2. **Task 2: Wire v1 route handlers to requireV1ApiPrincipal and install openai SDK** - `2e775a3` (feat)

## Files Created/Modified
- `apps/api/src/routes/auth.ts` - Added requireV1ApiPrincipal with bearer-only resolution
- `apps/api/src/routes/v1-plugin.ts` - Added onSend hook for Content-Type enforcement
- `apps/api/src/routes/chat-completions.ts` - Switched to requireV1ApiPrincipal
- `apps/api/src/routes/images-generations.ts` - Switched to requireV1ApiPrincipal
- `apps/api/src/routes/responses.ts` - Switched to requireV1ApiPrincipal
- `apps/api/package.json` - Added openai devDependency
- `apps/api/test/routes/chat-completions-route.test.ts` - Updated mocks for bearer-only auth
- `apps/api/test/routes/images-generations-route.test.ts` - Updated mocks for bearer-only auth
- `apps/api/test/routes/responses-route.test.ts` - Updated mocks for bearer-only auth
- `apps/api/test/routes/rbac-settings-enforcement.test.ts` - Updated test for v1 bearer auth
- `apps/api/test/routes/analytics-route.test.ts` - Added addHook stub to FakeApp
- `apps/api/test/routes/guest-chat-route.test.ts` - Added addHook stub to FakeApp
- `apps/api/test/routes/guest-attribution-route.test.ts` - Added addHook stub to FakeApp
- `apps/api/test/routes/user-api-keys-route.test.ts` - Added addHook stub to FakeApp

## Decisions Made
- requireV1ApiPrincipal is standalone and does not call resolvePrincipal, avoiding all JWT session and x-api-key fallback paths
- requiredScope parameter kept for call-site API compatibility but not enforced in v1 path (deferred to future phase)
- onSend hook placement after setNotFoundHandler and before route registrations ensures all v1 responses get Content-Type enforcement

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated test mocks for bearer-only auth**
- **Found during:** Task 2 (wire v1 route handlers)
- **Issue:** Existing tests used session auth (JWT) and x-api-key header mocks which no longer work with requireV1ApiPrincipal
- **Fix:** Updated all v1 route test mocks to use Authorization: Bearer header with resolveApiKey mocks, simplified service mocks removing unused supabaseAuth/authz/userSettings
- **Files modified:** 4 route test files (chat-completions, images-generations, responses, rbac-settings-enforcement)
- **Verification:** All 231 tests pass
- **Committed in:** 2e775a3 (Task 2 commit)

**2. [Rule 1 - Bug] Added addHook stub to FakeApp in tests using registerRoutes**
- **Found during:** Task 2 (wire v1 route handlers)
- **Issue:** Tests using registerRoutes call v1Plugin which now calls app.addHook, but FakeApp lacked addHook method
- **Fix:** Added `addHook() {}` stub to FakeApp in 4 test files
- **Files modified:** analytics-route.test.ts, guest-chat-route.test.ts, guest-attribution-route.test.ts, user-api-keys-route.test.ts
- **Verification:** All 57 test files pass with 0 errors
- **Committed in:** 2e775a3 (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (2 bugs caused by auth changes)
**Impact on plan:** Both auto-fixes necessary for test correctness after switching auth functions. No scope creep.

## Issues Encountered
None beyond the test fixes documented above.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Bearer-only auth established for all /v1/* routes
- Content-Type enforcement active for non-streaming responses
- openai SDK available for plan 02 integration tests
- Existing non-v1 auth paths (JWT session, x-api-key) remain untouched

---
*Phase: 03-auth-compliance*
*Completed: 2026-03-18*
