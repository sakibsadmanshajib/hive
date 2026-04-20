# Phase 10: Routing & Storage Critical Fixes - Research

**Researched:** 2026-04-14
**Domain:** Go infrastructure repair: routing schema, Supabase Storage S3-compatible client, file/batch persistence
**Confidence:** HIGH for codebase findings and migration/storage direction; MEDIUM for live Supabase E2E behavior until credentials are tested

<user_constraints>
## User Constraints (from CONTEXT.md)

### Phase Boundary

Fix the three infrastructure bugs that break all inference and media endpoints, replace the legacy S3-compatible client storage client with a portable S3-compatible thin client backed by Supabase Storage, wire the batch worker's StorageUploader, convert filestore auto-schema to proper Supabase migrations, and fully purge every legacy local object-store emulator reference from the codebase. This phase does not add new endpoints, change routing logic, or expand the API surface — it makes existing code work correctly.

### Locked Decisions

#### Storage Client Approach
- Custom thin S3 client (~150 lines) wrapping net/http with S3v4 request signing
- Use a standalone S3v4 signing library — not from-scratch HMAC, not full AWS SDK
- Path-style URLs: `endpoint/bucket/key` format (Supabase S3 endpoint supports this)
- Zero legacy S3-compatible client dependency — full purge from go.mod, go.sum, all imports, all code, all comments, all docs

#### Storage Package Architecture
- New shared Go module at `packages/storage/` in the go.work workspace
- Package defines a `Storage` interface for portability across S3-compatible backends
- Ships with an `S3Client` struct that implements `Storage` — works with any S3-compatible endpoint (Supabase, AWS S3, Cloudflare R2, etc.)
- Switching providers = new config values, same adapter. Future non-S3 backends get their own adapter
- Both edge-api and control-plane import `packages/storage`
- Each consuming service defines its own narrow interface (StorageBackend, StorageUploader) that the concrete `*S3Client` satisfies — standard Go accept-interfaces pattern

#### Degradation Behavior
- Storage credentials are **required for startup** — no graceful skip, no fallback
- Every developer needs a Supabase project with storage enabled (already required for DB)
- Services fail fast if S3 credentials are missing or invalid
- No env-var gate — storage is always required in all environments

#### Bucket Provisioning
- Buckets (hive-files, hive-images) are pre-provisioned via Supabase dashboard or CLI
- App does not auto-create buckets — keeps S3 client pure and provider-portable
- Fails fast on first storage operation if bucket doesn't exist
- Document bucket creation in setup instructions

#### Migration Strategy
- **Remove `ensureCapabilityColumns` entirely** from `routing/repository.go` — no runtime ALTER TABLE
- Add proper Supabase migration for the 5 media capability columns on `provider_capabilities` table:
  - `supports_image_generation`, `supports_image_edit`, `supports_tts`, `supports_stt`, `supports_batch`
- **Convert filestore `ensureSchema` to proper Supabase migrations** — move all CREATE TABLE/INDEX statements for `files`, `uploads`, `upload_parts`, `batches` from Go code into `supabase/migrations/`
- Delete `ensureSchema()` and `ensureCapabilityColumns()` functions and their call sites
- All schema management through Supabase migrations only — no runtime DDL

#### Dependency Purge
- Full legacy S3-compatible client removal: go.mod, go.sum, all import paths, all code references
- Run `go mod tidy` after removal to clean transitive dependencies
- Zero references to legacy local object-store emulator in application code, Docker config, documentation, comments, or variable names

#### Environment Documentation
- Add all S3/storage vars to `.env.example` with Supabase Storage S3 example values:
  - `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET_FILES`, `S3_BUCKET_IMAGES`
- Fixes the `.env.example` completeness gap noted in CLAUDE.md cleanup items

#### Verification
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

### Deferred Ideas (OUT OF SCOPE)

None — discussion stayed within phase scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| ROUT-02 | Requests route only to approved providers/models satisfying capability matrix, fallback policy, and account/key allowlists. | Add the missing media capability columns to `public.provider_capabilities`, remove runtime DDL against the wrong table, and test `ListRouteCandidates` against the migrated schema so routing stops failing before policy evaluation. |
| API-05 | Image endpoints are OpenAI-compatible. | Keep existing image handler API surface; replace storage dependency with shared `packages/storage` implementation for URL-mode image upload and presigned URLs. Test routing SQL no longer breaks image generation. |
| API-06 | Speech, transcription, and translation endpoints are OpenAI-compatible. | Audio does not need object storage, but it depends on the same media routing columns. Ensure endpoint registration is not conditionally skipped due to object-storage init side effects. |
| API-07 | Files, uploads, and batches flows support official SDK integrations. | Replace storage client, preserve file/upload multipart method contracts, move filestore schema to migrations, wire control-plane batch `StorageUploader`, and fix missing internal response/update fields that currently block content and batch output flows. |
</phase_requirements>

## Summary

Phase 10 should be planned as an infrastructure repair, not a feature expansion. The routing bug is a schema ownership problem: `ListRouteCandidates` reads media capability columns from `public.provider_capabilities`, while `ensureCapabilityColumns` silently alters `route_capabilities`. The fix is a proper Supabase migration plus deletion of runtime DDL and its swallowed errors.

The storage bug should be solved by a new shared Go module at `packages/storage` using `net/http` and the official AWS SigV4 signer package. Supabase Storage supports the S3 protocol, PutObject, GetObject, DeleteObject, presigned query auth, and multipart operations needed by the current edge handlers. The planner should not introduce the AWS S3 service client or any provider-specific SDK.

There is one important hidden dependency chain for API-07: wiring storage is necessary but not sufficient for full batch/file flows. The control-plane filestore HTTP responses currently omit internal `storage_path`, `s3_upload_id`, `output_file_id`, and `error_file_id` fields that edge-api clients expect, and `UpdateBatchStatus` ignores the worker's update map. Plan these fixes with the three headline bugs or batch output verification will still fail.

**Primary recommendation:** Implement a shared `packages/storage` S3-over-HTTP client with AWS SigV4 signing, add two Supabase migrations, remove all runtime DDL and legacy object-store dependencies, then verify routing, files/uploads, image URL mode, audio routing, and batch output through Docker-only tests.

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go workspace | `go 1.24.0`, `toolchain go1.24.13` | Multi-module build for `apps/edge-api`, `apps/control-plane`, and new `packages/storage` | Existing project standard in `go.work`; CLAUDE.md requires Docker-only Go workflow. |
| Go `net/http` | stdlib | Thin storage client transport | Satisfies user decision for a custom HTTP client and avoids full S3 SDK/client lock-in. |
| `github.com/aws/aws-sdk-go-v2/aws/signer/v4` | module `github.com/aws/aws-sdk-go-v2 v1.41.5`, published 2026-03-26 | AWS Signature Version 4 request and presign support | Official AWS signing package; standalone signer API supports `SignHTTP` and `PresignHTTP` without using an S3 client. |
| `github.com/aws/aws-sdk-go-v2/aws` | same module | Static `aws.Credentials` type for signer calls | Minimal dependency paired with the signer; avoids hand-written credential/signature plumbing. |
| Supabase migrations | existing `supabase/migrations/*.sql` | Routing capability and filestore schema changes | Project already stores all Postgres schema here; context explicitly bans runtime DDL. |
| `github.com/jackc/pgx/v5` | `v5.7.2` in control-plane | Postgres access for routing and filestore repositories | Existing database driver and repository pattern. |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| Go `httptest` | stdlib | Mock Supabase S3-compatible endpoints | Unit tests for path-style URL construction, method/query shape, headers, presign query params, and multipart XML. |
| Go `encoding/xml` | stdlib | CompleteMultipartUpload request body and response parsing | Build S3 XML for multipart completion without adding SDK clients. |
| Go `crypto/sha256` + `encoding/hex` | stdlib | Payload hash when signing fixed small bodies | Use when request body is buffered or known; otherwise use `UNSIGNED-PAYLOAD` for S3 over TLS. |
| `github.com/hibiken/asynq` | `v0.26.0` in control-plane | Existing batch polling worker | Do not replace; only wire real `StorageUploader`. |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| AWS SigV4 signer only | AWS SDK S3 client | Full client handles many edge cases but violates the locked "not full AWS SDK" implementation decision and reintroduces provider client coupling. |
| AWS SigV4 signer only | Hand-written HMAC/canonical request code | Smaller dependency graph but high risk: canonical URI, query sorting, header selection, presign expiry, and path escaping are easy to get wrong. |
| S3 protocol via `net/http` | Supabase Storage REST object endpoints | The phase context locks path-style S3 protocol and multipart API compatibility; direct REST endpoints would require different upload semantics and new adapter behavior. |
| Supabase migrations | Runtime `ALTER TABLE` / `CREATE TABLE IF NOT EXISTS` | Runtime DDL already caused this outage and hides schema drift until production traffic hits it. |

**Installation / workspace commands:**

```bash
docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && mkdir -p packages/storage && go work use ./packages/storage'
docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace/packages/storage && go mod init github.com/hivegpt/hive/packages/storage && go get github.com/aws/aws-sdk-go-v2@v1.41.5'
docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace/apps/edge-api && go mod tidy'
docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace/apps/control-plane && go mod tidy'
```

**Version verification performed:**

```bash
docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'go list -m -json github.com/aws/aws-sdk-go-v2@latest'
```

Result: `github.com/aws/aws-sdk-go-v2 v1.41.5`, published `2026-03-26T18:11:26Z`, `GoVersion: 1.24`.

```bash
docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'go list -m -json github.com/aws/smithy-go@latest'
```

Result: `github.com/aws/smithy-go v1.24.3`, published `2026-04-02T18:46:42Z`, `GoVersion: 1.24`. Do not import `smithy-go` directly unless the signer module requires it transitively.

## Architecture Patterns

### Recommended Project Structure

```text
packages/storage/
├── go.mod
├── storage.go        # Storage interface, Config, CompletePart type
├── s3.go             # net/http S3Client implementation
├── signing.go        # SigV4 helper and payload-hash policy
└── s3_test.go        # httptest coverage for object and multipart operations

supabase/migrations/
├── 20260414_01_provider_capabilities_media_columns.sql
└── 20260414_02_filestore_tables.sql
```

### Pattern 1: Shared Concrete Client, Local Narrow Interfaces

**What:** `packages/storage` owns the concrete `S3Client` and broad storage contract. Edge and control-plane packages keep their local small interfaces, such as `files.StorageBackend`, `images.StorageInterface`, and `batchstore.StorageUploader`.

**When to use:** Always for this phase. It matches the existing handler/test patterns and keeps package dependencies pointing inward instead of importing edge-api types into shared code.

**Example:**

```go
// Source: codebase pattern plus pkg.go.dev signer API.
package storage

type CompletePart struct {
	PartNumber int
	ETag       string
}

type Storage interface {
	Upload(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error
	Download(ctx context.Context, bucket, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, bucket, key string) error
	PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
	InitMultipartUpload(ctx context.Context, bucket, key, contentType string) (string, error)
	UploadPart(ctx context.Context, bucket, key, uploadID string, partNum int, body io.Reader, size int64) (string, error)
	CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []CompletePart) error
	AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error
}
```

### Pattern 2: Path-Style URLs, No Path Normalization

**What:** Build URLs as `endpoint/bucket/key`, preserving the endpoint path `/storage/v1/s3` and preserving object-key slash semantics.

**When to use:** Every storage operation. Supabase S3 endpoints include a path component, which is why the old client failed.

**Planner guidance:**
- Parse `S3_ENDPOINT` as a full URL or host/path plus `S3_USE_SSL`.
- Do not use `path.Join`, because it normalizes `..`, duplicate slashes, and trailing slashes that may be part of an object key.
- Escape each bucket/key segment with `url.PathEscape`, then join with `/`.
- For standalone AWS signer usage, set `req.URL.Opaque = "//" + req.URL.Host + req.URL.EscapedPath()` before signing to avoid Go HTTP escaping mismatches.

### Pattern 3: Storage Config Must Be Validated at Startup

**What:** Both `edge-api` and `control-plane` should construct storage from the same config loader and fail startup if required values are absent or malformed.

**When to use:** During `main.go` service wiring, before route registration and worker startup.

**Minimum config:**
- `S3_ENDPOINT`
- `S3_ACCESS_KEY`
- `S3_SECRET_KEY`
- `S3_REGION` or an explicit documented default
- `S3_BUCKET_FILES`
- `S3_BUCKET_IMAGES` for edge image URL mode
- `S3_USE_SSL` only if endpoint may be provided without a scheme

**Important:** The phase context omits `S3_REGION`, but Supabase S3 setup and SigV4 both require a region in the credential scope. Add `S3_REGION` to `.env.example`, Docker Compose, and service config, or document an explicit default with a test proving it works against Supabase.

### Pattern 4: SQL Migrations Only

**What:** Move both routing capability columns and filestore tables into Supabase migrations. Constructors should not run DDL.

**When to use:** Routing repository and filestore repository construction.

**Migration sketch:**

```sql
-- Source: current repository.go schema and routing query shape.
alter table public.provider_capabilities
  add column if not exists supports_image_generation boolean not null default false,
  add column if not exists supports_image_edit boolean not null default false,
  add column if not exists supports_tts boolean not null default false,
  add column if not exists supports_stt boolean not null default false,
  add column if not exists supports_batch boolean not null default false;
```

### Anti-Patterns to Avoid

- **Runtime schema mutation in constructors:** This caused the routing outage and masks migration drift.
- **Silent best-effort infra initialization:** Current storage and routing failures were hidden behind warnings or ignored errors. Required infra should fail fast.
- **Cross-app type coupling:** Do not make `packages/storage` import `apps/edge-api/internal/files` just to reuse `CompletePart`; define shared types and adapt in edge code.
- **Full provider SDK clients:** Do not introduce a full AWS or Supabase client to solve signing and basic object operations.
- **Documentation-only purge:** The final purge must remove imports, module checksums, comments, plans, and setup docs according to the user's zero-reference constraint.

## Current Code Findings

| Area | File | Finding | Planning Impact | Confidence |
|------|------|---------|-----------------|------------|
| Routing schema | `apps/control-plane/internal/routing/repository.go` | `NewPgxRepository` calls `ensureCapabilityColumns`, and that function alters `route_capabilities`; `ListRouteCandidates` joins `public.provider_capabilities` and selects the five media columns. | Delete `ensureCapabilityColumns` and add a migration against `public.provider_capabilities`. | HIGH |
| Routing migration | `supabase/migrations/20260331_02_routing_policy.sql` | `provider_capabilities` exists with text/cache/reasoning flags only. | Add media capability columns with `boolean not null default false`; decide whether seed data needs updates for media aliases. | HIGH |
| Edge storage | `apps/edge-api/internal/files/storage.go` | `legacy S3-compatible client` imports and `old storage client core` implementation provide the exact method surface to preserve. | Replace the file or move implementation to `packages/storage`; then update edge handlers/adapters. | HIGH |
| Edge startup | `apps/edge-api/cmd/server/main.go` | Storage init failure only logs a warning and disables file/image/audio/batch routes. | Replace with fail-fast config/client initialization; ensure audio route registration is not accidentally tied to object storage unless storage is already required. | HIGH |
| Control-plane startup | `apps/control-plane/cmd/server/main.go` | Batch worker is created with `StorageUploader: nil`; control-plane lacks storage env wiring in Docker Compose. | Initialize shared storage client in control-plane and pass it to `batchstore.NewBatchWorker`; add Docker env vars. | HIGH |
| Filestore schema | `apps/control-plane/internal/filestore/repository.go` | `NewRepository` calls `ensureSchema`, creating `files`, `uploads`, `upload_parts`, and `batches` at runtime. | Move DDL to migration and simplify constructor. | HIGH |
| Batch status persistence | `apps/control-plane/internal/filestore/repository.go` | `UpdateBatchStatus` ignores the `updates` map and only updates `status`. | Batch worker can upload output but output file IDs and counts will not persist until this is fixed. | HIGH |
| Internal file response | `apps/control-plane/internal/filestore/http.go` | `fileToResponse` omits `storage_path`; edge `files.Client` expects it for content download and batch file processing. | Add internal-only response fields. Public edge API already hides `StoragePath` with `json:"-"`. | HIGH |
| Internal upload response | `apps/control-plane/internal/filestore/http.go` | `uploadToResponse` omits `s3_upload_id` and `storage_path`; edge upload client expects both. | Multipart add-part/complete will still fail after storage wiring unless these are returned and persisted. | HIGH |
| Internal batch response | `apps/control-plane/internal/filestore/http.go` | `batchToResponse` omits `output_file_id`, `error_file_id`, and status timestamps. | API-07 batch output response will not expose produced files unless added. | HIGH |
| Docker config | `deploy/docker/docker-compose.yml` | Edge has S3 env vars; control-plane does not. No service for the old local emulator remains. | Add storage env to control-plane; keep no emulator service. | HIGH |
| Workspace | `go.work` | Only edge-api and control-plane are in workspace. | Add `use ./packages/storage`. | HIGH |
| Dependency purge | `apps/edge-api/go.mod`, `apps/edge-api/go.sum` | `old object-storage dependency` and transitive entries remain. | Remove imports and run module tidy in edge-api. | HIGH |

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| SigV4 canonical signing | HMAC/canonical request implementation | `github.com/aws/aws-sdk-go-v2/aws/signer/v4` | Canonical URI/query/header rules, presign expiry, and S3 payload hash handling are easy to get wrong. |
| Storage abstraction | Provider-specific storage code in each app | `packages/storage.Storage` plus local narrow interfaces | Keeps one implementation for edge-api and control-plane while retaining testable handlers. |
| Schema management | Runtime DDL in repository constructors | Supabase SQL migrations | Runtime DDL already broke inference and hid the failure path. |
| Multipart XML format | String concatenation | `encoding/xml` structs | Avoids malformed XML and escaping bugs in `CompleteMultipartUpload`. |
| Storage tests against live service only | Manual Supabase-only validation | `httptest` unit tests plus live E2E smoke | Unit tests catch URL/signing/multipart regressions without credentials; live tests verify Supabase compatibility. |

**Key insight:** This phase is about removing hidden infrastructure assumptions. The risky pieces are not handler APIs; they are schema drift, storage request canonicalization, and internal control-plane data plumbing.

## Common Pitfalls

### Pitfall 1: Adding Columns to the Wrong Table Again

**What goes wrong:** Routing still fails because `provider_capabilities` lacks media columns while another table gets altered.

**Why it happens:** The old helper name sounds right but targets `route_capabilities`, while the active query joins `provider_capabilities`.

**How to avoid:** Delete the helper before adding the migration; write a repository test that exercises `ListRouteCandidates` against rows containing all five media fields.

**Warning signs:** `POST /v1/chat/completions` returns 502 with a SQL error mentioning missing `supports_image_generation`.

### Pitfall 2: Missing SigV4 Region

**What goes wrong:** The client builds and sends requests but Supabase rejects signatures.

**Why it happens:** `SignHTTP` requires a region in the credential scope, and Supabase S3 setup provides endpoint and region together. Current `.env.example` has no `S3_REGION`.

**How to avoid:** Add explicit `S3_REGION`; validate it at startup; include it in signer calls as the region argument.

**Warning signs:** 403 responses with signature mismatch despite correct access key and endpoint.

### Pitfall 3: Normalizing S3 Object Paths

**What goes wrong:** Objects upload under one key but downloads/presigns target another key.

**Why it happens:** `path.Join` and some URL helpers clean paths; S3 object keys are byte/string keys, not filesystem paths.

**How to avoid:** Escape each path segment and join manually. Preserve the endpoint path and object key slashes.

**Warning signs:** httptest server sees `/bucket/account/file` in one operation but `/storage/v1/s3/bucket/account/file` or a normalized key in another.

### Pitfall 4: Treating Storage as Optional

**What goes wrong:** Routes disappear from edge-api when storage config is invalid, and UAT sees 404s rather than a clear startup failure.

**Why it happens:** Current edge-api startup logs a warning and conditionally registers media/file/batch routes.

**How to avoid:** Fail fast in both services when storage config or client creation fails. Unit-test startup helpers around missing config.

**Warning signs:** `GET /v1/batches` or `POST /v1/files` returns 404 after service startup.

### Pitfall 5: Wiring Batch Storage Without Persisting Output Fields

**What goes wrong:** Worker uploads output JSONL but batch remains without `output_file_id` or request counts.

**Why it happens:** `UpdateBatchStatus` ignores `updates`, and internal batch responses omit output/error fields.

**How to avoid:** Implement a whitelist-based dynamic update for known batch fields, and add tests covering `output_file_id`, `error_file_id`, and request count updates.

**Warning signs:** Worker logs upload success but `GET /v1/batches/{id}` has no output file.

### Pitfall 6: Multipart Upload Metadata Not Returned

**What goes wrong:** Upload creation succeeds but add-part/complete fails with missing upload ID or storage path.

**Why it happens:** Control-plane stores `s3_upload_id` and `storage_path`, but response helpers do not return them to the edge internal client.

**How to avoid:** Add internal JSON fields to upload responses and tests around `uploads/{id}/parts`, `complete`, and `cancel`.

**Warning signs:** Edge handler returns "upload missing multipart state" after `POST /v1/uploads` succeeded.

### Pitfall 7: Zero-Reference Purge Conflicts With Historical Planning Docs

**What goes wrong:** Final `rg -i "legacy local object-store emulator"` still finds references in `.planning`, `CLAUDE.md`, or this research/context material.

**Why it happens:** User constraints require copying the original context, which itself contains the forbidden term. The phase success criterion says documentation too, with no historical-doc exemption.

**How to avoid:** Planner must either include a final doc-scrub task for historical planning docs or explicitly ask the user to exempt immutable GSD history. Given the stated constraint, plan the scrub.

**Warning signs:** Application code is clean, but root-level `rg -i "legacy local object-store emulator"` still returns planning or docs hits.

## Code Examples

Verified patterns from official sources and current codebase.

### Signed HTTP Request Helper

```go
// Source: https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/aws/signer/v4
package storage

const unsignedPayload = "UNSIGNED-PAYLOAD"

func (c *S3Client) sign(ctx context.Context, req *http.Request, payloadHash string) error {
	if payloadHash == "" {
		payloadHash = unsignedPayload
	}
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.URL.Opaque = "//" + req.URL.Host + req.URL.EscapedPath()

	creds := aws.Credentials{
		AccessKeyID:     c.accessKey,
		SecretAccessKey: c.secretKey,
	}
	return c.signer.SignHTTP(ctx, creds, req, payloadHash, "s3", c.region, c.now())
}
```

### Presigned GET URL

```go
// Source: https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/aws/signer/v4
func (c *S3Client) PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.objectURL(bucket, key), nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	q.Set("X-Amz-Expires", strconv.FormatInt(int64(ttl/time.Second), 10))
	req.URL.RawQuery = q.Encode()
	req.URL.Opaque = "//" + req.URL.Host + req.URL.EscapedPath()

	creds := aws.Credentials{AccessKeyID: c.accessKey, SecretAccessKey: c.secretKey}
	signed, _, err := c.signer.PresignHTTP(ctx, creds, req, unsignedPayload, "s3", c.region, c.now())
	if err != nil {
		return "", err
	}
	return signed, nil
}
```

### Complete Multipart Upload XML Shape

```go
// Source: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CompleteMultipartUpload.html
type completeMultipartUpload struct {
	XMLName xml.Name              `xml:"CompleteMultipartUpload"`
	Parts   []completeMultipartPart `xml:"Part"`
}

type completeMultipartPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}
```

### Storage Wiring Pattern

```go
// Source: existing main.go wiring, updated for shared storage.
storageClient, err := storage.NewS3Client(storage.Config{
	Endpoint:  mustEnv("S3_ENDPOINT"),
	AccessKey: mustEnv("S3_ACCESS_KEY"),
	SecretKey: mustEnv("S3_SECRET_KEY"),
	Region:    mustEnv("S3_REGION"),
	HTTPClient: http.DefaultClient,
})
if err != nil {
	log.Fatalf("storage unavailable: %v", err)
}

batchWorker := batchstore.NewBatchWorker(
	filestoreSvc,
	resolveLiteLLMBaseURL(),
	resolveLiteLLMMasterKey(),
	storageClient,
	resolveBucketFiles(),
)
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Runtime repository DDL | Supabase migrations only | Phase 10 decision after 2026-04-13 UAT failure | Avoids hidden schema drift and wrong-table mutations. |
| `legacy S3-compatible client` client | Thin S3-over-HTTP client with official SigV4 signer | Phase 10 decision after Supabase endpoint path incompatibility | Supports Supabase endpoint paths and preserves provider portability. |
| Optional storage degradation | Startup-required storage config | Phase 10 decision | Missing credentials produce a clear startup failure instead of disabled routes. |
| Edge-only storage implementation | Shared `packages/storage` module | Phase 10 decision | Allows batch worker and edge handlers to use the same storage implementation. |
| Batch worker nil uploader | Real `StorageUploader` wired in control-plane | Phase 10 decision | Enables output/error file upload for completed upstream batches. |

**Deprecated/outdated:**
- Runtime `ensureCapabilityColumns`: wrong table and no longer acceptable.
- Runtime `ensureSchema`: should be removed after filestore migration lands.
- `legacy S3-compatible client` dependency and all old local-emulator docs/config: explicitly out of phase after purge.
- Edge startup route gating on storage init: conflicts with fail-fast requirement.

## Open Questions

1. **What exact Supabase S3 region should local/dev use?**
   - What we know: Supabase S3 setup requires endpoint, access keys, and region; AWS SigV4 requires region.
   - What's unclear: The current env docs do not include `S3_REGION`.
   - Recommendation: Add `S3_REGION` explicitly and require developers to copy it from Supabase Storage S3 settings. Do not infer it from endpoint.

2. **Should media capability seed data be updated in this phase?**
   - What we know: The migration will add columns defaulting to false. Existing routing seed data may still not include image/audio/batch-capable routes or aliases.
   - What's unclear: Whether the UAT image/audio smoke will use an alias already present in seed data.
   - Recommendation: Keep routing logic unchanged, but add only the minimal seed updates required for existing media aliases if E2E would otherwise fail with "no route" rather than provider error.

3. **Does "zero references" include historical GSD docs?**
   - What we know: The user said documentation and no resource references, and root `rg -i "legacy local object-store emulator"` currently returns many `.planning` hits.
   - What's unclear: Whether immutable historical phase summaries may be exempt.
   - Recommendation: Plan a final repository-wide doc scrub or ask the user for a narrow exemption before final verification. This research file temporarily contains the term because user constraints must be copied verbatim.

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` with stdlib `httptest`; existing JS SDK tests use Vitest |
| Config file | Go: none; JS SDK: `packages/sdk-tests/js/vitest.config.ts` |
| Quick run command | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/internal/routing ./apps/control-plane/internal/filestore ./apps/control-plane/internal/batchstore ./apps/edge-api/internal/files ./apps/edge-api/internal/images ./apps/edge-api/internal/audio ./apps/edge-api/internal/batches ./apps/edge-api/cmd/server ./packages/storage -count=1'` |
| Full suite command | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/... ./apps/edge-api/... ./packages/storage/... -count=1'` |
| E2E smoke command | `docker compose --env-file .env -f deploy/docker/docker-compose.yml up --build` plus curl checks listed below |

### Phase Requirements to Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|--------------|
| ROUT-02 | `ListRouteCandidates` works with five media capability columns on `provider_capabilities` and no constructor DDL. | unit/integration | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/internal/routing -run TestListRouteCandidates -count=1'` | Existing routing tests yes; repository DB test likely Wave 0 |
| ROUT-02 | Chat completion no longer fails with missing media columns. | E2E smoke | `curl -sS -X POST http://localhost:8080/v1/chat/completions ...` after stack-up | Manual/Wave 0 script |
| API-05 | Image URL mode uploads provider image bytes and returns Supabase signed URL; SQL routing errors are gone. | unit + E2E smoke | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/edge-api/internal/images -count=1'` | Existing handler tests yes |
| API-06 | Audio speech/transcription/translation routes remain registered and route through fixed capability schema. | unit + E2E smoke | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/edge-api/internal/audio ./apps/control-plane/internal/routing -count=1'` | Existing handler/routing tests yes |
| API-07 | File upload/download/delete use shared storage client against path-style Supabase endpoint. | unit + E2E smoke | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/edge-api/internal/files ./packages/storage -count=1'` | Existing file handler tests yes; storage tests Wave 0 |
| API-07 | Uploads multipart lifecycle preserves `s3_upload_id`, `storage_path`, and completes object assembly. | unit | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/edge-api/internal/files ./apps/control-plane/internal/filestore ./packages/storage -run Upload -count=1'` | Edge tests yes; control-plane tests Wave 0 |
| API-07 | Batch worker uploads completed output/error files and persists output IDs/counts. | unit/integration | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/control-plane/internal/batchstore ./apps/control-plane/internal/filestore -run Batch -count=1'` | Wave 0 |
| API-07 | `GET /v1/batches` returns 200 with storage required at startup. | E2E smoke | `curl -sS http://localhost:8080/v1/batches -H 'Authorization: Bearer ...'` after stack-up | Manual/Wave 0 script |

### Sampling Rate

- **Per task commit:** Run the narrow package command for touched areas, plus `packages/storage` tests if storage code changed.
- **Per wave merge:** Run the full Go suite command above.
- **Phase gate:** Full Go suite green, Docker Compose stack starts with real Supabase storage credentials, and curl E2E smoke covers chat, image, file upload, and batches list.

### Curl E2E Smoke Targets

```bash
curl -sS -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $HIVE_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"ping"}]}'

curl -sS -X POST http://localhost:8080/v1/images/generations \
  -H "Authorization: Bearer $HIVE_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","prompt":"test image","n":1,"size":"1024x1024"}'

curl -sS -X POST http://localhost:8080/v1/files \
  -H "Authorization: Bearer $HIVE_API_KEY" \
  -F "purpose=batch" \
  -F "file=@/tmp/hive-phase10.jsonl;type=application/jsonl"

curl -sS http://localhost:8080/v1/batches \
  -H "Authorization: Bearer $HIVE_API_KEY"
```

### Wave 0 Gaps

- [ ] `packages/storage/s3_test.go` covers path-style endpoint with existing path, `PutObject`, `GetObject`, `DeleteObject`, presign query params, multipart create/upload/complete/abort, and no path normalization.
- [ ] `apps/control-plane/internal/routing/repository_test.go` or equivalent DB-backed test covers `provider_capabilities` media columns and no runtime DDL.
- [ ] `apps/control-plane/internal/filestore/repository_test.go` covers migrated schema assumptions and `UpdateBatchStatus` update-field persistence.
- [ ] `apps/control-plane/internal/filestore/http_test.go` covers internal `storage_path`, `s3_upload_id`, `output_file_id`, and `error_file_id` fields.
- [ ] `apps/control-plane/internal/batchstore/worker_test.go` covers completed upstream batch downloading, storage upload, file metadata creation, and batch update fields.
- [ ] `apps/edge-api/cmd/server/main_test.go` or config helper tests cover fail-fast storage config and route registration.
- [ ] E2E smoke script or documented manual command set for chat/image/file/batches against live Docker Compose.

## Planning Checklist

1. Add migrations first: provider media capability columns and filestore tables/indexes.
2. Delete runtime DDL and constructor call sites before touching storage.
3. Create `packages/storage`, add it to `go.work`, and implement/test the S3 HTTP client.
4. Adapt edge file/image/batch handlers to shared storage types while preserving local narrow interfaces.
5. Wire control-plane storage config and batch worker uploader.
6. Fix filestore internal response fields and `UpdateBatchStatus` update persistence.
7. Remove legacy storage dependency from edge module and run module tidy in Docker.
8. Scrub docs/comments/config and verify root `rg` according to user zero-reference constraint.
9. Run package tests, full Go suite, then live Docker Compose smoke.

## Sources

### Primary (HIGH confidence)

- Local project: `CLAUDE.md` for Docker-only workflow, Go 1.24, Supabase Storage direction, and known Phase 10 bugs.
- Local project: `.planning/phases/10-routing-storage-critical-fixes/10-CONTEXT.md` for locked implementation decisions.
- Local project: `.planning/REQUIREMENTS.md` for ROUT-02, API-05, API-06, API-07.
- Local project: `.planning/UAT-REPORT.md` and `.planning/v1.0-MILESTONE-AUDIT.md` for confirmed integration gaps.
- Local code: `apps/control-plane/internal/routing/repository.go`, `apps/control-plane/internal/filestore/repository.go`, `apps/control-plane/internal/filestore/http.go`, `apps/control-plane/internal/batchstore/worker.go`, `apps/edge-api/internal/files/storage.go`, `apps/edge-api/internal/files/client.go`, `apps/edge-api/cmd/server/main.go`, `apps/control-plane/cmd/server/main.go`, `deploy/docker/docker-compose.yml`, `go.work`.
- Supabase S3 Authentication: https://supabase.com/docs/guides/storage/s3/authentication
- Supabase S3 Compatibility: https://supabase.com/docs/guides/storage/s3/compatibility
- Supabase S3 Uploads: https://supabase.com/docs/guides/storage/uploads/s3-uploads
- Supabase Bucket Creation: https://supabase.com/docs/guides/storage/buckets/creating-buckets
- AWS SigV4 signer package docs: https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/aws/signer/v4
- AWS S3 SigV4 header auth: https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-header-based-auth.html
- AWS S3 CompleteMultipartUpload API: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CompleteMultipartUpload.html

### Secondary (MEDIUM confidence)

- Local skill: `/home/sakib/.agents/skills/supabase-postgres-best-practices/references/schema-foreign-key-indexes.md`
- Local skill: `/home/sakib/.agents/skills/supabase-postgres-best-practices/references/schema-primary-keys.md`
- Local skill: `/home/sakib/.agents/skills/supabase-postgres-best-practices/references/schema-lowercase-identifiers.md`

### Tertiary (LOW confidence)

- None used for recommendations. Web search was used only to locate official Supabase/AWS documentation.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH. The signer package version was verified via Docker `go list`, and the rest is current project-standard Go/stdlib/Supabase migration tooling.
- Architecture: HIGH. The shared module and narrow-interface pattern are explicit user decisions and match existing edge/control-plane code.
- Pitfalls: HIGH for local code findings; MEDIUM for live Supabase signature behavior until `S3_REGION` and credentials are validated in E2E.
- Validation: MEDIUM. Existing Go tests are broad, but storage package, control-plane filestore repository, and batch worker tests need Wave 0 coverage.

**Research date:** 2026-04-14
**Valid until:** 2026-05-14 for codebase findings; re-check Supabase/AWS package docs within 30 days if implementation is delayed.
