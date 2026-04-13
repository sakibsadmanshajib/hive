# Phase 10: Routing & Storage Critical Fixes - Context

**Gathered:** 2026-04-12
**Status:** Ready for planning

<domain>
## Phase Boundary

Fix the three infrastructure bugs that break all inference and media endpoints, replace the minio-go storage client with a portable S3-compatible thin client backed by Supabase Storage, wire the batch worker's StorageUploader, convert filestore auto-schema to proper Supabase migrations, and fully purge every MinIO reference from the codebase. This phase does not add new endpoints, change routing logic, or expand the API surface — it makes existing code work correctly.

</domain>

<decisions>
## Implementation Decisions

### Storage Client Approach
- Custom thin S3 client (~150 lines) wrapping net/http with S3v4 request signing
- Use a standalone S3v4 signing library — not from-scratch HMAC, not full AWS SDK
- Path-style URLs: `endpoint/bucket/key` format (Supabase S3 endpoint supports this)
- Zero minio-go dependency — full purge from go.mod, go.sum, all imports, all code, all comments, all docs

### Storage Package Architecture
- New shared Go module at `packages/storage/` in the go.work workspace
- Package defines a `Storage` interface for portability across S3-compatible backends
- Ships with an `S3Client` struct that implements `Storage` — works with any S3-compatible endpoint (Supabase, AWS S3, Cloudflare R2, etc.)
- Switching providers = new config values, same adapter. Future non-S3 backends get their own adapter
- Both edge-api and control-plane import `packages/storage`
- Each consuming service defines its own narrow interface (StorageBackend, StorageUploader) that the concrete `*S3Client` satisfies — standard Go accept-interfaces pattern

### Degradation Behavior
- Storage credentials are **required for startup** — no graceful skip, no fallback
- Every developer needs a Supabase project with storage enabled (already required for DB)
- Services fail fast if S3 credentials are missing or invalid
- No env-var gate — storage is always required in all environments

### Bucket Provisioning
- Buckets (hive-files, hive-images) are pre-provisioned via Supabase dashboard or CLI
- App does not auto-create buckets — keeps S3 client pure and provider-portable
- Fails fast on first storage operation if bucket doesn't exist
- Document bucket creation in setup instructions

### Migration Strategy
- **Remove `ensureCapabilityColumns` entirely** from `routing/repository.go` — no runtime ALTER TABLE
- Add proper Supabase migration for the 5 media capability columns on `provider_capabilities` table:
  - `supports_image_generation`, `supports_image_edit`, `supports_tts`, `supports_stt`, `supports_batch`
- **Convert filestore `ensureSchema` to proper Supabase migrations** — move all CREATE TABLE/INDEX statements for `files`, `uploads`, `upload_parts`, `batches` from Go code into `supabase/migrations/`
- Delete `ensureSchema()` and `ensureCapabilityColumns()` functions and their call sites
- All schema management through Supabase migrations only — no runtime DDL

### Dependency Purge
- Full minio-go removal: go.mod, go.sum, all import paths, all code references
- Run `go mod tidy` after removal to clean transitive dependencies
- Zero references to MinIO in application code, Docker config, documentation, comments, or variable names

### Environment Documentation
- Add all S3/storage vars to `.env.example` with Supabase Storage S3 example values:
  - `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET_FILES`, `S3_BUCKET_IMAGES`
- Fixes the `.env.example` completeness gap noted in CLAUDE.md cleanup items

### Verification
- Unit tests with httptest-mocked storage for the S3 client and handler wiring
- Docker Compose stack-up with real Supabase Storage
- Curl-based E2E smoke tests against running stack:
  - `POST /v1/chat/completions` → 200 (not 502)
  - `POST /v1/images/generations` → 200 or provider error (not routing SQL error)
  - `POST /v1/files` → 200 (upload succeeds to Supabase Storage)
  - `GET /v1/batches` → 200 (list works)

### Claude's Discretion
- Specific standalone S3v4 signing library selection
- Internal S3Client implementation details (buffer sizes, retry logic, timeout values)
- Migration file naming/numbering convention
- httptest mock structure for unit tests

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Bug Documentation
- `CLAUDE.md` § "Known Issues Found During v1.0 Audit" — Documents all 3 bugs: ensureCapabilityColumns wrong table, file storage not wired, StorageUploader nil
- `CLAUDE.md` § "Mistakes to Avoid" — Lessons on runtime ALTER TABLE, silent error swallowing, fatal crashes on optional deps
- `.planning/UAT-REPORT.md` — Runtime UAT results showing 502 on inference, S3 incompatibility details
- `.planning/v1.0-MILESTONE-AUDIT.md` — Integration gaps #1, #2, #3 that this phase closes

### Existing Code to Modify
- `apps/control-plane/internal/routing/repository.go` — Contains ensureCapabilityColumns (wrong table), ListRouteCandidates (queries missing columns)
- `apps/edge-api/internal/files/storage.go` — Current minio-go StorageClient to be replaced
- `apps/control-plane/cmd/server/main.go:270` — Batch worker receives nil StorageUploader
- `apps/control-plane/internal/filestore/repository.go` — Contains ensureSchema to be converted to migration
- `apps/control-plane/internal/batchstore/worker.go` — StorageUploader interface definition

### Architecture References
- `.planning/phases/04-model-catalog-provider-routing/04-CONTEXT.md` — Routing policy, capability matrix, provider-blind posture
- `.planning/phases/07-media-file-and-async-api-surface/07-CONTEXT.md` — File storage decisions, batch processing model, handler patterns

### Requirements
- `.planning/REQUIREMENTS.md` § ROUT-02 — Routing must use capability matrix
- `.planning/REQUIREMENTS.md` § API-05, API-06, API-07 — Media/file/batch endpoint requirements

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `apps/edge-api/internal/files/storage.go` — Current StorageClient API surface (Upload, Download, Delete, PresignedURL, multipart ops) defines the method signatures to preserve
- `apps/edge-api/internal/batches/storage_adapter.go` — Existing adapter pattern bridges StorageClient to batches.StorageBackend interface
- `apps/control-plane/internal/batchstore/worker.go` — StorageUploader interface already defined, just needs a real implementation wired in
- `supabase/migrations/` — Existing migration directory for all schema changes

### Established Patterns
- Handler structs with `NewHandler(deps)` constructor and narrow interface dependencies
- Accept-interfaces pattern: services define narrow interfaces, concrete types satisfy them
- Supabase Postgres for all relational data via pgxpool
- go.work multi-module workspace: edge-api, control-plane, and packages/* all coexist

### Integration Points
- `apps/edge-api/cmd/server/main.go` — Wires StorageClient into file/image/batch handlers; must switch to packages/storage
- `apps/control-plane/cmd/server/main.go` — Wires nil StorageUploader into batch worker; must switch to packages/storage
- `go.work` — Must add `use ./packages/storage`
- `deploy/docker/docker-compose.yml` — May have MinIO service references to remove
- `.env.example` — Needs S3/storage credential documentation

</code_context>

<specifics>
## Specific Ideas

- "It is crucial that there is no mention or any code, any resource or anything related to minio in our codebase" — absolute zero-tolerance for MinIO references
- "Make it compatible so we can easily move to S3 if we want to. Maybe use an adapter or something" — the Storage interface + S3Client adapter pattern enables provider portability
- The Storage interface should be clean enough that adding AWS S3, Cloudflare R2, or GCS is just a new adapter file implementing the same interface

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 10-routing-storage-critical-fixes*
*Context gathered: 2026-04-12*
