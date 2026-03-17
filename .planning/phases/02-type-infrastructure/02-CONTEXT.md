# Phase 2: Type Infrastructure - Context

**Gathered:** 2026-03-17
**Status:** Ready for planning

<domain>
## Phase Boundary

Install TypeBox + Fastify type provider for runtime request validation, and generate importable OpenAI TypeScript types from the local spec file. Wire TypeBox schemas on all four v1 routes. Response shape correctness (matching OpenAI spec fields exactly) is for later phases — this phase establishes the infrastructure they depend on.

</domain>

<decisions>
## Implementation Decisions

### Type generation
- Run `openapi-typescript` once → commit the generated output to the repo (e.g., `apps/api/src/types/openai.d.ts` or similar)
- Regenerate manually when the spec changes — not a build-time or CI step
- Update `docs/reference/openai-openapi.yml` to the latest version before generating types (local copy flagged as potentially stale v2.3.0)

### Schema coverage
- Wire TypeBox schemas on ALL four v1 routes in this phase: `chat-completions`, `models`, `images-generations`, `responses`
- Not just scaffolding — each route gets an actual request body/params schema validated by Fastify

### Unknown field behavior
- **Strict rejection**: Fastify AJV returns 400 for unknown/extra fields on all v1 routes
- Use `additionalProperties: false` on all TypeBox schemas
- Error response must use the Phase 1 format: `{ error: { message, type, param, code } }` — already handled by v1Plugin's scoped error handler

### Claude's Discretion
- Exact package versions for `@sinclair/typebox`, `@fastify/type-provider-typebox`, `openapi-typescript`
- File location for generated types (within `apps/api/src/`)
- Whether to use `@fastify/type-provider-typebox` or the `TypeBoxTypeProvider` directly
- TypeBox schema granularity (required vs optional fields on request schemas — follow OpenAI spec)

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### OpenAI spec
- `docs/reference/openai-openapi.yml` — Source of truth for OpenAI request/response schemas. Update to latest before type generation. 73k lines, OpenAPI 3.1.0.

### Phase 1 foundation
- `apps/api/src/routes/api-error.ts` — `sendApiError` helper and `STATUS_TO_TYPE` map. All 400 validation errors from this phase must go through this.
- `apps/api/src/routes/v1-plugin.ts` — Scoped Fastify plugin. TypeBox type provider must be registered here or at the app level so it applies to all v1 routes.

### Existing routes to migrate
- `apps/api/src/routes/chat-completions.ts` — Current plain-TS `ChatBody` type, no schema
- `apps/api/src/routes/models.ts` — Current state
- `apps/api/src/routes/images-generations.ts` — Current state
- `apps/api/src/routes/responses.ts` — Current state

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `apps/api/src/routes/api-error.ts` — `sendApiError(reply, status, message, opts)` — use this for all 400 validation error responses
- `apps/api/src/routes/v1-plugin.ts` — Scoped plugin with error handler already set up. Type provider registration belongs here or at the Fastify app level.

### Established Patterns
- Routes use `FastifyInstance` without a type provider currently — migration to typed provider is the core change
- `app.post<{ Body: ChatBody }>` pattern will shift to `app.post({ schema: { body: TypeBoxSchema } })` with the type provider inferred automatically
- All v1 routes are registered inside `v1Plugin` — type provider wired here gets inherited by all child routes

### Integration Points
- `apps/api/src/routes/index.ts` — Registers v1Plugin; type provider setup may need to happen at app creation level (before plugin registration)
- `apps/api/src/app.ts` (or equivalent entry point) — Where Fastify instance is created; type provider must be set at construction time via `Fastify({ ... }).withTypeProvider<TypeBoxTypeProvider>()`

</code_context>

<specifics>
## Specific Ideas

- No specific references — open to standard `@fastify/type-provider-typebox` + `openapi-typescript` approach

</specifics>

<deferred>
## Deferred Ideas

- None — discussion stayed within phase scope

</deferred>

---

*Phase: 02-type-infrastructure*
*Context gathered: 2026-03-17*
