# Phase 7: Surface Expansion - Context

**Gathered:** 2026-03-18
**Status:** Ready for planning

<domain>
## Phase Boundary

Make `/v1/embeddings` (new), `/v1/images/generations` (stub → real), and `/v1/responses` (stub → real + schema-hardened) fully schema-compliant. All three must work with the official `openai` npm SDK without errors. No new endpoints beyond these three — differentiator headers and model aliasing are Phase 8.

</domain>

<decisions>
## Implementation Decisions

### SURF-01: Embeddings endpoint (new)

[auto] Selected all decisions — following Phase 5 route→service→provider pattern

- New `POST /v1/embeddings` route registered in `v1-plugin.ts` via `registerEmbeddingsRoute()`
- New `apps/api/src/schemas/embeddings.ts` TypeBox schema for `CreateEmbeddingRequest`: `model` (required string), `input` (required — `string | string[]`), `encoding_format` (optional `"float" | "base64"`, default `"float"`), `dimensions` (optional integer), `user` (optional string)
- New `AiService.embeddings()` method — follows same route→service→provider chain as `chatCompletions()`
- New `ProviderRegistry.embeddings()` dispatch method; new `OpenAICompatibleClient.embeddings()` method that POSTs to `/v1/embeddings` and returns parsed JSON
- Route to an OpenRouter-hosted embedding model; model selection uses existing model catalog (`capability: "embedding"` or similar); Claude's Discretion for exact default model ID
- Return full `CreateEmbeddingResponse`: `{ object: "list", data: [{ object: "embedding", embedding: number[], index: number }], model: string, usage: { prompt_tokens: number, total_tokens: number } }`
- Credit/usage tracking follows Phase 5 pattern: pre-charge based on model credits, record via `UsageService.add()` with endpoint `/v1/embeddings`
- Error handling: 400 unknown model, 402 insufficient credits, 502 provider failure — all via `sendApiError()`

### SURF-02: Images endpoint (stub → real + schema fix)

[auto] Selected: wire real OpenRouter call; fix response schema; forward all params

- Replace the fake `example.invalid` URL in `AiService.imageGeneration()` with a real HTTP call to OpenRouter's `/v1/images/generations` endpoint via `ProviderRegistry.imageGeneration()`
- Fix response shape: remove the non-compliant `object: "list"` field — OpenAI's `ImagesResponse` schema is `{ created: number, data: Image[] }` only
- `Image` objects in `data[]`: include `url` (when `response_format: "url"`, the default) or `b64_json` (when `response_format: "b64_json"`); include `revised_prompt` if returned by provider
- All existing schema params forwarded to upstream: `model`, `prompt`, `n`, `size`, `response_format`, `quality`, `style`, `user` — no silent dropping
- If provider doesn't support `b64_json`, return 400 with clear message via `sendApiError()`
- Keep `requireV1ApiPrincipal`, rate limiter, and credit/usage tracking pattern from existing route

### SURF-03: Responses endpoint (schema-harden + real provider call)

[auto] Selected: expand schema to full CreateResponse fields; translate to chat completion internally; return compliant Response object

- Expand `ResponsesBodySchema` in `apps/api/src/schemas/responses.ts` to cover key `CreateResponse` fields (with `additionalProperties: false`):
  - `model` (required string)
  - `input` (required — `string | Array<InputItem>` where InputItem covers text and message objects)
  - `instructions` (optional string — system prompt equivalent)
  - `temperature` (optional number)
  - `max_output_tokens` (optional integer)
  - `tools` (optional array — pass-through)
  - `tool_choice` (optional — pass-through)
  - `text` (optional — response_format equivalent, pass-through)
  - `user` (optional string)
- Update `AiService.responses()` to accept the full request body instead of just `input: string`
- Internal translation: map `input` → `messages` (user turn), `instructions` → system message, then route as a chat completion to OpenRouter via `ProviderRegistry.chat()`
- Return compliant `Response` object: `{ id: "resp_<uuid>", object: "response", created_at: <unix_int>, status: "completed", model: string, output: [{ type: "message", id: "msg_<uuid>", role: "assistant", status: "completed", content: [{ type: "output_text", text: string }] }], usage: { input_tokens, output_tokens, total_tokens } }`
- Use generated OpenAI types from `apps/api/src/types/openai.d.ts` for compile-time shape enforcement where types exist
- Credit/usage tracking: use `total_tokens` from the upstream chat completion response (same as Phase 5 CHAT-03 pattern)

### Claude's Discretion
- Exact embedding model ID to use as default (pick a well-supported OpenRouter embedding model, e.g., `openai/text-embedding-ada-002` or `nomic-ai/nomic-embed-text-v1.5`)
- Whether `ProviderRegistry.embeddings()` delegates to `OpenAICompatibleClient.embeddings()` or a specialized client
- TypeBox schema detail level for `Response.output[].content` and `CreateResponse.input` array items — balance completeness vs. over-engineering for v1
- Timeout and retry policy for embeddings (likely no retry, same as streaming)

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements
- `.planning/REQUIREMENTS.md` §SURF-01, §SURF-02, §SURF-03 — Acceptance criteria for all three endpoints

### OpenAI spec (schema authority)
- `docs/reference/openai-openapi.yml` — Full OpenAI spec; authoritative for `CreateEmbeddingRequest`, `CreateEmbeddingResponse`, `ImagesResponse`, `Image`, `CreateResponse`, `Response` schemas
- `apps/api/src/types/openai.d.ts` — Generated TypeScript types from the spec; use for compile-time enforcement

### Existing infrastructure to extend
- `apps/api/src/routes/v1-plugin.ts` — Register `registerEmbeddingsRoute()`; images and responses already registered
- `apps/api/src/routes/images-generations.ts` — Existing route (minor fixes only — response field)
- `apps/api/src/routes/responses.ts` — Existing route (update to pass full body to service)
- `apps/api/src/domain/ai-service.ts` — Add `embeddings()` method; fix `imageGeneration()` stub; fix `responses()` stub
- `apps/api/src/providers/registry.ts` — Add `embeddings()` dispatch; fix `imageGeneration()` to make real call
- `apps/api/src/providers/openai-compatible-client.ts` — Add `embeddings()` method
- `apps/api/src/providers/types.ts` — Add `ProviderEmbeddingsRequest` / `ProviderEmbeddingsResult` types
- `apps/api/src/schemas/embeddings.ts` — New file (TypeBox schema for embeddings request)
- `apps/api/src/schemas/responses.ts` — Expand existing schema to full CreateResponse fields
- `apps/api/src/schemas/images-generations.ts` — Existing schema (no changes needed)

### Prior phase patterns
- `.planning/phases/05-chat-completions-non-streaming/05-CONTEXT.md` — Route→service→provider pattern, credit/usage tracking, param forwarding
- `.planning/phases/06-chat-completions-streaming/06-CONTEXT.md` — `chatStream()` method pattern (reference for adding new provider methods)
- `.planning/phases/01-error-format/1-CONTEXT.md` — `sendApiError()` usage patterns

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `requireV1ApiPrincipal(request, reply, services, capability)`: Apply to all three routes — `"chat"` for embeddings and responses, `"image"` for images
- `inferUsageChannel(request, principal)`: Already used in images and responses routes — keep pattern
- `sendApiError(reply, status, message, opts)`: Use for all error responses (400 unknown model, 402 credits, 429 rate limit, 502 provider failure)
- `services.rateLimiter.allow(principal.userId)`: Already in both existing routes — keep
- `CreditService.consume()` + `UsageService.add()`: Credit/usage tracking — mirror Phase 5 pattern exactly
- `ProviderRegistry.imageGeneration()`: Already exists (line 200 in registry.ts) — fix to make real call instead of stub
- `OpenAICompatibleClient.chat()` / `chatStream()`: Existing methods in `openai-compatible-client.ts` — mirror structure for new `embeddings()` method

### Established Patterns
- Route → service: `services.ai.embeddings(principal.userId, request.body, usageContext)` → check `"error" in result`
- Error shape from service: `{ statusCode, error, code? }` → `sendApiError()` in route
- Success shape from service: `{ statusCode, body, headers }` → set headers, `reply.code().send(body)` in route
- Response headers from service result: `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits` — forward all in routes
- TypeBox schemas: `additionalProperties: false` on all request schemas (Phase 2 pattern)
- Model resolution: `services.models.findById(modelId)` or `services.models.pickDefault(capability)` — 400 on unknown model

### Integration Points
- `v1-plugin.ts`: Import and call `registerEmbeddingsRoute(app, services)` — no other changes
- `AiService.embeddings()`: New method — model resolution → credit pre-charge → `registry.embeddings()` → return `CreateEmbeddingResponse`
- `AiService.imageGeneration()`: Replace stub body — call `registry.imageGeneration()` with real params → return compliant response (no `object: "list"`)
- `AiService.responses()`: Change signature to accept full request body → translate to chat messages → call `registry.chat()` → map response to `Response` object
- `ProviderRegistry.embeddings()`: New method — dispatch to primary provider's `embeddings()` method (same dispatch pattern as `chat()`)
- `OpenAICompatibleClient.embeddings()`: New method — POST to `/v1/embeddings`, parse JSON response body (not streaming)

</code_context>

<specifics>
## Specific Ideas

- The `openai` SDK's `client.embeddings.create()` and `client.images.generate()` must work end-to-end — these are the acceptance test boundaries per SURF-01 and SURF-04 in ROADMAP.md success criteria
- Images route already has the correct TypeBox schema and route structure — the main work is replacing `example.invalid` with a real provider call and removing the extra `object: "list"` field from the response body
- Responses endpoint is intentionally translated to a chat completion under the hood — OpenRouter doesn't support `/v1/responses` natively. The translation is: `input` (string) → `[{ role: "user", content: input }]`, `instructions` → `[{ role: "system", content: instructions }]`
- The current `responses()` service method has no `headers` in its success return — fix this to include `x-model-routed` etc. (the route already has code to set them)

</specifics>

<deferred>
## Deferred Ideas

- None — discussion stayed within phase scope
- `x-request-id` response header — Phase 8 (DIFF-04)
- Model aliasing (accepting `gpt-4o` → routing to best provider) — Phase 8 (DIFF-03)
- Full streaming support for `/v1/responses` — out of scope for this phase; Phase 7 is non-streaming only

</deferred>

---

*Phase: 07-surface-expansion*
*Context gathered: 2026-03-18*
