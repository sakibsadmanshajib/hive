# Feature Landscape: OpenAI API Compliance

**Domain:** OpenAI-compatible inference proxy API
**Researched:** 2026-03-16
**Confidence:** HIGH (derived directly from OpenAI OpenAPI spec v2.3.0 at `docs/reference/openai-openapi.yml`)

## Table Stakes

Features the OpenAI SDK exercises by default. Missing any of these causes SDK errors, type mismatches, or silent data loss. These are non-negotiable for drop-in compatibility.

### TS-1: Chat Completions Response Schema Compliance

| Aspect | Detail |
|--------|--------|
| **Why Expected** | `client.chat.completions.create()` validates the response shape. SDKs parse `id`, `object`, `choices`, `created`, `model`, `usage` fields by name. Missing required fields cause runtime errors in typed SDKs (Python pydantic, Go struct, Java). |
| **Complexity** | Medium |
| **SDK Method** | `client.chat.completions.create()` |
| **Current Gap** | Hive proxies the upstream body directly. Must ensure all required fields (`id`, `object: "chat.completion"`, `choices[].finish_reason`, `choices[].index`, `choices[].message`, `choices[].logprobs`, `created`, `model`) are present even when upstream omits them. |
| **Spec Required Fields** | `id`, `choices`, `created`, `model`, `object` (all required). `usage` is optional but expected. `service_tier`, `system_fingerprint` are optional. |

### TS-2: Chat Completions Request Parameter Pass-Through

| Aspect | Detail |
|--------|--------|
| **Why Expected** | SDKs send all parameters from `CreateChatCompletionRequest`. Rejecting or ignoring known params causes silent behavior differences. |
| **Complexity** | Medium |
| **SDK Method** | `client.chat.completions.create(model=, messages=, temperature=, ...)` |
| **Key Parameters** | `messages` (required), `model` (required), `stream`, `stream_options`, `temperature`, `top_p`, `n`, `stop`, `max_completion_tokens`, `presence_penalty`, `frequency_penalty`, `logprobs`, `top_logprobs`, `response_format`, `seed`, `tools`, `tool_choice`, `parallel_tool_calls`, `user`, `reasoning_effort` |
| **Current Gap** | Route only extracts `model` and `messages`. All other parameters must be forwarded to upstream provider. |

### TS-3: Streaming SSE Format

| Aspect | Detail |
|--------|--------|
| **Why Expected** | `client.chat.completions.create(stream=True)` expects `text/event-stream` with `data: {json}\n\n` lines terminated by `data: [DONE]\n\n`. Each chunk is `CreateChatCompletionStreamResponse` with `choices[].delta` instead of `choices[].message`. |
| **Complexity** | Medium |
| **SDK Method** | `client.chat.completions.create(stream=True)`, iteration via `for chunk in stream:` |
| **Chunk Required Fields** | `id`, `choices` (with `delta`, `finish_reason`, `index`), `created`, `model`, `object: "chat.completion.chunk"` |
| **Current Gap** | Must verify SSE format matches exactly, including null `finish_reason` on intermediate chunks and proper `finish_reason` on final content chunk. |

### TS-4: Streaming Usage Telemetry

| Aspect | Detail |
|--------|--------|
| **Why Expected** | When `stream_options: {"include_usage": true}` is sent, the SDK expects a final chunk with `usage` object and empty `choices: []` before `data: [DONE]`. All intermediate chunks must have `usage: null`. Without this, `response.usage` is `None` in streaming mode. |
| **Complexity** | Medium |
| **SDK Method** | `client.chat.completions.create(stream=True, stream_options={"include_usage": True})` |
| **Spec** | `ChatCompletionStreamOptions.include_usage` triggers the behavior. |
| **Current Gap** | Listed as active requirement. Must emit `CompletionUsage` with `prompt_tokens`, `completion_tokens`, `total_tokens`, plus `prompt_tokens_details` and `completion_tokens_details` sub-objects. |

### TS-5: CompletionUsage Object Shape

| Aspect | Detail |
|--------|--------|
| **Why Expected** | SDKs access `response.usage.prompt_tokens`, `.completion_tokens`, `.total_tokens`. Typed SDKs also access `.prompt_tokens_details.cached_tokens` and `.completion_tokens_details.reasoning_tokens`. |
| **Complexity** | Low |
| **SDK Method** | `response.usage` on both streaming and non-streaming responses |
| **Required Fields** | `prompt_tokens` (int), `completion_tokens` (int), `total_tokens` (int) |
| **Optional Sub-objects** | `prompt_tokens_details: {cached_tokens, audio_tokens}`, `completion_tokens_details: {reasoning_tokens, audio_tokens, accepted_prediction_tokens, rejected_prediction_tokens}` |

### TS-6: Error Response Format

| Aspect | Detail |
|--------|--------|
| **Why Expected** | OpenAI SDKs catch and parse `ErrorResponse` with shape `{"error": {"message": str, "type": str, "param": str|null, "code": str|null}}`. SDKs raise typed exceptions (`AuthenticationError`, `RateLimitError`, `BadRequestError`) based on HTTP status AND the `type`/`code` fields. Wrong shape = generic `APIError` with no useful info. |
| **Complexity** | Low |
| **SDK Method** | All methods (error handling path) |
| **Current Gap** | Hive currently sends `{ error: "rate limit exceeded" }` (string, not object). Must wrap in `{ error: { message, type, param, code } }` format. |
| **Status Code Mapping** | 400 = `invalid_request_error`, 401 = `authentication_error` (not `invalid_api_key`), 403 = `permission_error`, 429 = `rate_limit_error`, 500 = `server_error`, 503 = `service_unavailable` |

### TS-7: Bearer Token Authentication

| Aspect | Detail |
|--------|--------|
| **Why Expected** | `OpenAI(api_key="sk-...")` sends `Authorization: Bearer sk-...`. This is the only auth mechanism OpenAI SDKs use. The spec defines `ApiKeyAuth` as `type: http, scheme: bearer`. |
| **Complexity** | Low |
| **SDK Method** | Client constructor: `OpenAI(api_key=..., base_url=...)` |
| **Current State** | Already supports Bearer token. Verify no edge cases with `x-api-key` header fallback breaking SDK expectations. |

### TS-8: Models List Endpoint Schema

| Aspect | Detail |
|--------|--------|
| **Why Expected** | `client.models.list()` expects `{"object": "list", "data": [{"id", "object": "model", "created", "owned_by"}]}`. Each model object requires all four fields. |
| **Complexity** | Low |
| **SDK Method** | `client.models.list()`, `client.models.retrieve(model_id)` |
| **Current Gap** | Hive returns `capability` and `costType` (non-standard fields) but omits `created` and `owned_by` (required by spec). Non-standard fields are fine (SDKs ignore them), but missing required fields break typed SDKs. |

### TS-9: Models Retrieve Endpoint

| Aspect | Detail |
|--------|--------|
| **Why Expected** | `client.models.retrieve("model-id")` calls `GET /v1/models/{model}` and expects a single `Model` object. |
| **Complexity** | Low |
| **SDK Method** | `client.models.retrieve("gpt-4o")` |
| **Current Gap** | Endpoint not implemented. Must return `{"id", "object": "model", "created", "owned_by"}` or 404 with proper error format. |

### TS-10: Images Generations Response Schema

| Aspect | Detail |
|--------|--------|
| **Why Expected** | `client.images.generate()` expects `ImagesResponse` with `created` (int) and `data` (array of Image objects). Each Image has `url` or `b64_json`, plus optional `revised_prompt`. |
| **Complexity** | Low |
| **SDK Method** | `client.images.generate(model=, prompt=, ...)` |
| **Current Gap** | Verify response includes all required fields. New spec adds `background`, `output_format`, `size`, `quality` to response (optional but useful). |

### TS-11: Responses API Schema Compliance

| Aspect | Detail |
|--------|--------|
| **Why Expected** | `client.responses.create()` is the newer OpenAI API surface. SDKs exercise `POST /v1/responses` and expect the `Response` schema back. |
| **Complexity** | High (large schema surface) |
| **SDK Method** | `client.responses.create(model=, input=, ...)` |
| **Current Gap** | Route exists but schema compliance needs verification against the full `Response` and `CreateResponse` schemas in the spec. |

### TS-12: Content-Type Headers

| Aspect | Detail |
|--------|--------|
| **Why Expected** | SDKs validate `Content-Type: application/json` for non-streaming and `Content-Type: text/event-stream` for streaming. Wrong content type causes parse failures. |
| **Complexity** | Low |
| **SDK Method** | All methods (response parsing) |

## Differentiators

Features that set Hive apart from vanilla OpenAI. Not expected by the SDK, but provide competitive value for developers choosing a proxy.

### D-1: Provider Routing Metadata Headers

| Aspect | Detail |
|--------|--------|
| **Value Proposition** | Developers see which provider and model actually served their request. Unique to multi-provider proxies. Enables debugging and cost optimization. |
| **Complexity** | Low (already partially implemented) |
| **Headers** | `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits` |
| **Notes** | Already in chat-completions route. Extend to all endpoints. SDKs expose raw headers via `response.headers`. |

### D-2: Cost Transparency in Usage

| Aspect | Detail |
|--------|--------|
| **Value Proposition** | Return actual credit cost alongside token counts. No other proxy does per-request cost in the response body. |
| **Complexity** | Low |
| **Implementation** | Add `x_hive_cost` or similar non-standard field to the `usage` object, or use response headers. Non-standard fields in JSON are safely ignored by SDKs. |

### D-3: Model Aliasing / Smart Routing

| Aspect | Detail |
|--------|--------|
| **Value Proposition** | Accept `gpt-4o` and route to the cheapest capable provider. Users don't need to know Hive-specific model IDs. |
| **Complexity** | Medium |
| **Notes** | Requires a model alias registry mapping OpenAI model names to available providers. |

### D-4: Embeddings Endpoint

| Aspect | Detail |
|--------|--------|
| **Value Proposition** | Expands use cases beyond chat. Developers building RAG pipelines need embeddings from the same API. |
| **Complexity** | Medium |
| **SDK Method** | `client.embeddings.create(model=, input=)` |
| **Spec** | `POST /v1/embeddings` with `CreateEmbeddingRequest` -> `CreateEmbeddingResponse`. Response: `{"object": "list", "data": [{"object": "embedding", "embedding": [...], "index": 0}], "model": "...", "usage": {"prompt_tokens", "total_tokens"}}` |
| **Provider** | Route to OpenRouter embedding models. |

### D-5: Enhanced Model Metadata

| Aspect | Detail |
|--------|--------|
| **Value Proposition** | Return capability tags, pricing, context window size alongside standard model fields. Helps developers pick models programmatically. |
| **Complexity** | Low |
| **Notes** | Add non-standard fields to model objects. SDKs ignore unknown fields, so this is safe. Keep `id`, `object`, `created`, `owned_by` as required base. |

### D-6: Request ID Tracking

| Aspect | Detail |
|--------|--------|
| **Value Proposition** | Return `x-request-id` header on every response for debugging and support. OpenAI does this; proxies often don't. |
| **Complexity** | Low |
| **SDK Method** | `response.headers["x-request-id"]` (SDKs expose this) |

### D-7: Organization/Project Headers

| Aspect | Detail |
|--------|--------|
| **Value Proposition** | Accept and log `OpenAI-Organization` and `OpenAI-Project` headers. Some enterprise users send these. Silently accepting (not rejecting) improves compatibility. |
| **Complexity** | Low |
| **Notes** | Don't need to implement org/project logic -- just don't reject these headers. |

## Anti-Features

Features to explicitly NOT build. These are in the OpenAI spec but outside Hive's scope, add massive complexity, or have no upstream provider support.

### AF-1: Assistants API (`/v1/assistants/*`)

| Why Avoid | Deprecated by OpenAI themselves. Massive stateful API surface (threads, messages, runs, steps, file attachments). Would require a full execution engine. |
| What to Do Instead | Return 404 with proper error format: `{"error": {"message": "Assistants API is not supported", "type": "invalid_request_error", "param": null, "code": "unsupported_endpoint"}}` |
| Spec Status | Marked `deprecated: true` in spec |

### AF-2: Fine-Tuning API (`/v1/fine_tuning/*`)

| Why Avoid | Hive is a routing proxy, not a training platform. No upstream provider exposes fine-tuning through a unified API. |
| What to Do Instead | Return proper 404 error. |

### AF-3: Realtime WebSocket API

| Why Avoid | Entirely different protocol (WebSocket, not HTTP). Requires persistent connections, audio streaming, session management. Massive infrastructure investment. |
| What to Do Instead | Defer indefinitely. Not compatible with proxy architecture. |

### AF-4: Audio API (`/v1/audio/*`)

| Why Avoid | Speech synthesis, transcription, translation require specialized providers. No unified routing layer exists. |
| What to Do Instead | Defer until OpenRouter or similar provides audio model routing. |

### AF-5: Files and Uploads API (`/v1/files`, `/v1/uploads`)

| Why Avoid | Requires object storage, multipart upload handling, file lifecycle management. Only useful for fine-tuning and assistants (both out of scope). |
| What to Do Instead | Defer until file ingestion feature is planned. |

### AF-6: Batch API (`/v1/batches`)

| Why Avoid | Requires job queue, async processing, result storage. Significant infrastructure. Low demand signal. |
| What to Do Instead | Defer until demand is validated. |

### AF-7: Legacy Completions (`/v1/completions`)

| Why Avoid | Deprecated by OpenAI. No modern SDK uses this by default. Chat completions is the replacement. |
| What to Do Instead | Return 404 with deprecation message in error. |

### AF-8: Evals, Containers, Vector Stores, Moderations

| Why Avoid | Platform-specific OpenAI features. Not part of the inference proxy value proposition. |
| What to Do Instead | Return 404. |

### AF-9: Organization/Admin APIs (`/organization/*`)

| Why Avoid | Hive has its own user/billing model. Emulating OpenAI's org structure adds no value. |
| What to Do Instead | Not even worth returning 404. Don't register routes. |

## Feature Dependencies

```
TS-6 (Error Format)       --> ALL other features (errors are universal)
TS-7 (Bearer Auth)        --> ALL other features (auth gates everything)
TS-1 (Response Schema)    --> TS-3 (Streaming, same shape minus message/delta swap)
TS-3 (Streaming SSE)      --> TS-4 (Streaming Usage, extends streaming)
TS-5 (Usage Object)       --> TS-4 (Streaming Usage, same shape)
TS-8 (Models List)        --> TS-9 (Models Retrieve, same data source)
TS-8 (Models List)        --> D-5 (Enhanced Metadata, extends model objects)
TS-2 (Request Params)     --> D-3 (Model Aliasing, intercepts model param)
D-4 (Embeddings)          --> TS-6, TS-7, TS-5 (needs error format, auth, usage)
TS-1 (Response Schema)    --> D-1 (Routing Headers, extends response)
TS-1 (Response Schema)    --> D-2 (Cost Transparency, extends usage)
```

## MVP Recommendation

### Phase 1: Foundation (Error + Auth + Schema fixes)

Prioritize these first -- they unblock everything else and fix the most SDK-breaking issues:

1. **TS-6: Error Response Format** -- Currently broken. Every error path is wrong. Fix first.
2. **TS-7: Bearer Auth verification** -- Already works, just audit edge cases.
3. **TS-8: Models List Schema** -- Add `created`, `owned_by` fields. Quick fix.
4. **TS-9: Models Retrieve** -- New route, simple implementation.
5. **TS-12: Content-Type Headers** -- Verify correct on all endpoints.

### Phase 2: Chat Completions Compliance

6. **TS-1: Response Schema** -- Ensure all required fields present, normalize upstream responses.
7. **TS-2: Request Parameter Pass-Through** -- Forward all known params to upstream.
8. **TS-5: Usage Object Shape** -- Normalize upstream usage into spec format.
9. **TS-3: Streaming SSE Format** -- Verify chunk format, finish_reason, [DONE] terminator.
10. **TS-4: Streaming Usage Telemetry** -- `stream_options.include_usage` support.

### Phase 3: Expand Surface

11. **D-4: Embeddings Endpoint** -- New endpoint, standard schema.
12. **TS-10: Images Schema** -- Verify/fix existing endpoint.
13. **TS-11: Responses API** -- Audit against full spec.

### Phase 4: Differentiators

14. **D-1: Routing Headers** -- Already partially done, standardize across endpoints.
15. **D-2: Cost Transparency** -- Add credit cost to responses.
16. **D-3: Model Aliasing** -- Accept OpenAI model names, route intelligently.
17. **D-5: Enhanced Model Metadata** -- Add capability/pricing to model objects.
18. **D-6: Request ID Tracking** -- `x-request-id` on all responses.

**Defer:** All anti-features. Return proper 404 errors for known-but-unsupported endpoints.

## Sources

- OpenAI OpenAPI Spec v2.3.0: `docs/reference/openai-openapi.yml` (local, authoritative)
- Hive project context: `.planning/PROJECT.md`
- Hive existing routes: `apps/api/src/routes/*.ts`
- Confidence: HIGH -- all findings derived from the canonical OpenAI spec
