---
status: testing
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
updated: 2026-04-21T04:09:00Z
---

## Current Test

number: 2
name: Model Catalog Endpoint
expected: |
  `GET /v1/models` via edge-api returns OpenAI-shaped JSON with data array
  containing at least hive-default, hive-auto aliases. Same aliases appear
  in control-plane `/catalog/models`.
awaiting: user response

## Tests

### 1. Cold Start Smoke Test
expected: Fresh `docker compose up --build` brings redis, litellm, control-plane, edge-api to healthy; /health on 8080 and 8081 return 200; no fatal errors in logs.
result: issue
reported: "I killed all running core, I deleted all the volumes. All other services are become healthy, but control-plane service is unhealthy: Container docker-control-plane-1 Error dependency control-plane failed to start — dependency failed to start: container docker-control-plane-1 is unhealthy"
severity: blocker

### 2. Model Catalog Endpoint
expected: `GET /v1/models` via edge-api returns OpenAI-shaped JSON with data array containing at least hive-default, hive-auto aliases. Same aliases appear in control-plane `/catalog/models`.
result: [pending]

### 3. Internal Route Resolution For Media + Batch
expected: Control-plane route resolver returns 2xx for `{alias_id:"hive-auto"}` combined with each capability flag: need_image_generation, need_tts, need_stt, need_batch. route-openrouter-auto eligible for all four.
result: [pending]

### 4. Chat Completions (Non-Streaming)
expected: `POST /v1/chat/completions` with `{"model":"hive-default","messages":[{"role":"user","content":"ping"}]}` returns HTTP 200 with body containing `"object":"chat.completion"`. Provider name never appears in response.
result: [pending]

### 5. Edge-API Fail-Fast On Missing Storage Env
expected: Start edge-api with S3_REGION (or other required S3 var) unset. Process exits with clear "storage unavailable" error; /health never comes up. Core storage env is required, not optional.
result: [pending]

### 6. File Upload Round-Trip Through Supabase Storage
expected: `POST /v1/files` multipart upload succeeds (2xx). Returned file id resolves via `GET /v1/files/{id}` and `GET /v1/files/{id}/content`. Object lands in `hive-files` bucket in Supabase Storage.
result: [pending]

### 7. Image Generation Reservation (Strict Policy)
expected: `POST /v1/images/generations` with `{"model":"hive-auto","prompt":"test","n":1,"size":"1024x1024"}` either succeeds (2xx image response) or fails with strict-mode credit enforcement (HTTP 409 insufficient credits). No 400 validation errors, no provider leak.
result: [pending]

### 8. Audio Speech + Transcription Reservations (Strict Policy)
expected: `POST /v1/audio/speech` and multipart `POST /v1/audio/transcriptions` with `model=hive-auto` both create strict reservations. Either 2xx audio payload or 409 insufficient credits — no 400 validation regression.
result: [pending]

### 9. Batch Create With Attribution
expected: `POST /v1/batches` with JSONL referencing a single `body.model` succeeds. Returned batch record carries api_key_id, model_alias, estimated_credits. Mixed or missing body.model is rejected before reservation creation.
result: [pending]

### 10. Terminal Batch Settlement
expected: When a batch completes, worker finalizes the reservation with actual_credits ≤ estimated_credits. Ledger reflects spend attributed to the batch's api_key_id and model_alias. No overcharge beyond the reserved estimate.
result: [pending]

### 11. Provider-Blind Error Sanitization
expected: Force an upstream failure (bad key, rate limit). Customer-visible response contains OpenAI-style error envelope; no "openrouter", "groq", raw upstream host, or provider detail appears in body.
result: [pending]

### 12. No FX / USD On BD-Visible Surfaces
expected: BD checkout or payment-intent response for a BD account contains no `amount_usd` field, no FX rate, no currency-exchange language. BDT-only surface.
result: [pending]

## Summary

total: 12
passed: 0
issues: 1
pending: 11
skipped: 0

## Gaps

- truth: "Fresh docker compose up --build brings all four core services healthy and /health returns 200 on 8080 and 8081"
  status: failed
  reason: "User reported: control-plane container unhealthy after fresh volume wipe + compose up; edge-api aborts with 'dependency control-plane failed to start — container docker-control-plane-1 is unhealthy'. Other services (redis, litellm) came up healthy."
  severity: blocker
  test: 1
  root_cause: "control-plane healthcheck in deploy/docker/docker-compose.yml lines 131-135 uses interval=5s retries=3 with no start_period. Air-driven Go compile on cold start exceeds the 15s window so the first three healthchecks fail and compose marks control-plane unhealthy. edge-api depends_on control-plane with condition: service_healthy (lines 8-10) so edge-api aborts in 'Created' state before control-plane warms up. Once compiled, control-plane is healthy (verified). Same pattern threatens edge-api (compose.yml lines 26-30) under its downstream dependents."
  artifacts:
    - path: "deploy/docker/docker-compose.yml"
      issue: "control-plane healthcheck lacks start_period; 15s window too short for first-boot Go compile under air"
    - path: "deploy/docker/docker-compose.yml"
      issue: "edge-api healthcheck also has no start_period — same failure mode for its downstream dependents"
  missing:
    - "Add start_period (>= 60s; 120s safer for first cold build) to control-plane healthcheck"
    - "Add start_period to edge-api healthcheck for symmetry"
    - "Optionally raise retries from 3 to 5 to match redis pattern"
  debug_session: ""
