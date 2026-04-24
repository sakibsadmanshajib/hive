---
phase: 10-routing-storage-critical-fixes
plan: 06
subsystem: storage
tags: [go, filestore, batchstore, storage, s3, asynq]

# Dependency graph
requires:
  - phase: 10-routing-storage-critical-fixes
    provides: Supabase-owned filestore schema from 10-03 and shared S3 client from 10-04
provides:
  - Internal control-plane filestore JSON fields required by edge clients
  - Allowlist-based batch status update persistence for output IDs, timestamps, and request counts
  - Control-plane batch worker wired to the shared S3 storage client
affects: [edge-api, files, uploads, batches, storage]

# Tech tracking
tech-stack:
  added: [github.com/hivegpt/hive/packages/storage]
  patterns: [internal response metadata serialization, allowlist SQL updates, fail-fast storage startup config, narrow worker interfaces]

key-files:
  created:
    - .planning/phases/10-routing-storage-critical-fixes/10-06-SUMMARY.md
  modified:
    - apps/control-plane/internal/filestore/http.go
    - apps/control-plane/internal/filestore/repository.go
    - apps/control-plane/internal/filestore/repository_test.go
    - apps/control-plane/internal/batchstore/worker.go
    - apps/control-plane/cmd/server/main.go
    - apps/control-plane/go.mod
    - apps/control-plane/go.sum
    - deploy/docker/docker-compose.yml

key-decisions:
  - "Internal control-plane responses now expose storage metadata for edge-api clients while public edge response types keep those values out of customer JSON."
  - "UpdateBatchStatus rejects unsupported update fields instead of ignoring them, so no caller-supplied key can enter generated SQL."
  - "Control-plane records a local replace for packages/storage because Docker go mod tidy otherwise attempts to fetch the private workspace module from GitHub."

patterns-established:
  - "Batch status update SQL is built from a fixed key-to-column allowlist with positional parameters only."
  - "Batch worker depends on a local FileService interface while still returning filestore.File from CreateFile."
  - "Control-plane storage startup uses loadStorageConfigFromEnv and fails fast with storage unavailable errors when S3 config is incomplete."

requirements-completed: [API-07]

# Metrics
duration: 12min
completed: 2026-04-20
---

# Phase 10 Plan 06: Control-Plane Storage Repair Summary

**Control-plane filestore metadata, batch update persistence, and real S3-backed batch output uploads.**

## Performance

- **Duration:** 12 min
- **Started:** 2026-04-20T03:45:43Z
- **Completed:** 2026-04-20T03:57:32Z
- **Tasks:** 3
- **Files modified:** 8

## Accomplishments

- Added internal `storage_path`, `s3_upload_id`, batch output/error IDs, and batch timestamp fields to control-plane filestore responses.
- Replaced the placeholder batch status update with allowlist-based dynamic SQL that persists output IDs, reservation/upstream IDs, request counts, and timestamp fields.
- Wired control-plane batch polling to `packages/storage.NewS3Client` with required S3 config from Docker Compose.
- Introduced a narrow `batchstore.FileService` interface so the completed-output worker test can use fakes without a concrete filestore service.

## Task Commits

Each task was committed atomically:

1. **Task 1: Return internal filestore fields required by edge clients** - `20cc8e2` (feat)
2. **Task 2: Persist allowed batch update fields** - `0c26878` (fix)
3. **Task 3: Wire shared storage into control-plane batch worker** - `edde3d0` (feat)

**Plan metadata:** final docs commit created after state updates.

## Files Created/Modified

- `apps/control-plane/internal/filestore/http.go` - Serializes internal storage and batch metadata fields expected by edge clients.
- `apps/control-plane/internal/filestore/repository.go` - Persists allowed batch update fields with conversion and SQL allowlisting.
- `apps/control-plane/internal/filestore/repository_test.go` - Adds timestamp/count conversion coverage for batch update normalization.
- `apps/control-plane/internal/batchstore/worker.go` - Uses a narrow file-service interface and sends only allowlisted update fields.
- `apps/control-plane/cmd/server/main.go` - Loads S3 config, constructs `storage.NewS3Client`, and passes it to the batch worker.
- `apps/control-plane/go.mod` - Adds the shared storage module requirement and local replace.
- `apps/control-plane/go.sum` - Records storage and tidy-managed checksums.
- `deploy/docker/docker-compose.yml` - Supplies control-plane S3 endpoint, credentials, region, and files bucket env vars.
- `.planning/phases/10-routing-storage-critical-fixes/10-06-SUMMARY.md` - Records execution results and verification.

## Verification

- **Task 1 RED:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/filestore -run "TestInternalFileResponseIncludesStoragePath|TestInternalUploadResponseIncludesMultipartFields|TestInternalBatchResponseIncludesOutputFieldsAndTimestamps" -count=1 -v'`
  - Result: exited 1 as expected, with failures for missing `storage_path`, `s3_upload_id`, and `output_file_id`.
- **Task 1 GREEN:** same command exited 0.
- **Task 2 RED:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/filestore -run TestUpdateBatchStatusPersistsAllowedFields -count=1 -v'`
  - Result: exited 1 as expected, with `UpdateBatchStatus must persist allowed field upstream_batch_id`.
- **Task 2 extra RED:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && gofmt -w apps/control-plane/internal/filestore/repository_test.go && go test ./apps/control-plane/internal/filestore -run "TestUpdateBatchStatusPersistsAllowedFields|TestNormalizeBatchUpdateValue" -count=1 -v'`
  - Result: exited 1 as expected because normalization helpers did not exist.
- **Task 2 GREEN:** same focused command exited 0, and `go test ./apps/control-plane/internal/filestore -count=1` exited 0.
- **Task 3 RED:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/batchstore -run TestBatchWorkerStoresCompletedOutputFiles -count=1 -v'`
  - Result: exited 1 as expected, with `cannot use fileSvc ... as *filestore.Service`.
- **Task 3 GREEN and final suite:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/filestore ./apps/control-plane/internal/batchstore ./apps/control-plane/cmd/server -count=1'`
  - Result: exited 0 after Task 3 and again after the Task 3 commit.

## Decisions Made

- Unsupported batch update keys now return `unsupported batch update field` instead of being ignored. This makes misuse visible and keeps SQL generation constrained to known columns.
- Timestamp update values are stored as UTC `time.Time` values for timestamptz columns after accepting `int`, `int64`, `float64`, or `json.Number` Unix seconds.
- The batch worker no longer includes redundant `status` keys inside the update map. Status remains the explicit `UpdateBatchStatus` argument.
- Control-plane uses a local `replace` for the shared storage module so Docker-only `go mod tidy` does not require private GitHub module access.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Used corrected Docker toolchain invocation**
- **Found during:** Task verification
- **Issue:** The documented `toolchain sh -lc` form is swallowed by this Compose entrypoint, as documented by earlier Phase 10 summaries.
- **Fix:** Used `--entrypoint /bin/sh` with `PATH=/usr/local/go/bin:$PATH` for all real Go verification commands.
- **Files modified:** None
- **Verification:** All RED and GREEN commands executed actual `go test` runs.
- **Committed in:** N/A, execution-only deviation

**2. [Rule 1 - Bug] Removed redundant worker status update-map keys**
- **Found during:** Task 3 (Wire shared storage into control-plane batch worker)
- **Issue:** Task 2 made `UpdateBatchStatus` reject unsupported keys as planned, while the existing worker also placed `status` inside the update map. That would make real worker updates fail even though status is already passed separately.
- **Fix:** Removed `status` from completed, failed, and cancelled update maps.
- **Files modified:** `apps/control-plane/internal/batchstore/worker.go`
- **Verification:** Final control-plane filestore, batchstore, and cmd/server test command exited 0.
- **Committed in:** `edde3d0`

**3. [Rule 3 - Blocking] Added local storage module replace for Docker tidy**
- **Found during:** Task 3 (`go mod tidy`)
- **Issue:** `go mod tidy` in `apps/control-plane` attempted to fetch `github.com/hivegpt/hive/packages/storage` from private GitHub instead of resolving the workspace module.
- **Fix:** Added `require github.com/hivegpt/hive/packages/storage v0.0.0` and `replace github.com/hivegpt/hive/packages/storage => ../../packages/storage`.
- **Files modified:** `apps/control-plane/go.mod`, `apps/control-plane/go.sum`
- **Verification:** `go mod tidy -v` and the final control-plane test command exited 0.
- **Committed in:** `edde3d0`

---

**Total deviations:** 3 auto-fixed (1 bug, 2 blocking issues)
**Impact on plan:** All fixes were necessary to complete the planned behavior. No public API expansion or edge-api implementation work was added by this plan.

## Issues Encountered

- Host `gofmt` was unavailable. Formatting was run inside the Docker toolchain per project convention.
- The worktree had pre-existing or parallel changes in `.gitignore`, `.claude/`, edge-api module files, `apps/edge-api/internal/files/storage.go`, and `go.work.sum`. They were not staged or committed by this plan.

## User Setup Required

None for automated verification. Runtime control-plane startup now requires `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_REGION`, and `S3_BUCKET_FILES`; Docker Compose supplies the files bucket default as `hive-files`.

## Next Phase Readiness

Control-plane storage and batch internals are ready for downstream edge-api storage wiring and Phase 10 smoke verification. Remaining live validation still depends on real Supabase Storage credentials and pre-created buckets.

## Self-Check: PASSED

- Found all modified task files and this summary on disk.
- Found task commits `20cc8e2`, `0c26878`, and `edde3d0` in git history.

---
*Phase: 10-routing-storage-critical-fixes*
*Completed: 2026-04-20*
