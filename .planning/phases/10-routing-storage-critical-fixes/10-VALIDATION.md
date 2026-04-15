---
phase: 10
slug: routing-storage-critical-fixes
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-04-14
---

# Phase 10 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` with stdlib `httptest`; existing JS SDK tests use Vitest |
| **Config file** | Go: none; JS SDK: `packages/sdk-tests/js/vitest.config.ts` |
| **Quick run command** | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/internal/routing ./apps/control-plane/internal/filestore ./apps/control-plane/internal/batchstore ./apps/edge-api/internal/files ./apps/edge-api/internal/images ./apps/edge-api/internal/audio ./apps/edge-api/internal/batches ./apps/edge-api/cmd/server ./packages/storage -count=1'` |
| **Full suite command** | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/... ./apps/edge-api/... ./packages/storage/... -count=1'` |
| **Estimated runtime** | ~180 seconds for quick package set; full suite depends on Docker image cache |

---

## Sampling Rate

- **After every task commit:** Run the narrow package command for touched areas, plus `./packages/storage` tests when storage code changed.
- **After every plan wave:** Run the full Go suite command.
- **Before `$gsd-verify-work`:** Full Go suite must be green and Docker Compose smoke must pass with real Supabase Storage credentials.
- **Max feedback latency:** 180 seconds for package-level validation.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 10-01-01 | 01 | 0 | API-07 | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./packages/storage -count=1'` | W0 creates `packages/storage/s3_test.go` | pending |
| 10-01-02 | 01 | 0 | ROUT-02 | unit/integration | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/internal/routing -run TestListRouteCandidates -count=1'` | Existing tests plus W0 DB-backed case | pending |
| 10-01-03 | 01 | 0 | API-07 | unit/integration | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/internal/filestore -count=1'` | W0 expands control-plane filestore tests | pending |
| 10-01-04 | 01 | 0 | API-07 | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/internal/batchstore -run Batch -count=1'` | W0 creates worker coverage | pending |
| 10-01-05 | 01 | 0 | API-05, API-06, API-07 | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/edge-api/cmd/server ./apps/edge-api/internal/files ./apps/edge-api/internal/images ./apps/edge-api/internal/audio ./apps/edge-api/internal/batches -count=1'` | Existing edge tests plus W0 config/registration tests | pending |
| 10-02-01 | 02 | 1 | ROUT-02 | unit/integration | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/internal/routing -count=1'` | yes | pending |
| 10-03-01 | 03 | 1 | API-07 | unit/integration | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/internal/filestore -count=1'` | yes after W0 | pending |
| 10-04-01 | 04 | 2 | API-07 | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./packages/storage -count=1'` | yes after W0 | pending |
| 10-05-01 | 05 | 3 | API-05, API-06, API-07 | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/edge-api/internal/files ./apps/edge-api/internal/images ./apps/edge-api/internal/audio ./apps/edge-api/internal/batches ./apps/edge-api/cmd/server -count=1'` | yes | pending |
| 10-06-01 | 06 | 3 | API-07 | unit/integration | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/internal/batchstore ./apps/control-plane/internal/filestore ./apps/control-plane/cmd/server -count=1'` | yes after W0 | pending |
| 10-07-01 | 07 | 4 | ROUT-02, API-05, API-06, API-07 | full suite + smoke | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/... ./apps/edge-api/... ./packages/storage/... -count=1'` | yes | pending |

---

## Wave 0 Requirements

- [ ] `packages/storage/s3_test.go` covers path-style endpoint with existing path, `PutObject`, `GetObject`, `DeleteObject`, presign query params, multipart create/upload/complete/abort, and no path normalization.
- [ ] `apps/control-plane/internal/routing/repository_test.go` or equivalent DB-backed test covers `provider_capabilities` media columns and no runtime DDL.
- [ ] `apps/control-plane/internal/filestore/repository_test.go` covers migrated schema assumptions and `UpdateBatchStatus` update-field persistence.
- [ ] `apps/control-plane/internal/filestore/http_test.go` covers internal `storage_path`, `s3_upload_id`, `output_file_id`, and `error_file_id` fields.
- [ ] `apps/control-plane/internal/batchstore/worker_test.go` covers completed upstream batch downloading, storage upload, file metadata creation, and batch update fields.
- [ ] `apps/edge-api/cmd/server/main_test.go` or config helper tests cover fail-fast storage config and route registration.
- [ ] E2E smoke script or documented manual command set covers chat, image, file upload, and batches list against live Docker Compose.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Docker Compose starts with real Supabase Storage credentials and pre-created buckets. | API-05, API-07 | Requires project-specific Supabase S3 endpoint, access key, secret key, region, and buckets. | Set `.env` with `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_REGION`, `S3_BUCKET_FILES`, and `S3_BUCKET_IMAGES`; run `docker compose --env-file .env -f deploy/docker/docker-compose.yml up --build`; verify edge-api and control-plane health endpoints return 200. |
| Chat inference no longer fails due to missing routing capability columns. | ROUT-02 | Requires live API key, provider key, and running LiteLLM/provider path. | `curl -sS -X POST http://localhost:8080/v1/chat/completions -H "Authorization: Bearer $HIVE_API_KEY" -H "Content-Type: application/json" -d '{"model":"auto","messages":[{"role":"user","content":"ping"}]}'`; response must not be 502 from routing SQL failure. |
| Image generation no longer fails due to routing SQL or storage wiring. | API-05 | Requires live provider image route and storage bucket. | `curl -sS -X POST http://localhost:8080/v1/images/generations -H "Authorization: Bearer $HIVE_API_KEY" -H "Content-Type: application/json" -d '{"model":"auto","prompt":"test image","n":1,"size":"1024x1024"}'`; response must be 200 or provider-level error, not routing SQL error or disabled storage error. |
| File upload stores content in Supabase Storage. | API-07 | Requires live Supabase Storage credentials and bucket. | `curl -sS -X POST http://localhost:8080/v1/files -H "Authorization: Bearer $HIVE_API_KEY" -F "purpose=batch" -F "file=@/tmp/hive-phase10.jsonl;type=application/jsonl"`; response must be 200 and include a file id. |
| Batch list endpoint stays registered with storage required at startup. | API-07 | Requires live stack and authenticated account. | `curl -sS http://localhost:8080/v1/batches -H "Authorization: Bearer $HIVE_API_KEY"`; response must be 200. |
| Repository-wide legacy storage purge. | API-05, API-07 | Historical `.planning` docs may need explicit treatment, so final policy must be confirmed during execution. | `rg -i "minio|github.com/minio|minio-go" .` returns only approved historical planning exceptions, or returns no matches if zero-reference policy includes `.planning`. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency target < 180s for package-level validation
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
