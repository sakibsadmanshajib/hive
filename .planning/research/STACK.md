# Technology Stack: OpenAI API Compliance Layer

**Project:** Hive - OpenAI API Compliance Hardening
**Researched:** 2026-03-16
**Overall Confidence:** MEDIUM (versions based on training data through May 2025; verify exact latest before installing)

## Current State Assessment

Hive's API layer is **minimal on validation infrastructure**:
- No schema validation library (no Zod, Ajv, TypeBox, or JSON Schema)
- No OpenAI type definitions -- hand-rolled `ChatBody` type with `model?: string; messages?: Array<{role: string; content: string}>`
- No streaming implementation in the route layer (providers have streaming awareness but routes return plain JSON)
- Error responses use ad-hoc `{ error: string }` instead of OpenAI's `{ error: { message, type, param, code } }` format
- `/v1/models` returns custom fields (`capability`, `costType`) not in the OpenAI spec, missing required fields (`created`, `owned_by`)

This means the compliance milestone is greenfield for validation/types -- no migration cost, but everything must be built.

## Recommended Stack

### Type Generation from OpenAI OpenAPI Spec

| Technology | Version | Purpose | Why | Confidence |
|------------|---------|---------|-----|------------|
| openapi-typescript | ^7.x | Generate TypeScript types from `openai-openapi.yml` | The standard tool for OpenAPI 3.1 to TS types. Produces zero-runtime type definitions directly from the 73K-line spec file. No alternatives come close for OpenAPI 3.1 support. | HIGH |

**How it works:**
```bash
pnpm add -Dw openapi-typescript

npx openapi-typescript docs/reference/openai-openapi.yml -o packages/shared/src/openai-types.ts
```

This produces exact TypeScript types for every OpenAI request/response schema. Use these as the source of truth for writing TypeBox schemas and typing response builders.

### Schema Validation (Request Input)

| Technology | Version | Purpose | Why | Confidence |
|------------|---------|---------|-----|------------|
| @sinclair/typebox | ^0.34.x | Runtime request validation + TypeScript inference | Fastify's recommended schema library. Compiles to JSON Schema that Fastify uses natively with Ajv (already bundled) and fast-json-stringify. Zero additional runtime -- Fastify already validates JSON Schema internally; TypeBox just provides a TypeScript-native authoring layer. | HIGH |
| @fastify/type-provider-typebox | ^5.x | Wire TypeBox schemas into Fastify route definitions | Gives validated `request.body` with full type inference. The official Fastify team's recommended type provider. | HIGH |

**Why TypeBox over Zod:**
- Fastify already bundles Ajv and compiles JSON Schema validators at startup. TypeBox produces JSON Schema directly, so there is zero additional runtime dependency -- Fastify's built-in validation pipeline handles everything.
- Zod requires `@fastify/type-provider-zod` which adds a Zod-to-JSON-Schema compilation step. This works but adds an unnecessary layer when Fastify already speaks JSON Schema natively.
- TypeBox schemas produce both TypeScript types AND JSON Schema from a single definition. No dual maintenance.
- TypeBox is maintained by the Fastify team member (@sinclairzx81) and is the recommended approach in Fastify 5 documentation.
- For an inference proxy, performance difference is irrelevant (LLM calls take 500ms-30s). The deciding factor is **native Fastify integration** -- TypeBox hooks into Fastify's existing validation/serialization without additional middleware.

**When to use Zod instead:** If you need complex conditional validation logic (discriminated unions, transforms, refinements beyond JSON Schema capabilities). For OpenAI request validation, JSON Schema covers all cases.

### Response Shaping (Output Compliance)

| Technology | Version | Purpose | Why | Confidence |
|------------|---------|---------|-----|------------|
| (generated types from openapi-typescript) | -- | Type-check response objects at compile time | No runtime validation needed on responses -- you control them. TypeScript compile-time checking against generated types catches schema drift. | HIGH |

**Pattern:** Create response builder functions typed against the generated OpenAI types:
```typescript
import type { components } from '@hive/shared/openai-types';

type ChatCompletionResponse = components['schemas']['CreateChatCompletionResponse'];

function buildChatCompletionResponse(
  providerResult: ProviderExecutionResult,
  model: string,
  usage: TokenUsage
): ChatCompletionResponse {
  return {
    id: `chatcmpl-${generateId()}`,
    object: 'chat.completion',
    created: Math.floor(Date.now() / 1000),
    model,
    choices: [{ index: 0, message: { role: 'assistant', content: providerResult.content }, finish_reason: 'stop' }],
    usage: { prompt_tokens: usage.prompt, completion_tokens: usage.completion, total_tokens: usage.total },
  };
}
```

### SSE Streaming Compliance

| Technology | Version | Purpose | Why | Confidence |
|------------|---------|---------|-----|------------|
| (native Fastify reply.raw) | -- | Server-Sent Events streaming | Fastify's `reply.raw` gives direct access to Node's `http.ServerResponse`. No library needed -- SSE is a simple text protocol (`data: ...\n\n`). Adding a library adds abstraction over 10 lines of code. | HIGH |

**OpenAI SSE format requirements:**
```
data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","choices":[...]}\n\n
data: [DONE]\n\n
```

**Implementation pattern:**
```typescript
reply.raw.writeHead(200, {
  'Content-Type': 'text/event-stream',
  'Cache-Control': 'no-cache',
  'Connection': 'keep-alive',
  'Transfer-Encoding': 'chunked',
});

for await (const chunk of providerStream) {
  const sseChunk = buildStreamChunk(chunk);
  reply.raw.write(`data: ${JSON.stringify(sseChunk)}\n\n`);
}

// Final chunk with usage (OpenAI spec: stream_options.include_usage)
if (includeUsage) {
  reply.raw.write(`data: ${JSON.stringify(buildFinalUsageChunk(usage))}\n\n`);
}
reply.raw.write('data: [DONE]\n\n');
reply.raw.end();
```

### OpenAI Error Format Compliance

| Technology | Version | Purpose | Why | Confidence |
|------------|---------|---------|-----|------------|
| (custom Fastify error handler) | -- | Map all errors to OpenAI error object shape | OpenAI errors follow a strict shape: `{ error: { message, type, param, code } }`. This is 30 lines of scoped Fastify error handler on the `/v1` plugin, not a library. | HIGH |

**Required error mapping:**

| HTTP Status | OpenAI `type` | When |
|-------------|---------------|------|
| 400 | `invalid_request_error` | Bad request body, missing fields |
| 401 | `authentication_error` | Invalid/missing API key |
| 403 | `permission_error` | Insufficient credits, model not allowed |
| 404 | `not_found_error` | Unknown model, unknown endpoint |
| 429 | `rate_limit_error` | Rate limit exceeded |
| 500 | `server_error` | Provider failure, internal error |
| 503 | `server_error` | All providers down |

### Testing: OpenAI SDK as Integration Test Client

| Technology | Version | Purpose | Why | Confidence |
|------------|---------|---------|-----|------------|
| openai | ^4.x (verify latest) | Integration test client | The ultimate compliance test: if the official OpenAI Node SDK works against your API, you are compatible. Use it as a test client pointing at localhost. This is what LiteLLM and every serious OpenAI-compatible proxy does. | HIGH |
| vitest | ^2.1.8 (already installed) | Test runner | Already in the stack. Use for both unit tests (schema validation) and integration tests (SDK client). | HIGH |

**Testing strategy (3 layers):**

1. **Unit tests -- Schema validation:**
   ```typescript
   import { Value } from '@sinclair/typebox/value';
   import { ChatCompletionRequestSchema } from '../schemas/openai/chat-completion';

   test('accepts valid chat completion request', () => {
     const valid = Value.Check(ChatCompletionRequestSchema, {
       model: 'gpt-4o',
       messages: [{ role: 'user', content: 'Hello' }],
     });
     expect(valid).toBe(true);
   });

   test('rejects request without model', () => {
     const valid = Value.Check(ChatCompletionRequestSchema, {
       messages: [{ role: 'user', content: 'Hello' }],
     });
     expect(valid).toBe(false);
   });
   ```

2. **Unit tests -- Response shape:**
   ```typescript
   test('chat completion response has required fields', () => {
     const response = buildChatCompletionResponse(mockResult, 'gpt-4o', mockUsage);
     expect(response).toHaveProperty('id');
     expect(response.id).toMatch(/^chatcmpl-/);
     expect(response).toHaveProperty('object', 'chat.completion');
     expect(response).toHaveProperty('usage.prompt_tokens');
     expect(response.created).toBeLessThan(10_000_000_000); // seconds, not ms
   });
   ```

3. **Integration tests -- OpenAI SDK client:**
   ```typescript
   import OpenAI from 'openai';

   const client = new OpenAI({
     apiKey: 'test-api-key',
     baseURL: 'http://localhost:3000/v1',
   });

   test('chat completions via SDK', async () => {
     const completion = await client.chat.completions.create({
       model: 'openrouter/auto',
       messages: [{ role: 'user', content: 'Say hello' }],
     });
     expect(completion.choices[0].message.content).toBeTruthy();
     expect(completion.usage).toBeDefined();
   });

   test('streaming via SDK', async () => {
     const stream = await client.chat.completions.create({
       model: 'openrouter/auto',
       messages: [{ role: 'user', content: 'Say hello' }],
       stream: true,
     });
     const chunks = [];
     for await (const chunk of stream) {
       chunks.push(chunk);
     }
     expect(chunks.length).toBeGreaterThan(0);
     expect(chunks[0].object).toBe('chat.completion.chunk');
   });

   test('streaming with usage telemetry', async () => {
     const stream = await client.chat.completions.create({
       model: 'openrouter/auto',
       messages: [{ role: 'user', content: 'Say hello' }],
       stream: true,
       stream_options: { include_usage: true },
     });
     let lastChunk;
     for await (const chunk of stream) {
       lastChunk = chunk;
     }
     expect(lastChunk?.usage).toBeDefined();
     expect(lastChunk?.usage?.total_tokens).toBeGreaterThan(0);
   });

   test('error format matches OpenAI spec', async () => {
     try {
       await client.chat.completions.create({
         model: 'nonexistent-model',
         messages: [{ role: 'user', content: 'Hello' }],
       });
     } catch (e) {
       expect(e).toBeInstanceOf(OpenAI.NotFoundError);
     }
   });
   ```

### Supporting Libraries

| Library | Version | Purpose | When to Use | Confidence |
|---------|---------|---------|-------------|------------|
| nanoid | ^5.x | Generate unique IDs for `chatcmpl-*`, `embd-*` prefixes | Response ID generation. Lightweight, URL-safe. ESM-only since v5 -- verify tsconfig supports ESM. | MEDIUM |
| @fastify/rate-limit | ^10.x | OpenAI-style rate limiting with `x-ratelimit-*` headers | Hive already has custom rate limiter. Evaluate whether to adopt this for standard headers or extend existing. | LOW |

## Alternatives Considered

| Category | Recommended | Alternative | Why Not |
|----------|-------------|-------------|---------|
| Type generation | openapi-typescript | openapi-zod-client | Generates Zod schemas from OpenAPI, but produces 50K+ lines for the full OpenAI spec. Better to generate lean TS types and hand-write focused schemas for implemented endpoints only. |
| Type generation | openapi-typescript | Manual types | The OpenAI spec is 73K lines. Manual types drift from the real spec. Generated types are authoritative. |
| Validation | TypeBox + Fastify native | Zod + @fastify/type-provider-zod | Zod works but adds an extra layer. Fastify already bundles Ajv and validates JSON Schema natively. TypeBox produces JSON Schema directly -- zero additional runtime. TypeBox is maintained by the Fastify team. |
| Validation | TypeBox | Ajv directly | Ajv is what Fastify uses internally, but writing raw JSON Schema by hand has no TypeScript inference. TypeBox is the TypeScript-native authoring layer for JSON Schema. |
| SSE | Native Fastify reply.raw | @fastify/sse, eventsource-parser | SSE sending is trivial. Libraries add abstraction without value. For consuming provider SSE streams, use the provider SDK's built-in stream handling. |
| Test client | openai npm package | Manual HTTP assertions (fetch/undici) | The whole point is SDK compatibility. Testing with raw HTTP misses SDK-specific parsing, header expectations, and streaming behavior. |
| ID generation | nanoid | uuid | OpenAI IDs are short prefixed strings (`chatcmpl-abc123`), not UUIDs. nanoid produces compact URL-safe IDs. |
| ID generation | nanoid | crypto.randomUUID() | Built-in but produces UUID format. Need prefix + short random string, which nanoid handles cleanly. |

## Full Dependency Summary

### New Production Dependencies (apps/api)

```bash
pnpm add --filter @hive/api @sinclair/typebox @fastify/type-provider-typebox nanoid
```

| Package | Purpose | Size Impact |
|---------|---------|-------------|
| @sinclair/typebox | Schema definition + validation | ~50KB (tree-shakeable) |
| @fastify/type-provider-typebox | Fastify integration for TypeBox | ~5KB |
| nanoid | ID generation | ~1KB |

### New Dev Dependencies

```bash
pnpm add -D openapi-typescript openai
```

| Package | Purpose | Used In |
|---------|---------|---------|
| openapi-typescript | Generate TS types from OpenAI spec | Build script only |
| openai | SDK integration test client | Test files only |

### Package Scripts to Add

```json
{
  "scripts": {
    "generate:openai-types": "openapi-typescript docs/reference/openai-openapi.yml -o packages/shared/src/openai-types.ts"
  }
}
```

### Already In Stack (No Changes Needed)

| Package | Version | Role in Compliance |
|---------|---------|-------------------|
| fastify | ^5.1.0 | Core server, native Ajv validation, plugin encapsulation |
| vitest | ^2.1.8 | Test runner for schema + SDK integration tests |
| prom-client | ^15.1.3 | Metrics for compliance telemetry (request counts per endpoint) |
| ioredis | ^5.4.2 | Rate limiting state, potential API key caching |

## Architecture Integration

### Where types and schemas live

```
packages/shared/src/
  openai-types.ts              <- Generated by openapi-typescript (DO NOT EDIT)

apps/api/src/
  schemas/openai/
    chat-completion.ts         <- TypeBox schema for chat completions request
    models.ts                  <- TypeBox schema for models list/retrieve
    images.ts                  <- TypeBox schema for image generation request
    embeddings.ts              <- TypeBox schema for embeddings request
    errors.ts                  <- TypeBox schema for OpenAI error envelope
    common.ts                  <- Shared types (usage object, etc.)
  openai/
    response-builders.ts       <- Typed against generated openai-types
    error-handler.ts           <- Fastify error handler (scoped to /v1 plugin)
    sse-writer.ts              <- SSE streaming utility
    id.ts                      <- Prefixed ID generation (chatcmpl-*, embd-*, etc.)
```

### Validation flow

```
Request in
  -> Fastify route (TypeBox schema validates body via built-in Ajv)
  -> Route handler (request.body is typed via TypeBox inference)
  -> Provider call
  -> Response builder (return type checked against generated OpenAI types)
  -> JSON response out (fast-json-stringify if response schema provided)
```

### Streaming flow

```
Request in (stream: true)
  -> Fastify route validates body (same TypeBox schema, stream field accepted)
  -> Route handler branches on stream: true
  -> Provider streaming call (async iterable)
  -> Transform provider chunks to OpenAI chunk format (typed)
  -> SSE write to reply.raw
  -> Final usage chunk (if stream_options.include_usage)
  -> data: [DONE]
```

## How Other OpenAI-Compatible Proxies Approach This

### LiteLLM (Python, largest OSS proxy)
- Hand-written Pydantic models matching OpenAI schemas (not generated from spec)
- Tests use the `openai` Python SDK as client -- the gold standard approach
- SSE streaming via custom async generators
- Takeaway: **Test with the official SDK. Hand-write focused schemas for endpoints you implement.**

### OpenRouter
- Acts as upstream for Hive -- their API is already OpenAI-compatible
- They transform provider responses into OpenAI format server-side
- Takeaway: **Hive can largely pass through OpenRouter responses, but must validate/reshape for fields OpenRouter omits or adds differently (e.g., `usage` in streaming, model ID normalization).**

### Portkey, AI Gateway (Cloudflare)
- TypeScript-based with runtime validation
- Focus on response normalization across providers
- Takeaway: **The hard part is normalizing provider-specific quirks, not the schema itself.**

## Version Verification Notes

All versions below are based on training data through May 2025. Run `npm view <package> version` before installing to get the exact latest.

| Package | Stated Version | Confidence | Action |
|---------|---------------|------------|--------|
| openapi-typescript | ^7.x | MEDIUM | `npm view openapi-typescript version` -- may be v7.x or v8.x by March 2026 |
| @sinclair/typebox | ^0.34.x | MEDIUM | `npm view @sinclair/typebox version` -- active development, check latest |
| @fastify/type-provider-typebox | ^5.x | MEDIUM | `npm view @fastify/type-provider-typebox version` -- verify Fastify 5.1 compat |
| openai (SDK) | ^4.x | MEDIUM | `npm view openai version` -- v5 may exist. The `baseURL` override pattern is stable across versions |
| nanoid | ^5.x | HIGH | Stable, ESM-only since v5. Verify tsconfig supports ESM imports |

## Sources

- OpenAI OpenAPI spec: `docs/reference/openai-openapi.yml` (local, v2.3.0) -- HIGH confidence
- Hive codebase analysis: `apps/api/package.json`, route files, provider types -- HIGH confidence
- Fastify + TypeBox integration: Recommended by Fastify team, documented pattern -- HIGH confidence
- openapi-typescript: Widely used OSS tool for OpenAPI 3.1 type generation -- MEDIUM confidence (version may have changed)
- LiteLLM testing patterns: Known from training data -- MEDIUM confidence
- Package versions: Training data through May 2025 -- MEDIUM confidence, verify before use

---
*Stack research: 2026-03-16*
