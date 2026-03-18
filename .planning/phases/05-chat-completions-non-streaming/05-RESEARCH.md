# Phase 5: Chat Completions (Non-Streaming) - Research

**Researched:** 2026-03-18
**Domain:** OpenAI-compatible chat completions, OpenRouter upstream integration
**Confidence:** HIGH

## Summary

Phase 5 replaces the current stub in `AiService.chatCompletions()` (and `RuntimeAiService.chatCompletions()`) with a real upstream call to OpenRouter, ensuring full OpenAI schema compliance for non-streaming responses. The codebase already has significant infrastructure in place: the `OpenRouterProviderClient` (extending `OpenAICompatibleProviderClient`) handles HTTP calls via `fetchWithRetry`, the `ProviderRegistry` routes model IDs to providers, and the `ChatCompletionsBodySchema` already validates all CHAT-02 parameters. The primary gaps are: (1) the provider client's `chat()` method only forwards `model` and `messages` (not additional params like temperature, tools, etc.), (2) the response body is missing `logprobs: null` and `usage` fields required by CHAT-01/CHAT-03, and (3) there is no `stream: true` guard.

The approach is straightforward: extend the provider pipeline to pass through all request parameters, map the upstream response to the full `CreateChatCompletionResponse` shape, and add the streaming guard. The existing `OpenAICompatibleProviderClient.chat()` method must be updated to accept and forward additional parameters, and both `AiService` (test double) and `RuntimeAiService` (production) must be updated to pass the full request body through.

**Primary recommendation:** Extend `ProviderChatRequest` and `OpenAICompatibleProviderClient.chat()` to accept and forward all CHAT-02 parameters as a pass-through object, then ensure the response shape includes `logprobs: null` on each choice and the `usage` object.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Extend `AiService.chatCompletions()` to accept a full params object containing all CHAT-02 fields
- Route passes the entire validated request body through to ai-service -- no field is silently dropped
- ai-service forwards the params object to OpenRouter's `/v1/chat/completions` as-is (pass-through, not re-mapped)
- `AiService` makes a real HTTP call to OpenRouter using `fetch` (Node 18+ built-in) -- already done via `fetchWithRetry`
- `stream: false` is hardcoded/enforced -- if client sends `stream: true`, return 400
- OpenRouter API key sourced from environment (`OPENROUTER_API_KEY`) -- already configured
- Non-2xx upstream responses surfaced as proper `sendApiError()` with upstream status and message
- Extract `usage` directly from OpenRouter response body, pass through unchanged
- If OpenRouter omits `usage`, return zeros rather than omitting
- Always include `logprobs: null` in each choice object
- `finish_reason` taken from upstream response
- Use generated OpenAI types for compile-time enforcement
- Headers `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits` must be populated from real upstream call

### Claude's Discretion
- HTTP client implementation details (fetch vs. node-fetch, timeout values, retry on network error)
- Error message wording for the "streaming not yet supported" 400 response
- Whether to add a response TypeBox schema for compile-time route reply type-checking

### Deferred Ideas (OUT OF SCOPE)
- Streaming (`stream: true`) -- Phase 6
- `stream_options: { include_usage: true }` -- Phase 6 (CHAT-05)
- `x-request-id` response header -- Phase 8 (DIFF-04)
- Model aliasing -- Phase 8 (DIFF-03)
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| CHAT-01 | Response includes all required fields: `id`, `object`, `created`, `model`, `choices` (with `finish_reason`, `index`, `message`, `logprobs`), and `usage` | OpenAI `CreateChatCompletionResponse` type in `openai.d.ts:5901-5939` defines exact shape. Current response is missing `logprobs` in choices and `usage` at top level. |
| CHAT-02 | All request parameters forwarded to upstream provider | `ChatCompletionsBodySchema` already validates all params. Gap: `ProviderChatRequest` only has `model`+`messages`; `OpenAICompatibleProviderClient.chat()` only sends those two fields in request body. Must extend to pass through all params. |
| CHAT-03 | `usage` object with `prompt_tokens`, `completion_tokens`, `total_tokens` from upstream | `OpenAICompatibleChatResponse` type already parses `usage` from upstream. Gap: it's consumed internally but not surfaced in the API response body. Must pass through to response. |
</phase_requirements>

## Standard Stack

### Core (Already in Project)
| Library | Purpose | Why Standard |
|---------|---------|--------------|
| `@fastify/type-provider-typebox` | Request validation + type inference | Already set up in Phase 2 |
| `@sinclair/typebox` | Schema definitions | Already used for `ChatCompletionsBodySchema` |
| Node 18+ built-in `fetch` | HTTP client for upstream calls | Already used via `fetchWithRetry` in provider clients |
| `openapi-typescript` (generated types) | Compile-time response shape enforcement | `openai.d.ts` already generated from OpenAI spec |

### No New Dependencies Required
This phase requires zero new npm packages. All infrastructure exists.

## Architecture Patterns

### Current Architecture (what exists)
```
Route (chat-completions.ts)
  -> validates body via ChatCompletionsBodySchema
  -> calls services.ai.chatCompletions(userId, modelId, messages, usageContext)
     -> RuntimeAiService resolves model, checks credits
     -> calls providerRegistry.chat(modelId, messages)
        -> ProviderRegistry routes to OpenRouterProviderClient
        -> OpenAICompatibleProviderClient.chat({ model, messages })
           -> fetchWithRetry POST to OpenRouter /chat/completions
              (ONLY sends model + messages in body)
        -> Returns { content, providerUsed, providerModel }
     -> Constructs response body (stub-like, missing logprobs + usage)
```

### Target Architecture (what needs to change)
```
Route (chat-completions.ts)
  -> Guard: if stream === true, return 400
  -> validates body via ChatCompletionsBodySchema
  -> calls services.ai.chatCompletions(userId, request.body, usageContext)
     -> RuntimeAiService resolves model, checks credits
     -> calls providerRegistry.chat(modelId, messages, params)  [NEW: params]
        -> ProviderRegistry routes to OpenRouterProviderClient
        -> OpenAICompatibleProviderClient.chat({ model, messages, ...params })
           -> fetchWithRetry POST to OpenRouter /chat/completions
              (sends ALL params in body, stream: false enforced)
        -> Returns { content, providerUsed, providerModel, rawResponse }  [NEW: raw]
     -> Maps upstream response to CreateChatCompletionResponse shape
        (includes logprobs: null, usage object, all fields)
```

### Key Change Points (4 layers)

**Layer 1: Route handler** (`chat-completions.ts`)
- Add `stream: true` guard before calling service
- Pass full `request.body` instead of just `model` + `messages`

**Layer 2: AiService / RuntimeAiService** (`ai-service.ts`, `services.ts`)
- Change signature to accept full params object
- Pass params through to provider registry
- Map upstream raw response to OpenAI-compliant shape with all CHAT-01 fields

**Layer 3: ProviderRegistry** (`registry.ts`)
- Extend `chat()` to accept and pass through additional params

**Layer 4: OpenAICompatibleProviderClient** (`openai-compatible-client.ts`)
- Extend `chat()` to include all params in the POST body
- Return the full upstream response (not just extracted content) so that `finish_reason`, `logprobs`, `usage`, `id`, `created` can be surfaced
- Enforce `stream: false` in the outbound request body

### Anti-Patterns to Avoid
- **Re-mapping parameters:** Don't rename or transform request params before sending to OpenRouter. OpenRouter uses the same parameter names as OpenAI. Pass through as-is.
- **Extracting only `content` from upstream:** The current `OpenAICompatibleProviderClient.chat()` extracts just `content` and discards the rest. Must return enough of the raw response for CHAT-01 compliance.
- **Omitting null fields:** OpenAI SDK expects `logprobs: null` to be present, not absent. Same for `usage` -- must be present even if zeros.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| HTTP with retries/timeout | Custom fetch wrapper | Existing `fetchWithRetry` | Already handles retries, timeouts, abort signals |
| Request validation | Manual param checking | Existing `ChatCompletionsBodySchema` | TypeBox already validates all CHAT-02 params |
| Response type checking | Runtime assertions | `CreateChatCompletionResponse` from `openai.d.ts` | Compile-time enforcement via TypeScript |
| Error formatting | Custom error JSON | Existing `sendApiError()` | Already produces OpenAI error format |

## Common Pitfalls

### Pitfall 1: Forgetting `logprobs: null` in choices
**What goes wrong:** OpenAI SDK's TypeScript types mark `logprobs` as required (not optional) on each choice. Omitting it causes the SDK to see `undefined` for a required field.
**Why it happens:** The OpenAI spec defines `logprobs` as `{content, refusal} | null` -- it must be present, just null when not requested.
**How to avoid:** Always set `logprobs: upstream.logprobs ?? null` on each choice.
**Warning signs:** SDK integration test shows `undefined` for `choices[0].logprobs`.

### Pitfall 2: Breaking the existing provider abstraction
**What goes wrong:** Changing `ProviderChatRequest` or `ProviderChatResponse` types breaks all provider clients (Ollama, Groq, OpenAI, Gemini, Anthropic), not just OpenRouter.
**Why it happens:** All providers implement the same `ProviderClient` interface.
**How to avoid:** Make the extra params optional on `ProviderChatRequest`. Other providers can ignore them. For the raw response, add an optional field rather than changing existing ones.
**Warning signs:** TypeScript errors in other provider client files.

### Pitfall 3: `usage` being optional in OpenAI spec but required by CHAT-03
**What goes wrong:** The OpenAI TypeScript type marks `usage` as optional (`usage?: CompletionUsage`), but CHAT-03 requires it always be present.
**Why it happens:** Streaming responses may omit usage, so the spec marks it optional.
**How to avoid:** For non-streaming, always include `usage` in response. If upstream omits it, return `{ prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 }`.
**Warning signs:** SDK test fails because `response.usage` is undefined.

### Pitfall 4: Upstream error handling losing context
**What goes wrong:** OpenRouter returns structured error responses with messages, but the current `OpenAICompatibleProviderClient` just throws a generic `Error` on non-2xx.
**Why it happens:** `throw new Error(\`${this.name} request failed with status ${response.status}\`)` loses the upstream error body.
**How to avoid:** Parse the upstream error body and include the message in the error response sent to the client. Map upstream status codes appropriately (e.g., 429 from OpenRouter should become 429 to client, not 502).
**Warning signs:** Client sees "openrouter request failed with status 429" as a 502 instead of a proper 429.

### Pitfall 5: Not enforcing `stream: false` in outbound request
**What goes wrong:** If `stream` is not explicitly set to `false` in the outbound request to OpenRouter, and a future code path accidentally passes `true`, the response would be SSE format but parsed as JSON.
**Why it happens:** The outbound body may inherit whatever the client sent.
**How to avoid:** Always set `stream: false` in the body sent to OpenRouter, overriding any client value.

## Code Examples

### CreateChatCompletionResponse Required Shape (from openai.d.ts:5901-5939)
```typescript
// Source: apps/api/src/types/openai.d.ts lines 5901-5939
{
  id: string;                        // e.g. "chatcmpl-abc123"
  object: "chat.completion";         // literal
  created: number;                   // unix timestamp
  model: string;                     // model used
  choices: Array<{
    finish_reason: "stop" | "length" | "tool_calls" | "content_filter" | "function_call";
    index: number;
    message: {
      content: string | null;
      refusal: string | null;
      tool_calls?: ChatCompletionMessageToolCall[];
      // annotations, audio, function_call also optional
      role: "assistant";
    };
    logprobs: {
      content: ChatCompletionTokenLogprob[] | null;
      refusal: ChatCompletionTokenLogprob[] | null;
    } | null;
  }>;
  usage?: {                          // optional in spec, REQUIRED by CHAT-03
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
    completion_tokens_details?: { ... };
    prompt_tokens_details?: { ... };
  };
  service_tier?: string;
  system_fingerprint?: string;
}
```

### Stream Guard Pattern
```typescript
// In chat-completions.ts route handler, BEFORE calling service
if (request.body?.stream === true) {
  return sendApiError(reply, 400,
    "Streaming is not yet supported. Set stream: false or omit the stream parameter.",
    { code: "unsupported_parameter" }
  );
}
```

### Extended Provider Chat Request Pattern
```typescript
// In providers/types.ts - extend with optional params
export type ProviderChatRequest = {
  model: string;
  messages: ProviderChatMessage[];
  // Pass-through params for OpenAI-compatible providers
  params?: Record<string, unknown>;
};
```

### Full Upstream Request Body Pattern
```typescript
// In openai-compatible-client.ts chat() method
body: JSON.stringify({
  model: request.model,
  messages: request.messages,
  stream: false,  // Always enforce non-streaming
  ...request.params,  // Forward all CHAT-02 params
}),
```

### Response Mapping Pattern
```typescript
// Map upstream OpenRouter response to CHAT-01 compliant shape
const upstreamBody = await response.json();
return {
  id: upstreamBody.id ?? `chatcmpl-${randomUUID().slice(0, 12)}`,
  object: "chat.completion" as const,
  created: upstreamBody.created ?? Math.floor(Date.now() / 1000),
  model: upstreamBody.model ?? model.id,
  choices: (upstreamBody.choices ?? []).map((choice: any, i: number) => ({
    index: choice.index ?? i,
    finish_reason: choice.finish_reason ?? "stop",
    message: {
      role: "assistant" as const,
      content: choice.message?.content ?? null,
      refusal: choice.message?.refusal ?? null,
      ...(choice.message?.tool_calls ? { tool_calls: choice.message.tool_calls } : {}),
    },
    logprobs: choice.logprobs ?? null,  // CRITICAL: must be null, not absent
  })),
  usage: {
    prompt_tokens: upstreamBody.usage?.prompt_tokens ?? 0,
    completion_tokens: upstreamBody.usage?.completion_tokens ?? 0,
    total_tokens: upstreamBody.usage?.total_tokens ?? 0,
  },
};
```

## State of the Art

| Old Approach (current) | New Approach (Phase 5) | Impact |
|------------------------|------------------------|--------|
| Stub response in AiService | Real OpenRouter HTTP call | Actual AI responses |
| Only `model` + `messages` sent to provider | All CHAT-02 params forwarded | Full parameter support |
| No `logprobs` field in choices | `logprobs: null` always present | SDK compatibility |
| No `usage` in response | `usage` always present from upstream | Token tracking |
| No stream guard | 400 on `stream: true` | Clean scope boundary |
| Provider returns only `content` string | Provider returns full response data | Complete response mapping |

## Open Questions

1. **Upstream error body parsing**
   - What we know: OpenRouter returns JSON error bodies with messages on non-2xx
   - What's unclear: Exact error format and whether status codes should be mapped 1:1
   - Recommendation: Parse error body, forward status code (capping at 502 for 5xx), include upstream message

2. **Response TypeBox schema**
   - What we know: User left this to Claude's discretion
   - Recommendation: Skip for now. The generated OpenAI TypeScript types provide compile-time checking. Adding a TypeBox response schema adds overhead without significant benefit since we're forwarding upstream responses, not generating them.

3. **`refusal` field in message**
   - What we know: OpenAI spec includes `refusal: string | null` on `ChatCompletionResponseMessage`
   - What's unclear: Whether OpenRouter always includes this field
   - Recommendation: Default to `null` if absent from upstream, per the spec

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | vitest (latest) |
| Config file | `apps/api/vitest.config.ts` or inferred |
| Quick run command | `cd apps/api && npx vitest run --passWithNoTests` |
| Full suite command | `cd apps/api && npx vitest run` |

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| CHAT-01 | Response has all required fields (id, object, created, model, choices w/ finish_reason+index+message+logprobs, usage) | unit | `cd apps/api && npx vitest run test/domain/ai-service-chat.test.ts -x` | No - Wave 0 |
| CHAT-01 | SDK returns fully typed response with no undefined required fields | integration | `cd apps/api && npx vitest run test/routes/chat-completions-compliance.test.ts -x` | No - Wave 0 |
| CHAT-02 | All request params forwarded to upstream (temperature, tools, etc.) | unit | `cd apps/api && npx vitest run test/domain/ai-service-chat.test.ts -x` | No - Wave 0 |
| CHAT-02 | Stream guard returns 400 for stream:true | unit | `cd apps/api && npx vitest run test/routes/chat-completions-route.test.ts -x` | Partial - existing file needs new test |
| CHAT-03 | Usage object present with prompt_tokens, completion_tokens, total_tokens | unit | `cd apps/api && npx vitest run test/domain/ai-service-chat.test.ts -x` | No - Wave 0 |
| CHAT-03 | Usage defaults to zeros when upstream omits | unit | `cd apps/api && npx vitest run test/domain/ai-service-chat.test.ts -x` | No - Wave 0 |

### Sampling Rate
- **Per task commit:** `cd apps/api && npx vitest run --passWithNoTests`
- **Per wave merge:** `cd apps/api && npx vitest run`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `apps/api/test/domain/ai-service-chat.test.ts` -- covers CHAT-01, CHAT-02, CHAT-03 (unit tests with mocked provider)
- [ ] `apps/api/test/routes/chat-completions-compliance.test.ts` -- covers CHAT-01 SDK compliance (response shape validation)
- [ ] Add stream guard test to existing `apps/api/test/routes/chat-completions-route.test.ts`

## Sources

### Primary (HIGH confidence)
- `apps/api/src/types/openai.d.ts` lines 5901-5939 -- `CreateChatCompletionResponse` schema (generated from OpenAI spec)
- `apps/api/src/types/openai.d.ts` lines 5349-5365 -- `CompletionUsage` schema
- `apps/api/src/types/openai.d.ts` lines 5100-5107 -- `ChatCompletionResponseMessage` schema
- `apps/api/src/providers/openai-compatible-client.ts` -- current provider client implementation
- `apps/api/src/providers/types.ts` -- provider request/response types
- `apps/api/src/runtime/services.ts` lines 607-688 -- `RuntimeAiService.chatCompletions()` current implementation
- `apps/api/src/schemas/chat-completions.ts` -- TypeBox request schema with all CHAT-02 fields

### Secondary (MEDIUM confidence)
- [OpenRouter API documentation](https://openrouter.ai/docs/api/api-reference/chat/send-chat-completion-request) -- OpenRouter is OpenAI-compatible, same parameter names

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - all libraries already in project, no new deps needed
- Architecture: HIGH - clear 4-layer change pattern identified from reading actual source code
- Pitfalls: HIGH - identified from actual code analysis (missing logprobs, provider abstraction, usage handling)

**Research date:** 2026-03-18
**Valid until:** 2026-04-17 (stable domain, 30 days)
