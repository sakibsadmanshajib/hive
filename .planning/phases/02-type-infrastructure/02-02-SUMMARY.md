---
phase: 02-type-infrastructure
plan: 02
subsystem: api
tags: [typebox, fastify, ajv, validation, openai-compat]

requires:
  - phase: 02-type-infrastructure/01
    provides: TypeBox schemas with additionalProperties:false for all v1 routes
  - phase: 01-error-format
    provides: OpenAI error envelope format and v1Plugin error handler
provides:
  - All 3 POST v1 routes validate requests via AJV/TypeBox schemas
  - Extra/unknown fields rejected with 400 + OpenAI error format
  - Integration test suite proving validation behavior
affects: [03-chat-completions, 07-responses-api]

tech-stack:
  added: []
  patterns: [schema-driven route registration with Fastify opts object, TypeBoxTypeProvider in route function signatures]

key-files:
  created:
    - apps/api/test/routes/typebox-validation.test.ts
  modified:
    - apps/api/src/routes/chat-completions.ts
    - apps/api/src/routes/images-generations.ts
    - apps/api/src/routes/responses.ts
    - apps/api/src/routes/models.ts
    - apps/api/src/routes/v1-plugin.ts

key-decisions:
  - "Used FastifyInstance<any,any,any,any,TypeBoxTypeProvider> generic to propagate type provider through route registration functions"
  - "Added null-safe fallback on sendApiError calls to fix string|undefined type mismatch surfaced by stricter TypeBox inference"

patterns-established:
  - "Schema-driven routes: app.post(path, { schema: { body: Schema } }, handler) replaces app.post<{ Body: T }>(path, handler)"
  - "FakeApp test mocks accept 3-arg post(path, opts, handler) for schema-based registration"

requirements-completed: [FOUND-06, FOUND-07]

duration: 4min
completed: 2026-03-18
---

# Phase 2 Plan 02: Wire TypeBox Schemas Summary

**TypeBox schemas wired into all 3 POST v1 routes with AJV validation rejecting unknown fields as 400 invalid_request_error**

## Performance

- **Duration:** 4 min
- **Started:** 2026-03-18T03:00:39Z
- **Completed:** 2026-03-18T03:05:03Z
- **Tasks:** 2
- **Files modified:** 10

## Accomplishments
- All 3 POST v1 routes (chat/completions, images/generations, responses) now validate requests via TypeBox schemas
- Extra/unknown fields on any POST route return 400 with OpenAI error format (all 4 fields present)
- 9 integration tests prove validation behavior including nested unknown field rejection
- All 231 existing tests pass without regression

## Task Commits

Each task was committed atomically:

1. **Task 1: Wire TypeBox schemas into all 4 v1 route handlers** - `c37b804` (feat)
2. **Task 2: Create integration tests for TypeBox validation** - `576a321` (test)

## Files Created/Modified
- `apps/api/src/routes/chat-completions.ts` - Schema-driven route registration with ChatCompletionsBodySchema
- `apps/api/src/routes/images-generations.ts` - Schema-driven route registration with ImagesGenerationsBodySchema
- `apps/api/src/routes/responses.ts` - Schema-driven route registration with ResponsesBodySchema
- `apps/api/src/routes/models.ts` - Phase 4 comment placeholder for ModelsParamsSchema
- `apps/api/src/routes/v1-plugin.ts` - TypeBoxTypeProvider added to function signature
- `apps/api/test/routes/typebox-validation.test.ts` - 9 integration tests for TypeBox validation
- `apps/api/test/routes/chat-completions-route.test.ts` - FakeApp updated for 3-arg post()
- `apps/api/test/routes/images-generations-route.test.ts` - FakeApp updated for 3-arg post()
- `apps/api/test/routes/responses-route.test.ts` - FakeApp updated for 3-arg post()
- `apps/api/test/routes/rbac-settings-enforcement.test.ts` - FakeApp updated for 3-arg post()

## Decisions Made
- Used `FastifyInstance<any,any,any,any,TypeBoxTypeProvider>` to propagate type provider through route registration functions, enabling proper request.body type inference from schemas
- Added `?? "Unknown error"` fallback on sendApiError calls where stricter TypeBox inference surfaced `string | undefined` for `result.error`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] TypeBoxTypeProvider propagation through function signatures**
- **Found during:** Task 1 (Wire schemas)
- **Issue:** Plain `FastifyInstance` type lost TypeBox type provider, causing `request.body` to be typed as `{}`
- **Fix:** Updated all route registration functions and v1Plugin to use `FastifyInstance<any,any,any,any,TypeBoxTypeProvider>`
- **Files modified:** chat-completions.ts, images-generations.ts, responses.ts, v1-plugin.ts
- **Verification:** `npx tsc --noEmit` passes cleanly
- **Committed in:** c37b804

**2. [Rule 1 - Bug] Null-safe sendApiError calls**
- **Found during:** Task 1 (Wire schemas)
- **Issue:** Stricter TypeBox inference surfaced `result.error` as `string | undefined`, incompatible with `sendApiError(message: string)`
- **Fix:** Added `?? "Unknown error"` fallback on all 3 sendApiError calls
- **Files modified:** chat-completions.ts, images-generations.ts, responses.ts
- **Verification:** `npx tsc --noEmit` passes cleanly
- **Committed in:** c37b804

**3. [Rule 3 - Blocking] FakeApp test mocks incompatible with 3-arg post()**
- **Found during:** Task 2 (Integration tests)
- **Issue:** Existing FakeApp mocks used `post(path, handler)` but schema-based registration calls `post(path, opts, handler)`
- **Fix:** Updated FakeApp.post() in 4 test files to accept both 2-arg and 3-arg forms
- **Files modified:** 4 existing test files
- **Verification:** Full test suite (231 tests) passes
- **Committed in:** 576a321

---

**Total deviations:** 3 auto-fixed (1 bug, 2 blocking)
**Impact on plan:** All auto-fixes necessary for TypeScript compilation and test compatibility. No scope creep.

## Issues Encountered
None beyond the auto-fixed deviations above.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- TypeBox validation infrastructure complete for all v1 routes
- Phase 3 (chat completions) can build on validated request bodies
- Phase 7 (responses API) has schema foundation ready
- ModelsParamsSchema ready for Phase 4 when /v1/models/:model is added

---
*Phase: 02-type-infrastructure*
*Completed: 2026-03-18*
