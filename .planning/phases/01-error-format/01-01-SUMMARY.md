---
phase: 01-error-format
plan: 01
subsystem: api
tags: [fastify, openai, error-handling, error-format]

# Dependency graph
requires: []
provides:
  - "sendApiError helper for OpenAI-shaped error responses"
  - "STATUS_TO_TYPE mapping (400-429 to OpenAI error types)"
  - "v1Plugin Fastify plugin with scoped error/not-found handlers"
  - "OpenAIErrorType union type"
affects: [01-error-format, 02-chat-completions]

# Tech tracking
tech-stack:
  added: []
  patterns: [skip-override for Fastify plugin scope control, OpenAI error envelope]

key-files:
  created:
    - apps/api/src/routes/api-error.ts
    - apps/api/src/routes/v1-plugin.ts
  modified:
    - apps/api/test/routes/api-error-format.test.ts

key-decisions:
  - "Used Symbol.for('skip-override') instead of fastify-plugin dependency for scope control"
  - "STATUS_TO_TYPE covers 400, 401, 402, 403, 404, 429; all others default to server_error"

patterns-established:
  - "OpenAI error envelope: { error: { message, type, param, code } } with all four fields always present"
  - "v1Plugin scoped error handling: register inside app.register() for encapsulation"

requirements-completed: [FOUND-01]

# Metrics
duration: 3min
completed: 2026-03-17
---

# Phase 1 Plan 1: Error Format Infrastructure Summary

**sendApiError helper and v1Plugin with scoped OpenAI error/not-found handlers covering all HTTP status-to-type mappings**

## Performance

- **Duration:** 3 min
- **Started:** 2026-03-17T21:26:30Z
- **Completed:** 2026-03-17T21:29:16Z
- **Tasks:** 1 (TDD: RED by previous agent, GREEN+REFACTOR in this session)
- **Files modified:** 3

## Accomplishments
- sendApiError helper produces correct OpenAI error shape for all status codes (400, 401, 402, 403, 404, 429, 500+)
- v1Plugin with scoped setErrorHandler catches thrown errors and reformats to OpenAI shape
- v1Plugin setNotFoundHandler returns 404 with OpenAI format for unknown /v1/* routes
- All 12 tests passing, all 222 existing tests unbroken

## Task Commits

Each task was committed atomically:

1. **Task 1 RED: Failing tests** - `fef1a54` (test) - by previous agent
2. **Task 1 GREEN: Implementation** - `faf630e` (feat)

**Plan metadata:** [pending] (docs: complete plan)

_Note: TDD task split across two agents (RED in previous, GREEN in this session)_

## Files Created/Modified
- `apps/api/src/routes/api-error.ts` - sendApiError helper, STATUS_TO_TYPE mapping, OpenAIErrorType type
- `apps/api/src/routes/v1-plugin.ts` - Fastify plugin with scoped error and not-found handlers
- `apps/api/test/routes/api-error-format.test.ts` - 12 tests covering all status mappings, plugin scoping, malformed JSON, isolation

## Decisions Made
- Used `Symbol.for('skip-override')` pattern instead of adding fastify-plugin dependency -- achieves same encapsulation control without new dependency
- Test routes registered at same scope as v1Plugin for proper Fastify encapsulation semantics; isolation test wraps plugin in intermediate scope

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed test route registration for Fastify scoping**
- **Found during:** Task 1 GREEN (implementation)
- **Issue:** Tests registered routes on root app after plugin registration; Fastify scoped error handlers only apply to routes within the plugin's encapsulation context
- **Fix:** Used skip-override on v1Plugin so error handlers apply to registering scope; wrapped isolation test's plugin in intermediate scope to prevent leaking
- **Files modified:** apps/api/src/routes/v1-plugin.ts, apps/api/test/routes/api-error-format.test.ts
- **Verification:** All 12 tests pass, isolation test confirms non-v1 routes unaffected
- **Committed in:** faf630e

---

**Total deviations:** 1 auto-fixed (1 bug fix)
**Impact on plan:** Necessary for correct Fastify scoping behavior. No scope creep.

## Issues Encountered
None beyond the scoping fix documented above.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Error format infrastructure complete, ready for route migration (Plan 02)
- v1Plugin ready to receive route registrations inside its scope
- sendApiError available for direct use in route handlers

---
*Phase: 01-error-format*
*Completed: 2026-03-17*
