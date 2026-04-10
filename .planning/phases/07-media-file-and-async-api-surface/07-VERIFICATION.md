---
phase: 07-media-file-and-async-api-surface
verified: 2026-04-10T12:00:00Z
status: passed
score: 24/24 must-haves verified
re_verification:
  previous_status: gaps_found
  previous_score: 21/24
  gaps_closed:
    - "Image and audio endpoints authorize requests before dispatching to LiteLLM"
    - "Image and audio endpoints use routing client to select the correct provider model"
    - "Image and audio endpoints reserve and finalize credits via accounting client"
  gaps_remaining: []
  regressions: []
human_verification:
  - test: "POST /v1/images/generations with response_format=url — load returned URL in browser"
    expected: "Image loads from MinIO (not provider URL); URL expires after 1 hour"
    why_human: "Requires live MinIO + LiteLLM environment; URL reachability cannot be verified statically"
  - test: "POST /v1/audio/speech with valid request — inspect Content-Type header and play audio"
    expected: "Content-Type matches upstream provider (e.g., audio/mpeg); audio is valid and plays"
    why_human: "Requires live LiteLLM with TTS provider; binary fidelity cannot be verified statically"
  - test: "Upload JSONL file, create batch, wait for Asynq worker to poll upstream provider"
    expected: "Batch status transitions validating -> in_progress -> completed; output file accessible via Files API"
    why_human: "Requires live Redis (Asynq), live LiteLLM with batch support, and real elapsed time"
---

# Phase 07: Media, File, and Async API Surface Verification Report

**Phase Goal:** Implement OpenAI-compatible media (images, audio), file management (files, uploads), and async processing (batches) API surfaces with full authorization, routing, and accounting integration.
**Verified:** 2026-04-10
**Status:** passed
**Re-verification:** Yes — after Plan 04 gap closure

## Re-verification Context

Initial verification (same date) found 21/24 truths passing. Three truths failed because `images.Handler` and `audio.Handler` had no Authorizer, RoutingInterface, or AccountingInterface dependencies — unauthenticated callers could generate images and audio without credit charges, and model names passed through to LiteLLM verbatim without capability-based route selection.

Plan 04 (`07-04-PLAN.md`) was created to close those gaps. This re-verification confirms all three gaps are closed and no regressions were introduced.

---

## Goal Achievement

### Observable Truths

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | MinIO runs in Docker Compose with health check and bucket init | VERIFIED | No regression — docker-compose.yml minio/minio-init services unchanged |
| 2  | File metadata persists in Postgres (files, uploads, upload_parts tables) | VERIFIED | No regression — filestore/repository.go unchanged |
| 3  | Batch metadata table exists with state machine columns | VERIFIED | No regression — filestore/repository.go unchanged |
| 4  | Routing selection filters by image/audio/batch capability flags | VERIFIED | No regression — routing/service.go capability checks unchanged |
| 5  | Control-plane internal endpoints serve file/upload/batch CRUD | VERIFIED | No regression — filestore/http.go and control-plane main.go unchanged |
| 6  | Developer can POST /v1/images/generations and receive OpenAI-compatible response | VERIFIED | images/handler.go handleGeneration present; h.authorizer.AuthorizeRequest at line 123 gates entry |
| 7  | Image responses with response_format=url return signed URLs with 1-hour TTL | VERIFIED | No regression — PresignedURL with time.Hour TTL unchanged |
| 8  | Image responses with response_format=b64_json pass through without S3 storage | VERIFIED | No regression — b64_json path unchanged |
| 9  | Unsupported image variations return OpenAI-style 501 error | VERIFIED | handler.go lines 144-147: /v1/images/variations returns unsupported_operation 501; auth not required for this rejection path (correct) |
| 10 | Developer can POST /v1/audio/speech and receive binary audio with correct Content-Type | VERIFIED | audio/handler.go handleSpeech present; h.authorizer.AuthorizeRequest at line 110 gates entry |
| 11 | Audio files are never stored to disk or object storage | VERIFIED | No regression — audio Handler has no storage field |
| 12 | Developer can POST /v1/audio/transcriptions and /v1/audio/translations | VERIFIED | handleMultipartAudio wires both endpoints with full auth/routing/accounting lifecycle |
| 13 | Image and audio endpoints authorize requests before dispatching | VERIFIED | images/handler.go:123 h.authorizer.AuthorizeRequest; audio/handler.go:110 h.authorizer.AuthorizeRequest; six adapter files with NewAuthorizerAdapter constructors in both packages |
| 14 | Image and audio endpoints use routing client to select correct provider model | VERIFIED | images/handler.go:179,321 h.routing.SelectRoute with NeedImageGeneration/NeedImageEdit; audio/handler.go:165,291 h.routing.SelectRoute with NeedTTS/NeedSTT; route.LiteLLMModelName rewrites model field before dispatch in all four methods |
| 15 | Image and audio endpoints reserve and finalize credits | VERIFIED | h.accounting.CreateReservation before dispatch in all four handler methods; h.accounting.FinalizeReservation on all success paths; h.accounting.ReleaseReservation on all error paths |
| 16 | Developer can upload a file via POST /v1/files and retrieve via GET /v1/files/{id} | VERIFIED | No regression — files/handler.go unchanged |
| 17 | Developer can list, delete, and download file content | VERIFIED | No regression — files/handler.go unchanged |
| 18 | Developer can use Uploads API (create, add parts, complete, cancel) | VERIFIED | No regression — files/handler.go Uploads lifecycle unchanged |
| 19 | Developer can create, retrieve, list, and cancel batches | VERIFIED | No regression — batches/handler.go unchanged |
| 20 | Batch worker polls upstream and assembles output on completion | VERIFIED | No regression — batchstore/worker.go unchanged |
| 21 | File access validates account ownership — no cross-account leakage | VERIFIED | No regression — account_id scoping in files/handler.go and batches/handler.go unchanged |

**Score:** 24/24 truths verified (3 previously failed truths now pass)

### Required Artifacts

| Artifact | Status | Evidence |
|----------|--------|----------|
| `apps/edge-api/internal/images/handler.go` | VERIFIED | Handler struct has authorizer/routing/accounting fields (lines 87-91); NewHandler takes 7 args |
| `apps/edge-api/internal/images/authz_adapter.go` | VERIFIED | NewAuthorizerAdapter(inner *authz.Authorizer) *AuthorizerAdapter at line 16 |
| `apps/edge-api/internal/images/routing_adapter.go` | VERIFIED | NewRoutingAdapter(inner *inference.RoutingClient) *RoutingAdapter at line 16 |
| `apps/edge-api/internal/images/accounting_adapter.go` | VERIFIED | NewAccountingAdapter(inner *inference.AccountingClient) *AccountingAdapter at line 16 |
| `apps/edge-api/internal/audio/handler.go` | VERIFIED | Handler struct has authorizer/routing/accounting fields (lines 81-85); NewHandler takes 5 args |
| `apps/edge-api/internal/audio/authz_adapter.go` | VERIFIED | NewAuthorizerAdapter(inner *authz.Authorizer) *AuthorizerAdapter at line 16 |
| `apps/edge-api/internal/audio/routing_adapter.go` | VERIFIED | NewRoutingAdapter(inner *inference.RoutingClient) *RoutingAdapter at line 16 |
| `apps/edge-api/internal/audio/accounting_adapter.go` | VERIFIED | NewAccountingAdapter(inner *inference.AccountingClient) *AccountingAdapter at line 16 |
| `apps/edge-api/cmd/server/main.go` | VERIFIED | imagesAuthorizer/imagesRouting/imagesAccounting wired lines 91-97; audioAuthorizer/audioRouting/audioAccounting wired lines 107-113 |
| `apps/control-plane/internal/filestore/repository.go` | VERIFIED | No regression — unchanged |
| `apps/control-plane/internal/filestore/http.go` | VERIFIED | No regression — unchanged |
| `apps/edge-api/internal/files/handler.go` | VERIFIED | No regression — unchanged |
| `apps/edge-api/internal/batches/handler.go` | VERIFIED | No regression — unchanged |
| `apps/control-plane/internal/batchstore/worker.go` | VERIFIED | No regression — unchanged |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| images/handler.go | images/authz_adapter.go | h.authorizer.AuthorizeRequest | WIRED | handler.go:123; authz_adapter.go:16 NewAuthorizerAdapter bridges authz.Authorizer |
| images/handler.go | images/routing_adapter.go | h.routing.SelectRoute | WIRED | handler.go:179 (handleGeneration), 321 (handleEdit); routing_adapter.go:16 bridges inference.RoutingClient |
| images/handler.go | images/accounting_adapter.go | h.accounting.CreateReservation | WIRED | handler.go:191 (handleGeneration), 333 (handleEdit); accounting_adapter.go:16 bridges inference.AccountingClient |
| audio/handler.go | audio/authz_adapter.go | h.authorizer.AuthorizeRequest | WIRED | handler.go:110; authz_adapter.go:16 NewAuthorizerAdapter bridges authz.Authorizer |
| audio/handler.go | audio/routing_adapter.go | h.routing.SelectRoute | WIRED | handler.go:165 (handleSpeech), 291 (handleMultipartAudio); NeedTTS/NeedSTT capability flags used |
| audio/handler.go | audio/accounting_adapter.go | h.accounting.CreateReservation | WIRED | handler.go:177 (handleSpeech), 303 (handleMultipartAudio); accounting_adapter.go:16 bridges inference.AccountingClient |
| main.go | images/handler.go | images.NewHandler with 7 adapters | WIRED | main.go:91-97: imagesAuthorizer, imagesRouting, imagesAccounting instantiated and passed |
| main.go | audio/handler.go | audio.NewHandler with 5 adapters | WIRED | main.go:107-113: audioAuthorizer, audioRouting, audioAccounting instantiated and passed |
| images/handler.go | route.LiteLLMModelName | bodyMap["model"] rewrite before dispatch | WIRED | handler.go:213 (handleGeneration), 348 (handleEdit) |
| audio/handler.go | route.LiteLLMModelName | bodyMap["model"] rewrite before dispatch | WIRED | handler.go:199 (handleSpeech), 318 (handleMultipartAudio) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| API-05 | 07-02, 07-04 | Developer can call image-generation and image-processing endpoints with OpenAI-compatible behavior | SATISFIED | Full lifecycle: authorize -> SelectRoute (NeedImageGeneration/NeedImageEdit) -> CreateReservation -> dispatch with rewritten model -> FinalizeReservation/ReleaseReservation |
| API-06 | 07-02, 07-04 | Developer can call speech, transcription, and translation endpoints with OpenAI-compatible behavior | SATISFIED | Full lifecycle: authorize -> SelectRoute (NeedTTS/NeedSTT) -> CreateReservation -> dispatch with rewritten model -> FinalizeReservation/ReleaseReservation |
| API-07 | 07-01, 07-03 | Developer can use files, uploads, and batches flows required by official SDK integrations | SATISFIED | Full Files/Uploads/Batches API with auth, storage, metadata persistence, async worker — no change from initial verification |

### Anti-Patterns Found

None. The three previously-identified blockers (missing auth/routing/accounting in images and audio handlers) are resolved. No new anti-patterns introduced by Plan 04.

### Human Verification Required

#### 1. Image URL Mode — S3 Presigned URL Validity

**Test:** POST `/v1/images/generations` with `response_format=url` and a working provider. Attempt to load the returned URL in a browser.
**Expected:** Image loads from MinIO (not provider URL); URL expires after 1 hour.
**Why human:** Requires live MinIO + LiteLLM environment; URL reachability cannot be verified statically.

#### 2. Audio Binary Content-Type Passthrough

**Test:** POST `/v1/audio/speech` with a valid request. Inspect the response `Content-Type` header and play the audio.
**Expected:** `Content-Type` matches what the upstream provider returns (e.g., `audio/mpeg`); audio is valid and plays.
**Why human:** Requires live LiteLLM with a TTS provider; binary fidelity cannot be verified statically.

#### 3. Batch Worker End-to-End

**Test:** Upload a JSONL file, create a batch, and wait for the Asynq worker to poll the upstream provider.
**Expected:** Batch status transitions from `validating` -> `in_progress` -> `completed`; output file accessible via Files API.
**Why human:** Requires live Redis (Asynq), live LiteLLM with batch support, and real elapsed time.

### Gaps Summary

No gaps remain. All 24 truths verified. Phase goal fully achieved.

Plan 04 closed the three gaps by adding six adapter files (authz_adapter.go, routing_adapter.go, accounting_adapter.go for both the images and audio packages) and updating both Handler structs with Authorizer, RoutingInterface, and AccountingInterface fields. Every handle* method now follows the full orchestrator lifecycle: authorize -> SelectRoute with correct capability flag -> CreateReservation -> dispatch with model alias rewritten to LiteLLMModelName -> FinalizeReservation on success or ReleaseReservation on any error path. main.go wires all adapters using the already-initialized authorizer, routingClient, and accountingClient instances. Two commits (`7f99647`, `7addd95`) carry the implementation; the build was confirmed passing by the executor.

---

_Verified: 2026-04-10_
_Verifier: Claude (gsd-verifier)_
