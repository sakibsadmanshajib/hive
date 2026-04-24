---
phase: 10-routing-storage-critical-fixes
verified: 2026-04-20T19:56:23Z
status: gaps_found
score: 4/6 must-haves verified
gaps:
  - truth: "All 3 previously broken flows pass"
    status: failed
    reason: "Automated route/storage tests pass, but live smoke was skipped and media/batch runtime paths still send accounting requests that control-plane rejects."
    artifacts:
      - path: "apps/edge-api/internal/images/accounting_adapter.go"
        issue: "Sends PolicyMode \"soft\", but control-plane accepts only strict or temporary_overage."
      - path: "apps/edge-api/internal/audio/accounting_adapter.go"
        issue: "Sends PolicyMode \"soft\", but control-plane accepts only strict or temporary_overage."
      - path: "apps/edge-api/internal/batches/accounting_adapter.go"
        issue: "Sends ModelAlias \"\" and PolicyMode \"soft\", both rejected by control-plane reservation validation."
      - path: "apps/edge-api/internal/batches/handler.go"
        issue: "Batch create does not pass any model alias into reservation input and returns 402 when reservation fails."
    missing:
      - "Use an accounting policy mode accepted by control-plane or update the contract consistently."
      - "Derive and pass a valid model alias for batch reservations before creating the batch."
      - "Run the live smoke script with S3_REGION and HIVE_API_KEY after the runtime path is fixed."
  - truth: "Batch final settlement correctly attributes spend and usage per API key and model"
    status: failed
    reason: "Batch worker stores output files and request counts, but it never finalizes the reservation or records actual spend/usage by API key and model. KEY-04 is still Pending in REQUIREMENTS.md and is not claimed by any Phase 10 plan frontmatter."
    artifacts:
      - path: "apps/control-plane/internal/batchstore/worker.go"
        issue: "Completion path updates filestore status only; no accounting finalization, usage event, API-key usage finalization, or model attribution call."
      - path: "apps/control-plane/internal/batchstore/types.go"
        issue: "BatchPollPayload carries reservation_id and endpoint, but no api_key_id, model_alias, or actual-credit data."
      - path: "apps/edge-api/internal/batches/accounting_adapter.go"
        issue: "Initial reservation omits model_alias, so downstream per-model attribution cannot be correct."
      - path: ".planning/REQUIREMENTS.md"
        issue: "KEY-04 remains Pending for Phase 10."
    missing:
      - "Persist or propagate API key and model attribution for batch work."
      - "Finalize or release the batch reservation on terminal batch states."
      - "Record usage/spend events and API-key/model rollups from actual batch completion data."
---

# Phase 10: Routing & Storage Critical Fixes Verification Report

**Phase Goal:** Fix the three infrastructure bugs that break all inference and media endpoints, and fully remove the legacy local object-storage implementation from the codebase.
**Verified:** 2026-04-20T19:56:23Z
**Status:** gaps_found
**Re-verification:** No - initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
| --- | --- | --- | --- |
| 1 | `provider_capabilities` table has all 5 media capability columns via proper SQL migration. | VERIFIED | `supabase/migrations/20260414_01_provider_capabilities_media_columns.sql` alters `public.provider_capabilities`, adds `supports_image_generation`, `supports_image_edit`, `supports_tts`, `supports_stt`, and `supports_batch`, and backfills `route-openrouter-auto`. Fresh targeted routing test exited 0. |
| 2 | File/image/audio/batch endpoints use Supabase Storage REST API with no legacy object-store client dependency. | VERIFIED | `packages/storage` implements the shared SigV4 HTTP client; edge and control-plane construct `storage.NewS3Client`; no old storage module references were found outside the purge script generator. |
| 3 | Batch worker has a wired StorageUploader for output file upload. | VERIFIED | Control-plane startup constructs the shared storage client and passes it to `batchstore.NewBatchWorker`; worker uploads output/error files before creating filestore records. |
| 4 | Zero references to the legacy local object-storage implementation remain in application code, Docker config, or documentation. | VERIFIED | `sh scripts/phase10-scrub-legacy-storage.sh --check` exited 0. Additional `rg` for old storage module names and local endpoint tokens returned no matches outside the scrub script itself. |
| 5 | All 3 previously broken flows pass: image/audio routing, file/batch registration, batch output. | FAILED | Automated package tests pass, but live smoke was skipped because `S3_REGION` and `HIVE_API_KEY` are missing. Code inspection found media/batch accounting adapters send policy/model values rejected by control-plane, so these flows are not proven and batch create can fail before registration. |
| 6 | Batch final settlement correctly attributes spend and usage per API key and model. | FAILED | Batch completion updates filestore status, output IDs, and counts only. No batch path calls reservation finalization or usage recording; `KEY-04` is still Pending in `REQUIREMENTS.md`. |

**Score:** 4/6 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `supabase/migrations/20260414_01_provider_capabilities_media_columns.sql` | Provider media/batch columns and route backfill | VERIFIED | Lines 1-15 add all required columns and backfill `route-openrouter-auto`. |
| `supabase/migrations/20260414_02_filestore_tables.sql` | Migration-owned files/uploads/batches schema | VERIFIED | Present and covered by `apps/control-plane/internal/filestore` tests. |
| `apps/control-plane/internal/routing/repository.go` | No runtime DDL; selects media columns | VERIFIED | `NewPgxRepository` only returns the repository; `ListRouteCandidates` selects all media/batch fields. |
| `packages/storage/storage.go`, `packages/storage/s3.go`, `packages/storage/signing.go` | Shared HTTP SigV4 storage client | VERIFIED | `S3Client` satisfies `Storage`, validates S3 env-derived config, signs requests with `UNSIGNED-PAYLOAD`, and avoids the full S3 SDK client. |
| `apps/edge-api/cmd/server/main.go` | Fail-fast storage config and route registration | VERIFIED | Requires all S3 env vars, constructs `storage.NewS3Client`, and registers image/audio/file/upload/batch routes after storage initialization. |
| `apps/control-plane/cmd/server/main.go` | Control-plane shared storage startup | VERIFIED | Requires S3 endpoint/key/secret/region/files bucket and constructs `storage.NewS3Client` before worker startup. |
| `apps/control-plane/internal/batchstore/worker.go` | Output/error file upload and batch status persistence | PARTIAL | Uploads output/error files and persists counts/status; missing accounting finalization and per-key/per-model usage attribution. |
| `scripts/phase10-smoke.sh` | Status-aware live smoke | PARTIAL | Syntax check passed. Live command was not run because runtime env is incomplete. |
| `scripts/phase10-scrub-legacy-storage.sh` | Repeatable purge check | VERIFIED | `--check` exited 0. |

### Key Link Verification

| From | To | Via | Status | Details |
| --- | --- | --- | --- | --- |
| Routing repository | Provider capability migration | Migrated columns selected by repository | WIRED | Repository query references `c.supports_image_generation`, `c.supports_image_edit`, `c.supports_tts`, `c.supports_stt`, `c.supports_batch`; targeted route tests pass. |
| Edge server | Shared storage package | `storage.NewS3Client` | WIRED | Edge startup passes S3 endpoint/key/secret/region and fails fast on missing values. |
| Edge route registration | Media/file/batch handlers | `registerMediaFileBatchRoutes` | WIRED | Images, audio, files, uploads, and batches routes are registered after storage setup. |
| Control-plane server | Batch worker storage | `storage.NewS3Client` -> `NewBatchWorker` | WIRED | Worker receives non-nil storage client from startup wiring. |
| Batch worker | Filestore repository | `UpdateBatchStatus` field map | WIRED | Output/error file IDs, timestamps, and request counts are allowlisted and persisted. |
| Batch worker | Accounting/usage finalization | Reservation/usage APIs | NOT_WIRED | No `FinalizeReservation`, `ReleaseReservation`, `RecordUsageEvent`, API-key finalization, or model attribution appears in batch worker or batch edge paths. |
| Media/batch adapters | Control-plane accounting | Reservation payload | NOT_WIRED | Adapters send unsupported `PolicyMode: "soft"`; batch also sends `ModelAlias: ""`, while control-plane validation rejects both. |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| --- | --- | --- | --- | --- |
| ROUT-02 | 10-01, 10-02, 10-03, 10-07, 10-08 | Requests route only to approved providers/models satisfying capability matrix and allowlists. | SATISFIED for Phase 10 routing bug | Migration target/backfill is correct; targeted route/media test passed fresh. |
| API-05 | 10-01, 10-02, 10-03, 10-04, 10-05, 10-07, 10-08 | Image endpoints work with supported OpenAI-compatible operations. | BLOCKED | Storage/routing wiring exists, but image accounting adapter uses unsupported policy mode and live smoke did not run. |
| API-06 | 10-01, 10-02, 10-03, 10-05, 10-07, 10-08 | Speech/transcription/translation endpoints work. | BLOCKED | Audio routes and routing capability exist, but audio accounting adapter uses unsupported policy mode and live smoke did not run. |
| API-07 | 10-01, 10-02, 10-03, 10-04, 10-05, 10-06, 10-07, 10-08 | Files/uploads/batches flows work for SDK integrations. | PARTIAL/BLOCKED | File/storage and batch output upload paths are implemented; batch create/final settlement remain broken for accounting and attribution. |
| KEY-04 | Phase init and ROADMAP only; no Phase 10 PLAN frontmatter claims it | Hive tracks usage and spend per API key and per model. | BLOCKED / ORPHANED | `REQUIREMENTS.md` still marks KEY-04 Pending for Phase 10; batch final settlement does not record usage/spend by API key and model. |

**Orphaned requirement:** `KEY-04` is listed in the Phase 10 ROADMAP requirements and in the user-provided init IDs, but no `10-0*-PLAN.md` frontmatter includes it. This is a real gap because the code also lacks the corresponding batch finalization/attribution wiring.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| `apps/edge-api/internal/images/accounting_adapter.go` | 30 | Unsupported policy mode string | Blocker | Image endpoint reservation is rejected by control-plane accounting validation. |
| `apps/edge-api/internal/audio/accounting_adapter.go` | 30 | Unsupported policy mode string | Blocker | Audio endpoint reservation is rejected by control-plane accounting validation. |
| `apps/edge-api/internal/batches/accounting_adapter.go` | 28-30 | Blank model alias and unsupported policy mode | Blocker | Batch reservation cannot carry per-model attribution and is rejected by control-plane validation. |
| `apps/control-plane/internal/batchstore/worker.go` | 93-132 | Missing final settlement side effect | Blocker | Batch completion updates status/counts only; no usage/spend attribution occurs. |

General placeholder scan (`TODO`, `FIXME`, `PLACEHOLDER`, `return null`, empty handlers, `console.log`) found no matches in the Phase 10 touched code set.

### Human Verification Required

#### 1. Live Phase 10 Smoke

**Test:** Populate non-empty `S3_REGION` and `HIVE_API_KEY`, ensure Supabase Storage buckets exist, start the stack, then run `sh scripts/phase10-smoke.sh`.
**Expected:** Chat returns an OpenAI-compatible success response; image/audio return success or provider-level OpenAI-style errors but not routing/no-eligible/storage-disabled/accounting-contract errors; file upload returns `"object":"file"`; batch list returns `"object":"list"`.
**Why human:** Requires live local credentials and external Supabase/provider configuration. Current `.env` has storage credentials except `S3_REGION`; shell also lacks `HIVE_API_KEY`.

#### 2. Batch Completion Accounting

**Test:** After fixing the attribution gap, create a batch, let the worker poll a terminal upstream batch, and inspect usage/spend summaries grouped by API key and model.
**Expected:** Reservation finalizes or releases on terminal state, usage event has non-null `api_key_id` and correct `model_alias`, and spend rollups include the batch actual credits.
**Why human:** Requires a running queue/worker and realistic upstream batch terminal data.

### Gaps Summary

The storage and routing repairs are present: provider capability columns moved to the correct table, route eligibility is backfilled, edge/control-plane startup uses the shared Supabase S3 client, batch output upload is wired, and legacy storage module references are gone.

The phase goal is not fully achieved because runtime media/batch accounting is still inconsistent with control-plane validation, live smoke was not run, and KEY-04 batch final settlement is missing. These gaps block claiming that all previously broken flows pass or that batch spend/usage is attributed per API key and model.

### Commands Run

| Command | Result |
| --- | --- |
| `cat .planning/phases/10-routing-storage-critical-fixes/*-VERIFICATION.md 2>/dev/null || true` | No previous verification found. |
| `sed -n ...` over `ROADMAP.md`, `REQUIREMENTS.md`, all Phase 10 plans/summaries, and `10-VALIDATION.md` | Read planning context and must-haves. |
| `node /home/sakib/.codex/get-shit-done/bin/gsd-tools.cjs verify artifacts ...` | Tool could not parse nested `must_haves` frontmatter; manual verification used instead. |
| `node /home/sakib/.codex/get-shit-done/bin/gsd-tools.cjs verify key-links ...` | Tool could not parse nested `must_haves` frontmatter; manual verification used instead. |
| `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/routing -run "TestProviderCapabilitiesMigrationBackfillsMediaAndBatchRoutes|TestSelectRouteSucceedsForSeededMediaAndBatchCapabilities" -count=1'` | Exited 0. |
| `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/routing ./apps/edge-api/internal/images ./apps/edge-api/internal/audio ./apps/edge-api/internal/files ./apps/edge-api/internal/batches ./packages/storage -count=1'` | Exited 0. |
| `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/... ./apps/edge-api/... ./packages/storage/... -count=1'` | Exited 0. |
| `docker compose --env-file .env -f deploy/docker/docker-compose.yml config --services` | Exited 0; warned `S3_REGION` is unset; listed `control-plane` and `edge-api`. |
| `sh -n scripts/phase10-smoke.sh` | Exited 0. |
| `sh scripts/phase10-scrub-legacy-storage.sh --check` | Exited 0. |
| `rg` inspections for legacy storage references, accounting attribution, route/storage wiring, and placeholder patterns | Found no legacy/placeholder matches; found accounting/KEY-04 gaps described above. |
| `awk` env status probe for smoke vars | `.env` has S3 endpoint/access/secret/buckets set, but not `S3_REGION`; shell lacks `HIVE_API_KEY`. |

---

_Verified: 2026-04-20T19:56:23Z_
_Verifier: Claude (gsd-verifier)_
