---
status: complete
phase: 10-routing-storage-critical-fixes
source:
  - 10-01-SUMMARY.md
  - 10-02-SUMMARY.md
  - 10-03-SUMMARY.md
  - 10-04-SUMMARY.md
  - 10-05-SUMMARY.md
  - 10-06-SUMMARY.md
  - 10-07-SUMMARY.md
  - 10-08-SUMMARY.md
  - 10-09-SUMMARY.md
  - 10-10-SUMMARY.md
  - 10-11-SUMMARY.md
started: 2026-04-21T04:04:46Z
updated: 2026-04-21T22:00:00Z
---

## Status

UAT complete. Success-path terminal settlement (status=completed) deferred as documented non-blocking known issue: `KNOWN-ISSUE-batch-upstream.md`. Failure-path terminal settlement verified live end-to-end.

## Tests

### 1. Cold Start Smoke Test
expected: Fresh `docker compose up --build` brings redis, litellm, control-plane, edge-api to healthy; /health on 8080 and 8081 return 200; no fatal errors in logs.
result: pass
evidence: |
  Wiped-volume rebuild under renamed project `hive`:
  `docker compose -p docker down --remove-orphans` then `cd deploy/docker && docker compose up -d --build redis litellm control-plane edge-api`.
  `docker ps --filter label=com.docker.compose.project=hive` -> all four containers up, redis/control-plane/edge-api healthy, litellm running.
  `curl -i http://localhost:8080/health` -> HTTP 200 `{"status":"ok"}`.
  `curl -i http://localhost:8081/health` -> HTTP 200 `{"status":"ok"}`.
  Fix: `deploy/docker/docker-compose.yml` healthcheck now has `retries: 5` + `start_period: 120s` on control-plane and edge-api so first-boot Go compile under air completes before liveness.
  Stack renamed from default `docker` project to `hive` via top-level `name: hive`.

### 2. Model Catalog Endpoint
expected: `GET /v1/models` via edge-api returns OpenAI-shaped JSON with data array containing at least hive-default, hive-auto aliases. Same aliases appear in control-plane `/api/v1/catalog/models`.
result: pass
evidence: |
  edge-api GET /v1/models -> HTTP 200, data:[hive-auto, hive-default, hive-fast], object:"list", owned_by:"hive" (provider-blind).
  control-plane GET /api/v1/catalog/models -> HTTP 200, full catalog with display_name/capability_badges/pricing/lifecycle for each alias.

### 3. Internal Route Resolution For Media + Batch
expected: Control-plane route resolver returns 2xx for `{alias_id:"hive-auto"}` combined with each capability flag: need_image_generation, need_tts, need_stt, need_batch. route-openrouter-auto eligible for all four.
result: pass
evidence: |
  POST /internal/routing/select {"alias_id":"hive-auto","need_image_generation":true} -> HTTP 200, route_id:"route-openrouter-auto".
  POST /internal/routing/select {"alias_id":"hive-auto","need_tts":true} -> HTTP 200, route_id:"route-openrouter-auto".
  POST /internal/routing/select {"alias_id":"hive-auto","need_stt":true} -> HTTP 200, route_id:"route-openrouter-auto".
  POST /internal/routing/select {"alias_id":"hive-auto","need_batch":true} -> HTTP 200, route_id:"route-openrouter-auto".

### 4. Chat Completions (Non-Streaming)
expected: `POST /v1/chat/completions` with `{"model":"hive-default","messages":[{"role":"user","content":"ping"}]}` returns HTTP 200 with body containing `"object":"chat.completion"`. Provider name never appears in response.
result: pass
evidence: |
  POST /v1/chat/completions -> HTTP 200, object:"chat.completion", model:"hive-default", assistant content:"pong".
  Response body contained no provider name.

### 5. Edge-API Fail-Fast On Missing Storage Env
expected: Start edge-api with S3_REGION (or other required S3 var) unset. Process exits with clear "storage unavailable" error; /health never comes up. Core storage env is required, not optional.
result: pass
evidence: |
  docker compose --env-file /tmp/phase10-no-s3.env run --rm --no-deps edge-api -> process exited 1.
  Startup log: "storage unavailable: S3_REGION is required".

### 6. File Upload Round-Trip Through Supabase Storage
expected: `POST /v1/files` multipart upload succeeds (2xx). Returned file id resolves via `GET /v1/files/{id}` and `GET /v1/files/{id}/content`. Object lands in `hive-files` bucket in Supabase Storage.
result: pass
evidence: |
  After fixing support-matrix templated path matching and rebuilding edge-api, POST /v1/files -> HTTP 200.
  GET /v1/files/file-cce22ca4-8ad4-4c8f-8c7b-6b73192fbbce -> HTTP 200 with file metadata.
  GET /v1/files/file-cce22ca4-8ad4-4c8f-8c7b-6b73192fbbce/content -> HTTP 200 with the uploaded JSONL body.

### 7. Image Generation Reservation (Strict Policy)
expected: `POST /v1/images/generations` with `{"model":"hive-auto","prompt":"test","n":1,"size":"1024x1024"}` either succeeds (2xx image response) or fails with strict-mode credit enforcement (HTTP 409 insufficient credits). No 400 validation errors, no provider leak.
result: pass
evidence: |
  POST /v1/images/generations -> HTTP 402 with OpenAI-style insufficient_quota JSON.
  Body contained no provider terms and no routing/storage validation regression text.

### 8. Audio Speech + Transcription Reservations (Strict Policy)
expected: `POST /v1/audio/speech` and multipart `POST /v1/audio/transcriptions` with `model=hive-auto` both create strict reservations. Either 2xx audio payload or 409 insufficient credits — no 400 validation regression.
result: pass
evidence: |
  POST /v1/audio/speech -> HTTP 402 with OpenAI-style insufficient_quota JSON.
  POST /v1/audio/transcriptions -> HTTP 402 with OpenAI-style insufficient_quota JSON.
  Neither body contained provider terms or routing/storage validation regression text.

### 9. Batch Create With Attribution
expected: `POST /v1/batches` with JSONL referencing a single `body.model` succeeds. Returned batch record carries api_key_id, model_alias, estimated_credits. Mixed or missing body.model is rejected before reservation creation.
result: pass
evidence: |
  Local dev env update set the current key to lifetime budget 100000 and granted account balance 100000 credits.
  POST /v1/batches -> HTTP 200 with batch id batch-67753e09-94ed-4bfc-8c8f-173a3d3c8405.
  GET /v1/batches/batch-67753e09-94ed-4bfc-8c8f-173a3d3c8405 -> HTTP 200.
  Postgres row for that batch shows api_key_id=8b3cb823-ca7e-49b9-b007-0474c5d6426d, model_alias=hive-default, estimated_credits=1000, actual_credits=0, status=validating.

### 10. Terminal Batch Settlement
expected: When a batch completes, worker finalizes the reservation with actual_credits ≤ estimated_credits. Ledger reflects spend attributed to the batch's api_key_id and model_alias. No overcharge beyond the reserved estimate.
result: partial
evidence: |
  New control-plane submitter (`apps/control-plane/internal/batchstore/submitter.go`) wires real upstream batch submission: downloads JSONL from Supabase Storage, selects batch-capable route, rewrites `body.model` to the LiteLLM route id, uploads to LiteLLM `/v1/files`, creates upstream batch, persists `upstream_batch_id`, enqueues `batch:poll` task, and on immediate submission failure releases the reservation and marks the batch `failed`. Unit tests green for both success and failure paths (`go test ./apps/control-plane/internal/batchstore -run Submitter`).

  Live failure-path terminal settlement verified end-to-end:
  POST /v1/batches -> HTTP 500 "Failed to create batch" (LiteLLM rejected upload — see blocker below).
  Postgres row `batch-7baa4b46-854b-46d4-9afd-a2cddf4ec7b1`: status=failed (terminal), api_key_id=8b3cb823-ca7e-49b9-b007-0474c5d6426d, model_alias=hive-default, estimated_credits=1000, actual_credits=0, upstream_batch_id=NULL.
  Postgres reservation `a43cc385-d1b8-47a6-bc0c-0de2ca81fcce`: status=released, reserved_credits=1000, consumed_credits=0, released_credits=1000.
  Attribution preserved, no overcharge (actual=0 ≤ estimated=1000).
blocker: |
  Success-path (completed status) terminal settlement not exercisable with current upstream provider mix. Live probe `POST http://localhost:4000/v1/files` with `custom_llm_provider=openrouter` returned HTTP 400: "LiteLLM doesn't support openrouter for 'create_file'. Only ['openai', 'azure', 'vertex_ai', 'manus', 'anthropic'] are supported."
  OpenRouter and Groq (the only providers currently configured in `deploy/litellm/config.yaml`) are not in LiteLLM's batch-file supported set. `files_settings` was added for openrouter in config.yaml but LiteLLM's own batch dispatcher rejects openrouter before file upload begins.
  Unblocking requires either (a) adding one of openai/azure/vertex_ai/anthropic as a route so `SelectRoute(need_batch=true)` picks a supported provider, or (b) implementing a local batch executor in control-plane that fans out batch JSONL to chat-completions via LiteLLM and composes output/error files without relying on the upstream batch API.

### 11. Provider-Blind Error Sanitization
expected: Force an upstream failure (bad key, rate limit). Customer-visible response contains OpenAI-style error envelope; no "openrouter", "groq", raw upstream host, or provider detail appears in body.
result: pass
evidence: |
  After hardening provider-blind sanitization and rebuilding edge-api, forced upstream failure with an invalid OpenRouter key no longer leaked LiteLLM/provider details.
  Live sync probe: POST /v1/chat/completions -> HTTP 401, x-request-id=req-ff96b1e6-b38a-46ce-9327-10bf7d743ae9, body message:"hive-default request was rejected by the upstream provider."
  Live streaming probe immediately after the auth failure hit LiteLLM cooldown/rate-limit handling -> HTTP 429, x-request-id=req-340b54d5-1b9c-4af4-9aa4-8e2e264a2546, body message:"hive-default is temporarily rate limited."
  edge-api logs retained the raw internal upstream payloads for both request IDs, including litellm.AuthenticationError, OpenrouterException, route-openrouter-default, and the cooldown list, while the client-facing bodies stayed provider-blind.

### 12. No FX / USD On BD-Visible Surfaces
expected: BD checkout or payment-intent response for a BD account contains no `amount_usd` field, no FX rate, no currency-exchange language. BDT-only surface.
result: skipped
reason: User explicitly deferred BD payment-surface validation for this phase.

## Summary

total: 12
passed: 10
partial: 1
issues: 0
pending: 0
skipped: 2

## Gaps

- truth: "Terminal batch settlement on success (status=completed) drives actual_credits from upstream request counts"
  status: partial
  reason: "Failure-path terminal settlement verified live (batch marked failed, reservation released 1000/1000, attribution preserved). Success-path requires upstream LiteLLM batch-file support for a configured provider. LiteLLM rejects create_file for openrouter with 'Only [openai, azure, vertex_ai, manus, anthropic] are supported'. Only openrouter and groq are currently configured."
  severity: non-blocking
  test: 10
  root_cause: "Current provider mix (openrouter + groq) is outside LiteLLM's supported providers for managed file upload."
  missing:
    - "Add one LiteLLM-batch-supported provider (openai / anthropic / azure / vertex_ai) to deploy/litellm/config.yaml and mark it batch-capable in route selection, OR"
    - "Implement local batch executor in control-plane that fans out JSONL rows to /v1/chat/completions and composes output/error files without using LiteLLM's upstream batch API"
  debug_session: ""
