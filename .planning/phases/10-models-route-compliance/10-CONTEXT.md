# Phase 10: Models Route Compliance - Context

**Gathered:** 2026-03-21
**Status:** Ready for planning

<domain>
## Phase Boundary

Close exactly two gaps on GET /v1/models routes discovered by the v1.0 milestone audit:
1. Add auth guard so invalid/missing API keys return 401 (FOUND-02)
2. Add differentiator headers to all models route responses (DIFF-01)

No new endpoints, no changes to response bodies, no changes to other routes.

</domain>

<decisions>
## Implementation Decisions

### Auth Scope for Models Routes
- Use `requireV1ApiPrincipal` with no specific scope restriction — any valid API key grants access to models routes
- Matches OpenAI's behavior: any valid key can list/retrieve models, no chat/image scope gate needed
- Missing or invalid API key → 401 with `authentication_error` (consistent with all other v1 routes)

### Differentiator Header Values
- Models are a static catalog — no AI routing occurs
- Header values for both `GET /v1/models` and `GET /v1/models/{model}`:
  - `x-model-routed: ''` (empty string — no routing)
  - `x-provider-used: ''` (empty string — no provider)
  - `x-provider-model: ''` (empty string — no model dispatch)
  - `x-actual-credits: '0'` (catalog reads are free)
- All four headers must be present to satisfy DIFF-01

### DIFF-01 Closure Scope
- Fix models routes only — `GET /v1/models` and `GET /v1/models/{model}`
- Other routes (chat-completions, embeddings, images, responses) already set these headers from Phase 8
- No cross-route audit required in this phase

### Claude's Discretion
- Whether to set headers in the route handler directly (via reply.header calls) or via a shared utility
- Exact placement of requireV1ApiPrincipal call within each route handler

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements
- `.planning/REQUIREMENTS.md` — FOUND-02 (auth on models routes) and DIFF-01 (all v1/* differentiator headers)
- `.planning/ROADMAP.md` §Phase 10 — Success criteria: 4 explicit conditions

### Existing Implementation
- `apps/api/src/routes/models.ts` — Current models routes (no auth, no headers — this is the file to modify)
- `apps/api/src/routes/v1-plugin.ts` — v1 plugin where routes are registered; auth hook context
- `apps/api/src/routes/auth.ts` — `requireV1ApiPrincipal` function — use this for auth guard
- `apps/api/src/routes/chat-completions.ts` — Reference implementation: shows requireV1ApiPrincipal + header-setting pattern to replicate

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `requireV1ApiPrincipal(request, reply, services, scope?)` from `./auth` — drop-in auth guard; returns principal or sends 401 and returns undefined
- `reply.header("x-model-routed", ...)` pattern — used in chat-completions.ts for all 4 differentiator headers

### Established Patterns
- Auth guard: call `requireV1ApiPrincipal` at top of route handler; if it returns undefined, the 401 was already sent — return immediately
- Differentiator headers: set via `reply.header()` calls before returning the response body
- Error responses: `sendApiError(reply, 404, message, { type, code })` — already used in models.ts for 404 case

### Integration Points
- `apps/api/src/routes/models.ts` is where both changes land — auth guard at start of each handler, header calls before each return
- `apps/api/src/runtime/services.ts` — `RuntimeServices` type (already imported in models.ts via `services` param)

</code_context>

<specifics>
## Specific Ideas

- Auth behavior: match OpenAI exactly — any valid key works, missing/invalid → 401
- Header values: empty strings for routing headers (not "none" or "catalog"), `'0'` for credits
- This is a surgical fix: two changes per route handler (auth guard + header calls), no broader refactor

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope.

</deferred>

---

*Phase: 10-models-route-compliance*
*Context gathered: 2026-03-21*
