# Phase 4: Models Endpoint - Context

**Gathered:** 2026-03-18
**Status:** Ready for planning

<domain>
## Phase Boundary

Make `GET /v1/models` and `GET /v1/models/{model}` return fully OpenAI-spec-compliant responses. Every model object must have `id`, `object: "model"`, `created` (unix int), and `owned_by`. The retrieve endpoint must return a single model object or a 404 in OpenAI error format.

**Out of scope:** Model aliasing for routing in chat completions (Phase 8 DIFF-03). Pricing/capability metadata beyond the OpenAI spec fields. Dynamic catalog sync from OpenRouter API.

</domain>

<decisions>
## Implementation Decisions

### Model catalog — identity and aliases
- The public catalog stays **hardcoded** in `model-service.ts` — the existing static array approach is kept, but expanded with real model entries
- The current Hive internal abstractions (`guest-free`, `smart-reasoning`, `image-basic`) will be replaced/supplemented with real model entries that have real provider-namespaced IDs and OpenAI-compatible aliases
- **Each alias gets its own entry in the `/v1/models` list** — no grouping by logical model. If `gpt-4o` and `openai/gpt-4o` are aliases for the same model, they each appear as a separate object in the list
- The canonical (primary) `id` field for a model is the **OpenAI-compatible alias** (e.g., `gpt-4o`, `gpt-4o-mini`, `dall-e-3`)
- Provider-namespaced IDs also appear as their own entries:
  - OpenRouter-routed models: `openrouter/openai/gpt-5.4-nano`, `openrouter/x-ai/grok-4.20-beta`
  - OpenRouter special routers: `openrouter/auto`, `openrouter/free`
  - Direct provider models: `openai/gpt-5.4-nano`, `anthropic/claude-opus-4.6`

### `owned_by` field
- Derived from the upstream provider based on the model ID prefix
- `openai/...` or `gpt-*` aliases → `owned_by: "openai"`
- `anthropic/...` or `claude-*` aliases → `owned_by: "anthropic"`
- `openrouter/...` → `owned_by: "openrouter"`
- Other provider prefixes → match the prefix

### API-tier visibility
- **Show all models** in `/v1/models` — no filtering by tier at list time
- If a model is inaccessible with an API key, it fails at use time (chat completions, etc.), not at list time
- This keeps the listing logic simple and avoids auth-dependent catalog behavior

### 404 on unknown model (retrieve endpoint)
- HTTP 404 with OpenAI error format:
  ```json
  { "error": { "message": "The model 'x' does not exist", "type": "invalid_request_error", "code": "model_not_found" } }
  ```
- Use `sendApiError()` from `api-error.ts` — consistent with Phase 1 error format

### Response shape
- `/v1/models` list response: `{ "object": "list", "data": [...] }`
- Each model object in `data`: `{ "id": "...", "object": "model", "created": <unix int>, "owned_by": "..." }`
- Internal fields (`capability`, `costType`, `pricing`) are **NOT exposed** in the API response — they are internal to `GatewayModel` and stripped at the route handler level

### Claude's Discretion
- Exact `created` timestamp values per model (hardcode reasonable dates in the static array — approximate release dates work fine)
- Whether `created` and `owned_by` are added to the `GatewayModel` type or computed at serialization time
- Whether to derive `owned_by` from a helper function or encode it per model entry

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements
- `.planning/REQUIREMENTS.md` — FOUND-03 (list endpoint compliance), FOUND-04 (retrieve endpoint + 404)

### OpenAI spec
- `docs/reference/openai-openapi.yml` — Model object schema; `ListModelsResponse` and `Model` schemas define required fields

### Existing models code (primary targets for change)
- `apps/api/src/routes/models.ts` — Current route handler; needs retrieve route added and response shape fixed
- `apps/api/src/domain/model-service.ts` — Static catalog to expand; `list()` and `findById()` methods are the service interface
- `apps/api/src/domain/types.ts` — `GatewayModel` type; needs `created` and `owned_by` fields (or these are computed at serialization)
- `apps/api/src/schemas/models.ts` — `ModelsParamsSchema` already exists and is ready to wire into the retrieve route

### Error format (Phase 1)
- `apps/api/src/routes/api-error.ts` — `sendApiError()` helper; use for 404 model not found response

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `ModelService.findById(modelId)` in `model-service.ts:56` — already exists; use directly for the retrieve endpoint
- `ModelService.list()` in `model-service.ts:52` — already exists; use for the list endpoint
- `ModelsParamsSchema` in `schemas/models.ts` — TypeBox params schema for `{ model: string }` already defined; wire into retrieve route
- `sendApiError(reply, 404, message, { code })` in `api-error.ts` — use for model not found

### Established Patterns
- Route handlers call `services.models.list()` / `services.models.findById()` — service layer is the catalog source
- Response serialization strips internal fields at the route handler level (see current `models.ts` mapping)
- TypeBox schemas wired via `v1Plugin` scope (Phase 2 pattern) — follow the same pattern for the retrieve route
- `sendApiError()` is the canonical error helper — all error responses go through it (Phase 1 decision)

### Integration Points
- `apps/api/src/routes/models.ts` — add `GET /v1/models/:model` route here alongside existing list route
- `apps/api/src/domain/model-service.ts` — expand MODELS array with real entries; update `GatewayModel` type or add serialization helpers
- `v1Plugin` registration — the retrieve route must be registered inside `v1Plugin` scope for auth + Content-Type enforcement to apply

</code_context>

<specifics>
## Specific Ideas

- No specific UI/UX references — this is a pure API compliance phase
- The alias-per-entry approach means `gpt-4o` and `openai/gpt-4o` both appear in the list as peers — identical shape, different `id` and potentially different `owned_by`

</specifics>

<deferred>
## Deferred Ideas

- Model aliasing for routing (accepting `gpt-4o` in chat completions and routing it to the right provider) — Phase 8, DIFF-03
- Dynamic catalog sync from OpenRouter API — out of scope for this milestone
- Capability/pricing fields in model objects — v2 requirements (META-01)

</deferred>

---

*Phase: 04-models-endpoint*
*Context gathered: 2026-03-18*
