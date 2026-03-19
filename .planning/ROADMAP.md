# Roadmap: Hive OpenAI API Compliance

## Overview

This milestone transforms Hive's existing `/v1/*` endpoints from "routes that work" into a fully OpenAI-SDK-compatible API surface. The journey starts with the most visible breakage (error format), builds type infrastructure, then hardens each endpoint category in dependency order. Every phase delivers a verifiable improvement that can be tested with the official `openai` npm SDK.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: Error Format Standardization** - All API errors use OpenAI's nested error object format (completed 2026-03-17)
- [x] **Phase 2: Type Infrastructure** - TypeBox schemas and generated OpenAI types provide compile-time and runtime validation (completed 2026-03-18)
- [x] **Phase 3: Auth Compliance** - Bearer token auth works identically to OpenAI across all official SDKs (completed 2026-03-18)
- [x] **Phase 4: Models Endpoint** - `/v1/models` and `/v1/models/{model}` return fully compliant model objects (completed 2026-03-18)
- [x] **Phase 5: Chat Completions (Non-Streaming)** - Non-streaming chat completions match OpenAI response schema exactly (completed 2026-03-18)
- [x] **Phase 6: Chat Completions (Streaming)** - SSE streaming with proper chunk format, usage telemetry, and termination (completed 2026-03-18)
- [x] **Phase 7: Surface Expansion** - Embeddings, images, and responses endpoints are schema-compliant (completed 2026-03-19)
- [x] **Phase 8: Differentiators** - Hive-specific headers, credit cost, model aliasing, and request IDs on all endpoints (completed 2026-03-19)
- [x] **Phase 9: Operational Hardening** - Stub endpoints for unsupported APIs and GitHub issue tracking for deferred work (completed 2026-03-19)

## Phase Details

### Phase 1: Error Format Standardization
**Goal**: Every error response from `/v1/*` endpoints matches what OpenAI SDKs expect to parse
**Depends on**: Nothing (first phase)
**Requirements**: FOUND-01
**Success Criteria** (what must be TRUE):
  1. A malformed request to any `/v1/*` endpoint returns `{ "error": { "message": "...", "type": "invalid_request_error", "param": ..., "code": ... } }` with status 400
  2. A request with an invalid API key returns status 401 with `type: "authentication_error"`
  3. The official `openai` npm SDK can parse all error responses without crashing (no `undefined` access on error fields)
**Plans**: 2 plans

Plans:
- [ ] 01-01: Error helper + v1 plugin + tests
- [ ] 01-02: Migrate route error sends + restructure registration

### Phase 2: Type Infrastructure
**Goal**: All `/v1/*` routes have compile-time type safety from OpenAI's spec and runtime request validation via Fastify's native pipeline
**Depends on**: Phase 1
**Requirements**: FOUND-06, FOUND-07
**Success Criteria** (what must be TRUE):
  1. TypeScript compilation fails if a `/v1/*` response builder returns a shape that doesn't match the OpenAI spec
  2. Fastify rejects requests with invalid/extra fields on `/v1/*` routes and returns the Phase 1 error format
  3. Generated OpenAI types exist as importable TypeScript types from the spec file at `docs/reference/openai-openapi.yml`
**Plans**: 2 plans

Plans:
- [ ] 02-01: Install TypeBox + type provider, generate OpenAI types, configure Fastify AJV, create schemas
- [ ] 02-02: Wire schemas into route handlers + validation integration tests

### Phase 3: Auth Compliance
**Goal**: Bearer token authentication and content-type headers work identically to OpenAI across Python, Node, and Go SDKs
**Depends on**: Phase 1
**Requirements**: FOUND-02, FOUND-05
**Success Criteria** (what must be TRUE):
  1. `Authorization: Bearer sk-...` is the primary auth mechanism and works with the official `openai` Python, Node, and Go SDK client constructors
  2. The `x-api-key` fallback does not interfere with or override Bearer token behavior
  3. Non-streaming responses return `Content-Type: application/json` and streaming responses return `Content-Type: text/event-stream`
**Plans**: 2 plans

Plans:
- [ ] 03-01-PLAN.md — Simplify /v1/* auth to bearer-only + Content-Type onSend hook
- [ ] 03-02-PLAN.md — SDK integration tests for auth compliance and Content-Type

### Phase 4: Models Endpoint
**Goal**: Developers can call `/v1/models` and `/v1/models/{model}` and get responses that match OpenAI's schema
**Depends on**: Phase 2
**Requirements**: FOUND-03, FOUND-04
**Success Criteria** (what must be TRUE):
  1. `GET /v1/models` returns `{ "object": "list", "data": [...] }` where each model has `id`, `object: "model"`, `created` (unix int), `owned_by` fields
  2. `GET /v1/models/gpt-4o` returns a single model object with the same fields, or a 404 in OpenAI error format if not found
  3. The `openai` SDK's `client.models.list()` and `client.models.retrieve("model-id")` work without errors
**Plans**: 2 plans

Plans:
- [ ] 04-01-PLAN.md — Expand model catalog, add created/owned_by fields, fix list serialization, add retrieve route
- [ ] 04-02-PLAN.md — Unit tests and SDK integration tests for models compliance

### Phase 5: Chat Completions (Non-Streaming)
**Goal**: Non-streaming `/v1/chat/completions` responses are schema-complete and all request parameters are forwarded to providers
**Depends on**: Phase 2, Phase 3
**Requirements**: CHAT-01, CHAT-02, CHAT-03
**Success Criteria** (what must be TRUE):
  1. Response includes all required fields: `id`, `object: "chat.completion"`, `created`, `model`, `choices` (with `finish_reason`, `index`, `message`, `logprobs`), and `usage` (with `prompt_tokens`, `completion_tokens`, `total_tokens`)
  2. Parameters like `temperature`, `top_p`, `tools`, `tool_choice`, `response_format`, `max_completion_tokens`, `reasoning_effort` are forwarded to the upstream provider (not silently dropped)
  3. The `openai` SDK's `client.chat.completions.create()` returns a fully typed response with no undefined required fields
**Plans**: 2 plans

Plans:
- [ ] 05-01-PLAN.md — Provider pipeline + service + route for full non-streaming compliance
- [ ] 05-02-PLAN.md — Unit, route, and compliance tests for CHAT-01/02/03

### Phase 6: Chat Completions (Streaming)
**Goal**: Streaming chat completions follow the OpenAI SSE protocol exactly, including usage telemetry in the final chunk
**Depends on**: Phase 5
**Requirements**: CHAT-04, CHAT-05
**Success Criteria** (what must be TRUE):
  1. `stream: true` returns `text/event-stream` with `data: {json}\n\n` lines using `choices[].delta` (not `message`) and terminates with `data: [DONE]\n\n`
  2. With `stream_options: { include_usage: true }`, the final chunk before `[DONE]` contains a `usage` object with token counts and `choices: []`
  3. The `openai` SDK's `for await (const chunk of stream)` iteration works correctly and receives all chunks including usage
  4. Intermediate streaming chunks have `usage: null` (not omitted)
**Plans**: 2 plans

Plans:
- [ ] 06-01-PLAN.md — Streaming pipeline: chatStream() on provider/registry/service + route SSE branch
- [ ] 06-02-PLAN.md — SSE streaming compliance tests for CHAT-04/CHAT-05

### Phase 7: Surface Expansion
**Goal**: Embeddings, image generation, and responses endpoints are fully schema-compliant
**Depends on**: Phase 2, Phase 3
**Requirements**: SURF-01, SURF-02, SURF-03
**Success Criteria** (what must be TRUE):
  1. `POST /v1/embeddings` routes to an OpenRouter embedding model and returns `{ "object": "list", "data": [{ "embedding": [...], "index": 0 }], "model": "...", "usage": {...} }`
  2. `POST /v1/images/generations` returns `{ "created": <int>, "data": [{ "url": "..." }] }` matching the OpenAI Image schema
  3. `POST /v1/responses` handles the full `CreateResponse` request schema and returns a compliant `Response` object
  4. The `openai` SDK's `client.embeddings.create()` and `client.images.generate()` work without errors
**Plans**: 3 plans

Plans:
- [ ] 07-01-PLAN.md — Embeddings endpoint: new route, schema, provider method, service method
- [ ] 07-02-PLAN.md — Images fix (real provider call, no object field) + Responses expansion (full CreateResponse schema)
- [ ] 07-03-PLAN.md — Compliance tests for embeddings, images, and responses response shapes

### Phase 8: Differentiators
**Goal**: All `/v1/*` responses include Hive-specific metadata headers for transparency and debugging
**Depends on**: Phase 5, Phase 6, Phase 7
**Requirements**: DIFF-01, DIFF-02, DIFF-03, DIFF-04
**Success Criteria** (what must be TRUE):
  1. Every `/v1/*` response includes `x-request-id`, `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits` headers
  2. Standard OpenAI model names (e.g., `gpt-4o`, `gpt-4o-mini`) are accepted and routed to the best available provider
  3. The credit cost of each request is accessible either in response headers or the usage object
  4. Headers do not break OpenAI SDK parsing (SDKs ignore unknown headers)
**Plans**: 2 plans

Plans:
- [ ] 08-01-PLAN.md — x-request-id hook, model alias config, MVP AiService header gap fixes
- [ ] 08-02-PLAN.md — Model alias resolution tests and header compliance tests

### Phase 9: Operational Hardening
**Goal**: Unsupported OpenAI endpoints return informative errors instead of generic 404s, and all deferred work is tracked
**Depends on**: Phase 1
**Requirements**: OPS-01, OPS-02
**Success Criteria** (what must be TRUE):
  1. Requests to `/v1/audio/*`, `/v1/files`, `/v1/uploads`, `/v1/batches`, `/v1/completions`, `/v1/fine_tuning/*`, `/v1/moderations` return 404 with OpenAI error format including a "coming soon" or "not supported" message
  2. GitHub issues exist for each deferred endpoint with acceptance criteria, linked from the project's issue tracker
**Plans**: 2 plans

Plans:
- [ ] 09-01-PLAN.md — Stub routes for unsupported endpoints + compliance tests
- [ ] 09-02-PLAN.md — GitHub issues for deferred endpoint groups

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Error Format Standardization | 2/2 | Complete   | 2026-03-17 |
| 2. Type Infrastructure | 2/2 | Complete   | 2026-03-18 |
| 3. Auth Compliance | 2/2 | Complete   | 2026-03-18 |
| 4. Models Endpoint | 2/2 | Complete   | 2026-03-18 |
| 5. Chat Completions (Non-Streaming) | 2/2 | Complete    | 2026-03-18 |
| 6. Chat Completions (Streaming) | 1/2 | Complete    | 2026-03-18 |
| 7. Surface Expansion | 3/3 | Complete    | 2026-03-19 |
| 8. Differentiators | 2/2 | Complete   | 2026-03-19 |
| 9. Operational Hardening | 2/2 | Complete   | 2026-03-19 |
