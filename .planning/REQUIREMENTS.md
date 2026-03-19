# Requirements: Hive OpenAI API Compliance

**Defined:** 2026-03-17
**Core Value:** Developers can use Hive as a drop-in OpenAI-compatible API with transparent multi-provider routing and prepaid credit billing.

## v1 Requirements

Requirements for this milestone. Each maps to roadmap phases.

### Foundation

- [x] **FOUND-01**: All API error responses use OpenAI error format `{ error: { message, type, param, code } }` with correct status-to-type mapping (400=invalid_request_error, 401=authentication_error, 429=rate_limit_error, 500=server_error)
- [x] **FOUND-02**: Bearer token auth (`Authorization: Bearer <key>`) is verified compatible with official OpenAI Python, Node, and Go SDKs — no edge cases with x-api-key fallback breaking SDK expectations
- [x] **FOUND-03**: `GET /v1/models` returns OpenAI-compliant list with required `id`, `object: "model"`, `created` (unix timestamp), `owned_by` fields on each model object
- [x] **FOUND-04**: `GET /v1/models/{model}` returns a single model object or 404 with proper error format
- [x] **FOUND-05**: All `/v1/*` endpoints return correct `Content-Type` headers (`application/json` for non-streaming, `text/event-stream` for streaming)
- [x] **FOUND-06**: TypeBox + Fastify type provider set up for request validation on all `/v1/*` routes
- [x] **FOUND-07**: OpenAI TypeScript types generated from `docs/reference/openai-openapi.yml` via `openapi-typescript` for compile-time response shape validation

### Chat Completions

- [x] **CHAT-01**: `POST /v1/chat/completions` response includes all required fields: `id`, `object: "chat.completion"`, `choices` (with `finish_reason`, `index`, `message`, `logprobs`), `created`, `model`
- [x] **CHAT-02**: All `CreateChatCompletionRequest` parameters are forwarded to upstream provider: `temperature`, `top_p`, `n`, `stop`, `max_completion_tokens`, `presence_penalty`, `frequency_penalty`, `logprobs`, `top_logprobs`, `response_format`, `seed`, `tools`, `tool_choice`, `parallel_tool_calls`, `user`, `reasoning_effort`
- [x] **CHAT-03**: Non-streaming responses include `usage` object with `prompt_tokens`, `completion_tokens`, `total_tokens` (sourced from upstream provider response)
- [x] **CHAT-04**: `stream=true` returns SSE format (`text/event-stream`, `data: {json}\n\n` lines, `data: [DONE]\n\n` terminator) with `CreateChatCompletionStreamResponse` chunks using `choices[].delta` instead of `choices[].message`
- [x] **CHAT-05**: `stream_options: { include_usage: true }` emits a final chunk with `usage` object and empty `choices: []` before `[DONE]`; intermediate chunks have `usage: null`

### Surface Expansion

- [x] **SURF-01**: `POST /v1/embeddings` endpoint routes to OpenRouter embedding models and returns `CreateEmbeddingResponse` with `object: "list"`, `data[].embedding`, `data[].index`, `model`, `usage`
- [x] **SURF-02**: `POST /v1/images/generations` response is schema-compliant with `created` (int) and `data` array of Image objects (with `url` or `b64_json`, optional `revised_prompt`)
- [x] **SURF-03**: `POST /v1/responses` endpoint is audited and hardened against the full OpenAI `Response` and `CreateResponse` schemas

### Differentiators

- [x] **DIFF-01**: All `/v1/*` endpoints include `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits` response headers
- [x] **DIFF-02**: Usage object or response headers include actual credit cost for the request
- [x] **DIFF-03**: Model aliasing — accept standard OpenAI model names (e.g., `gpt-4o`, `gpt-4o-mini`) and route to the best available provider
- [x] **DIFF-04**: All `/v1/*` responses include `x-request-id` header for debugging and support

### Operational

- [ ] **OPS-01**: Stub endpoints registered for known-but-unsupported OpenAI APIs (`/v1/audio/*`, `/v1/files`, `/v1/uploads`, `/v1/batches`, `/v1/completions`, `/v1/fine_tuning/*`, `/v1/moderations`) returning 404 with proper OpenAI error format and a "coming soon" message
- [ ] **OPS-02**: GitHub issues created for each deferred endpoint following the feature issue template with acceptance criteria, so they are tracked for future milestones

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Audio

- **AUD-01**: `POST /v1/audio/speech` — text-to-speech via supported providers
- **AUD-02**: `POST /v1/audio/transcriptions` — speech-to-text
- **AUD-03**: `POST /v1/audio/translations` — audio translation

### Files & Storage

- **FILE-01**: `POST /v1/files` — file upload for context/ingestion
- **FILE-02**: `GET /v1/files` — list uploaded files
- **FILE-03**: `DELETE /v1/files/{file_id}` — delete files

### Batch Processing

- **BATCH-01**: `POST /v1/batches` — async batch inference jobs

### Content Safety

- **MOD-01**: `POST /v1/moderations` — content moderation endpoint

### Enhanced Model Metadata

- **META-01**: Model objects include capability tags, pricing, context window size as non-standard fields
- **META-02**: Organization/Project headers (`OpenAI-Organization`, `OpenAI-Project`) accepted silently and logged

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| `/v1/fine_tuning/*` | Hive is a routing proxy, not a training platform |
| `/v1/assistants/*` | Deprecated by OpenAI, massive stateful surface |
| Realtime WebSocket API | Different protocol, requires persistent connections |
| `/v1/vector_stores` | No vector DB integration planned |
| `/organization/*` admin APIs | Hive has its own user/billing model |
| Evals, Containers, ChatKit | Platform-specific OpenAI features, not inference proxy scope |
| Legacy `/v1/completions` | Deprecated by OpenAI, chat completions is the replacement |
| Web pipeline OpenAI compliance | Deliberately proprietary to prevent abuse |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| FOUND-01 | Phase 1 | Complete |
| FOUND-02 | Phase 3 | Complete |
| FOUND-03 | Phase 4 | Complete |
| FOUND-04 | Phase 4 | Complete |
| FOUND-05 | Phase 3 | Complete |
| FOUND-06 | Phase 2 | Complete |
| FOUND-07 | Phase 2 | Complete |
| CHAT-01 | Phase 5 | Complete |
| CHAT-02 | Phase 5 | Complete |
| CHAT-03 | Phase 5 | Complete |
| CHAT-04 | Phase 6 | Complete |
| CHAT-05 | Phase 6 | Complete |
| SURF-01 | Phase 7 | Complete |
| SURF-02 | Phase 7 | Complete |
| SURF-03 | Phase 7 | Complete |
| DIFF-01 | Phase 8 | Complete |
| DIFF-02 | Phase 8 | Complete |
| DIFF-03 | Phase 8 | Complete |
| DIFF-04 | Phase 8 | Complete |
| OPS-01 | Phase 9 | Pending |
| OPS-02 | Phase 9 | Pending |

**Coverage:**
- v1 requirements: 21 total
- Mapped to phases: 21
- Unmapped: 0

---
*Requirements defined: 2026-03-17*
*Last updated: 2026-03-17 after roadmap creation*
