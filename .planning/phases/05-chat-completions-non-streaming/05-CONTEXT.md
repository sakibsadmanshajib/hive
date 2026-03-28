# Phase 5: Chat Completions (Non-Streaming) - Context

**Gathered:** 2026-03-18
**Status:** Ready for planning

<domain>
## Phase Boundary

Wire the existing `/v1/chat/completions` route to a real upstream provider (OpenRouter), ensure all CHAT-01/02/03 required fields are in every non-streaming response, and forward all CHAT-02 parameters from the request body to upstream. Streaming (`stream: true`) is explicitly Phase 6 — this phase enforces `stream: false` or returns a not-supported error.

</domain>

<decisions>
## Implementation Decisions

### Parameter forwarding (CHAT-02)
- Extend `AiService.chatCompletions()` to accept a full params object containing all CHAT-02 fields: `temperature`, `top_p`, `n`, `stop`, `max_completion_tokens`, `presence_penalty`, `frequency_penalty`, `logprobs`, `top_logprobs`, `response_format`, `seed`, `tools`, `tool_choice`, `parallel_tool_calls`, `user`, `reasoning_effort`
- Route passes the entire validated request body (all fields) through to ai-service — no field is silently dropped
- ai-service forwards the params object to OpenRouter's `/v1/chat/completions` as-is (pass-through, not re-mapped)

### Upstream provider integration
- `AiService` makes a real HTTP call to OpenRouter's `/v1/chat/completions` endpoint using `fetch` (Node 18+ built-in)
- `stream: false` is hardcoded/enforced — if the client sends `stream: true`, return a 400 error indicating streaming is not yet supported (Phase 6)
- OpenRouter API key sourced from environment (`OPENROUTER_API_KEY`)
- If OpenRouter returns a non-2xx response, surface it as a proper `sendApiError()` with the upstream status code and message

### Usage token sourcing (CHAT-03)
- Extract `usage.prompt_tokens`, `usage.completion_tokens`, `usage.total_tokens` directly from the OpenRouter response body
- Pass through unchanged — no estimation or recalculation
- `usage` is a required field in the response; if OpenRouter omits it (unlikely), return zeros rather than omitting

### Response field completeness (CHAT-01)
- Always include `logprobs: null` in each choice object (OpenAI schema requires the field, even when not requested)
- `finish_reason` taken from upstream response (`"stop"`, `"length"`, `"tool_calls"`, etc.)
- `choices[].index` always present (0-based, mirrors upstream)
- `choices[].message` contains `role` and `content` from upstream
- Use generated OpenAI types (`apps/api/src/types/openai.d.ts`) for compile-time enforcement of response shape
- Headers forwarded: `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits` — all must be populated from the real upstream call

### Claude's Discretion
- HTTP client implementation details (fetch vs. node-fetch, timeout values, retry on network error)
- Error message wording for the "streaming not yet supported" 400 response
- Whether to add a response TypeBox schema for compile-time route reply type-checking (Phase 2 pattern)

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### OpenAI spec (response schema authority)
- `docs/reference/openai-openapi.yml` — Full OpenAI spec; `CreateChatCompletionResponse` and `ChatCompletionChoice` define the exact required fields for CHAT-01
- `apps/api/src/types/openai.d.ts` — Generated TypeScript types from the spec; use `CreateChatCompletionResponse` type to enforce response shape at compile time

### Requirements
- `.planning/REQUIREMENTS.md` §CHAT-01, §CHAT-02, §CHAT-03 — Acceptance criteria for this phase

### Existing infrastructure to extend
- `apps/api/src/routes/chat-completions.ts` — Route handler to extend (add param forwarding)
- `apps/api/src/domain/ai-service.ts` — Service stub to replace with real OpenRouter call
- `apps/api/src/schemas/chat-completions.ts` — TypeBox request schema (already complete for CHAT-02 fields)
- `apps/api/src/routes/api-error.ts` — `sendApiError()` helper for error responses

### Prior phase patterns
- `.planning/phases/01-error-format/1-CONTEXT.md` — Error format decisions (`sendApiError` usage)
- `.planning/phases/02-type-infrastructure/02-CONTEXT.md` — TypeBox + OpenAI type generation patterns
- `.planning/phases/03-auth-compliance/03-CONTEXT.md` — Auth and Content-Type hook patterns

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `sendApiError(reply, status, message, opts)`: Use for all error responses including upstream errors and streaming-not-supported 400
- `requireV1ApiPrincipal()`: Already wired in chat-completions route — no changes needed
- `ChatCompletionsBodySchema` (TypeBox): Already defines all CHAT-02 fields — use `Static<typeof ChatCompletionsBodySchema>` as the params type
- `ModelService.findById()` / `pickDefault()`: Existing model resolution — keep using for model lookup before calling upstream
- `CreditService.consume()` + `UsageService.add()`: Already called in ai-service — keep pattern, update credit tracking with real token counts

### Established Patterns
- Route → service: route calls service method with userId + validated body, checks `"error" in result`
- Error format: `{ statusCode, error, code? }` from service → `sendApiError()` in route
- Model resolution: `findById(modelId)` returns model object with `.id`, `.capability`; 400 on unknown model
- Response headers: route sets `x-model-routed`, etc. from `result.headers` — maintain this pattern

### Integration Points
- `AiService.chatCompletions()`: Signature change needed — add `params` object for CHAT-02 fields
- ai-service → OpenRouter: New outbound HTTP call replaces the current stub body
- Route handler: Add `stream: true` guard (return 400 before calling service)
- `result.body`: Must now include `usage` object — sourced from OpenRouter response

</code_context>

<specifics>
## Specific Ideas

- The current ai-service stub generates fake content (`MVP response: ...`). Phase 5 replaces this with the real OpenRouter call — the stub is fully deleted, not kept as fallback.
- `stream: false` enforcement: if `request.body.stream === true`, return 400 with a clear message like `"Streaming is not yet supported. Set stream: false or omit the stream parameter."` — keeps Phase 5 scope clean.
- Keep the credit/usage tracking in ai-service after getting the real response: use `usage.total_tokens` (or a credits formula) to update `UsageService`.

</specifics>

<deferred>
## Deferred Ideas

- Streaming (`stream: true`) — Phase 6
- `stream_options: { include_usage: true }` — Phase 6 (CHAT-05)
- `x-request-id` response header — Phase 8 (DIFF-04)
- Model aliasing (accepting `gpt-4o` → routing to best provider) — Phase 8 (DIFF-03)

</deferred>

---

*Phase: 05-chat-completions-non-streaming*
*Context gathered: 2026-03-18*
