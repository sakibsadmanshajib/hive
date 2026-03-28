# Phase 1: Error Format Standardization - Context

**Gathered:** 2026-03-17
**Status:** Ready for planning

<domain>
## Phase Boundary

Every error response from `/v1/*` endpoints matches what OpenAI SDKs expect to parse. All errors use the nested format `{ error: { message, type, param, code } }` with correct HTTP status codes. Web pipeline routes are NOT touched.

</domain>

<decisions>
## Implementation Decisions

### Error formatting approach
- Fastify encapsulated plugin registered at `/v1` prefix with its own `setErrorHandler` and `setNotFoundHandler`
- All `/v1/*` routes are registered inside this plugin scope — web pipeline routes stay outside and are unaffected
- A helper function `sendApiError(reply, status, message, opts?)` is available for explicit error sends in route handlers
- The scoped `setErrorHandler` acts as safety net — any uncaught throw or Fastify-native error within `/v1/*` gets reformatted automatically

### Domain layer errors — route-layer mapping
- Domain layer (`services.ai`) continues returning `{ error: string, statusCode: number }` — no changes to domain types
- The route layer (or error handler) maps `statusCode` to OpenAI `type` field:
  - 400 → `invalid_request_error`
  - 401 → `authentication_error`
  - 403 → `permission_error`
  - 404 → `not_found_error`
  - 429 → `rate_limit_error`
  - 500+ → `server_error`
- The `message` field comes from the existing error string

### Error field population
- `param` field: Populated where routes explicitly check for missing fields (e.g., `prompt`, `messages`). `null` for domain-layer errors and catch-all errors. Phase 2 schema validation will auto-populate `param` for all validation errors.
- `code` field: Small predefined set mapped from context:
  - `"invalid_api_key"` for 401 auth failures
  - `"rate_limit_exceeded"` for 429
  - `"model_not_found"` for 404 on models
  - `"invalid_request_error"` for 400 catch-all
  - `null` for 500s
- `type` field: Always populated via statusCode mapping (never null)

### Fastify native error coverage
- Phase 1 handles Fastify's own errors within the `/v1/*` scope (included, not deferred):
  - Malformed JSON body → 400 `invalid_request_error` with Fastify's parse error message
  - Unknown `/v1/*` route → 404 `not_found_error` via `setNotFoundHandler` on the plugin
  - Fastify validation errors → 400 `invalid_request_error` (basic formatting now, Phase 2 enriches with TypeBox)
- This ensures Phase 1's claim of "all errors standardized" is actually complete

### Scope — which routes get the new format
- `/v1/chat/completions` — yes
- `/v1/models` — yes
- `/v1/images/generations` — yes
- `/v1/responses` — yes
- Any future `/v1/*` routes — yes (automatic via plugin scope)
- Web pipeline routes (chat-sessions, guest-chat, payment-webhook, analytics) — NO, stay as `{ error: "string" }`

### Claude's Discretion
- Exact file/module name for the error helper and plugin
- Whether to use a class or plain function for error construction
- Test strategy and test file organization
- Whether to export a custom error class or just use the helper function

</decisions>

<specifics>
## Specific Ideas

- The OpenAI error shape is: `{ "error": { "message": "...", "type": "invalid_request_error", "param": null, "code": null } }`
- The `openai` npm SDK parses errors via `error.message`, `error.type`, `error.param`, `error.code` — all four fields must exist (even if null) or the SDK crashes on `undefined` access
- Auth errors from `auth.ts` currently send `{ error: "missing or invalid credentials" }` — these must become `{ error: { message: "missing or invalid credentials", type: "authentication_error", param: null, code: "invalid_api_key" } }`

</specifics>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### OpenAI error format specification
- `docs/reference/openai-openapi.yml` — Full OpenAI API spec; search for `Error` and `ErrorResponse` schema definitions for the canonical error shape

### Current error implementation
- `apps/api/src/routes/auth.ts` — Auth middleware with 401/403 error sends (lines 115, 124, 132)
- `apps/api/src/routes/chat-completions.ts` — Chat completions route with 429 and domain error forwarding
- `apps/api/src/routes/models.ts` — Models route error handling
- `apps/api/src/routes/images-generations.ts` — Image generation with 400/429 and domain errors
- `apps/api/src/routes/responses.ts` — Responses route with 429 and domain errors
- `apps/api/src/routes/index.ts` — Route registration (shows all routes and their prefixes)
- `apps/api/src/server.ts` — Server setup (Fastify instance creation)

### Project context
- `.planning/REQUIREMENTS.md` — FOUND-01 defines the error format requirement
- `.planning/ROADMAP.md` — Phase 1 success criteria (3 criteria that must be TRUE)
- `.planning/research/PITFALLS.md` — Pitfall #1 documents current error format breakage with SDK line references

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `auth.ts:requirePrincipal()` — Central auth function that sends 401/403 errors; single place to update for auth error formatting
- `auth.ts:requireApiPrincipal()` — Wrapper around `requirePrincipal`, used by all `/v1/*` routes
- `server.ts` — Fastify instance creation; plugin registration happens here

### Established Patterns
- Routes use `reply.code(N).send({ error: "string" })` — consistent flat format across all routes
- Domain layer returns `{ error: string, statusCode: number }` or success object — discriminated by `"error" in result`
- Route registration is done via `registerRoutes(app, services)` in `routes/index.ts`

### Integration Points
- `routes/index.ts:registerRoutes()` — Must be restructured to register `/v1/*` routes inside the error-formatting plugin scope
- `auth.ts:requirePrincipal()` — Error sends here need to use the new helper (or the plugin error handler catches them)
- Each `/v1/*` route file's explicit `reply.code().send()` calls — Need updating to use `sendApiError()` helper

</code_context>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 01-error-format*
*Context gathered: 2026-03-17*
