# Phase 7: Surface Expansion - Research

**Researched:** 2026-03-18
**Domain:** OpenAI-compatible embeddings, images, and responses endpoints
**Confidence:** HIGH

## Summary

Phase 7 expands the Hive API surface from chat completions to three additional endpoints: embeddings (new), images/generations (stub to real), and responses (stub to real with schema hardening). All three follow the established route-service-provider pattern from Phase 5/6 and must produce responses that pass the official `openai` SDK client without errors.

The codebase already has images and responses routes registered with stubs in `AiService`. The main work is: (1) creating a new embeddings route and provider method, (2) replacing the `example.invalid` stub in image generation with a real provider call and fixing the non-compliant `object: "list"` field, (3) expanding the responses schema to accept full `CreateResponse` fields and returning a compliant `Response` object by translating to chat completions internally.

**Primary recommendation:** Follow the exact route-service-provider chain pattern from Phase 5 chat completions. The embeddings endpoint is the only truly new route; images and responses are fixes/expansions of existing code. Add `"embedding"` as a new capability type to `GatewayModel` and `ModelService`.

<user_constraints>

## User Constraints (from CONTEXT.md)

### Locked Decisions
- SURF-01 (Embeddings): New `POST /v1/embeddings` route via `registerEmbeddingsRoute()` in `v1-plugin.ts`. New `AiService.embeddings()`, `ProviderRegistry.embeddings()`, `OpenAICompatibleClient.embeddings()`. Routes to OpenRouter embedding model. Full `CreateEmbeddingResponse` shape. Credit/usage tracking follows Phase 5 pattern.
- SURF-02 (Images): Replace `example.invalid` URL with real provider call. Remove non-compliant `object: "list"` field from response. Forward all params (including `quality`, `style`). Handle `b64_json` unsupported case with 400.
- SURF-03 (Responses): Expand `ResponsesBodySchema` to full `CreateResponse` fields (`model` required, `input` required as string or array, `instructions`, `temperature`, `max_output_tokens`, `tools`, `tool_choice`, `text`, `user`). Translate to chat completion internally. Return compliant `Response` object with `id`, `object`, `created_at`, `status`, `model`, `output`, `usage`.

### Claude's Discretion
- Exact embedding model ID for default (e.g., `openai/text-embedding-ada-002` or `nomic-ai/nomic-embed-text-v1.5`)
- Whether `ProviderRegistry.embeddings()` delegates to `OpenAICompatibleClient.embeddings()` or a specialized client
- TypeBox schema detail level for `Response.output[].content` and `CreateResponse.input` array items
- Timeout and retry policy for embeddings

### Deferred Ideas (OUT OF SCOPE)
- `x-request-id` response header (Phase 8 DIFF-04)
- Model aliasing (Phase 8 DIFF-03)
- Full streaming support for `/v1/responses` (out of scope; Phase 7 is non-streaming only)

</user_constraints>

<phase_requirements>

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| SURF-01 | `POST /v1/embeddings` returns `CreateEmbeddingResponse` with `object: "list"`, `data[].embedding`, `data[].index`, `model`, `usage` | OpenAI spec verified (lines 37953-38080). New route, service method, provider method, and TypeBox schema needed. Must add `"embedding"` capability to model catalog. |
| SURF-02 | `POST /v1/images/generations` response is schema-compliant with `created` (int) and `data` array of Image objects | OpenAI `ImagesResponse` spec verified (line 45709). Key fix: remove `object: "list"` from response body. Replace stub URL with real provider call via existing `registry.imageGeneration()`. |
| SURF-03 | `POST /v1/responses` endpoint hardened against full OpenAI `Response` and `CreateResponse` schemas | OpenAI `Response` spec verified (line 59283). Translate `input`+`instructions` to chat messages, call `registry.chat()`, map result to `Response` object with proper `output` structure. |

</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| @sinclair/typebox | (existing) | Request schema validation | Already used for all `/v1/*` route schemas |
| fastify | (existing) | HTTP framework | Project standard |
| vitest | (existing) | Test runner | Project standard |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| openai (npm) | (existing) | SDK compatibility testing | Acceptance criteria: `client.embeddings.create()` and `client.images.generate()` must work |

No new dependencies required. All work extends existing infrastructure.

## Architecture Patterns

### Recommended Project Structure
```
apps/api/src/
  schemas/
    embeddings.ts          # NEW - TypeBox schema for CreateEmbeddingRequest
    responses.ts           # EXPAND - full CreateResponse fields
    images-generations.ts  # NO CHANGE - schema already correct
  routes/
    embeddings.ts          # NEW - registerEmbeddingsRoute()
    responses.ts           # MINOR FIX - pass full body to service
    images-generations.ts  # MINOR FIX - forward quality/style params
  domain/
    ai-service.ts          # ADD embeddings(), FIX imageGeneration(), FIX responses()
    model-service.ts       # ADD embedding model(s), ADD "embedding" capability
    types.ts               # EXPAND capability union to include "embedding"
  providers/
    types.ts               # ADD ProviderEmbeddingsRequest/Response types
    registry.ts            # ADD embeddings() dispatch method
    openai-compatible-client.ts  # ADD embeddings() method
```

### Pattern 1: Route-Service-Provider Chain (from Phase 5)
**What:** Route authenticates + validates -> Service resolves model + charges credits + calls provider -> Provider makes HTTP call
**When to use:** All three endpoints follow this exact pattern
**Example:**
```typescript
// Route layer (embeddings.ts)
const result = await services.ai.embeddings(principal.userId, request.body, {
  channel: inferUsageChannel(request, principal),
  apiKeyId: principal.apiKeyId,
});
if ("error" in result) {
  return sendApiError(reply, result.statusCode, result.error ?? "Unknown error");
}
for (const [key, value] of Object.entries(result.headers)) {
  reply.header(key, value);
}
reply.code(result.statusCode);
return result.body;
```

### Pattern 2: Responses-to-Chat Translation
**What:** `/v1/responses` translates its input format to chat completion messages, calls `registry.chat()`, then maps the response back to the Response object format
**When to use:** SURF-03 only
**Example:**
```typescript
// In AiService.responses()
const messages: ProviderChatMessage[] = [];
if (body.instructions) {
  messages.push({ role: "system", content: body.instructions });
}
// input can be string or array of items
if (typeof body.input === "string") {
  messages.push({ role: "user", content: body.input });
} else if (Array.isArray(body.input)) {
  // Map input items to chat messages
  for (const item of body.input) {
    // Handle message objects and text items
  }
}
const chatResult = await this.registry.chat(model.id, messages, params);
// Map chatResult to Response object
```

### Pattern 3: Provider Client Method (from OpenAICompatibleClient.chat())
**What:** New `embeddings()` method on `OpenAICompatibleClient` mirrors the `chat()` method structure
**When to use:** SURF-01 provider layer
**Example:**
```typescript
// In OpenAICompatibleClient
async embeddings(request: ProviderEmbeddingsRequest): Promise<ProviderEmbeddingsResponse> {
  const response = await fetchWithRetry({
    provider: this.name,
    url: joinUrl(this.config.baseUrl, "/embeddings"),
    timeoutMs: this.config.timeoutMs,
    maxRetries: this.config.maxRetries,
    init: {
      method: "POST",
      headers: { authorization: `Bearer ${this.config.apiKey}`, "content-type": "application/json", ...this.config.extraHeaders },
      body: JSON.stringify({ model: request.model, input: request.input, encoding_format: request.encodingFormat, dimensions: request.dimensions, user: request.user }),
    },
  });
  // Parse and return
}
```

### Anti-Patterns to Avoid
- **Hardcoding response shapes without checking the spec:** The `ImagesResponse` does NOT have `object: "list"` -- that's the current bug to fix
- **Accepting only `string` for responses input:** The `CreateResponse.input` field can be a string OR an array of input items -- must handle both
- **Returning `output_text` from server:** That field is SDK-only (computed client-side); the server returns `output[]` array only
- **Using `completion_tokens` in Response usage:** The Response object uses `input_tokens`/`output_tokens` naming, NOT `prompt_tokens`/`completion_tokens`

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Embedding model catalog | Custom model discovery | Add entries to `MODELS[]` in `model-service.ts` with `capability: "embedding"` | Consistent with existing model management |
| Provider HTTP calls | Custom fetch logic | `fetchWithRetry()` from `http-client.ts` | Handles timeout, retry, provider error extraction |
| Request validation | Manual body parsing | TypeBox schemas with Fastify type provider | Consistent with all existing routes |
| Circuit breaking | Custom retry logic for providers | Existing `ProviderRegistry` candidates + circuit breaker | Already handles failover and health tracking |

## Common Pitfalls

### Pitfall 1: ImagesResponse has no `object` field
**What goes wrong:** The current stub returns `object: "list"` which is NOT in the OpenAI `ImagesResponse` schema. The SDK may reject or behave unexpectedly.
**Why it happens:** Confusion with `CreateEmbeddingResponse` which does have `object: "list"`.
**How to avoid:** Remove `object: "list"` from image generation response body. Only return `created` and `data[]`.
**Warning signs:** SDK `client.images.generate()` throws type errors or unexpected fields appear in response.

### Pitfall 2: Response object uses different usage field names
**What goes wrong:** Using `prompt_tokens`/`completion_tokens` instead of `input_tokens`/`output_tokens`.
**Why it happens:** Chat completions use `prompt_tokens`/`completion_tokens`, but the Responses API uses `input_tokens`/`output_tokens`.
**How to avoid:** Map from chat completion usage (`prompt_tokens` -> `input_tokens`, `completion_tokens` -> `output_tokens`) when constructing the Response object.
**Warning signs:** SDK response object has wrong field names or missing usage data.

### Pitfall 3: GatewayModel capability type doesn't include "embedding"
**What goes wrong:** `ModelService.pickDefault("embedding")` fails or `findById()` returns a model with wrong capability.
**Why it happens:** Current `GatewayModel.capability` is `"chat" | "image"` only.
**How to avoid:** Expand the type union to `"chat" | "image" | "embedding"` and add at least one embedding model to `MODELS[]`.
**Warning signs:** TypeScript compilation errors, runtime "No model for capability: embedding" errors.

### Pitfall 4: ProviderClient interface missing embeddings method
**What goes wrong:** `ProviderRegistry.embeddings()` can't dispatch because `ProviderClient` has no `embeddings?()` method.
**Why it happens:** The interface only has `chat`, `chatStream?`, and `generateImage?`.
**How to avoid:** Add `embeddings?(request: ProviderEmbeddingsRequest): Promise<ProviderEmbeddingsResponse>` to the `ProviderClient` interface (optional, like `generateImage?`).
**Warning signs:** TypeScript errors when accessing `client.embeddings`.

### Pitfall 5: Responses route still passes only `input: string`
**What goes wrong:** The route currently calls `services.ai.responses(userId, request.body?.input ?? "", ...)` -- passing only the input string, not the full body.
**Why it happens:** Original stub was minimal.
**How to avoid:** Change to `services.ai.responses(userId, request.body, ...)` passing the full validated body.
**Warning signs:** Service method doesn't receive `model`, `instructions`, `temperature`, etc.

### Pitfall 6: Response output structure is deeply nested
**What goes wrong:** Returning a flat text output instead of the proper nested structure.
**Why it happens:** The original stub returns `[{ type: "text", text: "..." }]` which is wrong.
**How to avoid:** Correct structure is `[{ type: "message", id: "msg_<uuid>", role: "assistant", status: "completed", content: [{ type: "output_text", text: "..." }] }]`.
**Warning signs:** SDK can't parse the output or throws on missing nested fields.

## Code Examples

### Embeddings TypeBox Schema
```typescript
// Source: OpenAI spec lines 37953-38046
import { Type, type Static } from "@sinclair/typebox";

export const EmbeddingsBodySchema = Type.Object(
  {
    model: Type.String(),
    input: Type.Union([
      Type.String(),
      Type.Array(Type.String(), { minItems: 1 }),
    ]),
    encoding_format: Type.Optional(
      Type.Union([Type.Literal("float"), Type.Literal("base64")])
    ),
    dimensions: Type.Optional(Type.Integer({ minimum: 1 })),
    user: Type.Optional(Type.String()),
  },
  { additionalProperties: false },
);

export type EmbeddingsBody = Static<typeof EmbeddingsBodySchema>;
```

### Expanded Responses TypeBox Schema
```typescript
// Source: OpenAI spec lines 59283+, CONTEXT.md decisions
import { Type, type Static } from "@sinclair/typebox";

const InputItemSchema = Type.Object({
  type: Type.Optional(Type.String()),
  role: Type.Optional(Type.String()),
  content: Type.Optional(Type.Union([
    Type.String(),
    Type.Array(Type.Any()),
  ])),
});

export const ResponsesBodySchema = Type.Object(
  {
    model: Type.String(),
    input: Type.Union([
      Type.String(),
      Type.Array(InputItemSchema),
    ]),
    instructions: Type.Optional(Type.String()),
    temperature: Type.Optional(Type.Number()),
    max_output_tokens: Type.Optional(Type.Integer()),
    tools: Type.Optional(Type.Array(Type.Any())),
    tool_choice: Type.Optional(Type.Any()),
    text: Type.Optional(Type.Any()),
    user: Type.Optional(Type.String()),
  },
  { additionalProperties: false },
);

export type ResponsesBody = Static<typeof ResponsesBodySchema>;
```

### Compliant Response Object Shape
```typescript
// Source: OpenAI spec lines 59283-59405
const responseBody = {
  id: `resp_${randomUUID().slice(0, 12)}`,
  object: "response" as const,
  created_at: Math.floor(Date.now() / 1000),
  status: "completed" as const,
  model: model.id,
  output: [
    {
      type: "message" as const,
      id: `msg_${randomUUID().slice(0, 12)}`,
      role: "assistant" as const,
      status: "completed" as const,
      content: [
        {
          type: "output_text" as const,
          text: chatResult.content,
        },
      ],
    },
  ],
  usage: {
    input_tokens: chatResult.rawResponse?.usage?.prompt_tokens ?? 0,
    output_tokens: chatResult.rawResponse?.usage?.completion_tokens ?? 0,
    total_tokens: chatResult.rawResponse?.usage?.total_tokens ?? 0,
  },
};
```

### Compliant ImagesResponse Shape (no `object` field)
```typescript
// Source: OpenAI spec lines 45709-45748
const imagesResponseBody = {
  created: Math.floor(Date.now() / 1000),
  // NO "object" field - ImagesResponse doesn't have one
  data: providerResult.data.map((img) => ({
    ...(img.url ? { url: img.url } : {}),
    ...(img.b64Json ? { b64_json: img.b64Json } : {}),
    // include revised_prompt if provider returns it
  })),
};
```

### Compliant CreateEmbeddingResponse Shape
```typescript
// Source: OpenAI spec lines 38047-38080
const embeddingResponseBody = {
  object: "list" as const,
  data: providerResult.data.map((item, index) => ({
    object: "embedding" as const,
    embedding: item.embedding,
    index,
  })),
  model: providerResult.providerModel,
  usage: {
    prompt_tokens: providerResult.usage?.promptTokens ?? 0,
    total_tokens: providerResult.usage?.totalTokens ?? 0,
  },
};
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Stub responses with fake URLs | Real provider calls through registry | Phase 7 | Endpoints become functional |
| `object: "list"` on ImagesResponse | No `object` field (per OpenAI spec) | Phase 7 | SDK compatibility fix |
| `responses()` accepts only `input: string` | Full `CreateResponse` body | Phase 7 | Full schema compliance |
| capability: "chat" or "image" only | Add "embedding" capability | Phase 7 | Embeddings model routing |

## Open Questions

1. **Default embedding model ID**
   - What we know: OpenRouter supports `openai/text-embedding-ada-002`, `openai/text-embedding-3-small`, and `nomic-ai/nomic-embed-text-v1.5`
   - Recommendation: Use `openai/text-embedding-3-small` as default -- it's the most commonly used, well-documented, and widely supported on OpenRouter. Add it to `MODELS[]` with `capability: "embedding"` and fixed pricing.

2. **Provider delegation for embeddings**
   - What we know: `OpenAICompatibleClient` already has `chat()` and `chatStream()` methods. OpenRouter's embeddings endpoint follows the same `/v1/embeddings` OpenAI-compatible pattern.
   - Recommendation: Add `embeddings()` directly to `OpenAICompatibleProviderClient` (same class). No specialized client needed -- the OpenAI-compatible pattern applies.

3. **TypeBox schema depth for Response input items**
   - What we know: Full `InputItem` schema is deeply nested with many variants (text, image, file, etc.)
   - Recommendation: For v1, use a pragmatic schema: accept `string | Array<object>` for input items with `additionalProperties: true` on the array item objects. This allows the SDK to send any valid input while we handle the common cases (string, message objects) in the service layer. Over-engineering the TypeBox schema for all InputItem variants adds complexity without value since we only translate to chat messages anyway.

4. **Timeout/retry for embeddings**
   - What we know: Chat uses configured timeout with retries; streaming uses 120s with no retries.
   - Recommendation: Use the same timeout and retry config as chat (non-streaming). Embeddings are fast, non-streaming requests -- standard retry policy is appropriate.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | vitest (existing) |
| Config file | apps/api/vitest.config.ts or package.json |
| Quick run command | `cd apps/api && npx vitest run --passWithNoTests` |
| Full suite command | `cd apps/api && npx vitest run` |

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| SURF-01 | Embeddings response shape compliance | unit | `cd apps/api && npx vitest run src/routes/__tests__/embeddings-compliance.test.ts` | No - Wave 0 |
| SURF-02 | Images response shape compliance (no `object` field, correct `data[]`) | unit | `cd apps/api && npx vitest run src/routes/__tests__/images-compliance.test.ts` | No - Wave 0 |
| SURF-03 | Response object shape compliance (nested output, correct usage fields) | unit | `cd apps/api && npx vitest run src/routes/__tests__/responses-compliance.test.ts` | No - Wave 0 |

### Sampling Rate
- **Per task commit:** `cd apps/api && npx vitest run --passWithNoTests`
- **Per wave merge:** `cd apps/api && npx vitest run`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `apps/api/src/routes/__tests__/embeddings-compliance.test.ts` -- covers SURF-01 response shape
- [ ] `apps/api/src/routes/__tests__/images-compliance.test.ts` -- covers SURF-02 response shape
- [ ] `apps/api/src/routes/__tests__/responses-compliance.test.ts` -- covers SURF-03 response shape

## Sources

### Primary (HIGH confidence)
- `docs/reference/openai-openapi.yml` lines 37953-38080 - CreateEmbeddingRequest/Response schemas
- `docs/reference/openai-openapi.yml` lines 41854-41880 - Embedding object schema
- `docs/reference/openai-openapi.yml` lines 45152-45174 - Image object schema
- `docs/reference/openai-openapi.yml` lines 45709-45748 - ImagesResponse schema
- `docs/reference/openai-openapi.yml` lines 59283-59405 - Response object schema
- `docs/reference/openai-openapi.yml` lines 62277-62310 - ResponseUsage schema
- Existing codebase: `ai-service.ts`, `registry.ts`, `openai-compatible-client.ts`, `types.ts`, `model-service.ts`

### Secondary (MEDIUM confidence)
- Phase 5/6 CONTEXT.md patterns for route-service-provider chain

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - no new dependencies, all existing infrastructure
- Architecture: HIGH - follows established Phase 5 patterns exactly, all source files examined
- Pitfalls: HIGH - verified against OpenAI spec, identified specific schema discrepancies in current code

**Research date:** 2026-03-18
**Valid until:** 2026-04-18 (stable patterns, OpenAI spec changes slowly)
