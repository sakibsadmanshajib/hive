---
phase: 07-media-file-and-async-api-surface
plan: "03"
subsystem: api
tags: [files-api, uploads-api, batches-api, asynq, legacy local object-store emulator, go, multipart, async-worker]

# Dependency graph
requires:
  - phase: 07-01
    provides: legacy local object-store emulator storage client, filestore/batchstore control-plane services, internal HTTP endpoints
  - phase: 07-02
    provides: edge-api handler patterns, authz snapshot, OpenAI error helpers
provides:
  - Files API edge handlers (upload, list, retrieve, delete, download content)
  - Uploads API edge handlers (create, add-part, complete, cancel) with S3 multipart
  - FilestoreClient - internal HTTP client from edge-api to control-plane filestore
  - Batches API edge handlers (create, list, retrieve, cancel)
  - BatchstoreClient - internal HTTP client from edge-api to control-plane batchstore
  - Asynq-based batch polling worker in control-plane with output file assembly
  - Adapter layer (accounting, authz, file, storage) for batches package
affects: [phase-08-payments, future fine-tuning, future assistants]

# Tech tracking
tech-stack:
  added: [asynq (batch worker task queue)]
  patterns:
    - Adapter pattern isolating batches package from direct service dependencies
    - Internal HTTP client pattern (FilestoreClient, BatchstoreClient) for edge-to-control-plane communication
    - S3 multipart upload lifecycle (init → add parts → complete/abort) via legacy local object-store emulator StorageClient
    - Account ownership enforcement at every Files/Uploads/Batches operation boundary

key-files:
  created:
    - apps/edge-api/internal/files/client.go
    - apps/edge-api/internal/files/handler.go
    - apps/edge-api/internal/files/handler_test.go
    - apps/edge-api/internal/batches/handler.go
    - apps/edge-api/internal/batches/handler_test.go
    - apps/edge-api/internal/batches/client.go
    - apps/edge-api/internal/batches/types.go
    - apps/edge-api/internal/batches/accounting_adapter.go
    - apps/edge-api/internal/batches/authz_adapter.go
    - apps/edge-api/internal/batches/file_adapter.go
    - apps/edge-api/internal/batches/storage_adapter.go
    - apps/control-plane/internal/batchstore/worker.go
    - apps/control-plane/internal/batchstore/types.go
  modified:
    - apps/edge-api/cmd/server/main.go
    - apps/control-plane/cmd/server/main.go
    - apps/control-plane/go.mod
    - apps/edge-api/go.mod

key-decisions:
  - "FilestoreClient and BatchstoreClient use plain http.Client with 10s timeout — no shared transport needed at this scale"
  - "Batches package uses adapter layer (accounting_adapter, authz_adapter, file_adapter, storage_adapter) to decouple handler from direct service imports"
  - "Batch worker uses Asynq for task queue — consistent with control-plane async patterns established in earlier phases"
  - "All file/upload/batch operations validate account ownership via AuthSnapshot.AccountID before any data access"

patterns-established:
  - "Internal client pattern: edge-api packages call control-plane via typed HTTP clients (FilestoreClient, BatchstoreClient)"
  - "Adapter isolation: domain packages (batches) depend on local interfaces, adapters in main.go wire real implementations"
  - "Multipart lifecycle: create upload → add parts via S3 UploadPart → complete merges S3 object and creates File record"

requirements-completed: [API-07]

# Metrics
duration: ~45min
completed: 2026-04-10
---

# Phase 07 Plan 03: Files API, Uploads API, and Batches API Summary

**Files API, Uploads API, and Batches API with Asynq-based async batch polling worker — full batch processing workflow from JSONL upload to result assembly**

## Performance

- **Duration:** ~45 min (two sessions; second session cut short before finalization)
- **Started:** 2026-04-10T00:20:00Z
- **Completed:** 2026-04-10
- **Tasks:** 2
- **Files modified:** 17

## Accomplishments

- Files API and Uploads API edge handlers with S3 multipart support and account ownership enforcement at every boundary
- FilestoreClient and BatchstoreClient as typed internal HTTP clients bridging edge-api to control-plane
- Batches API handlers (create, list, retrieve, cancel) with JSONL validation, credit reservation, and account isolation
- Asynq batch polling worker in control-plane that polls upstream providers and assembles output/error files on completion
- Adapter layer in batches package (accounting, authz, file, storage) cleanly decoupling handler logic from service wiring

## Task Commits

Each task was committed atomically:

1. **Task 1: Files API and Uploads API edge handlers** - `1bf0ccd` (feat)
2. **Task 2: Batches API edge handlers and async batchstore worker** - `ea6c862` (feat)

**Plan metadata:** (docs commit — see below)

## Files Created/Modified

- `apps/edge-api/internal/files/client.go` - FilestoreClient: HTTP client to control-plane filestore internal endpoints
- `apps/edge-api/internal/files/handler.go` - Files API and Uploads API HTTP handlers
- `apps/edge-api/internal/files/handler_test.go` - Tests for file upload, list, retrieve, delete, content, upload lifecycle
- `apps/edge-api/internal/batches/handler.go` - Batches API HTTP handlers (create, list, get, cancel)
- `apps/edge-api/internal/batches/handler_test.go` - Tests for batch create, retrieve, list, cancel
- `apps/edge-api/internal/batches/client.go` - BatchstoreClient: HTTP client to control-plane batchstore internal endpoints
- `apps/edge-api/internal/batches/types.go` - Batch domain types (BatchObject, BatchRequest, BatchError)
- `apps/edge-api/internal/batches/accounting_adapter.go` - Adapter wiring accounting service for credit reservation
- `apps/edge-api/internal/batches/authz_adapter.go` - Adapter wiring authz service for account validation
- `apps/edge-api/internal/batches/file_adapter.go` - Adapter wiring filestore client for input file validation
- `apps/edge-api/internal/batches/storage_adapter.go` - Adapter wiring storage client for output file assembly
- `apps/control-plane/internal/batchstore/worker.go` - Asynq HandleBatchPoll worker: polls provider, updates status, writes output file
- `apps/control-plane/internal/batchstore/types.go` - Batchstore types for worker task payloads
- `apps/edge-api/cmd/server/main.go` - Wired files and batches routes
- `apps/control-plane/cmd/server/main.go` - Wired batchstore worker with Asynq server
- `apps/control-plane/go.mod` / `apps/edge-api/go.mod` - Dependency updates for asynq

## Decisions Made

- FilestoreClient and BatchstoreClient are plain `http.Client` with 10s timeout — no shared transport needed at this scale; simpler and easier to test
- Batches package uses adapter interfaces rather than direct service imports — allows clean unit testing of handler logic without real DB/storage
- Asynq selected for batch worker task queue — consistent with async patterns in control-plane; simple Redis-backed queue fits polling use case
- Account ownership validated at every operation boundary using `AuthSnapshot.AccountID` from the authz package

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- Second session was cut short before finalization (SUMMARY.md, STATE.md, ROADMAP.md updates), requiring this recovery pass. All code was complete.

## User Setup Required

None - no external service configuration required beyond what was set up in Phase 07-01 (legacy local object-store emulator credentials, Redis for Asynq).

## Next Phase Readiness

- Files/Uploads/Batches API surface is complete and ready for integration testing
- Batch worker polls upstream providers and assembles output files — ready for Phase 08 payment gating and quota enforcement
- The internal HTTP client pattern (FilestoreClient, BatchstoreClient) is established as the standard edge-to-control-plane communication layer for Phase 08+

---
*Phase: 07-media-file-and-async-api-surface*
*Completed: 2026-04-10*
