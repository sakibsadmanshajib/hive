---
phase: 10-routing-storage-critical-fixes
plan: 10
subsystem: api
tags: [go, postgres, batches, attribution, control-plane, edge-api]

# Dependency graph
requires:
  - phase: 10-routing-storage-critical-fixes
    provides: Reservation-contract fixes from 10-09 so batch model attribution is available at edge create time
provides:
  - Durable batch attribution columns and internal API fields for api_key_id, model_alias, estimated_credits, and actual_credits
  - Edge batch create propagation of API key, model alias, reservation ID, and estimated credits into control-plane
  - Backward-compatible worker payload fields for batch attribution
affects: [batchstore, filestore, edge-api, accounting, phase-10-gap-closure]

# Tech tracking
tech-stack:
  added: []
  patterns: [migration-owned batch attribution columns, internal batch create attribution contract, backward-compatible worker payload extension]

key-files:
  created:
    - .planning/phases/10-routing-storage-critical-fixes/10-10-SUMMARY.md
    - supabase/migrations/20260420_01_batch_accounting_attribution.sql
  modified:
    - apps/control-plane/internal/filestore/types.go
    - apps/control-plane/internal/filestore/repository.go
    - apps/control-plane/internal/filestore/service.go
    - apps/control-plane/internal/filestore/http.go
    - apps/edge-api/internal/batches/client.go
    - apps/edge-api/internal/batches/handler.go
    - apps/control-plane/internal/batchstore/types.go

key-decisions:
  - "Batch attribution persists directly on public.batches with defaulted columns so migration rollout stays additive and existing rows remain readable."
  - "The internal batch create contract requires model_alias and estimated_credits so terminal settlement has attribution and spend baselines without recomputing edge-only state."
  - "BatchPollPayload carries attribution fields with omitempty tags so already-enqueued jobs still deserialize cleanly."

patterns-established:
  - "Internal control-plane batch responses expose attribution metadata even when public edge responses remain provider-safe."
  - "Edge batch creation forwards attribution explicitly instead of relying on downstream reconstruction from reservation state."

requirements-completed: [API-07, KEY-04]

# Metrics
duration: 7 min
completed: 2026-04-21
---

# Phase 10 Plan 10: Batch Attribution Persistence And Propagation Summary

**Batch attribution now persists on control-plane batch records and flows from edge batch creation into control-plane and worker payload contracts.**

## Performance

- **Duration:** 7 min
- **Started:** 2026-04-20T21:09:45-04:00
- **Completed:** 2026-04-20T21:16:23-04:00
- **Tasks:** 2
- **Files modified:** 11

## Accomplishments

- Added a Supabase migration plus filestore repository/service/http support for `api_key_id`, `model_alias`, `estimated_credits`, and `actual_credits` on batch records.
- Required attribution fields on the internal batch create path and exposed them on internal batch responses for control-plane callers.
- Propagated API key, model alias, reservation ID, and estimated credits from edge batch creation into control-plane create calls.
- Extended batch worker payloads so terminal settlement can consume the attribution data in the next plan without breaking already-enqueued jobs.

## Task Commits

Each task was committed atomically:

1. **Task 1: Persist batch attribution fields in filestore** - `53a25ef`, `7588848` (test, feat)
2. **Task 2: Propagate batch attribution from edge create to control-plane and worker payloads** - `4147747`, `068d87e` (test, feat)

**Plan metadata:** final docs commit created after state updates.

## Files Created/Modified

- `supabase/migrations/20260420_01_batch_accounting_attribution.sql` - Adds durable batch attribution columns and indexes.
- `apps/control-plane/internal/filestore/types.go` - Extends batch metadata with API key, model alias, and credit fields.
- `apps/control-plane/internal/filestore/repository.go` - Persists and reads the new attribution columns and allowlists `actual_credits` updates.
- `apps/control-plane/internal/filestore/service.go` - Validates attribution inputs on internal batch creation and populates persisted batch metadata.
- `apps/control-plane/internal/filestore/http.go` - Accepts attribution on internal batch create and exposes attribution on internal batch responses.
- `apps/control-plane/internal/filestore/repository_test.go` - Adds red source-contract tests for the migration and persistence shape.
- `apps/control-plane/internal/filestore/http_test.go` - Verifies internal batch responses include the attribution JSON fields.
- `apps/edge-api/internal/batches/client.go` - Sends API key, model alias, and estimated credits to `/internal/batches/create`.
- `apps/edge-api/internal/batches/handler.go` - Passes edge-derived attribution into batch creation after reservation succeeds.
- `apps/edge-api/internal/batches/handler_test.go` - Proves control-plane create receives the API key, model alias, estimated credits, and reservation ID.
- `apps/control-plane/internal/batchstore/types.go` - Extends worker payloads with optional attribution fields for terminal settlement.

## Test Evidence

- Task 1 red and green target:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/filestore -run "TestBatchAttributionMigrationAddsColumns|TestCreateBatchPersistsAttributionFields|TestUpdateBatchStatusPersistsAllowedFields|TestInternalBatchResponseIncludesAccountingAttribution" -count=1'`
  - Red result: exited 1 before implementation because `Batch` lacked the attribution fields referenced by the new tests.
  - Green result: exited 0 after the migration, filestore persistence, and internal response changes landed.

- Task 2 red and green target:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/edge-api/internal/batches ./apps/control-plane/internal/batchstore -run "TestBatchCreatePropagatesAttributionToControlPlane|TestBatchCreatePassesModelAliasToReservation|TestBatchWorkerStoresCompletedOutputFiles" -count=1'`
  - Red result: exited 1 before implementation because the handler tests expected the expanded `CreateBatch` signature and propagation fields.
  - Green result: exited 0 after the edge client, handler, and worker payload updates landed.

- Post-task filestore package verification:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/filestore -count=1'`
  - Result: exited 0.

- Post-task edge batch and worker verification:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/edge-api/internal/batches ./apps/control-plane/internal/batchstore -count=1'`
  - Result: exited 0.

## Decisions Made

- Persisted `model_alias` and credit fields directly on `public.batches` instead of reconstructing them later from reservation state so settlement can consume a stable batch record.
- Kept worker payload attribution fields optional to preserve backward compatibility for already-enqueued batch poll jobs.
- Required `model_alias` and `estimated_credits` on the internal create endpoint to fail fast when attribution is missing rather than delaying the error to terminal settlement.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- Unrelated local changes in `.env.example`, `.gitignore`, and `.claude/` were present before execution and were intentionally left untouched.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Plan 10-10 leaves batch attribution durable and propagated end to end, so 10-11 can finalize terminal reservations through accounting and close the remaining KEY-04 and smoke/verification gates.

## Self-Check: PASSED

- Found `10-10-SUMMARY.md`, the new migration, and the key control-plane and edge batch files on disk.
- Found task commits `53a25ef`, `7588848`, `4147747`, and `068d87e` in git history.
- The focused and post-task Docker go test commands all exited 0 after implementation.

---
*Phase: 10-routing-storage-critical-fixes*
*Completed: 2026-04-21*
