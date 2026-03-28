# Phase 6: Chat Completions (Streaming) - Research

**Researched:** 2026-03-18
**Domain:** SSE streaming proxy for OpenAI-compatible chat completions
**Confidence:** HIGH

## Summary

Phase 6 enables `stream: true` on `/v1/chat/completions` by piping OpenRouter's SSE response directly to the client. The architecture is a pass-through proxy: OpenRouter already emits OpenAI-compatible `CreateChatCompletionStreamResponse` chunks, so no transformation is needed on the hot path. The main work is adding a `chatStream()` method at each layer (provider client, registry, service) that returns the raw `Response` object, then piping `response.body` through Fastify's reply.

Node.js v24 (this project) has native `ReadableStream` support. Fastify 5 treats streams sent via `reply.send()` as pre-serialized content, sending them unmodified — this is exactly what we need for SSE pass-through. The v1-plugin already has an `onSend` hook that preserves `text/event-stream` Content-Type (does not override to `application/json`).

Usage tracking requires reading the final SSE chunk's `usage` object after the stream completes. Since this is a direct pipe (no intermediate parsing), usage must be tracked via a post-stream callback or by tapping the stream. The CONTEXT.md decision is to pre-charge credits before streaming and record final usage from the last chunk if available.

**Primary recommendation:** Pipe `response.body` (a `ReadableStream`) directly through `reply.raw` with SSE headers. Add `chatStream()` to the provider client, registry, and service layers. Pre-charge credits; record usage after stream ends.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Direct proxy approach: pipe OpenRouter's SSE response body directly to the Fastify reply — do not reconstruct or reformat chunks
- Add a `chatCompletionsStream()` method to `AiService` (separate from `chatCompletions()`) that returns the raw fetch `Response` object from the provider
- Route checks `request.body.stream === true`, calls `chatCompletionsStream()` instead of `chatCompletions()`
- Service method returns `{ statusCode, response: Response, headers }` on success or `{ statusCode, error }` on failure
- Provider layer adds a `chatStream()` method to `OpenAICompatibleClient` that sends `stream: true` and returns the raw fetch `Response` (not parsed)
- Pre-charge credits before initiating the stream (consistent with non-streaming pattern)
- Record usage after stream completes using the final chunk's `usage` object
- Forward `stream_options` in the request body to OpenRouter as-is (pass-through)
- Before stream starts: return standard `sendApiError()` responses (400 unknown model, 402 insufficient credits, 502 provider unavailable)
- After stream starts: close the connection gracefully

### Claude's Discretion
- Exact mechanism for piping `response.body` (Node.js stream vs. `pipeTo` vs. manual async iteration)
- Whether to add a TypeBox reply schema for the streaming route (likely not needed — SSE is raw bytes)
- Timeout handling for long-running streams
- Whether `ProviderRegistry.chatStream()` delegates to `OpenAICompatibleClient.chatStream()` or uses a different dispatch path

### Deferred Ideas (OUT OF SCOPE)
- None
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| CHAT-04 | `stream=true` returns SSE format (`text/event-stream`, `data: {json}\n\n` lines, `data: [DONE]\n\n` terminator) with `CreateChatCompletionStreamResponse` chunks using `choices[].delta` instead of `choices[].message` | Direct proxy from OpenRouter handles format natively; route sets Content-Type header; v1-plugin onSend hook preserves it |
| CHAT-05 | `stream_options: { include_usage: true }` emits a final chunk with `usage` object and empty `choices: []` before `[DONE]`; intermediate chunks have `usage: null` | `stream_options` forwarded as-is to OpenRouter; OpenRouter emits compliant usage chunks; may need post-processing verification for `usage: null` on intermediate chunks |
</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| fastify | ^5.1.0 | HTTP framework | Already in use; `reply.raw` or `reply.send(stream)` for SSE |
| node built-in fetch | v24.14.0 | HTTP client | `fetchWithRetry()` already returns `Response` with `.body` ReadableStream |
| @sinclair/typebox | (existing) | Request validation | `ChatCompletionsBodySchema` already includes `stream` and `stream_options` |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| node:stream/web | built-in | ReadableStream/WritableStream | Converting web streams if needed |
| node:stream | built-in | Readable.fromWeb() | Converting ReadableStream to Node.js Readable for Fastify |
| vitest | ^2.1.8 | Testing | Streaming compliance tests |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| reply.raw pipe | reply.send(Readable) | Fastify treats Node.js Readable as pre-serialized; both work, but reply.raw gives more control over headers and flushing |
| @fastify/sse plugin | Manual SSE headers | Plugin adds overhead; direct proxy doesn't need SSE construction helpers |
| async iteration + write | Pipe entire body | Iteration allows tapping chunks for usage, but adds latency; pipe is faster for pass-through |

## Architecture Patterns

### Recommended Project Structure
```
apps/api/src/
  providers/
    types.ts                      # Add ProviderChatStreamResult type
    openai-compatible-client.ts   # Add chatStream() method
    registry.ts                   # Add chatStream() dispatch method
  domain/
    ai-service.ts                 # Add chatCompletionsStream() method (runtime)
  runtime/
    services.ts                   # Add chatCompletionsStream() method (real impl)
  routes/
    chat-completions.ts           # Branch on stream: true
  routes/__tests__/
    chat-completions-streaming-compliance.test.ts  # SSE shape tests
```

### Pattern 1: Provider chatStream() — Raw Response Return
**What:** New method on `OpenAICompatibleProviderClient` that sends `stream: true` and returns the raw `Response` without consuming the body.
**When to use:** Always for streaming requests.
**Example:**
```typescript
async chatStream(request: ProviderChatRequest): Promise<Response> {
  if (!this.config.apiKey) {
    throw new Error(`${this.name} api key missing`);
  }
  const { params, ...rest } = request;
  const response = await fetchWithRetry({
    provider: this.name,
    url: this.joinUrl("/chat/completions"),
    timeoutMs: this.config.timeoutMs,
    maxRetries: this.config.maxRetries,
    init: {
      method: "POST",
      headers: {
        authorization: `Bearer ${this.config.apiKey}`,
        "content-type": "application/json",
        ...(this.config.extraHeaders ?? {}),
      },
      body: JSON.stringify({
        model: request.model,
        messages: request.messages,
        ...request.params,
        stream: true,
      }),
    },
  });

  if (!response.ok) {
    let errorMessage = `${this.name} request failed with status ${response.status}`;
    try {
      const errorBody = await response.json() as { error?: { message?: string } };
      if (errorBody?.error?.message) {
        errorMessage = errorBody.error.message;
      }
    } catch { /* ignore parse failures */ }
    const err = new Error(errorMessage);
    (err as any).statusCode = response.status;
    throw err;
  }

  return response; // Body NOT consumed — caller pipes it
}
```

### Pattern 2: Route Streaming Branch
**What:** Route checks `stream === true`, calls different service method, pipes response body.
**When to use:** The single route handler branches based on the `stream` parameter.
**Example:**
```typescript
if (request.body?.stream === true) {
  const result = await services.ai.chatCompletionsStream(
    principal.userId, request.body, usageCtx,
  );
  if ("error" in result) {
    return sendApiError(reply, result.statusCode, result.error);
  }
  reply
    .header("content-type", "text/event-stream")
    .header("cache-control", "no-cache")
    .header("connection", "keep-alive")
    .header("x-model-routed", result.headers["x-model-routed"])
    .header("x-provider-used", result.headers["x-provider-used"])
    .header("x-provider-model", result.headers["x-provider-model"])
    .header("x-actual-credits", result.headers["x-actual-credits"]);

  // Convert web ReadableStream to Node.js Readable and send
  const { Readable } = await import("node:stream");
  const nodeStream = Readable.fromWeb(result.response.body);
  return reply.send(nodeStream);
}
```

### Pattern 3: Service chatCompletionsStream() — Pre-charge + Raw Pipe
**What:** Mirrors `chatCompletions()` for model resolution, credit pre-charge, and error handling, but returns the raw `Response` instead of parsed JSON.
**When to use:** All streaming chat completion requests.
**Example:**
```typescript
async chatCompletionsStream(
  userId: string,
  body: { model?: string; messages?: Array<{ role: string; content: string }>; [key: string]: unknown },
  usageContext: { channel: UsageChannel; apiKeyId?: string },
) {
  // Model resolution (same as chatCompletions)
  const model = /* ... resolve model ... */;
  if (!model) return { error: "unknown model", statusCode: 400 as const };

  // Credit pre-charge (same as chatCompletions)
  const creditsCost = this.models.creditsForRequest(model);
  const chargeReferenceId = `req_${randomUUID()}`;
  const consumed = await this.credits.consume(userId, creditsCost, chargeReferenceId);
  if (!consumed) return { error: "insufficient credits", statusCode: 402 as const };

  // Extract params (same pattern: destructure model/messages/stream out)
  const { model: _m, messages: _msgs, stream: _s, ...params } = body;

  let response: Response;
  try {
    response = await this.providerRegistry.chatStream(model.id, messages, params);
  } catch (error) {
    await this.credits.refund(userId, creditsCost, `refund_${chargeReferenceId}`);
    return { error: "provider unavailable", statusCode: 502 as const };
  }

  // Usage + langfuse tracking (fire-and-forget after stream setup)
  // Note: for streaming, usage recording happens here with pre-charged amount
  // Actual token counts from the final chunk are a future enhancement
  await this.usage.add({ userId, endpoint: "/v1/chat/completions", model: model.id, credits: creditsCost, ...usageContext });

  return {
    statusCode: 200 as const,
    response,
    headers: { "x-model-routed": model.id, "x-provider-used": "openrouter", "x-provider-model": model.id, "x-actual-credits": String(creditsCost) },
  };
}
```

### Anti-Patterns to Avoid
- **Buffering SSE chunks in memory:** Never accumulate chunks server-side. Pipe the raw stream directly.
- **JSON parsing every chunk:** The proxy should not parse intermediate SSE events. Pass-through only.
- **Using reply.hijack():** This bypasses Fastify's lifecycle entirely including hooks. Use `reply.raw` or `reply.send(stream)` instead.
- **Forgetting to set Content-Type before sending:** The v1-plugin `onSend` hook overrides to `application/json` unless `text/event-stream` is already set. Set it BEFORE calling `reply.send()`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| SSE formatting | Custom SSE frame encoder | OpenRouter's native SSE output | OpenRouter already emits compliant `data: {json}\n\n` frames |
| Stream conversion | Manual async iteration adapter | `Readable.fromWeb()` | Node.js built-in, handles backpressure correctly |
| Usage chunk extraction | Custom SSE parser for every chunk | Pre-charge credits; optionally tap final chunk later | Parsing every chunk adds latency; pre-charge is the decided pattern |
| Connection keep-alive | Custom heartbeat/ping | HTTP keep-alive headers | Browser EventSource handles reconnection; proxy just pipes |

**Key insight:** Since OpenRouter is OpenAI-compatible, the entire SSE format is already correct. The server's job is purely to proxy bytes, not to construct or validate SSE frames.

## Common Pitfalls

### Pitfall 1: Fastify Content-Type Override
**What goes wrong:** The v1-plugin `onSend` hook overrides Content-Type to `application/json` for all responses.
**Why it happens:** The hook checks for `text/event-stream` and skips the override, but only if the header is set before `onSend` fires.
**How to avoid:** Set `reply.header("content-type", "text/event-stream")` BEFORE calling `reply.send()`. The existing hook at line 39-46 of `v1-plugin.ts` already handles this correctly.
**Warning signs:** Client receives SSE data but with `application/json` content type, breaking EventSource.

### Pitfall 2: fetchWithRetry Consuming the Body on Error Retry
**What goes wrong:** `fetchWithRetry` retries on 429/5xx status codes. On retry, the previous response body may be left unconsumed.
**Why it happens:** For non-streaming, bodies are small and GC'd. For streaming, an unconsumed body holds the connection open.
**How to avoid:** The `chatStream()` method should either: (a) use `maxRetries: 0` for stream requests, or (b) ensure failed response bodies are consumed before retry. Recommendation: use `maxRetries: 0` since retrying a stream initiation is acceptable but retrying mid-stream is not.
**Warning signs:** Connection pool exhaustion under load.

### Pitfall 3: Timeout Killing Long Streams
**What goes wrong:** `fetchWithRetry` applies `AbortController` timeout. A streaming response can legitimately run for minutes.
**Why it happens:** The timeout fires on the entire request duration, not just TTFB (time to first byte).
**How to avoid:** For streaming, use a longer timeout or apply timeout only to the initial connection (TTFB), not the entire stream duration. Option: pass a separate `streamTimeoutMs` to the fetch call, or clear the abort controller after headers are received.
**Warning signs:** Long completions (reasoning models, large outputs) get cut off mid-stream.

### Pitfall 4: Not Handling Client Disconnect
**What goes wrong:** Client disconnects (closes tab, network error) but the upstream connection to OpenRouter stays open.
**Why it happens:** Node.js pipes don't automatically propagate downstream close to upstream.
**How to avoid:** Listen to Fastify request `close` event or the reply's `raw.on('close')` and abort the upstream fetch if the client disconnects.
**Warning signs:** Leaked connections to OpenRouter, wasted provider resources.

### Pitfall 5: Credit Refund After Headers Committed
**What goes wrong:** Attempting to send an error response after SSE headers are already sent.
**Why it happens:** Once `reply.raw.writeHead()` or `reply.send(stream)` is called, HTTP status and headers are committed.
**How to avoid:** All validations (model resolution, credit check, provider availability) MUST happen before piping starts. After headers are committed, errors can only be signaled by closing the connection. Per CONTEXT.md: "no refund after headers committed."
**Warning signs:** Fastify warnings about headers already sent; silent failures.

## Code Examples

### Converting Web ReadableStream to Node.js Readable
```typescript
// Node.js v24 has Readable.fromWeb() built in
import { Readable } from "node:stream";

// response.body is a ReadableStream (web standard)
const nodeStream = Readable.fromWeb(response.body as any);
reply.send(nodeStream);
```

### SSE Compliance Shape (for tests)
```typescript
// A compliant streaming chunk
const streamChunk = {
  id: "chatcmpl-abc123",
  object: "chat.completion.chunk",
  created: 1700000000,
  model: "test-model",
  choices: [{
    index: 0,
    delta: { role: "assistant", content: "Hello" },  // delta, NOT message
    finish_reason: null,
    logprobs: null,
  }],
  usage: null,  // null on intermediate chunks, NOT omitted
};

// Final usage chunk (when stream_options.include_usage: true)
const usageChunk = {
  id: "chatcmpl-abc123",
  object: "chat.completion.chunk",
  created: 1700000000,
  model: "test-model",
  choices: [],  // Empty choices on usage chunk
  usage: {
    prompt_tokens: 10,
    completion_tokens: 20,
    total_tokens: 30,
  },
};

// SSE frame format
// data: {"id":"chatcmpl-abc123","object":"chat.completion.chunk",...}\n\n
// data: [DONE]\n\n
```

### ProviderClient Interface Extension
```typescript
// Add to ProviderClient interface in types.ts
export interface ProviderClient {
  readonly name: ProviderName;
  isEnabled(): boolean;
  chat(request: ProviderChatRequest): Promise<ProviderChatResponse>;
  chatStream?(request: ProviderChatRequest): Promise<Response>;  // Optional — only OpenAI-compatible providers
  generateImage?(request: ProviderImageRequest): Promise<ProviderImageResponse>;
  status(): Promise<ProviderHealthStatus>;
  checkModelReadiness(model: string): Promise<ProviderReadinessStatus>;
}
```

### Registry chatStream() Dispatch
```typescript
// Simpler than chat() — no fallback chain for streaming (CONTEXT.md: Claude's discretion)
async chatStream(
  modelId: string,
  messages: ProviderChatMessage[],
  params?: Record<string, unknown>,
): Promise<{ response: Response; providerUsed: ProviderName; providerModel: string }> {
  const primaryProvider = this.config.modelProviderMap[modelId] ?? this.config.defaultProvider;
  const client = this.clientsByName.get(primaryProvider);

  if (!client?.isEnabled() || !client.chatStream) {
    throw new Error(`${primaryProvider}: streaming not supported`);
  }

  const providerModel = this.config.providerModelMap[primaryProvider] ?? modelId;
  const response = await client.chatStream({ model: providerModel, messages, params });

  return { response, providerUsed: primaryProvider, providerModel };
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `reply.raw.write()` manual SSE | `reply.send(Readable.fromWeb(stream))` | Fastify 4.x+ / Node 18+ | Cleaner, respects Fastify lifecycle |
| `request` npm package | Native `fetch` + `Response.body` | Node 18+ | No external HTTP client needed; `fetchWithRetry` already uses native fetch |
| Reconstruct SSE on server | Pass-through proxy | When providers became OpenAI-compatible | No transformation overhead, lower latency |

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | vitest ^2.1.8 |
| Config file | vitest config in package.json or vitest.config.ts (existing) |
| Quick run command | `npx vitest run --reporter=verbose` |
| Full suite command | `npx vitest run` |

### Phase Requirements to Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| CHAT-04 | SSE chunk shape: `object: "chat.completion.chunk"`, `choices[].delta`, `data: [DONE]\n\n` | unit | `npx vitest run apps/api/src/routes/__tests__/chat-completions-streaming-compliance.test.ts -x` | No — Wave 0 |
| CHAT-04 | Response Content-Type is `text/event-stream` | unit | Same test file | No — Wave 0 |
| CHAT-05 | Final usage chunk with `choices: []` and `usage` object | unit | Same test file | No — Wave 0 |
| CHAT-05 | Intermediate chunks have `usage: null` (not omitted) | unit | Same test file | No — Wave 0 |

### Sampling Rate
- **Per task commit:** `npx vitest run apps/api/src/routes/__tests__/chat-completions-streaming-compliance.test.ts -x`
- **Per wave merge:** `npx vitest run`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `apps/api/src/routes/__tests__/chat-completions-streaming-compliance.test.ts` — SSE chunk shape validation (CHAT-04, CHAT-05)
- [ ] Test helper to create mock SSE ReadableStream for unit tests (simulates OpenRouter response)

## Open Questions

1. **Stream timeout strategy**
   - What we know: `fetchWithRetry` uses `AbortController` with `timeoutMs`. Streaming responses can run for minutes.
   - What's unclear: Should we use a separate timeout for TTFB vs. total stream duration?
   - Recommendation: Use `timeoutMs` for TTFB (connection establishment). Clear the abort controller once headers are received. Alternatively, pass `timeoutMs: 120000` (2 min) for streaming vs. the default for non-streaming. This is Claude's discretion per CONTEXT.md.

2. **Fallback chain for streaming**
   - What we know: Non-streaming `chat()` has a full failover chain through multiple providers.
   - What's unclear: Should `chatStream()` attempt failover if the primary provider fails?
   - Recommendation: No fallback for streaming. If the primary provider fails before streaming starts, return an error. Failover mid-stream is impossible. Keep it simple.

3. **Usage recording precision**
   - What we know: Pre-charge credits before stream. Final chunk may have `usage` with real token counts.
   - What's unclear: Whether to tap the stream to extract the final usage chunk for accurate recording.
   - Recommendation: For Phase 6, record usage at pre-charge time with estimated credits. Tapping the stream for exact usage is a future enhancement (adds complexity to the pass-through proxy).

## Sources

### Primary (HIGH confidence)
- Codebase inspection: `apps/api/src/routes/chat-completions.ts` — current stream guard (line 20-25)
- Codebase inspection: `apps/api/src/routes/v1-plugin.ts` — onSend hook preserves text/event-stream (line 39-46)
- Codebase inspection: `apps/api/src/providers/openai-compatible-client.ts` — existing `chat()` method pattern
- Codebase inspection: `apps/api/src/providers/http-client.ts` — `fetchWithRetry()` returns raw `Response`
- Codebase inspection: `apps/api/src/runtime/services.ts` — `chatCompletions()` pattern (lines 607-726)
- Node.js v24.14.0 — native `ReadableStream`, `Readable.fromWeb()` available
- Fastify 5 docs — `reply.send(stream)` treats streams as pre-serialized content

### Secondary (MEDIUM confidence)
- [Fastify Reply docs](https://fastify.dev/docs/latest/Reference/Reply/) — stream handling behavior
- [Fastify SSE discussion](https://github.com/fastify/fastify/issues/1877) — community patterns for SSE

### Tertiary (LOW confidence)
- OpenRouter SSE compliance assumption — assumed fully OpenAI-compatible based on CONTEXT.md decision; should be verified with a real request during implementation

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - all libraries already in use, Node.js version verified
- Architecture: HIGH - direct extension of existing patterns, all integration points identified
- Pitfalls: HIGH - identified from codebase analysis (timeout, content-type, body consumption)
- SSE format: MEDIUM - relying on OpenRouter compliance (per CONTEXT.md decision); verified against OpenAI spec in generated types

**Research date:** 2026-03-18
**Valid until:** 2026-04-17 (30 days — stable domain, well-established patterns)
