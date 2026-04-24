---
phase: 10-routing-storage-critical-fixes
plan: 01
subsystem: testing
tags: [go, storage, s3, supabase, edge-api, smoke]

requires:
  - phase: 07-media-file-and-async-api-surface
    provides: Existing file, image, audio, and batch handler contracts
  - phase: 10-routing-storage-critical-fixes
    provides: Phase context and validation contract for critical routing/storage repair
provides:
  - Red shared storage package tests for Supabase path-style S3 behavior
  - Red edge startup tests for required S3 config and public media/file/batch route registration
  - Status-aware Phase 10 runtime smoke script for chat, image, audio, file, and batch probes
affects: [routing, storage, edge-api, files, images, audio, batches, smoke]

tech-stack:
  added: [packages/storage module, POSIX sh smoke script]
  patterns: [httptest red contract tests, status-code-first smoke assertions]

key-files:
  created:
    - packages/storage/go.mod
    - packages/storage/storage.go
    - packages/storage/s3_test.go
    - scripts/phase10-smoke.sh
  modified:
    - go.work
    - apps/edge-api/cmd/server/main_test.go

key-decisions:
  - "Wave 0 storage tests validate constructor env errors but leave S3 methods stubbed with storage implementation pending."
  - "Edge route registration tests target a small helper signature that registers prebuilt handlers onto an http.ServeMux."
  - "Smoke probes capture HTTP status and body files before checking response content."

patterns-established:
  - "Storage path tests pin endpoint path preservation for Supabase URLs that already include /storage/v1/s3."
  - "Runtime smoke checks treat media provider errors as acceptable only after rejecting routing/storage-disabled failure text."

requirements-completed: [ROUT-02, API-05, API-06, API-07]

duration: 9min
completed: 2026-04-18
---

# Phase 10 Plan 01: Wave 0 Red Validation Summary

**Red storage and edge startup contracts plus status-aware runtime smoke probes for the Phase 10 routing/storage repair.**

## Performance

- **Duration:** 9 min
- **Started:** 2026-04-18T22:19:25Z
- **Completed:** 2026-04-18T22:27:35Z
- **Tasks:** 3
- **Files modified:** 6

## Accomplishments

- Added `packages/storage` to `go.work` with the shared `Config`, `CompletePart`, `Storage`, `S3Client`, and `NewS3Client` contract.
- Added red `httptest` coverage for required S3 config, Supabase path-style object URLs, SigV4 headers/presign query params, and multipart request shapes.
- Added red edge startup tests for fail-fast S3 env validation and public media/file/upload/batch route registration.
- Added `scripts/phase10-smoke.sh` with captured status/body checks for chat, image, audio speech, audio transcription, file upload, and batch list probes.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add red shared storage package tests** - `5357ab1` (test)
2. **Task 2: Add red edge startup storage-config tests** - `45d8bf4` (test)
3. **Task 3: Add the status-aware Phase 10 runtime smoke script** - `0b44727` (test)

**Plan metadata:** final docs commit created after state updates.

## Files Created/Modified

- `packages/storage/go.mod` - New shared storage module.
- `packages/storage/storage.go` - Storage contract and pending S3 method stubs.
- `packages/storage/s3_test.go` - Red S3 object, presign, and multipart contract tests.
- `go.work` - Adds `use ./packages/storage`.
- `apps/edge-api/cmd/server/main_test.go` - Red storage config and route registration tests.
- `scripts/phase10-smoke.sh` - Status-aware Phase 10 runtime smoke probe script.

## Verification

- **Task 1 RED:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'cd /workspace && /usr/local/go/bin/go test ./packages/storage -count=1'`
  - Result: exited 1 as expected.
  - Evidence: failures include `Upload returned error: storage implementation pending`, `PresignedURL returned error: storage implementation pending`, and `InitMultipartUpload returned error: storage implementation pending`.
- **Task 2 RED:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'cd /workspace && /usr/local/go/bin/go test ./apps/edge-api/cmd/server -run "TestLoadStorageConfig|TestRegisterMediaFileBatchRoutes" -count=1'`
  - Result: exited 1 as expected.
  - Evidence: build output includes `undefined: loadStorageConfigFromEnv` and `undefined: registerMediaFileBatchRoutes`.
- **Task 3 syntax:** `sh -n scripts/phase10-smoke.sh`
  - Result: exited 0.

## Decisions Made

- `NewS3Client` validates required config immediately so missing-env tests can pass while operation tests still fail red against pending S3 method stubs.
- Route registration tests use sentinel handlers to isolate mux registration from handler construction.
- The smoke script allows non-2xx media responses only when the body is an OpenAI-style JSON error and does not contain routing or storage-disabled failure text.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Corrected Docker verification invocation**
- **Found during:** Task 1 verification
- **Issue:** The plan's literal `docker compose ... run --rm toolchain sh -lc ...` command exited 0 without running tests because `deploy/docker/Dockerfile.toolchain` has `ENTRYPOINT ["/bin/sh", "-c"]`; the container PATH also omitted `/usr/local/go/bin`.
- **Fix:** Verification commands used `--entrypoint /bin/sh` and explicit `/usr/local/go/bin/go` or `/usr/local/go/bin/gofmt`.
- **Files modified:** None
- **Verification:** Corrected commands executed the test binaries and produced the expected red failures.
- **Committed in:** N/A, execution-only deviation

---

**Total deviations:** 1 auto-fixed (Rule 3)
**Impact on plan:** Verification evidence was captured correctly; no product scope changed.

## Issues Encountered

- Host Go tooling was not installed; formatting and Go tests were run through the Docker toolchain container.
- Parallel Phase 10 Plan 02 work produced unrelated commits and transient untracked files during execution. This plan did not modify or stage those files.

## User Setup Required

None - no external service configuration required for this Wave 0 red scaffolding.

## Next Phase Readiness

Plans 10-04 and 10-05 can implement the storage client and edge wiring against these red tests. The smoke script is ready for the final Phase 10 runtime gate once live Supabase Storage credentials and provider routes are available.

## Self-Check: PASSED

- Verified created files exist: `packages/storage/go.mod`, `packages/storage/storage.go`, `packages/storage/s3_test.go`, `scripts/phase10-smoke.sh`, and this summary.
- Verified task commits exist: `5357ab1`, `45d8bf4`, and `0b44727`.
- Verified `requirements-completed` includes `ROUT-02`, `API-05`, `API-06`, and `API-07`.

---
*Phase: 10-routing-storage-critical-fixes*
*Completed: 2026-04-18*
