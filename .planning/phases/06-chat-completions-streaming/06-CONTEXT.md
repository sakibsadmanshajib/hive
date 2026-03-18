# Phase 6: Chat Completions (Streaming) - Context

**Gathered:** 2026-03-18
**Status:** Ready for planning

<domain>
## Phase Boundary

Enable `stream: true` on `/v1/chat/completions` — remove the Phase 5 guard and wire real SSE streaming through the provider pipeline. The route must emit OpenAI-compatible SSE chunks (`choices[].delta`, `usage: null` on intermediate chunks, usage telemetry in the final chunk before `[DONE]`). Non-streaming path is unchanged.

</domain>

<decisions>
## Implementation Decisions

### Streaming transport (CHAT-04)
- Direct proxy approach: pipe OpenRouter's SSE response body directly to the Fastify reply — do not reconstruct or reformat chunks
- OpenRouter already emits OpenAI-compatible `CreateChatCompletionStreamResponse` chunks, so pass-through is correct
- Route sets `Content-Type: text/event-stream` and pipes the raw byte stream from the OpenRouter fetch response
- No intermediate buffering or JSON parsing of individual chunks on the hot path

### Service layer contract
- Add a `chatCompletionsStream()` method to `AiService` (separate from `chatCompletions()`) that returns the raw fetch `Response` object from the provider
- Route checks `request.body.stream === true`, calls `chatCompletionsStream()` instead of `chatCompletions()`
- Service method returns `{ statusCode, response: Response, headers }` on success or `{ statusCode, error }` on failure
- Provider layer adds a `chatStream()` method to `OpenAICompatibleClient` that sends `stream: true` and returns the raw fetch `Response` (not parsed)

### Usage tracking with streaming (CHAT-05 / CHAT-03)
- Pre-charge credits before initiating the stream (consistent with non-streaming pattern)
- Record usage after stream completes using the final chunk's `usage` object (`prompt_tokens`, `completion_tokens`, `total_tokens`)
- If stream ends without a usage chunk (provider omits it), fall back to a credits-based estimate
- Refund credits on stream initiation failure (before any bytes sent); no refund after headers committed

### `stream_options: { include_usage: true }` (CHAT-05)
- Forward `stream_options` in the request body to OpenRouter as-is (pass-through, same as all other CHAT-02 params)
- OpenRouter will include the final usage chunk natively — no server-side injection needed
- Intermediate chunks must have `usage: null` (not omitted) — verify OpenRouter compliance; if not compliant, inject `"usage": null` in a post-processing pass

### Error handling mid-stream
- Claude's Discretion: once SSE headers are committed, HTTP error responses are impossible
- Before stream starts: return standard `sendApiError()` responses (400 unknown model, 402 insufficient credits, 502 provider unavailable)
- After stream starts: close the connection gracefully; optionally emit a final `data: {"error": "..."}` event if the Fastify reply supports it at that point

### Claude's Discretion
- Exact mechanism for piping `response.body` (Node.js stream vs. `pipeTo` vs. manual async iteration)
- Whether to add a TypeBox reply schema for the streaming route (likely not needed — SSE is raw bytes)
- Timeout handling for long-running streams
- Whether `ProviderRegistry.chatStream()` delegates to `OpenAICompatibleClient.chatStream()` or uses a different dispatch path

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements
- `.planning/REQUIREMENTS.md` §CHAT-04, §CHAT-05 — SSE format contract, usage chunk requirements

### OpenAI SSE spec
- OpenAI streaming format: `data: {json}\n\n` lines, `choices[].delta` (not `message`), terminates with `data: [DONE]\n\n`
- Final usage chunk (when `stream_options.include_usage: true`): `choices: []`, `usage: { prompt_tokens, completion_tokens, total_tokens }`
- Intermediate chunks: `usage: null` (field present, value null)

### Existing infrastructure to extend
- `apps/api/src/routes/chat-completions.ts` — Route to modify (remove stream: true guard, add streaming path)
- `apps/api/src/domain/ai-service.ts` — Add `chatCompletionsStream()` method
- `apps/api/src/providers/openai-compatible-client.ts` — Add `chatStream()` method (returns raw Response)
- `apps/api/src/providers/registry.ts` — Add `chatStream()` dispatch method
- `apps/api/src/providers/types.ts` — Add `ProviderChatStreamResult` type

### Prior phase patterns
- `.planning/phases/01-error-format/1-CONTEXT.md` — `sendApiError()` usage (pre-stream errors)
- `.planning/phases/03-auth-compliance/03-CONTEXT.md` — Content-Type hook, streaming contract note
- `.planning/phases/05-chat-completions-non-streaming/05-CONTEXT.md` — Established route→service pattern, credit/usage tracking

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `fetchWithRetry()` in `providers/http-client.ts`: Used by `OpenAICompatibleClient` — reuse for stream fetch (pass `stream: true`, don't consume body)
- `requireV1ApiPrincipal` auth hook: Already applied to `/v1/chat/completions` — no change needed
- `sendApiError()`: Use for all pre-stream error responses (before headers committed)
- Credit/usage pattern in `AiService.chatCompletions()`: Mirror this exact pattern in `chatCompletionsStream()`

### Established Patterns
- Route → service: `services.ai.chatCompletionsStream(userId, request.body, usageContext)` → check `"error" in result`
- `openai-compatible-client.ts` `chat()` method sets `stream: false` explicitly — new `chatStream()` sets `stream: true`, skips JSON parsing
- `ProviderRegistry.chat()` dispatches to primary provider — `chatStream()` follows same dispatch logic
- Error format: `{ statusCode, error, code? }` from service → `sendApiError()` in route (pre-stream only)
- Response headers: `x-model-routed`, `x-provider-used`, `x-provider-model` set from result — also apply to streaming response

### Integration Points
- `apps/api/src/routes/chat-completions.ts`: Replace `stream: true → 400` guard with real streaming path; set `Content-Type: text/event-stream` and pipe body
- `AiService.chatCompletionsStream()`: New method — model resolution, credit pre-charge, call registry, return raw Response
- `OpenAICompatibleClient.chatStream()`: New method — POST with `stream: true`, return raw `Response` without reading body
- `ProviderRegistry.chatStream()`: New dispatch method — delegates to primary provider's `chatStream()`

</code_context>

<specifics>
## Specific Ideas

- Phase 5 enforces `stream: false` with a 400. Phase 6 removes this guard entirely — `stream: false` (or omitted) continues to use the existing non-streaming path; `stream: true` uses the new streaming path.
- The `openai` npm SDK's `for await (const chunk of stream)` must work — this implies correct SSE framing and that `[DONE]` terminates the iteration properly.
- OpenRouter is OpenAI-compatible and already emits correct streaming format — direct proxy is safe; avoid any transformation on the hot path.

</specifics>

<deferred>
## Deferred Ideas

- None — discussion stayed within phase scope

</deferred>

---

*Phase: 06-chat-completions-streaming*
*Context gathered: 2026-03-18*
