# Phase 1: Error Format Standardization - Research

**Researched:** 2026-03-17
**Domain:** OpenAI-compatible error response formatting, Fastify plugin scoping
**Confidence:** HIGH

## Summary

Phase 1 converts all `/v1/*` error responses from the current flat format (`{ error: "string" }`) to the OpenAI-compliant nested format (`{ error: { message, type, param, code } }`). This is the single most visible SDK incompatibility -- every error currently crashes official OpenAI SDKs because they access `error.message`, `error.type`, etc. on what is actually a plain string.

The implementation uses Fastify's encapsulated plugin system: a plugin wraps all `/v1/*` routes and sets its own `setErrorHandler` and `setNotFoundHandler` to catch and reformat errors. A `sendApiError()` helper function provides explicit error sends in route handlers. The domain layer (`services.ai`) is NOT changed -- only the route layer maps domain errors to OpenAI format.

**Primary recommendation:** Create an encapsulated Fastify plugin that scopes error handling to `/v1/*` routes without changing route path strings. Use a plain helper function (not a class) for `sendApiError`. All four fields (`message`, `type`, `param`, `code`) must always be present in the response, even when `null`.

<user_constraints>

## User Constraints (from CONTEXT.md)

### Locked Decisions
- Fastify encapsulated plugin registered at `/v1` prefix with its own `setErrorHandler` and `setNotFoundHandler`
- All `/v1/*` routes are registered inside this plugin scope -- web pipeline routes stay outside and are unaffected
- A helper function `sendApiError(reply, status, message, opts?)` is available for explicit error sends in route handlers
- The scoped `setErrorHandler` acts as safety net -- any uncaught throw or Fastify-native error within `/v1/*` gets reformatted automatically
- Domain layer (`services.ai`) continues returning `{ error: string, statusCode: number }` -- no changes to domain types
- Route layer maps `statusCode` to OpenAI `type` field: 400=invalid_request_error, 401=authentication_error, 403=permission_error, 404=not_found_error, 429=rate_limit_error, 500+=server_error
- `param` field: Populated where routes explicitly check for missing fields, `null` otherwise
- `code` field: Small predefined set (`invalid_api_key`, `rate_limit_exceeded`, `model_not_found`, `invalid_request_error`, `null` for 500s)
- `type` field: Always populated via statusCode mapping (never null)
- Phase 1 handles Fastify's own errors within `/v1/*` scope: malformed JSON body, unknown routes, validation errors
- Scope: `/v1/chat/completions`, `/v1/models`, `/v1/images/generations`, `/v1/responses` -- yes; web pipeline routes -- NO

### Claude's Discretion
- Exact file/module name for the error helper and plugin
- Whether to use a class or plain function for error construction
- Test strategy and test file organization
- Whether to export a custom error class or just use the helper function

### Deferred Ideas (OUT OF SCOPE)
None -- discussion stayed within phase scope

</user_constraints>

<phase_requirements>

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| FOUND-01 | All API error responses use OpenAI error format `{ error: { message, type, param, code } }` with correct status-to-type mapping | Error helper function + scoped error handler + route-by-route migration of `reply.code().send()` calls |

</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| fastify | ^5.1.0 (installed) | Web framework with encapsulated plugins | Already in use; plugin scoping is the mechanism for isolated error handling |
| vitest | (workspace dep) | Test framework | Already used for all existing tests in `apps/api/test/` |

### Supporting
No new dependencies required. This phase is pure refactoring of error response shapes using existing Fastify APIs.

**Installation:**
```bash
# No new packages needed
```

## Architecture Patterns

### Recommended Project Structure
```
apps/api/src/
├── routes/
│   ├── api-error.ts          # sendApiError helper + statusCode-to-type mapping
│   ├── v1-plugin.ts          # Fastify plugin that scopes error handling for /v1/*
│   ├── index.ts              # Updated to register v1 routes inside plugin
│   ├── auth.ts               # Updated error sends to use sendApiError
│   ├── chat-completions.ts   # Updated error sends
│   ├── models.ts             # Updated error sends
│   ├── images-generations.ts # Updated error sends
│   └── responses.ts          # Updated error sends
apps/api/test/
├── routes/
│   └── api-error-format.test.ts  # New: tests for error format compliance
```

### Pattern 1: Error Helper Function
**What:** A pure function that formats and sends an OpenAI-compliant error response.
**When to use:** Every explicit error send in `/v1/*` route handlers.
**Example:**
```typescript
// apps/api/src/routes/api-error.ts

type OpenAIErrorType =
  | "invalid_request_error"
  | "authentication_error"
  | "permission_error"
  | "not_found_error"
  | "rate_limit_error"
  | "server_error";

type ApiErrorOpts = {
  type?: OpenAIErrorType;
  param?: string | null;
  code?: string | null;
};

const STATUS_TO_TYPE: Record<number, OpenAIErrorType> = {
  400: "invalid_request_error",
  401: "authentication_error",
  403: "permission_error",
  404: "not_found_error",
  429: "rate_limit_error",
};

function resolveType(status: number, opts?: ApiErrorOpts): OpenAIErrorType {
  return opts?.type ?? STATUS_TO_TYPE[status] ?? "server_error";
}

export function sendApiError(
  reply: FastifyReply,
  status: number,
  message: string,
  opts?: ApiErrorOpts,
): void {
  reply.code(status).send({
    error: {
      message,
      type: resolveType(status, opts),
      param: opts?.param ?? null,
      code: opts?.code ?? null,
    },
  });
}
```

### Pattern 2: Scoped Error Handler Plugin
**What:** A Fastify plugin that wraps all `/v1/*` routes and catches any unhandled errors, reformatting them to OpenAI shape.
**When to use:** Registered once in server setup. All `/v1/*` route registrations happen inside this plugin.
**Example:**
```typescript
// apps/api/src/routes/v1-plugin.ts
import type { FastifyInstance } from "fastify";
import type { RuntimeServices } from "../runtime/services";

export async function v1Plugin(app: FastifyInstance, opts: { services: RuntimeServices }) {
  const { services } = opts;

  // Safety net: reformat any uncaught error to OpenAI shape
  app.setErrorHandler((error, request, reply) => {
    const status = error.statusCode ?? 500;
    const message = error.message || "Internal server error";
    reply.code(status).send({
      error: {
        message,
        type: STATUS_TO_TYPE[status] ?? "server_error",
        param: null,
        code: null,
      },
    });
  });

  // Handle unknown /v1/* routes
  app.setNotFoundHandler((request, reply) => {
    reply.code(404).send({
      error: {
        message: `Unknown API route: ${request.method} ${request.url}`,
        type: "not_found_error",
        param: null,
        code: null,
      },
    });
  });

  // Register all /v1/* routes inside this scope
  registerChatCompletionsRoute(app, services);
  registerModelsRoute(app, services);
  registerImagesGenerationsRoute(app, services);
  registerResponsesRoute(app, services);
}
```

### Pattern 3: Route-Layer Domain Error Mapping
**What:** Converting domain `{ error: string, statusCode: number }` to OpenAI format at the route layer.
**When to use:** Every route handler that checks `"error" in result`.
**Example:**
```typescript
// In a route handler:
const result = await services.ai.chatCompletions(...);
if ("error" in result) {
  return sendApiError(reply, result.statusCode, result.error, {
    code: result.statusCode === 404 ? "model_not_found" : undefined,
  });
}
```

### Pattern 4: Plugin Registration Without Prefix Change
**What:** Register the v1 plugin without a `prefix` option since routes already include `/v1/` in their paths.
**When to use:** In `server.ts` or `routes/index.ts`.
**Critical detail:** Current routes already hardcode `/v1/` in their path strings (e.g., `app.post("/v1/chat/completions", ...)`). Using `register` with `prefix: "/v1"` would double the prefix to `/v1/v1/...`. Instead, register the plugin without a prefix -- the encapsulation still provides scoped error handling.
```typescript
// In routes/index.ts
export function registerRoutes(app: FastifyInstance, services: RuntimeServices): void {
  // /v1/* routes inside encapsulated plugin (gets scoped error handler)
  void app.register(v1Plugin, { services });

  // Non-v1 routes stay outside (keep flat error format)
  registerHealthRoute(app);
  registerGuestChatRoute(app, services);
  // ... other web pipeline routes
}
```

### Anti-Patterns to Avoid
- **Global error handler for OpenAI format:** Do NOT use `app.setErrorHandler` at root level -- it would affect web pipeline routes that should keep `{ error: "string" }` format.
- **Changing domain layer types:** Do NOT modify `services.ai` to return OpenAI-shaped errors. The mapping belongs in the route layer.
- **Omitting null fields:** Do NOT omit `param` or `code` when they are null. The OpenAI SDK accesses all four fields -- omitting them causes `undefined` access. Always send all four fields.
- **Using prefix option on plugin:** Do NOT use `app.register(v1Plugin, { prefix: '/v1' })` because routes already include `/v1/` in their paths. This would create `/v1/v1/...` paths.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Plugin-scoped error handling | Custom middleware chain | Fastify `setErrorHandler` inside `app.register()` | Fastify's encapsulation guarantees scope isolation; custom middleware is fragile |
| JSON parse error catching | try/catch around body parsing | Fastify's built-in content-type parser errors caught by `setErrorHandler` | Fastify already parses JSON and throws with `statusCode: 400` |
| 404 for unknown routes | Manual route matching | Fastify `setNotFoundHandler` inside the plugin | Fastify handles this natively with proper encapsulation |

**Key insight:** Fastify's plugin encapsulation is the right tool for scoping error format changes to `/v1/*` routes. The error handler + not-found handler inside a registered plugin automatically catch all errors within that scope.

## Common Pitfalls

### Pitfall 1: Missing Fields Crash OpenAI SDK
**What goes wrong:** Omitting `param` or `code` (instead of sending `null`) causes `undefined` access in the OpenAI Node SDK when parsing errors.
**Why it happens:** Developers think optional means omittable. The SDK expects all four fields to exist.
**How to avoid:** Always include all four fields: `message`, `type`, `param`, `code`. Use `null` for absent values, never `undefined` or omission.
**Warning signs:** SDK throws generic `TypeError` instead of typed `AuthenticationError`, `BadRequestError`, etc.

### Pitfall 2: Auth Errors Sent Before Plugin Scope
**What goes wrong:** If `requireApiPrincipal()` sends errors using `reply.code(401).send({ error: "string" })` and auth runs before the plugin's error handler can intercept, errors bypass formatting.
**Why it happens:** Auth is called inside route handlers (not as a Fastify hook), so the reply is sent directly. The `setErrorHandler` only catches thrown errors, not explicit `reply.send()` calls.
**How to avoid:** Update `requirePrincipal()` in `auth.ts` to use `sendApiError()` for 401/403 responses. Since auth is called from within route handlers (inside the plugin scope), the helper will format correctly.
**Warning signs:** 401 responses still have `{ error: "string" }` format.

### Pitfall 3: Fastify Error Object Shape
**What goes wrong:** Fastify's error objects in `setErrorHandler` have `error.statusCode` (number), `error.message` (string), and optionally `error.validation` (array). Accessing wrong properties gives undefined.
**Why it happens:** Fastify errors are not standard `Error` objects -- they extend `Error` with Fastify-specific fields.
**How to avoid:** In the error handler, use `error.statusCode ?? 500` for status and `error.message` for the message. Check `error.validation` to detect Fastify schema validation errors.
**Warning signs:** All errors return 500 because `error.status` (wrong property) is undefined.

### Pitfall 4: Double Prefix on Plugin Registration
**What goes wrong:** Routes register at `/v1/v1/chat/completions` instead of `/v1/chat/completions`.
**Why it happens:** Using `app.register(plugin, { prefix: '/v1' })` when route handlers already include `/v1/` in their path strings.
**How to avoid:** Register the plugin without a prefix option. Routes already contain `/v1/` in their paths.
**Warning signs:** All `/v1/*` routes return 404.

### Pitfall 5: Web Pipeline Routes Getting OpenAI Error Format
**What goes wrong:** Guest chat, payment webhook, analytics routes start returning `{ error: { message, type, param, code } }` instead of `{ error: "string" }`.
**Why it happens:** These routes were accidentally registered inside the v1 plugin scope.
**How to avoid:** Explicitly separate route registration: v1 API routes inside the plugin, web pipeline routes outside. Review `routes/index.ts` carefully.
**Warning signs:** Frontend error handling breaks because it expects `response.error` to be a string.

## Code Examples

### Auth Error Migration
```typescript
// BEFORE (auth.ts:115)
reply.code(401).send({ error: "missing or invalid credentials" });

// AFTER
sendApiError(reply, 401, "missing or invalid credentials", {
  code: "invalid_api_key",
});
// Produces: { error: { message: "missing or invalid credentials", type: "authentication_error", param: null, code: "invalid_api_key" } }
```

### Route Domain Error Migration
```typescript
// BEFORE (chat-completions.ts:32)
return reply.code(result.statusCode).send({ error: result.error });

// AFTER
return sendApiError(reply, result.statusCode, result.error);
// Automatically maps statusCode to type via STATUS_TO_TYPE lookup
```

### Rate Limit Error Migration
```typescript
// BEFORE (chat-completions.ts:19)
return reply.code(429).send({ error: "rate limit exceeded" });

// AFTER
return sendApiError(reply, 429, "rate limit exceeded", {
  code: "rate_limit_exceeded",
});
```

### Fastify Error Handler Catching Malformed JSON
```typescript
// Fastify throws this automatically when JSON parsing fails:
// { statusCode: 400, message: "Unexpected token ...", code: "FST_ERR_CTP_INVALID_MEDIA_TYPE" }

// The scoped setErrorHandler catches it and reformats:
// { error: { message: "Unexpected token ...", type: "invalid_request_error", param: null, code: null } }
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Flat error `{ error: "string" }` | Nested `{ error: { message, type, param, code } }` | OpenAI API v1 (2023) | All official SDKs expect nested format |
| Global error handler | Encapsulated plugin error handler | Fastify 3+ (2020) | Allows different error formats per route group |

**Deprecated/outdated:**
- Flat error strings: Never compliant with OpenAI SDK expectations. Must be replaced.

## Open Questions

1. **402 status code mapping**
   - What we know: Hive uses 402 for "insufficient credits". OpenAI uses `type: "insufficient_quota"` for billing errors, but returns 429 not 402.
   - What's unclear: Should Hive change 402 to 429, or keep 402 with a custom type?
   - Recommendation: Keep 402 status code (it is semantically correct) but map to `type: "insufficient_quota"` and `code: "insufficient_quota"`. Add this to the STATUS_TO_TYPE map.

2. **Non-API `/v1/*` routes (internal routes)**
   - What we know: Routes like `/v1/analytics/internal`, `/v1/providers/status/internal`, `/v1/internal/guest/*`, `/v1/users/*`, `/v1/credits/balance` also use the `/v1/` prefix but are web pipeline routes.
   - What's unclear: Should these be inside or outside the error-formatting plugin?
   - Recommendation: Only the four OpenAI-facing routes (`chat/completions`, `models`, `images/generations`, `responses`) should be inside the plugin. All other `/v1/*` routes are internal/web routes and stay outside. The plugin does NOT use a `/v1` prefix -- it just scopes those four route registrations.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | vitest (workspace dependency) |
| Config file | Inherited from workspace (pnpm workspace) |
| Quick run command | `pnpm --filter @hive/api test` |
| Full suite command | `pnpm test` |

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| FOUND-01 | 400 errors return `{ error: { message, type: "invalid_request_error", param, code } }` | unit | `pnpm --filter @hive/api test -- api-error-format` | No -- Wave 0 |
| FOUND-01 | 401 auth errors return `type: "authentication_error"` with `code: "invalid_api_key"` | unit | `pnpm --filter @hive/api test -- api-error-format` | No -- Wave 0 |
| FOUND-01 | Fastify malformed JSON returns OpenAI error format | unit | `pnpm --filter @hive/api test -- api-error-format` | No -- Wave 0 |
| FOUND-01 | Unknown `/v1/*` route returns 404 with OpenAI error format | unit | `pnpm --filter @hive/api test -- api-error-format` | No -- Wave 0 |
| FOUND-01 | All four fields (message, type, param, code) always present | unit | `pnpm --filter @hive/api test -- api-error-format` | No -- Wave 0 |
| FOUND-01 | Web pipeline routes still return flat error format | unit | `pnpm --filter @hive/api test -- api-error-format` | No -- Wave 0 |

### Sampling Rate
- **Per task commit:** `pnpm --filter @hive/api test`
- **Per wave merge:** `pnpm test`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `apps/api/test/routes/api-error-format.test.ts` -- covers FOUND-01 (all error format assertions)
- [ ] Test helpers for creating Fastify instances with the v1 plugin registered (may extend existing `FakeApp` pattern or use `fastify.inject()`)

## Sources

### Primary (HIGH confidence)
- Codebase analysis: `apps/api/src/routes/auth.ts` -- current flat error pattern at lines 115, 124, 132
- Codebase analysis: `apps/api/src/routes/chat-completions.ts` -- domain error forwarding at line 32
- Codebase analysis: `apps/api/src/routes/index.ts` -- route registration structure
- Codebase analysis: `apps/api/src/server.ts` -- Fastify instance creation
- `.planning/research/PITFALLS.md` -- Pitfall 1 (error shape), Pitfall 12 (Fastify built-in errors)
- `.planning/phases/01-error-format/1-CONTEXT.md` -- locked implementation decisions

### Secondary (MEDIUM confidence)
- [Fastify error handler encapsulation](https://github.com/fastify/fastify/issues/1079) -- confirmed scoped error handlers work within registered plugins
- [Fastify server docs](https://fastify.dev/docs/latest/Reference/Server/) -- `setErrorHandler`, `setNotFoundHandler` API
- [OpenAI error codes guide](https://platform.openai.com/docs/guides/error-codes) -- canonical error type/code values
- [OpenAI Node SDK](https://github.com/openai/openai-node) -- APIError class parses `error.message`, `error.type`, `error.param`, `error.code`

### Tertiary (LOW confidence)
- OpenAI Node SDK error parsing internals -- could not access source directly, but the error shape is well-documented in OpenAI's API reference and the local OpenAPI spec

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH -- no new dependencies, all Fastify built-in APIs
- Architecture: HIGH -- Fastify plugin encapsulation is well-documented and the CONTEXT.md decisions are specific
- Pitfalls: HIGH -- verified against codebase with line references and cross-referenced with PITFALLS.md research

**Research date:** 2026-03-17
**Valid until:** 2026-04-17 (stable domain, no fast-moving dependencies)
