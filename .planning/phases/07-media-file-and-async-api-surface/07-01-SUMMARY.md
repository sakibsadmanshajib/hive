---
phase: 07-media-file-and-async-api-surface
plan: 01
subsystem: storage-infrastructure
tags: [legacy local object-store emulator, s3, filestore, routing, postgres, docker]
dependency_graph:
  requires: []
  provides:
    - legacy local object-store emulator-docker-service
    - s3-storage-client
    - filestore-internal-api
    - routing-capability-flags
  affects:
    - apps/edge-api/internal/files
    - apps/control-plane/internal/filestore
    - apps/control-plane/internal/routing
tech_stack:
  added:
    - old object-storage dependency v7.0.91
  patterns:
    - old storage client core for low-level multipart upload access
    - Repository auto-schema via ensureSchema on startup
    - RegisterRoutes function pattern for internal HTTP endpoints
key_files:
  created:
    - apps/edge-api/internal/files/storage.go
    - apps/edge-api/internal/files/types.go
    - apps/control-plane/internal/filestore/types.go
    - apps/control-plane/internal/filestore/repository.go
    - apps/control-plane/internal/filestore/service.go
    - apps/control-plane/internal/filestore/http.go
  modified:
    - deploy/docker/docker-compose.yml
    - apps/edge-api/go.mod
    - apps/edge-api/go.sum
    - apps/control-plane/internal/routing/types.go
    - apps/control-plane/internal/routing/repository.go
    - apps/control-plane/internal/routing/service.go
    - apps/control-plane/internal/routing/http.go
    - apps/control-plane/cmd/server/main.go
decisions:
  - old storage client core used instead of *old storage client because multipart methods (NewMultipartUpload, PutObjectPart, CompleteMultipartUpload, AbortMultipartUpload) are private on Client but public on Core
  - legacy S3-compatible client pinned to v7.0.91 (latest compatible with Go 1.24 — v7.0.100+ requires Go 1.25)
  - routing ensureCapabilityColumns is non-fatal at startup to match existing pattern where tables may not yet be seeded
metrics:
  duration: 9min
  completed: "2026-04-09"
  tasks: 3
  files: 8
---

# Phase 07 Plan 01: Storage Infrastructure, Filestore Service, and Routing Extensions Summary

legacy local object-store emulator S3 storage in Docker Compose, legacy S3-compatible client storage client, control-plane filestore internal API, and routing extended with 5 new capability flags (image generation/edit, TTS, STT, batch).

## Tasks Completed

| # | Name | Commit | Key Files |
|---|------|--------|-----------|
| 1 | legacy local object-store emulator Docker service, S3 storage client, and environment wiring | 7c5757d | deploy/docker/docker-compose.yml, apps/edge-api/internal/files/storage.go, apps/edge-api/internal/files/types.go |
| 2 | Postgres schemas and control-plane filestore service | b8ec373 | apps/control-plane/internal/filestore/{types,repository,service,http}.go, cmd/server/main.go |
| 3 | Routing capability flag extensions for image, audio, and batch | 326ca69 | apps/control-plane/internal/routing/{types,repository,service,http}.go |

## What Was Built

**Task 1:** Added `legacy local object-store emulator` and `legacy object-store bucket init` services to docker-compose.yml with health check and bucket initialization for `hive-files` and `hive-images` buckets. Added `legacy object-store data volume` volume. Wired S3 env vars into both `edge-api` and `control-plane` services. Created `apps/edge-api/internal/files` package with `StorageClient` (using `old storage client core` for multipart access), `StorageClient.Upload/Download/Delete/PresignedURL/InitMultipartUpload/UploadPart/CompleteMultipartUpload/AbortMultipartUpload`, and OpenAI-compatible types (`FileObject`, `UploadObject`, `UploadPartObject`, `FileListResponse`, `DeletedFileResponse`).

**Task 2:** Created `apps/control-plane/internal/filestore` package with auto-schema tables for `files`, `uploads`, `upload_parts`, and `batches`. Repository implements full CRUD with account_id scoping. Service adds business logic: `file-{uuid}` / `upload-{uuid}` / `batch-{uuid}` ID generation, `{account_id}/{id}/{filename}` storage path derivation, batch expiry 24h, file expiry 30d for batch purpose, upload expiry 1h. Internal HTTP API registered via `RegisterRoutes` with 15 endpoints. Wired into control-plane main.go after existing service initialization.

**Task 3:** Extended `SelectionInput` with `NeedImageGeneration/NeedImageEdit/NeedTTS/NeedSTT/NeedBatch`. Extended `RouteCandidate` with matching `Supports*` booleans. Added `ALTER TABLE route_capabilities ADD COLUMN IF NOT EXISTS` for all 5 columns on startup. Extended `matchesRequestedCapabilities` with 5 new filter checks. Extended HTTP request struct and `SelectionInput` mapping.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] legacy S3-compatible client multipart methods not available on *old storage client**
- **Found during:** Task 1 build
- **Issue:** Plan specified `client.NewMultipartUpload`, `client.PutObjectPart`, `client.CompleteMultipartUpload`, `client.AbortMultipartUpload` but these are unexported on `*old storage client` in v7
- **Fix:** Used `old storage client core` struct (which embeds `*old storage client`) — Core exposes all these as public methods
- **Files modified:** apps/edge-api/internal/files/storage.go
- **Commit:** 7c5757d

**2. [Rule 1 - Bug] legacy S3-compatible client v7 latest requires Go 1.25**
- **Found during:** Task 1 dependency fetch
- **Issue:** `go get old object-storage dependency@latest` resolved v7.0.100 which requires Go 1.25; project uses Go 1.24
- **Fix:** Pinned to v7.0.91, the most recent version compatible with Go 1.24
- **Files modified:** apps/edge-api/go.mod, apps/edge-api/go.sum
- **Commit:** 7c5757d

## Verification

- `go build ./apps/edge-api/internal/files/...` — PASSED
- `go build ./apps/control-plane/...` — PASSED
- `go test ./apps/control-plane/internal/routing/...` — PASSED (ok, 0.008s)
- docker-compose.yml contains legacy local object-store emulator, legacy object-store bucket init, S3 env vars for edge-api and control-plane — VERIFIED

## Self-Check: PASSED
