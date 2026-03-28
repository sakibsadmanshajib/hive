# Phase 2: Type Infrastructure - Research

**Researched:** 2026-03-17
**Domain:** TypeBox + Fastify type provider, openapi-typescript code generation
**Confidence:** HIGH

## Summary

This phase establishes two parallel type safety layers: (1) runtime request validation via TypeBox schemas wired into Fastify's native validation pipeline, and (2) compile-time response shape checking via TypeScript types generated from the OpenAI OpenAPI spec. The project uses Fastify 5.1.0 with vitest for testing.

The critical discovery is that `@fastify/type-provider-typebox@6` requires the new `typebox@1.x` package (not `@sinclair/typebox`), which uses subpath exports (`typebox/type`, `typebox/value`). Additionally, Fastify's default AJV config includes `removeAdditional: true`, which silently strips extra fields instead of rejecting them -- this MUST be overridden to `false` to satisfy the strict-rejection requirement.

**Primary recommendation:** Use `typebox@1.1.6` + `@fastify/type-provider-typebox@6.1.0` for runtime validation, `openapi-typescript@7.13.0` for compile-time types, and override Fastify's AJV `removeAdditional` to `false`.

<user_constraints>

## User Constraints (from CONTEXT.md)

### Locked Decisions
- Run `openapi-typescript` once, commit generated output to repo (e.g., `apps/api/src/types/openai.d.ts`)
- Regenerate manually when spec changes -- not a build-time or CI step
- Update `docs/reference/openai-openapi.yml` to latest version before generating types
- Wire TypeBox schemas on ALL four v1 routes: `chat-completions`, `models`, `images-generations`, `responses`
- Strict rejection: Fastify AJV returns 400 for unknown/extra fields on all v1 routes
- Use `additionalProperties: false` on all TypeBox schemas
- Error response must use Phase 1 format: `{ error: { message, type, param, code } }` -- already handled by v1Plugin's scoped error handler

### Claude's Discretion
- Exact package versions for `typebox`, `@fastify/type-provider-typebox`, `openapi-typescript`
- File location for generated types (within `apps/api/src/`)
- Whether to use `@fastify/type-provider-typebox` or `TypeBoxTypeProvider` directly
- TypeBox schema granularity (required vs optional fields on request schemas -- follow OpenAI spec)

### Deferred Ideas (OUT OF SCOPE)
- None

</user_constraints>

<phase_requirements>

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| FOUND-06 | TypeBox + Fastify type provider set up for request validation on all `/v1/*` routes | TypeBox v1 + type-provider-typebox v6 wired at app creation; schemas on all 4 routes with `additionalProperties: false`; AJV `removeAdditional: false` override |
| FOUND-07 | OpenAI TypeScript types generated from `docs/reference/openai-openapi.yml` via `openapi-typescript` for compile-time response shape validation | `openapi-typescript@7.13.0` CLI generates `.d.ts` from local YAML; committed to repo at `apps/api/src/types/openai.d.ts` |

</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `typebox` | 1.1.6 | JSON Schema type builder with static TS inference | New official package (replaces `@sinclair/typebox`); required by type-provider v6 |
| `@fastify/type-provider-typebox` | 6.1.0 | Fastify plugin that infers route types from TypeBox schemas | Official Fastify type provider; auto-infers `request.body` types from schema |
| `openapi-typescript` | 7.13.0 | Generate TS types from OpenAPI 3.x specs | De-facto standard for OpenAPI-to-TS; supports OpenAPI 3.1.0 |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `typescript` | ^5.x | Required peer dep for openapi-typescript | Already in project |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `typebox@1.x` | `@sinclair/typebox@0.34` + type-provider v5.2 | Old ecosystem; v5 will stop getting updates. v6 is the maintained path |
| `openapi-typescript` | `openapi-generator` | openapi-generator produces runtime code, not just types; overkill for type-only generation |

**Installation:**
```bash
cd apps/api
npm install typebox@1.1.6 @fastify/type-provider-typebox@6.1.0
npm install -D openapi-typescript@7.13.0
```

## Architecture Patterns

### Recommended Project Structure
```
apps/api/src/
  types/
    openai.d.ts          # Generated once from openapi-typescript, committed
  schemas/
    chat-completions.ts  # TypeBox request body schema
    images-generations.ts # TypeBox request body schema
    responses.ts         # TypeBox request body schema
    models.ts            # TypeBox params schema (for GET /v1/models/{model} later)
  routes/
    v1-plugin.ts         # Type provider registration + AJV config
    chat-completions.ts  # Route handler using schema
    models.ts            # Route handler
    images-generations.ts # Route handler using schema
    responses.ts         # Route handler using schema
```

### Pattern 1: Type Provider Registration
**What:** Wire TypeBoxTypeProvider at the Fastify instance level so all v1 routes inherit typed schemas.
**When to use:** At app creation or within the v1Plugin scope.
**Example:**
```typescript
// In server.ts - modify createApp()
import Fastify from 'fastify';
import { TypeBoxTypeProvider } from '@fastify/type-provider-typebox';

export function createApp() {
  const app = Fastify({
    logger: true,
    ajv: {
      customOptions: {
        // CRITICAL: Override default removeAdditional: true
        // Without this, extra fields are silently stripped instead of rejected
        removeAdditional: false,
      },
    },
  }).withTypeProvider<TypeBoxTypeProvider>();

  // ... rest of setup
  return app;
}
```

### Pattern 2: TypeBox Schema Definition with Strict Rejection
**What:** Define request body schemas using TypeBox Type.Object with additionalProperties: false.
**When to use:** For every v1 route that accepts a request body.
**Example:**
```typescript
// apps/api/src/schemas/chat-completions.ts
import { Type, type Static } from 'typebox/type';

export const ChatCompletionsBodySchema = Type.Object({
  model: Type.Optional(Type.String()),
  messages: Type.Optional(Type.Array(Type.Object({
    role: Type.String(),
    content: Type.String(),
  }, { additionalProperties: false }))),
}, { additionalProperties: false });

export type ChatCompletionsBody = Static<typeof ChatCompletionsBodySchema>;
```

### Pattern 3: Route with TypeBox Schema
**What:** Register routes with schema object so Fastify validates automatically.
**When to use:** Every v1 route handler.
**Example:**
```typescript
// apps/api/src/routes/chat-completions.ts
import type { FastifyInstance } from 'fastify';
import type { TypeBoxTypeProvider } from '@fastify/type-provider-typebox';
import { ChatCompletionsBodySchema } from '../schemas/chat-completions';

export function registerChatCompletionsRoute(
  app: FastifyInstance,  // typed via withTypeProvider at creation
  services: RuntimeServices,
): void {
  app.post('/v1/chat/completions', {
    schema: {
      body: ChatCompletionsBodySchema,
    },
  }, async (request, reply) => {
    // request.body is now typed as Static<typeof ChatCompletionsBodySchema>
    const model = request.body.model; // string | undefined - fully typed
    // ...
  });
}
```

### Pattern 4: Type Generation Command
**What:** One-time generation of TypeScript types from OpenAI spec.
**When to use:** When setting up or updating OpenAI types.
**Example:**
```bash
# First update the spec
curl -o docs/reference/openai-openapi.yml \
  https://raw.githubusercontent.com/openai/openai-openapi/master/openapi.yaml

# Then generate types
npx openapi-typescript docs/reference/openai-openapi.yml \
  -o apps/api/src/types/openai.d.ts
```

### Anti-Patterns to Avoid
- **Setting removeAdditional: true (Fastify default):** This silently strips unknown fields instead of rejecting them. The user explicitly wants 400 errors for extra fields.
- **Inline type generics instead of schema objects:** `app.post<{ Body: X }>()` gives NO runtime validation. Must use `schema: { body: ... }` for Fastify to validate.
- **Forgetting additionalProperties on nested objects:** TypeBox only applies `additionalProperties: false` at the level you specify. Nested `Type.Object()` calls need it too.
- **Using `@sinclair/typebox` with type-provider v6:** Version mismatch. v6 requires `typebox@^1.0.13` (the new package name).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Request body validation | Manual if/throw checks | Fastify schema + TypeBox | AJV is battle-tested, handles coercion, nested objects, arrays |
| OpenAI type definitions | Manual TS interfaces | openapi-typescript generation | 73K line spec; manual types will drift and miss fields |
| Type provider wiring | Custom generics per route | @fastify/type-provider-typebox | Automatic inference from schema to handler types |
| Validation error formatting | Custom error middleware | Fastify's built-in error handler (already done in Phase 1) | v1Plugin error handler already converts AJV errors to OpenAI format |

**Key insight:** Fastify's error handler in v1Plugin already catches validation errors and formats them as OpenAI error objects. AJV validation failures throw `FST_ERR_VALIDATION` errors with statusCode 400, which the existing error handler converts to `{ error: { message, type: "invalid_request_error", ... } }`. No additional error handling code needed.

## Common Pitfalls

### Pitfall 1: Fastify Default removeAdditional Silently Strips Fields
**What goes wrong:** Extra fields in request bodies are silently removed instead of causing a 400 error.
**Why it happens:** Fastify's default AJV configuration sets `removeAdditional: true`.
**How to avoid:** Set `ajv: { customOptions: { removeAdditional: false } }` in the Fastify constructor.
**Warning signs:** Tests pass for valid requests but unknown-field-rejection tests never fail.

### Pitfall 2: TypeBox v1 Import Path Change
**What goes wrong:** `import { Type } from '@sinclair/typebox'` fails or resolves wrong version.
**Why it happens:** `typebox@1.x` is a new package with subpath exports: `typebox/type`, `typebox/value`.
**How to avoid:** Use `import { Type, type Static } from 'typebox/type'`.
**Warning signs:** "Module not found" or type mismatch errors at compile time.

### Pitfall 3: additionalProperties Only Applies at Declared Level
**What goes wrong:** Nested objects accept extra fields even though the top-level schema rejects them.
**Why it happens:** JSON Schema (and TypeBox) `additionalProperties: false` only applies to the object level where it is declared.
**How to avoid:** Add `{ additionalProperties: false }` to EVERY `Type.Object()` call at every nesting level.
**Warning signs:** Tests for nested unknown fields pass validation when they should fail.

### Pitfall 4: AJV Config Scope - Global vs Scoped
**What goes wrong:** AJV customOptions set in a child plugin don't take effect.
**Why it happens:** Fastify's AJV instance is created at app construction time and shared. You cannot change AJV options per-plugin.
**How to avoid:** Set `ajv.customOptions` in the `Fastify()` constructor call, before any plugins are registered.
**Warning signs:** Validation behavior differs between direct app routes and plugin-registered routes.

### Pitfall 5: openapi-typescript Output is Ambient Declarations
**What goes wrong:** Generated `.d.ts` file can't be imported like a regular module.
**Why it happens:** `openapi-typescript` v7 generates `export interface` declarations in a `.d.ts` file. You use them via `import type { ... } from './types/openai'`.
**How to avoid:** Use `import type` for all references to generated types. They are compile-time only.
**Warning signs:** Runtime errors about missing modules or undefined imports.

### Pitfall 6: TypeBox Schema Type vs Route Handler Type Mismatch
**What goes wrong:** Route handler function signature accepts `FastifyInstance` without type provider, losing schema inference.
**Why it happens:** The `registerXRoute(app: FastifyInstance)` signature doesn't carry the type provider information.
**How to avoid:** Either use `FastifyInstance` with proper generic params, or rely on the `schema` option object approach where inference happens at call site.
**Warning signs:** `request.body` typed as `unknown` or `any` inside handlers despite having a schema.

## Code Examples

### AJV Override in Fastify Constructor
```typescript
// apps/api/src/server.ts
import Fastify from 'fastify';
import { TypeBoxTypeProvider } from '@fastify/type-provider-typebox';

export function createApp() {
  const app = Fastify({
    logger: true,
    ajv: {
      customOptions: {
        removeAdditional: false,  // Reject unknown fields (don't strip them)
        // Keep Fastify defaults for the rest:
        // coerceTypes: 'array' is set by Fastify internally
        // useDefaults: true is set by Fastify internally
      },
    },
  }).withTypeProvider<TypeBoxTypeProvider>();

  return app;
}
```

### TypeBox Schema with Nested additionalProperties
```typescript
// apps/api/src/schemas/chat-completions.ts
import { Type, type Static } from 'typebox/type';

const MessageSchema = Type.Object({
  role: Type.String(),
  content: Type.String(),
}, { additionalProperties: false });

export const ChatCompletionsBodySchema = Type.Object({
  model: Type.Optional(Type.String()),
  messages: Type.Optional(Type.Array(MessageSchema)),
  stream: Type.Optional(Type.Boolean()),
  temperature: Type.Optional(Type.Number()),
  top_p: Type.Optional(Type.Number()),
  n: Type.Optional(Type.Integer()),
  stop: Type.Optional(Type.Union([
    Type.String(),
    Type.Array(Type.String()),
  ])),
  max_completion_tokens: Type.Optional(Type.Integer()),
  presence_penalty: Type.Optional(Type.Number()),
  frequency_penalty: Type.Optional(Type.Number()),
  user: Type.Optional(Type.String()),
}, { additionalProperties: false });

export type ChatCompletionsBody = Static<typeof ChatCompletionsBodySchema>;
```

### Generated OpenAI Types Usage
```typescript
// After running: npx openapi-typescript docs/reference/openai-openapi.yml -o apps/api/src/types/openai.d.ts

// Usage in route handlers (compile-time only):
import type { components } from '../types/openai';

type CreateChatCompletionResponse = components['schemas']['CreateChatCompletionResponse'];
type CreateImageRequest = components['schemas']['CreateImageRequest'];

// Use to type-check response builders:
function buildChatResponse(data: any): CreateChatCompletionResponse {
  return {
    id: data.id,
    object: 'chat.completion',
    created: data.created,
    model: data.model,
    choices: data.choices,
    // TypeScript will error if required fields are missing
  };
}
```

### Validation Error Flow (Already Working)
```typescript
// When AJV rejects a request, Fastify throws FST_ERR_VALIDATION with statusCode 400.
// The v1Plugin error handler catches it:
//
// app.setErrorHandler((error: FastifyError, _request, reply) => {
//   const status = error.statusCode ?? 500;  // 400 for validation errors
//   const message = error.message || "Internal server error";
//   reply.code(status).send({
//     error: {
//       message,  // AJV error message like "body must NOT have additional properties"
//       type: STATUS_TO_TYPE[status] ?? "server_error",  // "invalid_request_error"
//       param: null,
//       code: null,
//     },
//   });
// });
//
// No new error handling code needed for validation rejection.
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `@sinclair/typebox` (0.x) | `typebox` (1.x) | 2024-2025 | New package name, subpath exports |
| `@fastify/type-provider-typebox@5` | `@6` | 2025 | Requires `typebox@^1.0.13` |
| openapi-typescript v6 | v7 | 2024 | Globbing deprecated, uses redocly.yaml for multi-schema |

**Deprecated/outdated:**
- `@sinclair/typebox`: Still on npm at 0.34.48 but TypeBox author moved to `typebox` package for v1
- `@fastify/type-provider-typebox@5`: Still works with Fastify 5 but won't receive new features

## Open Questions

1. **OpenAI spec version to download**
   - What we know: Local spec is v2.3.0 (flagged as potentially stale). Latest is at `https://raw.githubusercontent.com/openai/openai-openapi/master/openapi.yaml`
   - What's unclear: Exact latest version number (need to fetch and check)
   - Recommendation: Download fresh copy at implementation time, verify it parses correctly

2. **TypeBox v1 import compatibility with CommonJS tsconfig**
   - What we know: The project's `apps/api/tsconfig.json` uses `module: "CommonJS"` and `moduleResolution: "Node"`. TypeBox v1 exports use ESM subpath exports (`typebox/type`).
   - What's unclear: Whether Node moduleResolution correctly resolves subpath exports for `typebox/type`
   - Recommendation: Test during implementation. If resolution fails, may need to update `moduleResolution` to `"Node16"` or `"Bundler"`, or fall back to `@sinclair/typebox@0.34` + type-provider v5.2

3. **Existing test mocks may need updating**
   - What we know: Tests use `Fastify()` without type provider or AJV overrides
   - What's unclear: Whether adding AJV overrides will break existing tests
   - Recommendation: Existing tests that don't use schemas should be unaffected. New validation tests should use the same AJV config as production.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | vitest (version from workspace) |
| Config file | Root or workspace-level vitest config (no `apps/api/vitest.config.ts`) |
| Quick run command | `cd apps/api && npx vitest run --passWithNoTests` |
| Full suite command | `cd apps/api && npx vitest run` |

### Phase Requirements - Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| FOUND-06 | TypeBox schemas reject extra fields on all 4 v1 routes | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | No - Wave 0 |
| FOUND-06 | TypeBox schemas reject missing required fields | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | No - Wave 0 |
| FOUND-06 | Valid requests pass validation on all 4 routes | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | No - Wave 0 |
| FOUND-06 | Validation errors return OpenAI error format | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | No - Wave 0 |
| FOUND-07 | Generated types file exists and is importable | unit | `cd apps/api && npx tsc --noEmit` | No - Wave 0 (file generation) |

### Sampling Rate
- **Per task commit:** `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x`
- **Per wave merge:** `cd apps/api && npx vitest run`
- **Phase gate:** Full suite green + `npx tsc --noEmit` passes

### Wave 0 Gaps
- [ ] `apps/api/test/routes/typebox-validation.test.ts` -- covers FOUND-06 (all 4 routes, valid/invalid/extra fields)
- [ ] Verify `typebox/type` imports resolve with current tsconfig (CommonJS + Node moduleResolution)
- [ ] Generate `apps/api/src/types/openai.d.ts` -- covers FOUND-07

## Sources

### Primary (HIGH confidence)
- npm registry: `typebox@1.1.6`, `@fastify/type-provider-typebox@6.1.0` peer deps, `openapi-typescript@7.13.0`
- Existing codebase: `v1-plugin.ts`, `server.ts`, `chat-completions.ts`, `api-error.ts`

### Secondary (MEDIUM confidence)
- [Fastify Validation and Serialization docs](https://fastify.dev/docs/latest/Reference/Validation-and-Serialization/) -- AJV defaults documentation
- [openapi-typescript CLI docs](https://openapi-ts.dev/cli) -- generation command syntax
- [TypeBox GitHub](https://github.com/sinclairzx81/typebox) -- v1 import paths

### Tertiary (LOW confidence)
- [fastify-type-provider-typebox issue #200](https://github.com/fastify/fastify-type-provider-typebox/issues/200) -- additionalProperties default behavior discussion

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - versions verified against npm registry, peer deps confirmed
- Architecture: HIGH - patterns verified against existing codebase structure and Fastify docs
- Pitfalls: HIGH - AJV removeAdditional default confirmed via multiple sources; TypeBox v1 import change confirmed via npm exports

**Research date:** 2026-03-17
**Valid until:** 2026-04-17 (stable ecosystem, 30-day validity)
