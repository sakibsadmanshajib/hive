---
phase: 10-routing-storage-critical-fixes
plan: 04
subsystem: storage
tags: [go, storage, s3, sigv4, supabase, multipart]

requires:
  - phase: 10-routing-storage-critical-fixes
    provides: Wave 0 red storage tests for Supabase path-style S3 behavior
provides:
  - Shared `packages/storage` S3-over-HTTP client using net/http and AWS SigV4 signing
  - Path-style Supabase endpoint preservation for object, presign, and multipart requests
  - Multipart init/upload/complete/abort lifecycle with quoted ETag preservation
affects: [edge-api, files, images, batches, storage, supabase]

tech-stack:
  added: [github.com/aws/aws-sdk-go-v2 v1.41.5, github.com/aws/smithy-go v1.24.2]
  patterns: [thin S3-over-HTTP adapter, AWS SigV4 signing helper, path-style URL builder]

key-files:
  created:
    - packages/storage/go.sum
    - packages/storage/s3.go
    - packages/storage/signing.go
  modified:
    - packages/storage/go.mod
    - packages/storage/storage.go
    - packages/storage/s3_test.go
    - go.work.sum

key-decisions:
  - "Presigned URLs set X-Amz-Expires explicitly before calling v4.Signer.PresignHTTP because aws-sdk-go-v2 v1.41.5 does not expose a signer Expires option."
  - "UploadPart returns the ETag header exactly as received, including quotes, and CompleteMultipartUpload forwards that value into the XML payload."
  - "Verification uses the Docker toolchain with --entrypoint /bin/sh and /usr/local/go/bin/go so the tests actually execute under this compose entrypoint."

patterns-established:
  - "S3 object paths are built from the endpoint path plus url.PathEscape(bucket/key segments), without path.Join."
  - "All S3 operations sign requests with x-amz-content-sha256: UNSIGNED-PAYLOAD through the shared SignHTTP helper."

requirements-completed: [API-05, API-07]

duration: 11min
completed: 2026-04-20
---

# Phase 10 Plan 04: Shared Storage Client Summary

**Path-style S3-over-HTTP storage client with AWS SigV4 signing, Supabase endpoint preservation, presigned GET URLs, and multipart lifecycle support.**

## Performance

- **Duration:** 11 min
- **Started:** 2026-04-20T03:28:45Z
- **Completed:** 2026-04-20T03:39:27Z
- **Tasks:** 2
- **Files modified:** 7

## Accomplishments

- Implemented `S3Client` as the shared `Storage` implementation using `net/http`, the AWS SigV4 signer, and path-style URLs that preserve endpoint paths such as `/storage/v1/s3`.
- Added signed `PUT`, `GET`, `DELETE`, and presigned GET behavior with deterministic signing time support for tests.
- Added multipart init, part upload, complete, and abort requests with S3 query parameters and XML completion payloads.
- Added regression coverage for escaped bucket/key path segments and quoted multipart ETag preservation.

## Task Commits

Each task was committed atomically, with TDD red commits where new coverage was needed:

1. **Task 1 RED: storage path escaping coverage** - `a21b9d2` (test)
2. **Task 1 GREEN: signed object operations and presign** - `5f40534` (feat)
3. **Task 2 RED: multipart ETag coverage** - `16801bc` (test)
4. **Task 2 GREEN: multipart lifecycle** - `fad638c` (feat)

**Plan metadata:** final docs commit created after state updates.

## Files Created/Modified

- `packages/storage/s3.go` - S3Client implementation, path-style URL builder, object operations, and multipart lifecycle.
- `packages/storage/signing.go` - AWS SigV4 signing and presign helpers using `UNSIGNED-PAYLOAD`.
- `packages/storage/storage.go` - Shared public config, part, and interface definitions after moving implementation to `s3.go`.
- `packages/storage/s3_test.go` - Existing Wave 0 tests plus path escaping and multipart ETag coverage.
- `packages/storage/go.mod` - Adds AWS SDK v2 signer module dependency.
- `packages/storage/go.sum` - Records storage module dependency checksums.
- `go.work.sum` - Records workspace checksum for the AWS SDK module.

## Verification

- **Task 1 RED:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'cd /workspace && /usr/local/go/bin/go test ./packages/storage -run "TestS3ClientUploadEscapesBucketAndKeySegments" -count=1'`
  - Result: exited 1 as expected.
  - Evidence: `Upload returned error: storage implementation pending`.
- **Task 1 GREEN:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'cd /workspace && /usr/local/go/bin/go test ./packages/storage -run "TestNewS3Client|TestS3ClientUpload|TestS3ClientDownload|TestS3ClientDelete|TestS3ClientPresigned" -count=1'`
  - Result: exited 0.
- **Task 2 RED:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'cd /workspace && /usr/local/go/bin/go test ./packages/storage -run "TestS3ClientMultipart|TestS3ClientUploadPartRequiresETag" -count=1'`
  - Result: exited 1 as expected.
  - Evidence: pending multipart implementation and missing-ETag assertion failed red.
- **Task 2 GREEN:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'cd /workspace && /usr/local/go/bin/go test ./packages/storage -run "TestS3ClientMultipart|TestS3ClientUploadPartRequiresETag" -count=1'`
  - Result: exited 0.
- **Final storage suite:** `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'cd /workspace && /usr/local/go/bin/go test ./packages/storage -count=1'`
  - Result: exited 0.
- **No full S3 SDK client:** `rg 'github.com/aws/aws-sdk-go-v2/service/s3|service/s3' packages/storage`
  - Result: no matches.

## Decisions Made

- `S3Client` owns the endpoint, credentials, region, HTTP client, signer, and clock directly instead of retaining the raw `Config`.
- URL construction trims only the endpoint path's trailing slash, escapes bucket/key segments independently, and leaves object-key slash separators intact.
- Presigning follows the AWS signer package contract by adding `X-Amz-Expires` to the query before signing.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Corrected multipart ETag test contract**
- **Found during:** Task 2 (Implement multipart lifecycle)
- **Issue:** The inherited Wave 0 multipart test expected `UploadPart` to strip quotes from the `ETag`, but this plan explicitly requires returning the response header without stripping quotes.
- **Fix:** Updated the test to expect the quoted ETag and to accept the quoted value in the completion XML payload.
- **Files modified:** `packages/storage/s3_test.go`
- **Verification:** Task 2 RED failed for pending multipart behavior; Task 2 GREEN and final storage suite passed.
- **Committed in:** `16801bc` and `fad638c`

**2. [Rule 3 - Blocking] Used corrected Docker toolchain invocation**
- **Found during:** Task 1 and Task 2 verification
- **Issue:** The literal plan command form is swallowed by the toolchain container entrypoint in this repository, and host Go is not the project-standard test path.
- **Fix:** Ran all Go verification through `--entrypoint /bin/sh` and `/usr/local/go/bin/go` inside the Docker toolchain container.
- **Files modified:** None
- **Verification:** Corrected commands executed the intended Go test packages and returned expected red/green results.
- **Committed in:** N/A, execution-only deviation

---

**Total deviations:** 2 auto-fixed (1 bug, 1 blocking issue)
**Impact on plan:** No product scope changed. The fixes aligned tests and verification with the plan's stated storage contract.

## Issues Encountered

- The AWS signer package does not have a presign `Expires` option in `SignerOptions`; the implementation sets `X-Amz-Expires` directly in the query per the package documentation.
- A transient compile error used `resp.StatusText`; the implementation now uses `http.StatusText(resp.StatusCode)`.
- Another executor completed Plan 10-03 in parallel while this plan was running. This plan staged only its storage files and left unrelated `.gitignore` and `.claude/` worktree changes untouched.

## User Setup Required

None - no external service configuration required for httptest-backed storage verification.

## Next Phase Readiness

The shared storage module is ready for Plan 10-05 edge media/file/batch wiring and Plan 10-06 control-plane batch uploader wiring. Bucket/env documentation and repository-wide legacy storage purge remain for later Phase 10 plans.

## Self-Check: PASSED

- Verified created files exist: `packages/storage/go.sum`, `packages/storage/s3.go`, `packages/storage/signing.go`, and this summary.
- Verified modified files exist: `packages/storage/go.mod`, `packages/storage/storage.go`, `packages/storage/s3_test.go`, and `go.work.sum`.
- Verified task commits exist: `a21b9d2`, `5f40534`, `16801bc`, and `fad638c`.

---
*Phase: 10-routing-storage-critical-fixes*
*Completed: 2026-04-20*
