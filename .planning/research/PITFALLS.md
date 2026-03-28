# Domain Pitfalls: OpenAI API Compliance

**Domain:** OpenAI-compatible API proxy (Hive)
**Researched:** 2026-03-16
**Confidence:** HIGH (based on OpenAI OpenAPI spec v2.3.0 at `docs/reference/openai-openapi.yml` + codebase analysis with line references)

## Critical Pitfalls

Mistakes that break the official `openai` Python/Node/Go SDKs entirely.

### Pitfall 1: Error Response Shape Mismatch

**What goes wrong:** Hive returns `{ error: "string message" }` but OpenAI SDKs expect a nested object: `{ error: { message, type, param, code } }` where all four fields are required. The Python SDK (`openai >= 1.0`) parses `response["error"]["message"]` and `response["error"]["type"]` -- a flat string causes `TypeError` / `KeyError` crashes in client code.

**Current Hive state:** Every error path returns flat strings:
- `reply.code(401).send({ error: "missing or invalid credentials" })` (auth.ts:115)
- `reply.code(429).send({ error: "rate limit exceeded" })` (chat-completions.ts:19)
- `return { error: "unknown model", statusCode: 400 }` (services.ts:616)
- `return { error: "insufficient credits", statusCode: 402 }` (services.ts:622)

**OpenAI spec requires (schema `Error`, spec line 41894):**
```json
{
  "error": {
    "message": "Invalid API key provided",
    "type": "invalid_request_error",
    "param": null,
    "code": "invalid_api_key"
  }
}
```

**Consequences:** Every error from Hive crashes the official SDKs. Users see unhandled exceptions instead of meaningful error messages. This is the single most visible compatibility failure.

**Prevention:** Create a centralized `openaiError(status, message, type, code, param)` helper used by all routes. Map Hive's internal error strings to OpenAI error types:
- 400 -> `type: "invalid_request_error"`
- 401 -> `type: "invalid_request_error"`, `code: "invalid_api_key"`
- 402 -> `type: "insufficient_quota"`, `code: "insufficient_quota"`
- 429 -> `type: "rate_limit_exceeded"`, `code: "rate_limit_exceeded"`
- 502 -> `type: "server_error"`, `code: "upstream_error"`

**Detection:** Test with `openai` Python package -- any error response will throw generic exceptions if malformed instead of typed `AuthenticationError`, `RateLimitError`, etc.

**Phase:** Must be addressed in the first phase (error format standardization). Every other compliance feature is less visible than broken errors.

---

### Pitfall 2: Missing `usage` Object in Non-Streaming Responses

**What goes wrong:** The `openai` Python SDK accesses `response.usage.prompt_tokens` after every chat completion call. Many users rely on this for cost tracking. If `usage` is `null` or missing, code crashes with `AttributeError`.

**Current Hive state:** The `chatCompletions` response (services.ts:664-688) returns `choices`, `id`, `object`, `created`, `model` but **no `usage` field at all**. The provider client (openai-compatible-client.ts:92-98) does parse upstream `usage` from OpenRouter, but this data is discarded -- it is stored in `providerResult.usage` but never included in the response body sent to the client.

**OpenAI spec requires (`CompletionUsage`, spec line 36030):**
```json
{
  "usage": {
    "prompt_tokens": 9,
    "completion_tokens": 12,
    "total_tokens": 21,
    "completion_tokens_details": {
      "reasoning_tokens": 0,
      "accepted_prediction_tokens": 0,
      "rejected_prediction_tokens": 0
    },
    "prompt_tokens_details": {
      "cached_tokens": 0
    }
  }
}
```

**Consequences:** SDK users cannot track token usage. Libraries like LangChain and LlamaIndex that auto-track costs will fail or report zero usage.

**Prevention:** Thread `providerResult.usage` through to the response body. For the nested `*_details` objects, return them with zero defaults if not available from OpenRouter. Never let `usage` be absent -- use `{ prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 }` as fallback when upstream does not provide usage (especially free-tier models).

**Detection:** `assert response.usage is not None` in integration tests.

**Phase:** First phase, alongside error format. This is data the provider already returns -- just needs to be passed through.

---

### Pitfall 3: No Streaming Support on `/v1/chat/completions`

**What goes wrong:** When a client sends `stream: true`, Hive has no streaming codepath. The request parameter is entirely absent from the codebase. The `openai` SDK's streaming client expects SSE (`text/event-stream`) with `data: {chunk}\n\n` framing terminated by `data: [DONE]\n\n`. Receiving a JSON blob instead causes parsing failures.

**Current Hive state:** Zero references to "stream" in `chat-completions.ts` or `services.ts`. The `OpenAICompatibleProviderClient.chat()` always calls `response.json()` (openai-compatible-client.ts:83), never handles streaming responses. The `ChatBody` type does not include a `stream` field.

**OpenAI streaming spec requires:**
1. Response `Content-Type: text/event-stream`
2. Each chunk: `data: {"id":"...","object":"chat.completion.chunk","choices":[{"delta":{"content":"..."}}]}\n\n`
3. The `object` field must be `"chat.completion.chunk"` (not `"chat.completion"`)
4. Final data line: `data: [DONE]\n\n`
5. If `stream_options.include_usage` is true, a penultimate chunk with `usage` object and empty `choices: []`

**Consequences:** Any SDK user calling `client.chat.completions.create(stream=True)` gets a broken response. Streaming is table-stakes for chat applications -- this blocks most real-world usage.

**Prevention:**
1. Parse `stream` boolean from request body
2. If true: set `Content-Type: text/event-stream`, forward upstream SSE chunks from OpenRouter (which natively supports streaming)
3. Ensure `data: [DONE]` terminator is always sent, even on upstream error
4. Handle `stream_options.include_usage` to emit usage in final data chunk before `[DONE]`
5. Use distinct response shape for chunks: `object: "chat.completion.chunk"`, `delta` not `message`

**Detection:** `for chunk in client.chat.completions.create(stream=True): print(chunk)` -- must produce chunks.

**Phase:** Should be its own dedicated phase (phase 2) due to complexity. Requires changes to provider client, route handler, and response pipeline.

---

### Pitfall 4: `/v1/models` Response Missing Required Fields

**What goes wrong:** OpenAI SDKs validate the model object shape. The spec requires `id`, `object`, `created`, and `owned_by` (all four required, spec line 47880). Hive's `/v1/models` returns `id`, `object`, `capability`, `costType` -- missing `created` and `owned_by`, and including non-standard fields.

**Current Hive state (models.ts:8-13):**
```json
{ "id": "smart-reasoning", "object": "model", "capability": "chat", "costType": "variable" }
```

**OpenAI expects (spec line 47861-47891):**
```json
{ "id": "gpt-4o", "object": "model", "created": 1686935002, "owned_by": "openai" }
```

**Consequences:** The Go SDK (`openai-go`) uses strict struct unmarshaling -- missing required fields cause errors. The Python SDK is more lenient but tools built on it may fail. Non-standard fields (`capability`, `costType`) are harmless but signal to SDK users that this is not a compliant API.

**Prevention:** Add `created` (use a fixed epoch or model registration timestamp) and `owned_by` (use `"hive"` or the upstream provider name). Keep `capability` and `costType` as extra fields -- they do not break SDKs and provide value.

**Detection:** `client.models.list()` in all three official SDKs should succeed without errors.

**Phase:** First phase -- simple field additions.

## Moderate Pitfalls

### Pitfall 5: Chat Completion ID Format Wrong

**What goes wrong:** Hive generates IDs like `chatcmpl_a1b2c3d4e5f6` (underscore separator, 12-char suffix). OpenAI uses `chatcmpl-a1b2c3d4e5f6...` (hyphen separator, longer random portion). Some downstream tools (monitoring, logging, observability) parse the prefix with `id.startswith("chatcmpl-")` checks.

**Current Hive state:** `chatcmpl_${randomUUID().slice(0, 12)}` (services.ts:667)

**Prevention:** Change to `chatcmpl-${randomUUID()}` (full UUID, hyphen separator). Apply same pattern to other ID prefixes (`resp-`, `embd-`).

**Detection:** Check `response.id.startswith("chatcmpl-")` in tests.

**Phase:** First phase -- trivial string change.

---

### Pitfall 6: Rate Limit Headers Not Following OpenAI Convention

**What goes wrong:** OpenAI returns specific rate-limit headers: `x-ratelimit-limit-requests`, `x-ratelimit-remaining-requests`, `x-ratelimit-reset-requests`, and token equivalents. The `openai` Python SDK reads these to implement automatic retry-after backoff. Without them, the SDK's built-in retry logic cannot determine when to retry after a 429.

**Current Hive state:** Returns bare `{ error: "rate limit exceeded" }` with no rate-limit headers (chat-completions.ts:19). The in-memory rate limiter tracks per-user request counts but does not expose remaining quota.

**Prevention:** On 429 responses, include at minimum:
- `x-ratelimit-limit-requests: N`
- `x-ratelimit-remaining-requests: 0`
- `x-ratelimit-reset-requests: <seconds>`
- `retry-after: <seconds>`

**Detection:** Trigger rate limiting and check response headers in test.

**Phase:** Phase 1 or 2 -- depends on when rate limiting is hardened.

---

### Pitfall 7: OpenRouter Upstream Usage Inconsistency

**What goes wrong:** OpenRouter may return usage in a slightly different shape than OpenAI, or may omit it entirely for some models (especially free-tier models). If Hive forwards `usage` without normalization, some responses will have usage and others will not, creating inconsistent behavior for SDK users.

**Current Hive state:** The `OpenAICompatibleProviderClient` (openai-compatible-client.ts:92-98) handles missing usage as `undefined`, which propagates as a missing field in the client response.

**Prevention:** Always return a `usage` object, even if all values are zero. The OpenAI spec defines `default: 0` for all token fields (spec line 36036). Never let `usage` be `undefined`.

**Detection:** Test with free-tier models that may not return usage from OpenRouter.

**Phase:** First phase, alongside Pitfall 2 fix.

---

### Pitfall 8: Model ID Mapping Transparency

**What goes wrong:** Hive uses virtual model IDs (`smart-reasoning`, `guest-free`, `image-basic`) that do not match any real upstream model. The `model` field in responses also uses these virtual IDs (services.ts:670). SDK users who expect to see real model names (e.g., `gpt-4o`, `claude-3.5-sonnet`) in the response will be confused. If a user passes a real upstream model ID (e.g., `openai/gpt-4o`), Hive returns 400 because `findById` only matches virtual IDs.

**Prevention:**
1. Decide on a model ID strategy: either support upstream IDs directly, or maintain a clear mapping
2. The response `model` field should indicate what actually ran -- consider returning `providerResult.providerModel` (the upstream model) instead of the virtual ID
3. Document the mapping in `/v1/models` metadata

**Detection:** Try `client.chat.completions.create(model="gpt-4o")` -- should either work or return a clear error about model aliases.

**Phase:** Dedicated model catalog phase (listed as an active requirement in PROJECT.md).

---

### Pitfall 9: Auth Resolution Order Causes Silent Failures

**What goes wrong:** The auth flow (auth.ts:63-105) tries Supabase session token first, then falls back to API key lookup. For `/v1/*` public API routes, a valid Hive API key is first evaluated as a Supabase JWT (which fails silently), then re-evaluated as an API key. This adds latency and could cause confusing failures if Supabase is temporarily down.

**Prevention:** Use route-aware auth strategy. For `/v1/*` routes, try API key resolution first (cheaper, no external call needed). For web routes, try session auth first.

**Detection:** Measure auth latency for API key requests -- if it includes a Supabase round-trip, the ordering is wrong.

**Phase:** First phase -- auth optimization for public API routes.

## Minor Pitfalls

### Pitfall 10: Missing `system_fingerprint` Field

**What goes wrong:** OpenAI responses include `system_fingerprint` (e.g., `"fp_44709d6fcb"`). While not strictly required for SDK function, some observability tools and caching layers key on this value.

**Prevention:** Return `system_fingerprint: null` explicitly in the response body.

**Phase:** First phase -- trivial addition.

---

### Pitfall 11: `finish_reason` Hardcoded as `"stop"`

**What goes wrong:** OpenAI defines specific `finish_reason` values: `"stop"`, `"length"`, `"content_filter"`, `"tool_calls"`. Hive hardcodes `"stop"` (services.ts:674). When upstream providers return `"length"` (max tokens hit) or `"content_filter"`, Hive masks this, hiding important information.

**Prevention:** Pass through `finish_reason` from the upstream provider response. Add it to `ProviderChatResponse` type.

**Phase:** First phase -- requires threading the field through from provider response.

---

### Pitfall 12: Fastify Built-in Errors Not Wrapped

**What goes wrong:** Fastify generates its own 400 errors for malformed JSON, missing content-type, and payload too large. These use Fastify's format (`{ statusCode, error, message }`), not OpenAI's. SDK users sending malformed requests get non-OpenAI error shapes.

**Prevention:** Add a Fastify `setErrorHandler` that catches all errors (including Fastify-generated ones) and wraps them in OpenAI error format. Also handle `setNotFoundHandler` to return OpenAI-shaped 404s.

**Detection:** Send malformed JSON to `/v1/chat/completions` and verify the error matches OpenAI format.

**Phase:** First phase -- part of error standardization.

## Phase-Specific Warnings

| Phase Topic | Likely Pitfall | Mitigation |
|-------------|---------------|------------|
| Error standardization | Forgetting Fastify's built-in 404/400/413 errors | Add `setErrorHandler` + `setNotFoundHandler` that wrap in OpenAI format |
| Error standardization | Not mapping 402 (Hive-specific) to a sensible OpenAI type | Use `insufficient_quota` type -- matches what OpenAI returns for exhausted billing |
| Streaming | SSE line ending differences (`\n\n` vs `\r\n\r\n`) | Use `\n\n` consistently (matches OpenAI) and test with raw HTTP client |
| Streaming | Forgetting `data: [DONE]` terminator | Stream hangs forever on client side. Always emit `[DONE]` even on error |
| Streaming | Upstream OpenRouter disconnects mid-stream | Emit `[DONE]` on upstream failure, never leave the stream open |
| Streaming | Reusing non-streaming response builder for chunks | Chunks need `"chat.completion.chunk"` object type and `delta` not `message` |
| Streaming | Backpressure from slow clients | Use Node.js stream piping with Fastify `reply.raw` + proper backpressure |
| Streaming | `stream_options.include_usage` sent by default | Only include usage chunk when client explicitly requests it via `stream_options` |
| Usage telemetry | OpenRouter free models return no usage | Always return `usage` with zero defaults, never `undefined` |
| Embeddings endpoint | Wrong `object` type on items | Must be `"embedding"` per item and `"list"` at top level per spec |
| Model catalog | Hardcoded model list becomes stale | Pull from OpenRouter `/models` endpoint with cache TTL |
| Auth | SDK sends `Authorization: Bearer` only | Hive already supports this (auth.ts:81) -- maintain this behavior, do not break it |

## Sources

- OpenAI OpenAPI spec v2.3.0: `docs/reference/openai-openapi.yml` (local, authoritative)
- Hive codebase analysis with line references: routes, services, provider clients (local)
- OpenAI Error schema: spec lines 41894-41913
- OpenAI CompletionUsage schema: spec lines 36030-36060
- OpenAI Model schema: spec lines 47861-47884
- OpenAI streaming chunk schema: spec lines 37371-37421
- OpenAI stream_options: spec lines 35590-35596
