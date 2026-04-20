# Phase 7: Media, File, and Async API Surface - Research

**Researched:** 2026-04-09
**Domain:** File storage (Supabase/legacy local object-store emulator/S3), Redis-backed batch processing, LiteLLM image/audio proxy, OpenAI-compatible multipart flows
**Confidence:** HIGH (architecture) / MEDIUM (LiteLLM batch internals)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Use Supabase Storage for blob storage (S3-compatible, already in hosted stack)
- File metadata (id, purpose, filename, bytes, status, account ownership, created_at) tracked in a Postgres table — decoupled from blob access
- Match OpenAI file size limits: 512MB maximum
- Support both the simple Files API (single-request upload) and the Uploads API (multipart chunked uploads)
- Redis-backed worker queue for batch job processing (Redis already in stack)
- Forward batch requests to upstream provider batch APIs (e.g., OpenAI Batch API for 50% cost savings)
- Only support batching for providers that have batch API support; return unsupported error for providers without batch APIs
- Batch metadata and status tracking in Postgres (validating -> in_progress -> completed/failed)
- Redis worker polls upstream provider batch status, updates Postgres state, assembles result file on completion
- Support all inference endpoint types in batches (chat/completions, completions, embeddings)
- Reserve credits upfront on batch submission based on estimated cost from JSONL, finalize actual usage on completion, refund surplus
- Standard OpenAI-compatible batch responses only — no Hive-specific savings metadata in API responses
- Support image generations and edits at launch; variations endpoint deferred
- Image response supports both URL and base64 delivery via response_format parameter
- URLs use Supabase Storage signed URLs with 1-hour TTL
- Image model aliases follow the Hive-alias pattern (hive-image-1) — same provider-blind catalog pattern as text models
- Support all three audio operations at launch: speech (TTS), transcriptions (STT), and translations
- TTS responses stream binary audio directly from upstream to client (no intermediate storage)
- Transcription/translation: stream-forward multipart form upload audio to upstream provider — no intermediate storage
- Audio model aliases follow Hive-alias pattern (hive-tts-1, hive-whisper-1)
- Duration-based metering: TTS metered per character, transcription/translation metered per second of audio

### Claude's Discretion
- legacy local object-store emulator configuration for local Docker dev parity with Supabase Storage
- Exact Redis queue data structures and worker concurrency settings
- Batch JSONL validation rules and error reporting granularity
- Image edit mask handling and format normalization
- Audio format detection and codec negotiation details
- Exact Supabase Storage bucket naming and path conventions

### Deferred Ideas (OUT OF SCOPE)
- Image variations endpoint (DALL-E 2 only, niche usage)
- Batch savings metadata in API responses
- Supabase Queues (pgmq) as alternative to Redis for batch processing
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| API-05 | Developer can call image-generation and image-processing endpoints with OpenAI-compatible behavior for supported operations. | LiteLLM supports `/images/generations` and `/images/edits` with multipart form forwarding; model alias catalog extension pattern established; signed URL generation for Supabase Storage documented. |
| API-06 | Developer can call speech, transcription, and translation endpoints with OpenAI-compatible behavior for supported operations. | LiteLLM supports `/audio/speech`, `/audio/transcriptions`, `/audio/translations`; binary streaming relay pattern maps directly to `stream.go` adaptation; multipart form forward pattern for STT/translation. |
| API-07 | Developer can use `files`, `uploads`, and `batches` flows required by official SDK integrations. | OpenAI Files API (single upload), Uploads API (multipart chunked), and Batches API fully specced; legacy S3-compatible client v7 for S3-compatible storage client; Asynq for Redis-backed batch worker; Postgres tables needed for file metadata and batch state. |
</phase_requirements>

---

## Summary

Phase 7 extends the Hive API surface with three groups of endpoints that real OpenAI SDK integrations rely on: (1) Files and Uploads API for blob storage with Postgres metadata, (2) Batches API for async bulk processing through upstream provider batch endpoints, and (3) Image and Audio API routes that proxy through LiteLLM. The existing orchestrator, accounting, error handling, and routing patterns from Phases 4–6 are all directly reusable; the primary new infrastructure is a file storage client (legacy S3-compatible client v7 works against both local legacy local object-store emulator and Supabase Storage), a Postgres schema for file/batch metadata, and a Redis-backed background worker for batch polling.

LiteLLM already exposes `/images/generations`, `/images/edits`, `/audio/speech`, `/audio/transcriptions`, and `/audio/translations` on the same base URL the edge-api already targets. Image and audio endpoints differ from text inference in that images may involve multipart form data (edits) rather than JSON, and audio TTS returns binary bytes rather than JSON — both require handler-level adaptations but no new external dependencies. For batch processing, LiteLLM supports forwarding to OpenAI, Azure, Vertex, and Bedrock batch APIs; Hive's batch worker will be a separate Go goroutine pool (or lightweight Asynq worker) that polls upstream status and writes back to Postgres.

**Primary recommendation:** Use legacy S3-compatible client v7 as the unified S3 client (works against both legacy local object-store emulator in Docker and Supabase Storage in production). Use Asynq over raw Redis list polling for the batch worker — it gives retry, observability, and concurrency controls with minimal added complexity. Forward image and audio requests to LiteLLM using the same HTTP dispatch pattern as inference, adapted for multipart and binary bodies.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| legacy S3-compatible client/v7 | v7.0.x (latest ~7.0.91) | S3-compatible storage client for file upload/download/presigned URLs | Works against both legacy local object-store emulator (local dev) and Supabase Storage (production); one client, two environments |
| github.com/hibiken/asynq | v0.24.x | Redis-backed distributed task queue for batch worker | Production-ready Go library; built on go-redis already in stack; retry, priorities, scheduling, web UI; well-maintained 2025 |
| jackc/pgx/v5 | v5.7.2 (already in control-plane) | Postgres driver for file metadata and batch state tables | Already in control-plane go.mod |
| google/uuid | v1.6.0 (already in control-plane) | File ID, upload ID, batch ID generation | Already in stack |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| mime/multipart (stdlib) | Go stdlib | Parse incoming multipart file uploads for Files API | No external dep needed |
| net/http (stdlib) | Go stdlib | Binary streaming relay for audio TTS, multipart proxy for image edits | No external dep needed |
| encoding/json (stdlib) | Go stdlib | JSONL parsing for batch input validation | No external dep needed |
| bufio (stdlib) | Go stdlib | Line-by-line JSONL scanning | No external dep needed |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| legacy S3-compatible client/v7 | aws/aws-sdk-go-v2/s3 | AWS SDK works but heavier; legacy S3-compatible client is simpler and designed for S3-compatible services |
| Asynq | Raw Redis LPUSH/BRPOP polling | Raw Redis is sufficient but requires building retry, dead-letter, and observability from scratch |
| Asynq | Machinery | Machinery is broker-agnostic but heavier; Asynq is simpler for Redis-only use case |

**Installation (control-plane / new batch-worker service):**
```bash
go get old object-storage dependency@latest
go get github.com/hibiken/asynq@latest
```

---

## Architecture Patterns

### Recommended Package Structure

New packages in `apps/edge-api/internal/`:
```
internal/
├── files/           # Files API and Uploads API handlers + storage client
│   ├── handler.go   # HTTP handlers: POST/GET /v1/files, /v1/uploads/*
│   ├── storage.go   # S3/Supabase Storage client wrapper (legacy S3-compatible client)
│   ├── types.go     # FileObject, UploadObject, FilePart OpenAI types
│   └── handler_test.go
├── batches/         # Batches API handlers (HTTP surface only)
│   ├── handler.go   # POST /v1/batches, GET /v1/batches/{id}, cancel
│   ├── client.go    # Internal client to control-plane batch service
│   ├── types.go     # BatchObject, BatchRequest, BatchError OpenAI types
│   └── handler_test.go
├── images/          # Image generation and edits handlers
│   ├── handler.go   # POST /v1/images/generations, /v1/images/edits
│   ├── types.go     # ImageGenerationRequest/Response, ImageEditRequest
│   └── handler_test.go
└── audio/           # Audio speech, transcription, translation handlers
    ├── handler.go   # POST /v1/audio/speech, /v1/audio/transcriptions, /v1/audio/translations
    ├── types.go     # SpeechRequest, TranscriptionResponse
    └── handler_test.go
```

New packages in `apps/control-plane/internal/`:
```
internal/
├── filestore/       # File metadata CRUD (Postgres), signed URL generation
│   ├── repository.go
│   ├── service.go
│   ├── http.go      # Internal HTTP endpoints for edge-api to call
│   └── types.go
└── batchstore/      # Batch state machine (validating->in_progress->completed)
    ├── repository.go
    ├── service.go
    ├── http.go
    └── types.go
```

New binary (or goroutine in control-plane):
```
apps/control-plane/cmd/batch-worker/
└── main.go          # Asynq worker: poll upstream, update Postgres, assemble result file
```

### Pattern 1: File Storage Handler — Authorize then Stream to S3

**What:** The Files API single-upload (POST /v1/files) reads multipart form data from the client, streams it directly to Supabase Storage (via legacy S3-compatible client), and writes metadata to Postgres via the control-plane internal API.

**When to use:** All single-request file uploads (512 MB max).

```go
// Source: established inference handler pattern + legacy S3-compatible client docs
func handleFileUpload(w http.ResponseWriter, r *http.Request, storage *files.StorageClient, accounting *files.AccountingClient) {
    // 1. Authorize (reuse authorizer pattern from inference)
    // 2. Parse multipart: r.ParseMultipartForm(512 << 20)
    file, header, err := r.FormFile("file")
    purpose := r.FormValue("purpose")
    // 3. Validate purpose ("batch", "assistants", "vision", "fine-tune")
    // 4. Upload to Supabase Storage via legacy S3-compatible client
    _, err = storage.PutObject(ctx, bucketName, objectKey, file, header.Size, old storage client PutObjectOptions{
        ContentType: header.Header.Get("Content-Type"),
    })
    // 5. Write metadata to Postgres via control-plane internal API
    // 6. Return OpenAI FileObject JSON
}
```

### Pattern 2: Uploads API — Create/AddPart/Complete

**What:** The chunked Uploads API follows a three-step lifecycle: Create upload (POST /v1/uploads) → Add parts (POST /v1/uploads/{id}/parts) → Complete (POST /v1/uploads/{id}/complete). Parts are assembled via S3 multipart upload. The upload ID maps to an S3 multipart upload ID.

**When to use:** Files larger than practical for single-request upload; required for SDK compatibility.

```go
// S3 multipart upload lifecycle via legacy S3-compatible client
// Create: storage.NewMultipartUpload(ctx, bucket, key, opts)  → uploadID
// AddPart: storage.PutObjectPart(ctx, bucket, key, uploadID, partNum, reader, size, opts) → PartInfo
// Complete: storage.CompleteMultipartUpload(ctx, bucket, key, uploadID, parts, opts) → UploadInfo
// Cancel: storage.AbortMultipartUpload(ctx, bucket, key, uploadID)
```

### Pattern 3: Binary Audio Streaming (TTS)

**What:** Speech endpoint receives JSON body (model, input, voice, response_format), dispatches to LiteLLM `/audio/speech` (JSON POST), and pipes the binary response body directly to the client with the correct Content-Type. No SSE, no JSON wrapping.

**When to use:** POST /v1/audio/speech only.

```go
// Adapted from stream.go binary relay pattern (no SSE scanner needed)
func handleSpeech(orchestrator *Orchestrator, w http.ResponseWriter, r *http.Request) {
    // 1. Authorize + route (reuse orchestrator auth/route path)
    // 2. POST JSON body to LiteLLM /audio/speech
    resp, _ := litellm.Speech(ctx, litellmModel, body)
    defer resp.Body.Close()
    // 3. Copy Content-Type from upstream (audio/mp3, audio/opus, etc.)
    w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
    w.WriteHeader(http.StatusOK)
    io.Copy(w, resp.Body)  // pipe binary directly, no buffering
    // 4. Finalize accounting with character-count credits
}
```

### Pattern 4: Multipart Audio Forward (Transcription/Translation)

**What:** Transcription and translation endpoints receive multipart form data (audio file + parameters), rebuild the multipart request, and forward it to LiteLLM. The audio file bytes are streamed directly — never written to disk or Postgres.

**When to use:** POST /v1/audio/transcriptions and /v1/audio/translations.

```go
// Forward multipart to LiteLLM using a pipe
pr, pw := io.Pipe()
mw := multipart.NewWriter(pw)
go func() {
    // copy original form fields and file into new multipart body
    // substitute model field with litellmModel
    mw.Close(); pw.Close()
}()
req, _ := http.NewRequestWithContext(ctx, http.MethodPost, litellmURL+"/audio/transcriptions", pr)
req.Header.Set("Content-Type", mw.FormDataContentType())
// Forward response JSON directly to client
```

### Pattern 5: Image Generation/Edits Dispatch

**What:** Generations are JSON-body POSTs forwarded to LiteLLM `/images/generations`. Edits are multipart form POSTs forwarded to LiteLLM `/images/edits`. Both responses are normalized: provider model name replaced with Hive alias, any storage-backed URL (from the provider) replaced with a Supabase Storage signed URL if `response_format=url`.

**When to use:** POST /v1/images/generations and /v1/images/edits.

```go
// Image generation response type (OpenAI-compatible)
type ImageGenerationResponse struct {
    Created int64       `json:"created"`
    Data    []ImageData `json:"data"`
}
type ImageData struct {
    URL           *string `json:"url,omitempty"`
    B64JSON       *string `json:"b64_json,omitempty"`
    RevisedPrompt *string `json:"revised_prompt,omitempty"`
}
// For URL mode: store generated image in Supabase Storage and return
// signed URL with 1-hour TTL instead of provider URL
```

### Pattern 6: Batch Worker State Machine

**What:** Asynq worker processes batch jobs through a state machine. On submission, validate JSONL, estimate credits, reserve, and create upstream batch. Worker polls upstream every N seconds, updates Postgres, and on completion assembles the output JSONL file into Supabase Storage.

```
[Submitted] → validating → in_progress → [completed | failed | cancelled]
```

```go
// Asynq task definition
const TypeBatchPoll = "batch:poll"

type BatchPollPayload struct {
    BatchID          string
    AccountID        string
    ReservationID    string
    UpstreamBatchID  string
    Provider         string
}

// Worker handler
func HandleBatchPoll(ctx context.Context, t *asynq.Task) error {
    var p BatchPollPayload
    json.Unmarshal(t.Payload(), &p)
    // Check upstream batch status
    // If completed: assemble output file, finalize reservation, update Postgres
    // If in_progress: re-enqueue with delay (e.g., 30s)
    // If failed: release reservation, update Postgres
    return nil
}
```

### Pattern 7: Capability Flags for Image/Audio in Routing

**What:** `SelectRouteInput` and `RouteCandidate` in the control-plane routing types need new boolean capability fields for image generation, image editing, TTS, STT, and translation — following the exact same pattern as `NeedEmbeddings`, `SupportsEmbeddings`, etc.

```go
// In apps/control-plane/internal/routing/types.go — ADD:
type SelectionInput struct {
    // ... existing fields ...
    NeedImageGeneration bool `json:"need_image_generation"`
    NeedImageEdit       bool `json:"need_image_edit"`
    NeedTTS             bool `json:"need_tts"`
    NeedSTT             bool `json:"need_stt"`
    NeedBatch           bool `json:"need_batch"`
}

type RouteCandidate struct {
    // ... existing fields ...
    SupportsImageGeneration bool
    SupportsImageEdit       bool
    SupportsTTS             bool
    SupportsSTT             bool
    SupportsBatch           bool
}
```

### Anti-Patterns to Avoid

- **Storing audio in transit:** Audio for transcription/translation must never land in Supabase Storage or Postgres — pipe it through in-flight only (PRIV-01 alignment).
- **Blocking on upstream polling:** Batch status polling must be async — never block the HTTP response waiting for upstream completion.
- **Rewriting multipart in memory:** Build the forwarded multipart body with `io.Pipe()` — avoid reading the entire audio/image file into a `[]byte` before forwarding.
- **Using provider URLs in image responses:** Provider image URLs expose provider identity — always store-and-re-sign via Supabase Storage for `response_format=url`.
- **Trusting client-supplied file IDs for access:** Always validate file ownership against `account_id` from the auth snapshot before serving file content.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| S3 multipart upload lifecycle | Custom HTTP S3 client | legacy S3-compatible client v7 | Handles part tracking, retry, ETag assembly, presigned URLs — hundreds of edge cases |
| Redis task queue with retry | LPUSH/BRPOP polling loop | Asynq | Dead-letter queue, exponential backoff, scheduling, priorities — already solved |
| Presigned URL generation | HMAC-SHA256 signing code | legacy S3-compatible client `PresignedGetObject` | Signature V4 is complex; legacy S3-compatible client handles it correctly for both legacy local object-store emulator and Supabase |
| JSONL line parsing | Custom byte-scanner | `bufio.Scanner` line-by-line | Standard library; no dep needed |
| Multipart form data assembly | Raw boundary string building | `mime/multipart.NewWriter` | Boundary encoding, content-disposition headers, part interleaving — stdlib handles it |

**Key insight:** The file/upload/batch surface has significant incidental complexity in protocol-level details (S3 multipart ETag ordering, presigned URL signing, Redis task lifecycle) that standard libraries have already solved correctly.

---

## Common Pitfalls

### Pitfall 1: Supabase Storage S3 Endpoint vs. REST API
**What goes wrong:** Supabase Storage has two access modes — its native REST API (used by Supabase JS SDK) and an S3-compatible endpoint. legacy S3-compatible client targets the S3 endpoint. The S3 endpoint URL is `https://<project>.supabase.co/storage/v1/s3`, NOT `/storage/v1/object`.
**Why it happens:** The Supabase docs prominently show the REST/JS API; the S3 endpoint is documented separately.
**How to avoid:** Configure legacy S3-compatible client with `endpoint = <project>.supabase.co/storage/v1/s3` and use the project's S3 access key + secret (generated in Supabase dashboard). For local legacy local object-store emulator, use `legacy-object-store:9000`.
**Warning signs:** 403s or 404s on PutObject that don't occur with the Supabase REST API.

### Pitfall 2: legacy local object-store emulator in Docker Requires Bucket Creation on First Start
**What goes wrong:** legacy local object-store emulator starts empty — buckets don't exist. File upload calls fail with NoSuchBucket.
**Why it happens:** Unlike Supabase Storage where buckets are pre-created in the dashboard, local legacy local object-store emulator requires explicit bucket creation.
**How to avoid:** Add a startup script or use legacy local object-store emulator's `OLD_STORAGE_VOLUMES` + a bucket init container in docker-compose. Alternatively create the bucket on first use with `storage.MakeBucket(ctx, name, opts)` if it doesn't exist.

### Pitfall 3: LiteLLM Image Edits Require Multipart (Not JSON)
**What goes wrong:** Image edits (`/images/edits`) must be forwarded as `multipart/form-data`, not JSON. Sending JSON fails with 422.
**Why it happens:** The OpenAI `/images/edits` endpoint is multipart-only (image and mask are binary files). LiteLLM mirrors this.
**How to avoid:** Parse incoming multipart form data from the client, rebuild a new multipart body for LiteLLM using `mime/multipart.NewWriter` + `io.Pipe()`, set correct `Content-Type: multipart/form-data; boundary=...` header on the upstream request.

### Pitfall 4: Batch JSONL Validation Must Catch Malformed Requests Before Credit Reservation
**What goes wrong:** If JSONL is malformed or references unsupported endpoints, the batch fails after credits are reserved, requiring a manual release.
**Why it happens:** Processing the JSONL line-by-line to count and validate requests is an upfront cost that's easy to skip.
**How to avoid:** In the `validating` state, scan all JSONL lines before creating the reservation. Reject with a 400 if any line is malformed or uses an unsupported endpoint. Only reserve after validation passes.

### Pitfall 5: Audio Content-Type Must Be Passed Through Exactly
**What goes wrong:** Setting `Content-Type: audio/mp3` when LiteLLM returns `audio/mpeg` (or vice versa) causes SDK-side decoding failures.
**Why it happens:** OpenAI accepts `mp3`, `opus`, `aac`, `flac`, `wav`, `pcm` as `response_format` values, but the actual Content-Type in the HTTP response may differ (e.g., `audio/mpeg` for mp3).
**How to avoid:** Copy the `Content-Type` header from the upstream LiteLLM response directly — do not hardcode it.

### Pitfall 6: Batch Result File Must Be Stored Before Finalization
**What goes wrong:** Finalizing the reservation before writing the output file to Supabase Storage creates a window where the batch shows "completed" but the output file is not yet accessible.
**Why it happens:** Natural sequencing error when writing the completion flow.
**How to avoid:** Sequence strictly: (1) assemble output JSONL, (2) upload to Supabase Storage, (3) update Postgres with output_file_id, (4) finalize reservation.

### Pitfall 7: File Downloads Must Validate Account Ownership
**What goes wrong:** `GET /v1/files/{file_id}/content` leaks file content across accounts if ownership isn't checked.
**Why it happens:** Storage paths encode file IDs, not account IDs — any valid API key could request any file ID.
**How to avoid:** The file metadata row in Postgres includes `account_id`. Always validate that the requesting account matches before generating a presigned URL or streaming content.

---

## Code Examples

### File Metadata Table Schema

```sql
-- Source: OpenAI Files API object shape + PRIV-01 constraints
CREATE TABLE files (
    id           TEXT PRIMARY KEY,       -- "file-{uuid}"
    account_id   TEXT NOT NULL,
    purpose      TEXT NOT NULL,          -- 'batch', 'assistants', 'fine-tune', 'vision'
    filename     TEXT NOT NULL,
    bytes        BIGINT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'uploaded',  -- 'uploaded' | 'processed' | 'error'
    storage_path TEXT NOT NULL,          -- bucket/key in Supabase Storage
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ             -- 30 days for batch purpose
);

CREATE TABLE uploads (
    id              TEXT PRIMARY KEY,    -- "upload-{uuid}"
    account_id      TEXT NOT NULL,
    filename        TEXT NOT NULL,
    bytes           BIGINT NOT NULL,
    mime_type       TEXT NOT NULL,
    purpose         TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',  -- 'pending' | 'completed' | 'cancelled'
    s3_upload_id    TEXT,               -- legacy local object-store emulator/S3 multipart upload ID
    storage_path    TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL -- short TTL, e.g. 1 hour for pending uploads
);
```

### Batch State Table Schema

```sql
-- Source: OpenAI Batch API object shape
CREATE TABLE batches (
    id                    TEXT PRIMARY KEY,   -- "batch-{uuid}"
    account_id            TEXT NOT NULL,
    input_file_id         TEXT NOT NULL REFERENCES files(id),
    output_file_id        TEXT REFERENCES files(id),
    error_file_id         TEXT REFERENCES files(id),
    endpoint              TEXT NOT NULL,      -- '/v1/chat/completions', etc.
    completion_window     TEXT NOT NULL DEFAULT '24h',
    status                TEXT NOT NULL DEFAULT 'validating',
    -- validating | in_progress | finalizing | completed | failed | cancelled | expired
    provider              TEXT NOT NULL,
    upstream_batch_id     TEXT,               -- provider's batch ID
    reservation_id        TEXT,               -- Hive credit reservation ID
    request_counts_total  INT NOT NULL DEFAULT 0,
    request_counts_completed INT NOT NULL DEFAULT 0,
    request_counts_failed INT NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    in_progress_at        TIMESTAMPTZ,
    completed_at          TIMESTAMPTZ,
    failed_at             TIMESTAMPTZ,
    cancelled_at          TIMESTAMPTZ,
    expires_at            TIMESTAMPTZ NOT NULL
);
```

### legacy S3-compatible client Storage Client Pattern

```go
// Source: legacy S3-compatible client v7 docs
import (
    "old object-storage dependency"
    "old object-storage dependency credentials package"
)

func NewStorageClient(endpoint, accessKey, secretKey string, useSSL bool) (*old storage client, error) {
    return old storage client constructor(endpoint, &old storage client Options{
        Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
        Secure: useSSL,
    })
}

// Single upload
_, err = client.PutObject(ctx, bucket, objectKey, reader, size, old storage client PutObjectOptions{
    ContentType: contentType,
})

// Presigned download URL (1-hour TTL for image responses)
url, err := client.PresignedGetObject(ctx, bucket, objectKey, time.Hour, nil)

// Multipart upload lifecycle
uploadID, err := client.NewMultipartUpload(ctx, bucket, key, old storage client PutObjectOptions{})
part, err := client.PutObjectPart(ctx, bucket, key, uploadID, partNum, reader, size, old storage client PutObjectPartOptions{})
_, err = client.CompleteMultipartUpload(ctx, bucket, key, uploadID, completeParts, old storage client PutObjectOptions{})
```

### Asynq Batch Worker Pattern

```go
// Source: hibiken/asynq docs, adapted to project pattern
mux := asynq.NewServeMux()
mux.HandleFunc(TypeBatchPoll, HandleBatchPoll)

srv := asynq.NewServer(
    asynq.RedisClientOpt{Addr: redisAddr},
    asynq.Config{
        Concurrency: 10,
        Queues: map[string]int{
            "batch": 1,
        },
        RetryDelayFunc: asynq.DefaultRetryDelayFunc,
    },
)

// Enqueue polling task with delay
client := asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
task := asynq.NewTask(TypeBatchPoll, payload)
_, err = client.Enqueue(task,
    asynq.Queue("batch"),
    asynq.ProcessIn(30*time.Second),  // poll interval
    asynq.MaxRetry(72),               // ~36 hours of polling
    asynq.Timeout(25*time.Second),    // per-attempt timeout
)
```

### Handler Registration Pattern (main.go additions)

```go
// Source: established main.go pattern
filesHandler := files.NewHandler(authorizer, filesClient)
mux.Handle("/v1/files", filesHandler)
mux.Handle("/v1/files/", filesHandler)  // covers /v1/files/{id} and /v1/files/{id}/content

uploadsHandler := uploads.NewHandler(authorizer, uploadsClient)
mux.Handle("/v1/uploads", uploadsHandler)
mux.Handle("/v1/uploads/", uploadsHandler)  // covers /v1/uploads/{id}/parts, /complete, /cancel

batchesHandler := batches.NewHandler(authorizer, batchesClient)
mux.Handle("/v1/batches", batchesHandler)
mux.Handle("/v1/batches/", batchesHandler)  // covers /v1/batches/{id}, /cancel

imagesHandler := images.NewHandler(orchestrator)
mux.Handle("/v1/images/generations", imagesHandler)
mux.Handle("/v1/images/edits", imagesHandler)

audioHandler := audio.NewHandler(orchestrator)
mux.Handle("/v1/audio/speech", audioHandler)
mux.Handle("/v1/audio/transcriptions", audioHandler)
mux.Handle("/v1/audio/translations", audioHandler)
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Storing uploaded files locally on server | S3-compatible object storage | Pre-2020 | Files survive restarts, horizontally scalable |
| Polling batch status synchronously in request | Async worker with Redis queue | Pre-2022 | Request returns immediately; 24h+ batch windows become feasible |
| Storing provider image URLs in response | Presigned URLs from own storage | 2023+ | Avoids exposing provider identity; controls TTL |
| Buffering full audio before responding | Chunked Transfer Encoding streaming | 2022+ | Reduces TTFB for TTS; enables real-time audio playback |
| Multipart assembly in memory | Streaming with io.Pipe | Go idiomatic | Avoids OOM on large file uploads |

**Deprecated/outdated:**
- Direct LiteLLM URL embedding in responses: never done in Hive — provider-blind policy requires storing-and-re-signing all output assets.

---

## Open Questions

1. **LiteLLM batch endpoint behavior for non-OpenAI providers**
   - What we know: LiteLLM documents batch support for OpenAI, Azure, Vertex, Bedrock, vLLM. The Hive batch worker will forward to the upstream via LiteLLM's `/v1/batches`.
   - What's unclear: Whether LiteLLM's batch endpoint normalizes the response shape across providers, or whether provider-specific response shapes leak through.
   - Recommendation: Test against LiteLLM's `/v1/batches` endpoint directly in the local dev environment before finalizing the normalization layer. Plan for provider-blind sanitization on batch retrieval responses.

2. **Supabase Storage S3 endpoint availability in local dev**
   - What we know: Supabase Storage (hosted) exposes an S3-compatible endpoint. legacy local object-store emulator is the local equivalent.
   - What's unclear: Whether `SUPABASE_STORAGE_S3_ENDPOINT`, `SUPABASE_STORAGE_S3_ACCESS_KEY`, and `SUPABASE_STORAGE_S3_SECRET_KEY` env vars are the correct names to use, or whether Supabase uses different naming.
   - Recommendation: Verify env var names from Supabase dashboard before wiring them into docker-compose. Use a storage interface so the legacy S3-compatible client client can be swapped easily.

3. **Audio duration metering source of truth**
   - What we know: TTS is metered per character (character count from the input `text` field). Transcription/translation is metered per second of audio.
   - What's unclear: LiteLLM's transcription response includes a `duration` field — whether this is reliably populated for all providers or needs to be estimated from file size.
   - Recommendation: Extract `duration` from the LiteLLM transcription response JSON first; fall back to estimating from file size only if `duration` is absent.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | `go test` (stdlib), table-driven tests |
| Config file | none (standard `go test ./...`) |
| Quick run command | `go test ./apps/edge-api/internal/files/... ./apps/edge-api/internal/batches/... ./apps/edge-api/internal/images/... ./apps/edge-api/internal/audio/...` |
| Full suite command | `go test -race ./apps/edge-api/...` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| API-05 | Image generation request dispatched to LiteLLM with model alias rewrite | unit | `go test ./apps/edge-api/internal/images/... -run TestImageGeneration` | Wave 0 |
| API-05 | Image edits forwarded as multipart, not JSON | unit | `go test ./apps/edge-api/internal/images/... -run TestImageEditsMultipart` | Wave 0 |
| API-05 | Image response normalizes model alias, returns signed URL or b64 | unit | `go test ./apps/edge-api/internal/images/... -run TestNormalizeImageResponse` | Wave 0 |
| API-05 | Unsupported image operation (variations) returns OpenAI-style 501 | unit | `go test ./apps/edge-api/internal/images/... -run TestImageVariationsUnsupported` | Wave 0 |
| API-06 | Speech request dispatched, binary response piped with correct Content-Type | unit | `go test ./apps/edge-api/internal/audio/... -run TestSpeechBinaryRelay` | Wave 0 |
| API-06 | Transcription multipart forwarded without storing audio | unit | `go test ./apps/edge-api/internal/audio/... -run TestTranscriptionForward` | Wave 0 |
| API-06 | Translation multipart forwarded without storing audio | unit | `go test ./apps/edge-api/internal/audio/... -run TestTranslationForward` | Wave 0 |
| API-07 | File upload stores blob in S3, writes metadata row | unit | `go test ./apps/edge-api/internal/files/... -run TestFileUpload` | Wave 0 |
| API-07 | File retrieve returns OpenAI FileObject, validates account ownership | unit | `go test ./apps/edge-api/internal/files/... -run TestFileRetrieve` | Wave 0 |
| API-07 | Uploads API: create→addPart→complete lifecycle | unit | `go test ./apps/edge-api/internal/files/... -run TestUploadsLifecycle` | Wave 0 |
| API-07 | Batch submit validates JSONL before reserving credits | unit | `go test ./apps/edge-api/internal/batches/... -run TestBatchValidateBeforeReserve` | Wave 0 |
| API-07 | Batch worker state machine: validating→in_progress→completed | unit | `go test ./apps/control-plane/internal/batchstore/... -run TestBatchStateMachine` | Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./apps/edge-api/internal/...` (no -race for speed)
- **Per wave merge:** `go test -race ./apps/edge-api/... ./apps/control-plane/...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `apps/edge-api/internal/files/handler_test.go` — covers API-07 file upload/retrieve
- [ ] `apps/edge-api/internal/images/handler_test.go` — covers API-05 image generation/edits
- [ ] `apps/edge-api/internal/audio/handler_test.go` — covers API-06 speech/transcription/translation
- [ ] `apps/edge-api/internal/batches/handler_test.go` — covers API-07 batch submit/retrieve
- [ ] `apps/control-plane/internal/filestore/repository_test.go` — covers file metadata CRUD
- [ ] `apps/control-plane/internal/batchstore/repository_test.go` — covers batch state machine
- [ ] Docker legacy local object-store emulator service in docker-compose for local dev storage

---

## Sources

### Primary (HIGH confidence)
- Codebase: `apps/edge-api/internal/inference/` — All handler, orchestrator, streaming, litellm, accounting, routing patterns
- Codebase: `apps/control-plane/internal/routing/types.go` — Capability flag extension pattern
- Codebase: `apps/control-plane/go.mod` — pgx/v5, go-redis/v9, google/uuid already in stack
- Codebase: `deploy/docker/docker-compose.yml` — Redis already in stack; legacy local object-store emulator not yet added

### Secondary (MEDIUM confidence)
- LiteLLM docs (via WebSearch): `/images/generations`, `/images/edits`, `/audio/speech`, `/audio/transcriptions`, `/audio/translations`, `/batches` — all confirmed supported on LiteLLM proxy
- OpenAI API Reference (via WebSearch): Uploads API three-step lifecycle (create/addPart/complete), batch JSONL format, file purpose expiry rules
- Supabase Storage S3 compatibility (via WebSearch): S3 endpoint path, presigned URL support, PutObject for single upload, multipart for large files
- legacy S3-compatible client v7 docs (via WebSearch): `PutObject`, `NewMultipartUpload`, `PutObjectPart`, `CompleteMultipartUpload`, `PresignedGetObject` — confirmed API shape

### Tertiary (LOW confidence)
- Asynq v0.24 (WebSearch): Confirmed production-ready Redis task queue for Go; exact config options need validation against current pkg docs
- LiteLLM managed batches (WebSearch): Beta feature — the Hive batch worker will likely use LiteLLM's standard `/v1/batches` passthrough rather than its managed batches feature

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — legacy S3-compatible client and asynq are the established Go choices; all other dependencies already in the monorepo
- Architecture: HIGH — all patterns are direct extensions of existing inference handler conventions
- LiteLLM endpoint support: MEDIUM — confirmed by search/docs but exact request shapes for multipart forwarding should be tested in dev
- Batch worker internals: MEDIUM — Asynq API verified by docs; exact retry/concurrency tuning is Claude's discretion

**Research date:** 2026-04-09
**Valid until:** 2026-05-09 (LiteLLM releases frequently; batch/image/audio endpoint shapes stable)
