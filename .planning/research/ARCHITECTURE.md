# Architecture Patterns

**Domain:** OpenAI-compatible API proxy with dual API surfaces
**Researched:** 2026-03-16

## Recommended Architecture

### Dual-Surface Separation via Fastify Encapsulated Plugins

Use Fastify's `register()` with `prefix` to create two isolated plugin scopes. Each scope gets its own middleware chain, schema validation, error formatting, and serialization hooks. Shared business logic lives in the domain layer (unchanged), and each surface has a thin adapter that maps to/from its own request/response shapes.

```
                        createApp()
                            |
                +-----------+-----------+
                |                       |
        register(publicApi,       register(webApi,
          { prefix: '/v1' })        { prefix: '/web' })
                |                       |
        [OpenAI auth hook]        [Guest/session auth hook]
        [OpenAI schema validation] [Proprietary schema validation]
        [OpenAI error formatter]   [Proprietary error formatter]
        [OpenAI serializer]        [Proprietary serializer]
                |                       |
        Public routes:              Web routes:
        /chat/completions           /chat
        /models                     /chat/sessions
        /images/generations         /guest/chat
        /embeddings                 /guest/sessions
        /responses                  /billing/*
                                    /usage, /analytics, etc.
```

### Why Fastify Plugins (Not Separate Servers)

Fastify plugins with `prefix` provide **encapsulated contexts** -- decorators, hooks, and schemas registered inside a plugin scope do not leak to sibling scopes. This gives full isolation without the operational cost of two servers. The encapsulation is a core Fastify design pattern, not a workaround.

Key property: hooks registered inside `register(fn, { prefix })` only fire for routes within that prefix. This means the OpenAI auth hook (Bearer token -> API key resolution) never runs on web routes, and the guest token hook never runs on public API routes.

## Component Boundaries

| Component | Responsibility | Communicates With |
|-----------|---------------|-------------------|
| `public-api/` plugin | OpenAI-compatible surface: auth, validation, serialization, error format | Domain services via `RuntimeServices` |
| `web-api/` plugin | Proprietary web surface: guest auth, session auth, own shapes | Domain services via `RuntimeServices` |
| `domain/` (existing) | Pure business logic: credits, routing, model resolution, usage | Providers, stores (unchanged) |
| `schemas/openai/` | OpenAI request/response JSON schemas (Typebox or Zod) | Public API plugin only |
| `schemas/web/` | Proprietary request/response schemas | Web API plugin only |
| `transforms/` | Adapters between domain results and surface-specific shapes | Both plugins import relevant transforms |

### Boundary Rule

The domain layer MUST NOT import from `schemas/openai/` or `schemas/web/`. Domain types are the canonical internal representation. Each surface plugin transforms domain results into its own wire format.

## Data Flow

### Public API: `/v1/chat/completions` (OpenAI-compatible)

```
Client (OpenAI SDK)
  |
  | POST /v1/chat/completions  { model, messages, temperature, ... }
  | Authorization: Bearer sk-xxx
  |
  v
[Fastify onRequest] -- public-api auth hook
  |  Reads Bearer token -> resolves API key -> sets request.principal
  |  Returns 401 { error: { message, type, code } }  (OpenAI error shape)
  |
  v
[Fastify preValidation] -- OpenAI schema validation
  |  Validates body against OpenAI ChatCompletionRequest schema
  |  Returns 400 { error: { message, type: "invalid_request_error", param, code } }
  |
  v
[Route handler]
  |  1. Rate limit check
  |  2. Normalize request: OpenAI params -> domain ChatRequest
  |  3. Call services.ai.chatCompletions(...)
  |  4. Transform result: domain ChatResult -> OpenAI ChatCompletionResponse
  |
  v
[Fastify preSerialization] -- OpenAI envelope enforcement
  |  Ensures response has: id, object, created, model, choices, usage
  |  Strips any internal fields (x-model-routed becomes header, not body field)
  |
  v
[Fastify onSend] -- OpenAI headers
  |  Sets: content-type, x-request-id, openai-processing-ms
  |
  v
Client receives OpenAI-shaped response
```

### Web Pipeline: `/web/chat` (Proprietary)

```
Browser (Next.js app)
  |
  | POST /web/chat  { modelId, messages, sessionId }
  | Cookie: supabase-auth-token=xxx  OR  x-web-guest-token + x-guest-id
  |
  v
[Fastify onRequest] -- web-api auth hook
  |  Reads session cookie OR guest token -> sets request.principal
  |  Returns 403 { error: "forbidden" }  (simple proprietary shape)
  |
  v
[Fastify preValidation] -- proprietary schema validation
  |  Validates body against web-specific schema (different field names)
  |  Returns 400 { error: "invalid request", details: [...] }
  |
  v
[Route handler]
  |  1. Rate limit check (different limits for web vs API)
  |  2. Normalize: proprietary params -> domain ChatRequest
  |  3. Call services.ai.chatCompletions(...) or guestChatCompletions(...)
  |  4. Transform: domain ChatResult -> proprietary WebChatResponse
  |     (deliberately different shape: no "object" field, no "choices" array)
  |
  v
[Fastify preSerialization] -- strip internal metadata
  |  Ensures no OpenAI-shaped fields leak into web responses
  |
  v
Browser receives proprietary response
```

### Why Different Shapes Matter

The web pipeline MUST NOT return OpenAI-compatible shapes because:
1. If web responses look like `/v1/chat/completions`, users can point OpenAI SDKs at the web endpoint and bypass API key billing.
2. The web surface needs fields OpenAI doesn't have (sessionId, guestId, billing hints) and doesn't need fields OpenAI requires (usage.prompt_tokens, choices[].logprobs).
3. Different error shapes prevent automated SDK fallback from API to web endpoints.

## Patterns to Follow

### Pattern 1: Encapsulated Plugin per Surface

**What:** Each API surface is a Fastify plugin registered with a prefix. All middleware, hooks, schemas, and error handlers are scoped to that plugin.

**When:** Always -- this is the primary architectural pattern for the separation.

**Example:**

```typescript
// src/routes/public-api/index.ts
import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../../runtime/services";

export async function publicApiPlugin(app: FastifyInstance, opts: { services: RuntimeServices }) {
  const { services } = opts;

  // Scoped hook: only fires for /v1/* routes
  app.addHook("onRequest", async (request, reply) => {
    const principal = await resolveApiKeyPrincipal(request, services);
    if (!principal) {
      return reply.code(401).send({
        error: { message: "Invalid API key", type: "authentication_error", code: null }
      });
    }
    request.principal = principal;
  });

  // Scoped error handler: OpenAI error shape
  app.setErrorHandler((error, request, reply) => {
    reply.code(error.statusCode ?? 500).send({
      error: {
        message: error.message,
        type: mapToOpenAiErrorType(error),
        param: null,
        code: null,
      }
    });
  });

  // Register routes within this scope
  app.post("/chat/completions", chatCompletionsHandler(services));
  app.get("/models", modelsHandler(services));
  app.post("/images/generations", imagesHandler(services));
  app.post("/embeddings", embeddingsHandler(services));
  app.post("/responses", responsesHandler(services));
}

// src/routes/web-api/index.ts
export async function webApiPlugin(app: FastifyInstance, opts: { services: RuntimeServices }) {
  const { services } = opts;

  // Scoped hook: session/guest auth, NOT API key
  app.addHook("onRequest", async (request, reply) => {
    const principal = await resolveWebPrincipal(request, services);
    if (!principal) {
      return reply.code(403).send({ error: "forbidden" });
    }
    request.principal = principal;
  });

  // Proprietary error shape
  app.setErrorHandler((error, request, reply) => {
    reply.code(error.statusCode ?? 500).send({ error: error.message });
  });

  app.post("/chat", webChatHandler(services));
  app.get("/chat/sessions", chatSessionsHandler(services));
  // ... other web routes
}

// src/server.ts
export function createApp() {
  const app = Fastify({ logger: true });
  const services = createRuntimeServices();

  app.register(publicApiPlugin, { prefix: "/v1", services });
  app.register(webApiPlugin, { prefix: "/web", services });
  app.get("/health", healthHandler);

  return app;
}
```

### Pattern 2: Domain Adapter per Surface

**What:** Thin adapter functions that convert between surface-specific request/response shapes and domain types. Each surface has its own adapter module.

**When:** Every route handler that calls domain services.

**Example:**

```typescript
// src/routes/public-api/adapters.ts  (OpenAI surface)
import type { DomainChatResult } from "../../domain/types";

export function toOpenAiChatResponse(result: DomainChatResult) {
  return {
    id: result.id,
    object: "chat.completion" as const,
    created: result.createdAt,
    model: result.model,
    choices: result.choices.map((c, i) => ({
      index: i,
      message: { role: c.role, content: c.content },
      finish_reason: c.finishReason,
      logprobs: null,
    })),
    usage: {
      prompt_tokens: result.usage.promptTokens,
      completion_tokens: result.usage.completionTokens,
      total_tokens: result.usage.totalTokens,
    },
  };
}

// src/routes/web-api/adapters.ts  (Web surface)
export function toWebChatResponse(result: DomainChatResult) {
  return {
    id: result.id,
    model: result.model,
    content: result.choices[0]?.content ?? "",
    creditsUsed: result.creditsConsumed,
    // No "object", no "choices" array, no "usage" token counts
  };
}
```

### Pattern 3: Schema-Driven Validation at Hook Level

**What:** Use Fastify's built-in JSON schema validation (via `schema` option on routes) for request validation. Define schemas per-surface. Use `preSerialization` hooks for response shape enforcement in development/testing.

**When:** All routes on both surfaces.

**Why Fastify's built-in over Zod middleware:** Fastify compiles JSON schemas to fast-json-stringify and ajv validators at startup. This is measurably faster than Zod-in-middleware and integrates with Fastify's error handling (automatic 400 with validation details). Use Typebox to author schemas with TypeScript types, compile to JSON Schema for Fastify.

```typescript
import { Type } from "@sinclair/typebox";

const ChatCompletionRequestSchema = Type.Object({
  model: Type.String(),
  messages: Type.Array(Type.Object({
    role: Type.Union([Type.Literal("system"), Type.Literal("user"), Type.Literal("assistant")]),
    content: Type.String(),
  })),
  temperature: Type.Optional(Type.Number({ minimum: 0, maximum: 2 })),
  max_tokens: Type.Optional(Type.Integer({ minimum: 1 })),
  stream: Type.Optional(Type.Boolean()),
  // ... other OpenAI params
});

app.post("/chat/completions", {
  schema: { body: ChatCompletionRequestSchema },
  handler: chatCompletionsHandler(services),
});
```

### Pattern 4: Streaming as a First-Class Concern

**What:** SSE streaming for chat completions must be handled differently per surface. The public API streams OpenAI-shaped chunks (`data: {"id":"...","object":"chat.completion.chunk",...}\n\n`). The web pipeline can use a simpler streaming format or different chunk shape.

**When:** Any endpoint that supports `stream: true`.

**Architecture implication:** The domain layer returns an async iterable of domain chunks. Each surface adapter converts domain chunks to its wire format. The route handler pipes the transformed stream to the response.

```typescript
// Domain returns:
async function* streamChat(...): AsyncIterable<DomainChatChunk> { ... }

// Public API adapter:
function toOpenAiSSE(chunk: DomainChatChunk): string {
  return `data: ${JSON.stringify(toOpenAiChunkShape(chunk))}\n\n`;
}

// Web adapter:
function toWebSSE(chunk: DomainChatChunk): string {
  return `data: ${JSON.stringify({ content: chunk.delta, done: chunk.isLast })}\n\n`;
}
```

## Anti-Patterns to Avoid

### Anti-Pattern 1: Shared Route Files with Conditional Formatting

**What:** A single route handler that checks "am I being called from web or API?" and conditionally formats the response.

**Why bad:** Couples both surfaces in the same code path. Every change to OpenAI compliance risks breaking the web surface and vice versa. Testing requires mocking both auth paths in every test.

**Instead:** Separate route handlers per surface. Each calls domain services independently and formats with its own adapter.

### Anti-Pattern 2: OpenAI Types in the Domain Layer

**What:** Domain services returning objects shaped like `{ object: "chat.completion", choices: [...] }`.

**Why bad:** Leaks the public API contract into shared business logic. The web surface has to either forward OpenAI shapes (security risk) or strip them (fragile). Adding a third surface (CLI, mobile) means fighting the OpenAI shape everywhere.

**Instead:** Domain types are neutral: `{ id, model, choices: [{ role, content, finishReason }], usage: { promptTokens, ... } }`. Each surface adapter converts to its wire format. Note: the current `AiService.chatCompletions()` already returns a semi-OpenAI shape (`object: "chat.completion"`). This should be neutralized during the refactor.

### Anti-Pattern 3: Prefix-Only Separation Without Hook Isolation

**What:** Registering all routes on the root Fastify instance with different URL prefixes but shared hooks.

**Why bad:** A global `onRequest` hook fires for ALL routes. If the OpenAI auth hook is global, it runs on web routes too (confusing errors). If the error handler is global, one surface's error format leaks to the other.

**Instead:** Use `app.register()` to create encapsulated scopes. Hooks inside a registered plugin only fire for that plugin's routes.

### Anti-Pattern 4: Response Transformation in onSend

**What:** Using Fastify's `onSend` hook to rewrite the response body after the route handler returns.

**Why bad:** `onSend` receives the serialized string/Buffer, not the object. Parsing it back to transform is wasteful and error-prone. `preSerialization` receives the object but runs after the handler -- still indirect.

**Instead:** Transform in the route handler itself (or a thin wrapper). The handler owns the response shape. Use `preSerialization` only for assertion/validation in dev mode, not for transformation.

## Directory Structure (Target)

```
apps/api/src/
  routes/
    index.ts                    # Registers publicApiPlugin + webApiPlugin + health
    public-api/
      index.ts                  # Plugin: auth hook, error handler, route registration
      chat-completions.ts       # /v1/chat/completions handler
      models.ts                 # /v1/models handler
      images-generations.ts     # /v1/images/generations handler
      embeddings.ts             # /v1/embeddings handler
      responses.ts              # /v1/responses handler
      adapters.ts               # Domain -> OpenAI response transforms
      schemas.ts                # Typebox schemas for OpenAI request validation
      errors.ts                 # OpenAI error shape formatting
      auth.ts                   # API key / Bearer token resolution
    web-api/
      index.ts                  # Plugin: auth hook, error handler, route registration
      chat.ts                   # /web/chat handler
      guest-chat.ts             # /web/guest/chat handler
      chat-sessions.ts          # /web/chat/sessions handler
      guest-sessions.ts         # /web/guest/sessions handler
      billing.ts                # /web/billing/* handlers
      usage.ts                  # /web/usage handler
      adapters.ts               # Domain -> proprietary response transforms
      schemas.ts                # Typebox schemas for web request validation
      errors.ts                 # Proprietary error formatting
      auth.ts                   # Session cookie / guest token resolution
    shared/
      rate-limit.ts             # Shared rate limiting helper (used by both plugins)
  schemas/
    openai/
      chat-completion.ts        # OpenAI ChatCompletion request/response Typebox schemas
      models.ts                 # OpenAI Models list schema
      images.ts                 # OpenAI Images schema
      errors.ts                 # OpenAI error object schema
      common.ts                 # Shared OpenAI types (usage, etc.)
    web/
      chat.ts                   # Proprietary chat schemas
      sessions.ts               # Proprietary session schemas
```

## Migration Strategy: Current to Target

The current codebase registers all routes flatly in `routes/index.ts`. The migration should be incremental:

### Step 1: Create Plugin Shells (no route changes)

Create `public-api/index.ts` and `web-api/index.ts` as empty plugins. Update `server.ts` to register them. Move existing route registrations into the appropriate plugin. All existing behavior preserved -- just reorganized.

### Step 2: Scope Auth Hooks

Move `requireApiPrincipal` logic into `public-api/auth.ts` as a scoped `onRequest` hook. Move guest token validation into `web-api/auth.ts` as a scoped `onRequest` hook. Remove per-route auth calls from handlers.

### Step 3: Add OpenAI Schema Validation

Define Typebox schemas for each public API endpoint. Add `schema` option to route definitions. Add OpenAI-shaped error handler to public API plugin.

### Step 4: Neutralize Domain Types

Refactor `AiService` return types to be surface-neutral (remove `object: "chat.completion"` etc.). Create `public-api/adapters.ts` to convert domain results to OpenAI shapes. Create `web-api/adapters.ts` to convert domain results to proprietary shapes.

### Step 5: Differentiate Web Response Shapes

Change web route responses to use deliberately different field names and structure. Verify no OpenAI SDK can accidentally parse web responses.

## Scalability Considerations

| Concern | Current (100 users) | 10K users | 1M users |
|---------|---------------------|-----------|----------|
| Route isolation | Fastify plugins are zero-overhead (resolved at startup) | Same | Same |
| Schema validation | Ajv compilation at startup, per-request cost is ~microseconds | Same | Same |
| Streaming | Single-process SSE works | Need sticky sessions or switch to multi-process with Redis pub/sub for stream fan-out | Consider dedicated streaming workers |
| Auth resolution | Per-request DB lookup for API keys | Cache hot keys in Redis/LRU | API key cache with TTL + event invalidation |
| Rate limiting | In-memory sliding window | Redis-backed (already supported) | Redis cluster or token bucket with sliding window |

## Build Order (Dependencies)

```
1. Plugin shell + route reorganization
   (no behavior change, just structure)
   |
2. Scoped auth hooks per surface
   (depends on: plugin shells exist)
   |
3. OpenAI schema definitions (Typebox)
   (can parallel with step 2, no runtime dependency)
   |
4. Domain type neutralization
   (depends on: understanding current domain shapes)
   |
5. Surface-specific adapters
   (depends on: neutral domain types + OpenAI schemas)
   |
6. OpenAI error formatting
   (depends on: scoped error handlers from step 2)
   |
7. Web response shape differentiation
   (depends on: web adapter from step 5)
   |
8. Streaming pipeline per surface
   (depends on: adapters from step 5, can be parallel with 6-7)
   |
9. preSerialization assertions (dev mode)
   (depends on: schemas from step 3, nice-to-have)
```

Steps 1-2 are structural prerequisites. Steps 3-4 can run in parallel. Steps 5-8 depend on both 3 and 4. Step 9 is optional hardening.

## Sources

- Fastify encapsulation model: core framework design since v1, plugins inherit parent context but siblings are isolated. This is the documented way to scope hooks and decorators. (HIGH confidence -- core Fastify architecture)
- Typebox + Fastify integration: `@sinclair/typebox` is the Fastify team's recommended schema library, compiles to JSON Schema for ajv validation and fast-json-stringify. (HIGH confidence -- official Fastify ecosystem)
- OpenAI API error format: `{ error: { message, type, param, code } }` is the standard error envelope across all OpenAI endpoints. (HIGH confidence -- direct OpenAI API documentation)
- SSE streaming format: `data: {json}\n\n` with `[DONE]` sentinel is the OpenAI streaming convention adopted by all compatible proxies. (HIGH confidence -- OpenAI API documentation)
- Dual-surface proxy pattern: LiteLLM, OpenRouter, and similar proxies use adapter layers to normalize provider responses to OpenAI format while keeping internal representations neutral. (MEDIUM confidence -- training data pattern, not verified against current sources)
