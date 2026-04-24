---
phase: 10-routing-storage-critical-fixes
plan: 05
subsystem: storage
tags: [go, edge-api, storage, s3, files, images, audio, batches]

requires:
  - phase: 10-routing-storage-critical-fixes
    provides: Wave 0 edge storage config and route registration tests from 10-01
  - phase: 10-routing-storage-critical-fixes
    provides: Shared packages/storage S3 client from 10-04
provides:
  - Edge API fail-fast S3 storage configuration validation
  - Media, file, upload, and batch route registration after successful storage initialization
  - Edge API handler wiring against the shared packages/storage S3 client
  - Edge module dependency cleanup removing the old storage client dependency
affects: [edge-api, files, uploads, batches, images, audio, storage]

tech-stack:
  added: [github.com/hivegpt/hive/packages/storage]
  patterns: [fail-fast storage config, route-registration helper, shared storage part alias, local module replace]

key-files:
  created:
    - .planning/phases/10-routing-storage-critical-fixes/10-05-SUMMARY.md
  modified:
    - apps/edge-api/cmd/server/main.go
    - apps/edge-api/cmd/server/main_test.go
    - apps/edge-api/internal/files/types.go
    - apps/edge-api/internal/batches/storage_adapter.go
    - apps/edge-api/go.mod
    - apps/edge-api/go.sum
  deleted:
    - apps/edge-api/internal/files/storage.go

key-decisions:
  - "Edge API startup now treats storage as required and exits with storage unavailable errors when any required S3 env var or client setup fails."
  - "files.CompletePart aliases packages/storage.CompletePart so *storage.S3Client satisfies files.StorageBackend directly."
  - "The edge module records a local replace for packages/storage to keep Docker go mod tidy from fetching the private workspace module from GitHub."

patterns-established:
  - "Media, file, upload, and batch routes are registered through registerMediaFileBatchRoutes only after storage config and client construction succeed."
  - "Handlers accept the shared S3 client directly; adapters are limited to narrow compatibility surfaces outside the main edge route wiring."

requirements-completed: [API-05, API-06, API-07]

duration: 13min
completed: 2026-04-20
---

# Phase 10 Plan 05: Edge Storage Wiring Summary

**Edge API storage startup, media/file/batch route registration, and shared S3 client wiring.**

## Performance

- **Duration:** 13 min
- **Started:** 2026-04-20T03:45:35Z
- **Completed:** 2026-04-20T03:58:48Z
- **Tasks:** 3
- **Files modified:** 7

## Accomplishments

- Added `loadStorageConfigFromEnv` and `storageConfig` to require `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_REGION`, `S3_BUCKET_FILES`, and `S3_BUCKET_IMAGES`.
- Replaced optional edge storage degradation with fail-fast `storage.NewS3Client` startup and `log.Fatalf("storage unavailable: %v", err)`.
- Registered images, audio, files, uploads, and batches routes after storage config succeeds, with no silent skip path.
- Passed `*storage.S3Client` directly to image, file, and batch handlers by aliasing file multipart parts to the shared storage type.
- Deleted the old edge file storage client and removed the legacy storage dependency from `apps/edge-api/go.mod` and `go.sum`.

## Task Commits

Each task was committed atomically, with a RED test commit for the Task 2 TDD cycle:

1. **Task 1: Add edge storage config helper and fail-fast startup wiring** - `c540e2d` (feat)
2. **Task 2 RED: Add shared storage wiring contract** - `39fc815` (test)
3. **Task 2 GREEN: Register media, file, upload, and batch routes against shared storage** - `7ec42e0` (feat)
4. **Task 3: Remove edge legacy storage dependency and tidy module** - `b193245` (chore)

**Plan metadata:** final docs commit created after state updates.

## Files Created/Modified

- `apps/edge-api/cmd/server/main.go` - Required storage config, shared S3 client construction, and media/file/batch route registration.
- `apps/edge-api/cmd/server/main_test.go` - Compile-time contract test that the shared S3 client satisfies the file handler storage backend.
- `apps/edge-api/internal/files/types.go` - Aliases `CompletePart` to `packages/storage.CompletePart`.
- `apps/edge-api/internal/batches/storage_adapter.go` - Keeps the batches adapter on a narrow downloader interface without naming the old file storage client.
- `apps/edge-api/go.mod` - Requires `github.com/hivegpt/hive/packages/storage` and records the local workspace replace.
- `apps/edge-api/go.sum` - Tidy-managed checksum cleanup after removing the old dependency graph.
- `apps/edge-api/internal/files/storage.go` - Deleted legacy edge storage client.

## Verification

- **Task 1 RED:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'cd /workspace && /usr/local/go/bin/go test ./apps/edge-api/cmd/server -run TestLoadStorageConfig -count=1'`
  - Result: exited 1 as expected before implementation.
  - Evidence: build output included `undefined: loadStorageConfigFromEnv` and `undefined: registerMediaFileBatchRoutes`.
- **Task 1 GREEN:** same command exited 0.
- **Task 2 RED:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'cd /workspace && /usr/local/go/bin/go test ./apps/edge-api/cmd/server -run TestSharedStorageClientSatisfiesFilesStorageBackend -count=1'`
  - Result: exited 1 as expected.
  - Evidence: `*storage.S3Client does not implement files.StorageBackend` because `CompleteMultipartUpload` used `[]storage.CompletePart` instead of `[]files.CompletePart`.
- **Task 2 GREEN:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'cd /workspace && /usr/local/go/bin/go test ./apps/edge-api/cmd/server ./apps/edge-api/internal/files ./apps/edge-api/internal/images ./apps/edge-api/internal/audio ./apps/edge-api/internal/batches -count=1'`
  - Result: exited 0.
- **Task 3 final suite:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'cd /workspace && /usr/local/go/bin/go test ./apps/edge-api/... ./packages/storage/... -count=1'`
  - Result: exited 0.

## Decisions Made

- Edge storage config validates all required env vars before route registration. Bucket names no longer default in `main.go`; missing buckets are startup errors.
- `registerMediaFileBatchRoutes` is the single route registration point for public media, file, upload, and batch paths in the server package.
- `files.CompletePart` is a type alias, not a copy, so handler tests and the shared storage client compile against the same multipart completion type.
- Edge API uses the same local `replace github.com/hivegpt/hive/packages/storage => ../../packages/storage` pattern as control-plane for Docker-only module tidying.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Used corrected Docker toolchain invocation**
- **Found during:** Task 1, Task 2, and Task 3 verification
- **Issue:** The plan's literal `docker compose ... run --rm toolchain sh -lc ...` form is swallowed by the toolchain image entrypoint in this repository.
- **Fix:** Ran Go verification through `--entrypoint /bin/sh` and `/usr/local/go/bin/go`, matching prior Phase 10 execution.
- **Files modified:** None
- **Verification:** Corrected commands executed the intended Go tests and returned the expected red/green results.
- **Committed in:** N/A, execution-only deviation

**2. [Rule 3 - Blocking] Added route registration helper during Task 1**
- **Found during:** Task 1
- **Issue:** The Task 1 storage-config test package could not compile because Wave 0 had already added `TestRegisterMediaFileBatchRoutesRegistersAllPublicPaths`, which referenced `registerMediaFileBatchRoutes`.
- **Fix:** Added `registerMediaFileBatchRoutes` with the required media, audio, file, upload, and batch paths while implementing Task 1.
- **Files modified:** `apps/edge-api/cmd/server/main.go`
- **Verification:** Task 1 GREEN and Task 2 package verification both passed.
- **Committed in:** `c540e2d`

**3. [Rule 3 - Blocking] Added local replace for shared storage module**
- **Found during:** Task 3
- **Issue:** `go mod tidy` inside `apps/edge-api` tried to fetch `github.com/hivegpt/hive/packages/storage` from private GitHub without a local module replace.
- **Fix:** Added the edge-api local replace for `../../packages/storage`, matching the control-plane module pattern.
- **Files modified:** `apps/edge-api/go.mod`
- **Verification:** `go mod tidy` exited 0 and the final edge/storage Go suite passed.
- **Committed in:** `b193245`

---

**Total deviations:** 3 auto-fixed (3 blocking issues)
**Impact on plan:** All deviations were required to run the planned Docker tests and preserve the intended shared-storage wiring. No product scope was added.

## Issues Encountered

- Docker Compose emitted expected warnings for unset local environment variables during toolchain runs; these did not affect the test containers.
- Another executor worked on Plan 10-06 in parallel and left shared tracking changes plus `10-06-SUMMARY.md` in the worktree. This plan did not stage or modify the untracked 10-06 summary.

## User Setup Required

None for this plan's automated tests. Runtime startup now requires the S3 env vars documented in Phase 10: `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_REGION`, `S3_BUCKET_FILES`, and `S3_BUCKET_IMAGES`.

## Next Phase Readiness

Edge API media, file, upload, and batch routes now depend on the shared storage client and remain registered after startup. Later Phase 10 cleanup can focus on repository-wide legacy storage references and live smoke verification with real Supabase Storage credentials.

## Self-Check: PASSED

- Verified summary and key modified files exist: `apps/edge-api/cmd/server/main.go`, `apps/edge-api/cmd/server/main_test.go`, `apps/edge-api/internal/files/types.go`, `apps/edge-api/internal/batches/storage_adapter.go`, `apps/edge-api/go.mod`, and `apps/edge-api/go.sum`.
- Verified `apps/edge-api/internal/files/storage.go` is deleted.
- Verified task commits exist: `c540e2d`, `39fc815`, `7ec42e0`, and `b193245`.

---
*Phase: 10-routing-storage-critical-fixes*
*Completed: 2026-04-20*
