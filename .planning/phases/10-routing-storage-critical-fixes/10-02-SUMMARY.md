---
phase: 10-routing-storage-critical-fixes
plan: 02
subsystem: testing
tags: [go, routing, filestore, batchstore, supabase, tdd]

# Dependency graph
requires:
  - phase: 10-routing-storage-critical-fixes
    provides: Phase 10 research and validation contracts
provides:
  - Red routing schema ownership tests for provider_capabilities media columns and media/batch backfill
  - Red filestore migration ownership and internal response contract tests
  - Red batch worker output upload and status persistence seam test
affects: [10-03, 10-06, routing, filestore, batchstore]

# Tech tracking
tech-stack:
  added: []
  patterns: [source-and-migration contract tests, internal response JSON field assertions, fakeable service seam test]

key-files:
  created:
    - apps/control-plane/internal/routing/repository_schema_test.go
    - apps/control-plane/internal/filestore/repository_test.go
    - apps/control-plane/internal/filestore/http_test.go
    - apps/control-plane/internal/batchstore/worker_test.go
  modified:
    - apps/control-plane/internal/routing/service_test.go

key-decisions:
  - "Wave 0 stayed red-only: production routing, filestore, and batch worker code was not changed."
  - "Verification used corrected Docker toolchain invocation because the documented sh -lc form is swallowed by the toolchain entrypoint."

patterns-established:
  - "Migration ownership tests read repository source and supabase/migrations SQL from the repository root."
  - "Internal HTTP contract tests marshal package-private response helpers and assert exact JSON field names."
  - "Batch worker test defines the desired batchFileService seam for Plan 10-06."

requirements-completed: [ROUT-02, API-05, API-06, API-07]

# Metrics
duration: 8min
completed: 2026-04-18
---

# Phase 10 Plan 02: Wave 0 Red Validation Summary

**Red validation scaffolding for routing schema ownership, filestore internal contracts, and batch output persistence**

## Performance

- **Duration:** 8 min
- **Started:** 2026-04-18T22:19:50Z
- **Completed:** 2026-04-18T22:27:21Z
- **Tasks:** 3
- **Files modified:** 5

## Accomplishments

- Added routing source/migration tests that fail until runtime capability DDL is removed and `public.provider_capabilities` owns media/batch columns plus `route-openrouter-auto` backfill.
- Added filestore tests that fail until schema DDL moves to Supabase migrations, internal responses include storage/batch fields, and `UpdateBatchStatus` persists allowed update fields.
- Added a batch worker test that fails until `BatchWorker` accepts a fakeable file-service seam and persists completed output/error files plus request counts.

## RED Verification

All targeted commands were expected to exit non-zero in this Wave 0 plan.

1. **Routing**
   - Command used: `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && gofmt -w apps/control-plane/internal/routing/repository_schema_test.go apps/control-plane/internal/routing/service_test.go && go test ./apps/control-plane/internal/routing -run "TestRoutingRepositoryDoesNotRunCapabilityDDL|TestProviderCapabilitiesMigrationAddsMediaColumns|TestProviderCapabilitiesMigrationBackfillsMediaAndBatchRoutes|TestListRouteCandidatesSelectsMediaColumns|TestSelectRouteSucceedsForSeededMediaAndBatchCapabilities" -count=1 -v'`
   - Result: exit 1.
   - Failure reason: `repository.go` still contains `ensureCapabilityColumns`, and no migration yet alters `public.provider_capabilities` with the five media/batch columns.

2. **Filestore**
   - Command used: `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && gofmt -w apps/control-plane/internal/filestore/repository_test.go apps/control-plane/internal/filestore/http_test.go && go test ./apps/control-plane/internal/filestore -run "TestFilestoreSchemaLivesInSupabaseMigration|TestUpdateBatchStatusPersistsAllowedFields|TestInternal" -count=1 -v'`
   - Result: exit 1.
   - Failure reason: internal responses omit `storage_path`, `s3_upload_id`, and `output_file_id`; `NewRepository` still calls `ensureSchema`; `UpdateBatchStatus` does not persist allowed fields such as `upstream_batch_id`.

3. **Batch Worker**
   - Command used: `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && gofmt -w apps/control-plane/internal/batchstore/worker_test.go && go test ./apps/control-plane/internal/batchstore -run TestBatchWorkerStoresCompletedOutputFiles -count=1 -v'`
   - Result: exit 1.
   - Failure reason: compile failure, `cannot use fileSvc (variable of type *fakeBatchFileService) as *filestore.Service value in argument to NewBatchWorker`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add red routing schema, seed backfill, and media selection tests** - `dc94a99` (test)
2. **Task 2: Add red filestore migration and internal response tests** - `2cb1504` (test)
3. **Task 3: Add red batch worker output-upload test** - `df92ec5` (test)

## Files Created/Modified

- `apps/control-plane/internal/routing/repository_schema_test.go` - Source and migration assertions for provider capability media columns, no runtime DDL, and route backfill.
- `apps/control-plane/internal/routing/service_test.go` - Table-driven route selection coverage for seeded `hive-auto` media and batch capabilities.
- `apps/control-plane/internal/filestore/repository_test.go` - Source and migration assertions for filestore schema ownership and `UpdateBatchStatus` allowed fields.
- `apps/control-plane/internal/filestore/http_test.go` - Internal JSON response field assertions for files, uploads, and batches.
- `apps/control-plane/internal/batchstore/worker_test.go` - Completed-batch output/error file upload and status update contract test with the desired fakeable seam.

## Decisions Made

- Wave 0 stayed red-only. No production implementation was added in this plan.
- Batch worker test intentionally defines the desired `batchFileService` seam and fails to compile until Plan 10-06 changes `BatchWorker` away from concrete `*filestore.Service`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Corrected Docker toolchain verification command shape**
- **Found during:** Task 1 verification
- **Issue:** The documented `docker compose ... run --rm toolchain sh -lc '...'` command is swallowed by the `toolchain` service entrypoint (`/bin/sh -c`) and can exit 0 without running Go. Overriding the entrypoint also requires adding `/usr/local/go/bin` to `PATH`.
- **Fix:** Used `docker compose ... run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; ...'` for all real RED verification commands.
- **Files modified:** None
- **Verification:** Corrected commands ran `go test` and produced the expected non-zero RED failures for routing, filestore, and batchstore.
- **Committed in:** Documentation only in final metadata commit.

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** Verification output is stronger than the original command form because the tests actually executed. No production scope changed.

## Issues Encountered

- Expected RED failures were captured for all three tasks.
- The workspace had an unrelated untracked `.claude/` directory before and after execution; it was not modified or committed.

## User Setup Required

None - no external service configuration required for this red-test scaffolding.

## Next Phase Readiness

Plans 10-03 and 10-06 can now implement against explicit failing tests:

- 10-03 should remove runtime routing/filestore DDL and add Supabase migrations.
- 10-06 should add filestore update persistence, internal response fields, batch worker storage wiring, and the fakeable worker file-service seam.

## Self-Check: PASSED

- Found all created/modified task files and this summary on disk.
- Found task commits `dc94a99`, `2cb1504`, and `df92ec5` in git history.

---
*Phase: 10-routing-storage-critical-fixes*
*Completed: 2026-04-18*
