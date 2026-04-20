# Phase 7: Media, File, and Async API Surface - Context

**Gathered:** 2026-04-09
**Status:** Ready for planning

<domain>
## Phase Boundary

Extend OpenAI compatibility to file storage, batch processing, image generation/editing, and audio speech/transcription/translation workflows. This phase covers the Files API, Uploads API, Batches API, image endpoints (generations, edits), and audio endpoints (speech, transcriptions, translations). Variations endpoint is deferred. Vector stores, fine-tuning, realtime, and video endpoints are out of scope.

</domain>

<decisions>
## Implementation Decisions

### File Storage Backend
- Use Supabase Storage for blob storage (S3-compatible, already in hosted stack)
- File metadata (id, purpose, filename, bytes, status, account ownership, created_at) tracked in a Postgres table — decoupled from blob access
- Match OpenAI file size limits: 512MB maximum
- Support both the simple Files API (single-request upload) and the Uploads API (multipart chunked uploads) — required for full SDK compatibility with large files

### Batch Processing Model
- Redis-backed worker queue for batch job processing (Redis already in stack for rate limiting)
- Forward batch requests to upstream provider batch APIs (e.g., OpenAI Batch API for 50% cost savings) — only support batching for providers that have batch API support; return unsupported error for providers without batch APIs
- Batch metadata and status tracking in Postgres (validating -> in_progress -> completed/failed)
- Redis worker polls upstream provider batch status, updates Postgres state, assembles result file on completion
- Support all inference endpoint types in batches (chat/completions, completions, embeddings)
- Reserve credits upfront on batch submission based on estimated cost from JSONL, finalize actual usage on completion, refund surplus
- Standard OpenAI-compatible batch responses only — no Hive-specific savings metadata in API responses

### Image Endpoint Scope
- Support generations and edits at launch; variations endpoint deferred (DALL-E 2 only, rarely used)
- Image response supports both URL and base64 delivery via response_format parameter
- URLs use Supabase Storage signed URLs with 1-hour TTL (matches OpenAI behavior)
- Image model aliases follow the Hive-alias pattern (e.g., hive-image-1) mapped to upstream providers — same provider-blind catalog pattern as text models from Phase 4

### Audio Endpoint Scope
- Support all three operations at launch: speech (TTS), transcriptions (STT), and translations
- TTS responses stream binary audio directly from upstream to client (no intermediate storage) with correct Content-Type headers (audio/mp3, audio/opus, etc.)
- Transcription/translation: stream-forward multipart form upload audio to upstream provider — no intermediate storage, audio stays in-flight only (aligns with privacy rule)
- Audio model aliases follow Hive-alias pattern (hive-tts-1, hive-whisper-1) — consistent with text and image alias strategy
- Duration-based metering: TTS metered per character, transcription/translation metered per second of audio — fits existing per-model pricing catalog from Phase 4

### Claude's Discretion
- legacy local object-store emulator configuration for local Docker dev parity with Supabase Storage
- Exact Redis queue data structures and worker concurrency settings
- Batch JSONL validation rules and error reporting granularity
- Image edit mask handling and format normalization
- Audio format detection and codec negotiation details
- Exact Supabase Storage bucket naming and path conventions

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### OpenAI API Contract
- `packages/openai-contract/upstream/openapi.yaml` — Full upstream OpenAI API spec with file, batch, image, and audio endpoint schemas
- `packages/openai-contract/matrix/support-matrix.json` — Launch support classification for all endpoints including files, batches, images, audio
- `packages/openai-contract/generated/hive-openapi.yaml` — Generated Hive-specific OpenAPI contract

### Existing Inference Patterns
- `apps/edge-api/internal/inference/handler.go` — Handler registration pattern and orchestrator wiring
- `apps/edge-api/internal/inference/orchestrator.go` — Request lifecycle: route selection, reservation, dispatch, finalization
- `apps/edge-api/internal/inference/litellm_client.go` — LiteLLM proxy dispatch pattern
- `apps/edge-api/internal/inference/stream.go` — SSE streaming relay pattern (adapt for binary audio streaming)
- `apps/edge-api/internal/inference/types.go` — OpenAI-compatible type definitions

### Provider Routing
- `apps/edge-api/internal/catalog/client.go` — Model catalog client (extend for image/audio model aliases)
- `apps/control-plane/internal/routing/` — Route selection and capability matrix (add image/audio capabilities)

### Billing Integration
- `apps/edge-api/internal/inference/accounting_client.go` — Reservation lifecycle client (adapt for batch and media metering)

### Error Handling
- `apps/edge-api/internal/errors/openai.go` — OpenAI-style error responses
- `apps/edge-api/internal/middleware/unsupported.go` — Unsupported endpoint middleware

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `inference/orchestrator.go`: Request lifecycle (route -> reserve -> dispatch -> finalize) — reuse for image and audio single-request flows
- `inference/litellm_client.go`: LiteLLM HTTP dispatch — extend for image generation, audio TTS/STT forwarding
- `inference/stream.go`: SSE streaming relay — adapt pattern for binary audio byte streaming
- `inference/handler.go`: Handler + mux registration pattern — follow for new file/batch/image/audio handlers
- `inference/accounting_client.go`: Credit reservation client — reuse for batch bulk reservation and media metering
- `errors/openai.go`: OpenAI error envelope — reuse for all new endpoint error responses
- `catalog/client.go`: Model catalog snapshot — extend with image and audio model capabilities

### Established Patterns
- Handler structs with `NewHandler(deps)` constructor and method-based route handlers
- Orchestrator pattern: route selection -> credit reservation -> upstream dispatch -> usage finalization
- LiteLLM as upstream proxy — all provider dispatch goes through LiteLLM HTTP API
- Provider-blind error sanitization — upstream errors never leak provider identity
- Control-plane for metadata/config, edge-api for hot-path request serving

### Integration Points
- `apps/edge-api/cmd/server/main.go` — Register new handlers for /v1/files, /v1/uploads, /v1/batches, /v1/images, /v1/audio
- `apps/control-plane/internal/platform/db/pool.go` — Postgres pool for new file metadata and batch state tables
- `deploy/docker-compose.yml` — Add legacy local object-store emulator service for local dev file storage
- `packages/openai-contract/matrix/support-matrix.json` — Update support status for implemented endpoints

</code_context>

<specifics>
## Specific Ideas

- Batch processing should leverage upstream provider batch APIs for cost savings (e.g., OpenAI Batch API 50% discount) — this is a core value proposition, not just a pass-through
- Only providers with batch API support should be eligible for batch operations — no fallback to individual requests
- Generated image URLs should feel ephemeral (1-hour TTL) to match OpenAI behavior and avoid indefinite storage costs
- Audio transcription/translation should never persist audio files — stream-forward only, honoring the privacy-first architecture

</specifics>

<deferred>
## Deferred Ideas

- Image variations endpoint (DALL-E 2 only, niche usage) — add when/if demand exists
- Batch savings metadata in API responses — track internally for Phase 9 console analytics, not exposed in API
- Supabase Queues (pgmq) as alternative to Redis for batch processing — revisit if Redis worker proves insufficient

</deferred>

---

*Phase: 07-media-file-and-async-api-surface*
*Context gathered: 2026-04-09*
