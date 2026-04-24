---
phase: 06-core-text-embeddings-api
plan: 01
subsystem: api
tags: [go, http, accounting, usage, internal-api, service-to-service]

requires:
  - phase: 05-api-keys-hot-path-enforcement
    provides: accounting and usage service layer with reservation lifecycle
provides:
  - Internal accounting endpoints (create/finalize/release reservations)
  - Internal usage endpoints (start attempt, record event)
  - Router registration for internal routes without auth middleware
affects: [06-02, 06-03, 06-04]

tech-stack:
  added: []
  patterns:
    - "Internal endpoints at /internal/* prefix bypass auth middleware"
    - "Service-to-service calls accept account_id in JSON body"

key-files:
  created: []
  modified:
    - apps/control-plane/internal/accounting/http.go
    - apps/control-plane/internal/usage/http.go
    - apps/control-plane/internal/platform/http/router.go

key-decisions:
  - "Internal endpoints use same handler with separate methods to avoid duplicating service layer"
  - "UUID parsing helpers duplicated in usage package to avoid cross-package dependency"

patterns-established:
  - "Internal service-to-service endpoints: POST /internal/{domain}/{action} with account_id in body"

requirements-completed: [API-01]

duration: 3min
completed: 2026-04-08
---

# Plan 06-01: Internal Accounting & Usage Endpoints Summary

**5 internal HTTP endpoints for edge-to-control-plane reservation lifecycle and usage recording without Supabase auth**

## Performance

- **Duration:** ~3 min (orchestrator-assisted)
- **Tasks:** 1
- **Files modified:** 3

## Accomplishments
- Added 3 internal accounting endpoints: create, finalize, and release reservations
- Added 2 internal usage endpoints: start attempt and record event
- Registered all 5 routes in router without auth middleware for service-to-service calls
- Build and vet pass cleanly

## Task Commits

1. **Task 1: Internal accounting & usage endpoints** - `ed6a700` (feat)

## Files Created/Modified
- `apps/control-plane/internal/accounting/http.go` - Internal reservation create/finalize/release handlers
- `apps/control-plane/internal/usage/http.go` - Internal start-attempt and record-event handlers
- `apps/control-plane/internal/platform/http/router.go` - Route registration for /internal/* paths

## Decisions Made
- Used same Handler struct with separate internal methods rather than a new handler to avoid duplicating service injection
- Added parseInternalUUIDField helpers in usage package to avoid cross-package imports

## Deviations from Plan
None - plan executed as specified.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Internal endpoints ready for edge-api clients (06-02) to call
- Accounting reservation lifecycle fully accessible without auth

---
*Phase: 06-core-text-embeddings-api*
*Plan: 01*
*Completed: 2026-04-08*
