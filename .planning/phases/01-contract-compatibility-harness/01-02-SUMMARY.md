---
phase: 01-contract-compatibility-harness
plan: 02
subsystem: api
tags: [openai, openapi, swagger, middleware, error-handling, support-matrix]

# Dependency graph
requires:
  - phase: 01-contract-compatibility-harness (plan 01)
    provides: "Go module, Docker dev env, health/models endpoints, go.work"
provides:
  - "Pinned OpenAI OpenAPI spec (openapi.yaml)"
  - "Support matrix JSON classifying 148 endpoints into four statuses"
  - "OpenAI error envelope (WriteError, NewError, OpenAIError types)"
  - "Matrix loader with Lookup by method+path"
  - "UnsupportedEndpointMiddleware rejecting non-supported /v1/ endpoints"
  - "CompatHeaders middleware (x-request-id, openai-version, openai-processing-ms)"
  - "Swagger UI at /docs/ serving the OpenAI spec"
  - "Human-readable support-matrix.md documentation"
affects: [02-auth, 03-billing, 04-provider-abstraction, 06-inference-surface]

# Tech tracking
tech-stack:
  added: [swagger-ui-dist CDN v5, OpenAI OpenAPI spec]
  patterns: [openai-error-envelope, support-matrix-driven-middleware, compat-headers, provider-blind-errors]

key-files:
  created:
    - packages/openai-contract/upstream/openapi.yaml
    - packages/openai-contract/matrix/support-matrix.json
    - packages/openai-contract/overlays/hive-support-status.yaml
    - packages/openai-contract/scripts/import-spec.sh
    - packages/openai-contract/scripts/generate-matrix.sh
    - apps/edge-api/internal/errors/openai.go
    - apps/edge-api/internal/errors/openai_test.go
    - apps/edge-api/internal/matrix/types.go
    - apps/edge-api/internal/matrix/loader.go
    - apps/edge-api/internal/matrix/loader_test.go
    - apps/edge-api/internal/middleware/unsupported.go
    - apps/edge-api/internal/middleware/unsupported_test.go
    - apps/edge-api/internal/middleware/compat_headers.go
    - apps/edge-api/internal/middleware/compat_headers_test.go
    - apps/edge-api/docs/swagger.go
    - docs/support-matrix.md
  modified:
    - apps/edge-api/cmd/server/main.go

key-decisions:
  - "Used manual_spec branch for OpenAI spec (master branch returns 404)"
  - "UUID v4 via crypto/rand instead of adding uuid dependency"
  - "Swagger UI loaded from CDN instead of Go embed (spec outside module boundary)"
  - "responseRecorder wrapper intercepts WriteHeader to inject openai-processing-ms timing"

patterns-established:
  - "OpenAI error envelope: all API errors use {error:{message,type,param,code}} JSON shape"
  - "Provider-blind messaging: error messages never mention provider, upstream, or OpenAI"
  - "Support matrix as single source of truth: runtime middleware and docs both derive from support-matrix.json"
  - "Middleware chain order: CompatHeaders (outermost) -> UnsupportedEndpoint (inner) -> handler"

requirements-completed: [COMP-02, COMP-03, API-08]

# Metrics
duration: 6min
completed: 2026-03-28
---

# Phase 01 Plan 02: Contract & Compatibility Layer Summary

**OpenAI contract imported with 148-endpoint support matrix driving runtime middleware, error envelope, compat headers, and Swagger UI**

## Performance

- **Duration:** 6 min
- **Started:** 2026-03-28T07:33:15Z
- **Completed:** 2026-03-28T07:39:00Z
- **Tasks:** 2
- **Files modified:** 17

## Accomplishments
- Imported and pinned the full OpenAI OpenAPI spec from the manual_spec branch (148 endpoints)
- Classified all endpoints into four statuses: 1 supported_now, 24 planned_for_launch, 72 explicitly_unsupported, 51 out_of_scope
- Built OpenAI error envelope (WriteError/NewError) producing exact `{error:{message,type,param,code}}` JSON shape
- Created matrix loader with Lookup by method+path, used by runtime middleware
- UnsupportedEndpointMiddleware rejects non-supported /v1/ endpoints with provider-blind error messages
- CompatHeaders middleware adds x-request-id (UUID v4), openai-version, openai-processing-ms to every response
- Swagger UI serves at /docs/ loading swagger-ui-dist@5 from CDN
- Human-readable support-matrix.md groups all 148 endpoints by status

## Task Commits

Each task was committed atomically:

1. **Task 1: Import OpenAI spec, build support matrix, and create error envelope** - `b1a5d12` (feat)
2. **Task 2: Create middleware, Swagger handler, and wire server** - `6f38f1b` (feat)

## Files Created/Modified
- `packages/openai-contract/upstream/openapi.yaml` - Pinned copy of OpenAI OpenAPI spec
- `packages/openai-contract/upstream/SPEC_VERSION` - Spec version metadata
- `packages/openai-contract/matrix/support-matrix.json` - Machine-readable endpoint classification
- `packages/openai-contract/overlays/hive-support-status.yaml` - OpenAPI Overlay with x-hive-status per endpoint
- `packages/openai-contract/scripts/import-spec.sh` - Spec download script
- `packages/openai-contract/scripts/generate-matrix.sh` - Matrix regeneration reminder script
- `apps/edge-api/internal/errors/openai.go` - OpenAI error envelope builder (WriteError, NewError)
- `apps/edge-api/internal/errors/openai_test.go` - Error envelope table-driven tests
- `apps/edge-api/internal/matrix/types.go` - EndpointStatus type, MatrixEntry, SupportMatrix with Lookup
- `apps/edge-api/internal/matrix/loader.go` - LoadMatrix and LoadMatrixFromBytes
- `apps/edge-api/internal/matrix/loader_test.go` - Matrix loader and lookup tests
- `apps/edge-api/internal/middleware/unsupported.go` - Matrix-driven unsupported endpoint rejection
- `apps/edge-api/internal/middleware/unsupported_test.go` - Middleware tests covering all statuses + provider-blind check
- `apps/edge-api/internal/middleware/compat_headers.go` - OpenAI compatibility response headers
- `apps/edge-api/internal/middleware/compat_headers_test.go` - Header presence, uniqueness, and error response tests
- `apps/edge-api/docs/swagger.go` - Swagger UI handler serving spec from disk
- `apps/edge-api/cmd/server/main.go` - Wired middleware chain, matrix loading, and Swagger route
- `docs/support-matrix.md` - Human-readable endpoint reference table

## Decisions Made
- Used `manual_spec` branch for OpenAI spec download (the `master` and `main` branches return 404)
- Implemented UUID v4 with `crypto/rand` + `fmt.Sprintf` to avoid adding a uuid library dependency
- Used CDN-loaded Swagger UI (swagger-ui-dist@5) instead of Go embed since the spec lives outside the Go module boundary
- Used a responseRecorder wrapper to intercept WriteHeader and inject openai-processing-ms timing header

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Fixed OpenAI spec download URL**
- **Found during:** Task 1 (Import spec)
- **Issue:** The spec URL using `refs/heads/master` returned 404. The research doc noted the spec is on the `manual_spec` branch.
- **Fix:** Changed import-spec.sh to use `refs/heads/manual_spec` branch URL
- **Files modified:** packages/openai-contract/scripts/import-spec.sh
- **Verification:** Script downloads successfully, openapi.yaml contains `openapi: 3.0.0`
- **Committed in:** b1a5d12 (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** Necessary URL correction. No scope creep.

## Issues Encountered
- Go is not installed on the host (Docker-only dev workflow); all tests and builds run via `docker run golang:1.24-alpine`

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Contract layer complete: support matrix, error envelope, and middleware are ready for all future endpoint implementations
- Plan 03 (SDK compatibility harness) can now test against the middleware and error responses
- Phase 2+ endpoint implementations will update support-matrix.json to move endpoints from planned_for_launch to supported_now

## Self-Check: PASSED

All 14 key files verified present. Both task commits (b1a5d12, 6f38f1b) verified in git log.

---
*Phase: 01-contract-compatibility-harness*
*Completed: 2026-03-28*
