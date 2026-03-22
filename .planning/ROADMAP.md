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
- [x] **Phase 10: Models Route Compliance** - Auth guard and differentiator headers on GET /v1/models routes to close gaps found by milestone audit (completed 2026-03-22)
- [x] **Phase 11: Real OpenAI SDK Regression Tests** - Comprehensive SDK coverage for implemented endpoints, success/error paths, and CI-ready execution (completed 2026-03-22)
- [ ] **Phase 12: Embeddings Alias Runtime Compliance** - Accept the standard SDK-facing embeddings model id in the real runtime and lock it with regression coverage
- [x] **Phase 13: Error-Path DIFF Headers** - Preserve DIFF headers on all `/v1/*` error and stub responses (completed 2026-03-22)

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

### Phase 10: Models Route Compliance
**Goal**: Close auth and header gaps on GET /v1/models routes discovered by milestone audit — SDK clients with invalid keys must get 401, and all differentiator headers must be present
**Depends on**: Phase 3, Phase 4, Phase 8
**Requirements**: FOUND-02, DIFF-01
**Gap Closure**: Closes gaps from v1.0 milestone audit
**Success Criteria** (what must be TRUE):
  1. `GET /v1/models` and `GET /v1/models/{model}` return 401 with `authentication_error` when called with an invalid or missing API key
  2. The official `openai` SDK's `client.models.list()` with an invalid key raises `AuthenticationError` (not returns a list)
  3. All responses from `GET /v1/models` and `GET /v1/models/{model}` include `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits` headers (with static catalog-appropriate values)
  4. Existing models endpoint tests continue to pass with a valid API key
**Plans**: 1 plan

## Progress

**Execution Order:**
Phases execute in numeric order: 1 -> 2 -> 3 -> 4 -> 5 -> 6 -> 7 -> 8 -> 9 -> 10 -> 11 -> 12 -> 13

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
| 9. Operational Hardening | 2/2 | Complete    | 2026-03-19 |
| 10. Models Route Compliance | 1/1 | Complete   | 2026-03-22 |
| 11. Real OpenAI SDK Regression Tests | 1/1 | Complete | 2026-03-22 |
| 12. Embeddings Alias Runtime Compliance | 0/1 | Planned | - |
| 13. Error-Path DIFF Headers | 1/1 | Complete   | 2026-03-22 |

### Phase 11: Real OpenAI SDK regression tests — CI-style e2e

**Goal:** Expand the existing 5-test OpenAI SDK regression suite into a comprehensive CI-ready test suite covering all implemented endpoints with success and error paths via the real OpenAI Node.js SDK.
**Requirements**:
- CI-01: Success-path coverage for all endpoints (models list/retrieve, chat, embeddings, images, responses)
- CI-02: Streaming coverage via async iterator (chat.completions with stream:true)
- CI-03: Error-path coverage (401 auth, 402 credits, 429 rate-limit, 422 validation, 404 stubs)
- CI-04: Strict TypeScript compliance — no any/unknown/unsafe casts in test code or helpers
- CI-05: `pnpm --filter @hive/api test` exits 0 with all regression tests passing
**Depends on:** Phase 10
**Plans:** 1/1 plans complete

Plans:
- [x] TBD (run /gsd:plan-phase 11 to break down) (completed 2026-03-21)

### Phase 12: Embeddings Alias Runtime Compliance

**Goal:** Close the remaining embeddings alias gap so standard OpenAI SDK model IDs resolve in the real runtime catalog
**Depends on:** Phase 7, Phase 8, Phase 11
**Requirements:** DIFF-03
**Gap Closure:** Closes the remaining v1.0 milestone audit gaps for embeddings aliasing and the broken real-runtime SDK embeddings flow
**Success Criteria** (what must be TRUE):
  1. `POST /v1/embeddings` accepts `text-embedding-3-small` in the real runtime, not only the mock regression harness
  2. `resolveModelAlias()` maps standard SDK-facing embeddings model ids to production catalog entries consistently
  3. The official `openai` npm SDK's `client.embeddings.create({ model: "text-embedding-3-small" })` succeeds against the real runtime path
  4. Regression coverage exercises the production catalog path so the alias gap cannot hide behind mocks again
**Plans:** 0/1 plans complete

Plans:
- [ ] TBD (run `$gsd-plan-phase 12` to break down)

### Phase 13: Error-Path DIFF Headers

**Goal:** Close the remaining DIFF header contract gaps so all `/v1/*` error and stub responses preserve the differentiator headers
**Depends on:** Phase 1, Phase 7, Phase 8, Phase 9, Phase 10, Phase 11
**Requirements:** DIFF-01
**Gap Closure:** Closes the remaining v1.0 milestone audit gaps for non-success response headers across chat, embeddings, images, responses, and stub routes
**Success Criteria** (what must be TRUE):
  1. Auth and service-error paths for `POST /v1/chat/completions`, `/v1/embeddings`, `/v1/images/generations`, and `/v1/responses` include `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits`
  2. Unsupported `/v1/*` stub routes include the same DIFF headers on 404 responses
  3. Route handlers seed DIFF headers before calling `sendApiError()` or equivalent early-return paths
  4. Regression coverage proves the header contract on representative live error paths and stub responses
**Plans:** 1/1 plans complete

Plans:
- [x] 13-PLAN.md — Seed DIFF headers on v1 error and stub paths, then lock with live regressions (completed 2026-03-22)
