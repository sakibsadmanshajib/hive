# Phase 4: Models Endpoint - Research

**Researched:** 2026-03-18
**Domain:** OpenAI Models API compliance (list + retrieve endpoints)
**Confidence:** HIGH

## Summary

Phase 4 is a straightforward API compliance task: make `GET /v1/models` and `GET /v1/models/{model}` return responses matching the OpenAI Model object schema. The existing codebase already has `ModelService.list()`, `ModelService.findById()`, route registration in `models.ts`, and `ModelsParamsSchema` for the retrieve route params. The work is primarily: (1) expand the static MODELS catalog with real model entries including `created` and `owned_by` fields, (2) fix the list endpoint serialization to emit only spec-compliant fields, (3) add the retrieve route, and (4) return 404 in OpenAI error format for unknown models.

The OpenAI spec (from `docs/reference/openai-openapi.yml`) defines the Model object with exactly four required fields: `id` (string), `object` (literal "model"), `created` (integer, unix seconds), `owned_by` (string). The `ListModelsResponse` requires `object` (literal "list") and `data` (array of Model). No other fields are required.

**Primary recommendation:** Extend `GatewayModel` type with `created` and `owned_by`, expand the static catalog, fix serialization to strip internal fields, add retrieve route using existing `ModelsParamsSchema` and `sendApiError`.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Model catalog stays hardcoded in `model-service.ts` -- static array approach, expanded with real model entries
- Each alias gets its own entry in `/v1/models` list -- no grouping. `gpt-4o` and `openai/gpt-4o` are separate objects
- Canonical `id` field uses OpenAI-compatible alias (e.g., `gpt-4o`, `gpt-4o-mini`, `dall-e-3`)
- Provider-namespaced IDs also appear as their own entries (e.g., `openrouter/openai/gpt-5.4-nano`)
- `owned_by` derived from upstream provider based on model ID prefix
- Show all models in `/v1/models` -- no filtering by tier at list time
- 404 on unknown model uses `sendApiError()` with `{ code: "model_not_found" }`
- Internal fields (`capability`, `costType`, `pricing`) NOT exposed in API response -- stripped at route handler level
- Response shape: `{ "object": "list", "data": [...] }` with each model having `id`, `object`, `created`, `owned_by`

### Claude's Discretion
- Exact `created` timestamp values per model (hardcode approximate release dates)
- Whether `created` and `owned_by` are added to `GatewayModel` type or computed at serialization time
- Whether to derive `owned_by` from a helper function or encode it per model entry

### Deferred Ideas (OUT OF SCOPE)
- Model aliasing for routing in chat completions (Phase 8, DIFF-03)
- Dynamic catalog sync from OpenRouter API
- Capability/pricing fields in model objects (v2, META-01)
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| FOUND-03 | `GET /v1/models` returns OpenAI-compliant list with required `id`, `object: "model"`, `created` (unix timestamp), `owned_by` fields | OpenAI spec verified: Model schema requires exactly these 4 fields. ListModelsResponse requires `object: "list"` + `data` array. Existing `list()` method and route handler need serialization fix. |
| FOUND-04 | `GET /v1/models/{model}` returns single model object or 404 with proper error format | Existing `findById()` method ready. `ModelsParamsSchema` already defined. `sendApiError()` available for 404. Route needs to be added in `models.ts` and registered in v1Plugin scope. |
</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| fastify | (existing) | HTTP framework | Already in use, route registration pattern established |
| @sinclair/typebox | (existing) | Request schema validation | Already in use for `ModelsParamsSchema` |
| @fastify/type-provider-typebox | (existing) | Type inference for routes | Already wired in v1Plugin |

### Supporting
No new libraries needed. This phase uses only existing dependencies.

## Architecture Patterns

### Recommended Project Structure
No new files needed. Changes are confined to existing files:
```
apps/api/src/
  domain/
    types.ts          # Add created + owned_by to GatewayModel
    model-service.ts  # Expand MODELS array with real entries + owned_by helper
  routes/
    models.ts         # Fix list serialization, add retrieve route
  schemas/
    models.ts         # Already has ModelsParamsSchema (no changes)
```

### Pattern 1: Response Serialization at Route Handler Level
**What:** Internal `GatewayModel` objects contain fields not in the OpenAI spec (`capability`, `costType`, `pricing`). The route handler maps to only spec-compliant fields.
**When to use:** Always -- the service layer returns domain objects, the route layer serializes to API shape.
**Example:**
```typescript
// Source: Existing pattern in models.ts (to be fixed)
const model = services.models.findById(params.model);
if (!model) {
  return sendApiError(reply, 404,
    `The model '${params.model}' does not exist`,
    { code: "model_not_found" });
}
return {
  id: model.id,
  object: "model" as const,
  created: model.created,
  owned_by: model.ownedBy,
};
```

### Pattern 2: owned_by Derivation Helper
**What:** A pure function that derives `owned_by` from a model ID prefix.
**When to use:** When adding models to the catalog -- avoids encoding `owned_by` redundantly on every entry.
**Recommendation:** Use a helper function. This is cleaner than encoding `owned_by` per entry because the rules are systematic (prefix-based). Add `created` directly to each model entry since it varies per model.
```typescript
function deriveOwnedBy(modelId: string): string {
  if (modelId.startsWith("openrouter/")) return "openrouter";
  if (modelId.startsWith("openai/") || modelId.startsWith("gpt-") || modelId.startsWith("dall-e") || modelId.startsWith("o1") || modelId.startsWith("o3") || modelId.startsWith("o4")) return "openai";
  if (modelId.startsWith("anthropic/") || modelId.startsWith("claude-")) return "anthropic";
  if (modelId.startsWith("google/") || modelId.startsWith("gemini-")) return "google";
  if (modelId.startsWith("x-ai/") || modelId.startsWith("grok-")) return "x-ai";
  // Extract provider from "provider/model" pattern
  const slash = modelId.indexOf("/");
  if (slash > 0) return modelId.substring(0, slash);
  return "unknown";
}
```

### Pattern 3: Retrieve Route with TypeBox Params
**What:** Wire `ModelsParamsSchema` into a GET route for `/v1/models/:model`.
**When to use:** For the retrieve endpoint.
**Example:**
```typescript
import { ModelsParamsSchema } from "../schemas/models";

app.get<{ Params: ModelsParams }>("/v1/models/:model", {
  schema: { params: ModelsParamsSchema },
}, async (request, reply) => {
  const model = services.models.findById(request.params.model);
  if (!model) {
    return sendApiError(reply, 404,
      `The model '${request.params.model}' does not exist`,
      { code: "model_not_found" });
  }
  return serializeModel(model);
});
```

### Anti-Patterns to Avoid
- **Exposing internal fields:** Never include `capability`, `costType`, or `pricing` in the API response. These are internal to `GatewayModel`.
- **Auth-dependent filtering:** Do not filter the model list based on API key tier. Show all models; access control happens at use time.
- **Dynamic catalog:** Do not fetch from OpenRouter API. The catalog is static for this phase.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Error responses | Custom error JSON construction | `sendApiError()` from `api-error.ts` | Consistent format, already handles type mapping |
| Request param validation | Manual param parsing | `ModelsParamsSchema` + TypeBox | Already defined, provides type safety |
| Route scoping | Manual auth/content-type hooks | Register inside `v1Plugin` scope | Auth + Content-Type enforcement already wired |

## Common Pitfalls

### Pitfall 1: Leaking Internal Fields
**What goes wrong:** Route handler returns the full `GatewayModel` object, exposing `capability`, `costType`, `pricing` in the API response.
**Why it happens:** The current list endpoint already leaks `capability` and `costType`.
**How to avoid:** Always map through a serialization function that picks only `id`, `object`, `created`, `owned_by`.
**Warning signs:** SDK or test sees unexpected fields in response.

### Pitfall 2: Wrong Error Type for 404
**What goes wrong:** Using `type: "invalid_request_error"` instead of `type: "not_found_error"` for model 404.
**Why it happens:** OpenAI docs say model-not-found is `invalid_request_error` but the existing `STATUS_TO_TYPE` maps 404 to `not_found_error`.
**How to avoid:** Use `sendApiError(reply, 404, message, { type: "invalid_request_error", code: "model_not_found" })` to match OpenAI's actual behavior where model-not-found uses `invalid_request_error`. Verify against OpenAI SDK expectations.
**Warning signs:** SDK client throws unexpected error type.

### Pitfall 3: created Field as String Instead of Integer
**What goes wrong:** Returning `created` as an ISO string or floating-point number instead of a Unix integer (seconds).
**Why it happens:** JavaScript Date.now() returns milliseconds; JSON serialization of Date objects produces strings.
**How to avoid:** Store `created` as a plain integer (seconds since epoch). Use `Math.floor(Date.now() / 1000)` or hardcode unix timestamps directly.
**Warning signs:** SDK type validation fails on `created` field.

### Pitfall 4: Fastify Route Param Name Mismatch
**What goes wrong:** Route defined as `/v1/models/:model` but `ModelsParamsSchema` uses a different key.
**Why it happens:** Inconsistency between route param name and schema property name.
**How to avoid:** `ModelsParamsSchema` already defines `{ model: Type.String() }` -- match route param as `:model`.

## Code Examples

### Serialization Helper
```typescript
// Recommended: extract a reusable serializer
function serializeModel(model: GatewayModel): {
  id: string; object: "model"; created: number; owned_by: string;
} {
  return {
    id: model.id,
    object: "model",
    created: model.created,
    owned_by: deriveOwnedBy(model.id),
  };
}
```

### List Endpoint (Fixed)
```typescript
app.get("/v1/models", async () => {
  return {
    object: "list" as const,
    data: services.models.list().map(serializeModel),
  };
});
```

### Sample Model Entries
```typescript
// Approximate release dates as unix timestamps
const MODELS: GatewayModel[] = [
  {
    id: "gpt-4o",
    object: "model",
    created: 1715367600, // ~2024-05-10
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 8, inputTokensPer1m: 2.5, outputTokensPer1m: 10 },
  },
  {
    id: "openai/gpt-4o",
    object: "model",
    created: 1715367600,
    capability: "chat",
    costType: "variable",
    pricing: { creditsPerRequest: 8, inputTokensPer1m: 2.5, outputTokensPer1m: 10 },
  },
  // ... more entries
];
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Internal model IDs (`guest-free`, `smart-reasoning`) | Real provider-namespaced IDs + OpenAI aliases | Phase 4 | Models list becomes useful to SDK consumers |
| Leaking `capability`/`costType` in response | Strict 4-field serialization | Phase 4 | OpenAI SDK compatibility |

## Open Questions

1. **Exact model catalog entries**
   - What we know: Need OpenAI aliases (gpt-4o, gpt-4o-mini, etc.) and provider-namespaced IDs
   - What's unclear: Full list of models to include -- should match what Hive actually routes to
   - Recommendation: Start with a representative set (5-10 models covering OpenAI, Anthropic, and OpenRouter special routers). Can expand later. The architecture supports easy additions.

2. **What happens to existing internal model IDs?**
   - What we know: `guest-free`, `smart-reasoning`, `image-basic` are used internally for routing
   - What's unclear: Whether these should remain in the catalog alongside real model IDs
   - Recommendation: Keep them if they are still used by the web pipeline. They will appear in the models list (per "show all" decision) but that is acceptable since web pipeline needs them.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | vitest (existing) |
| Config file | apps/api/vitest.config.ts or inferred from package.json |
| Quick run command | `cd apps/api && pnpm test` |
| Full suite command | `cd apps/api && pnpm test` |

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| FOUND-03 | List endpoint returns compliant model objects | unit | `cd apps/api && pnpm vitest run test/routes/models-route.test.ts -t "list"` | No -- Wave 0 |
| FOUND-03 | Each model has id, object, created (int), owned_by | unit | `cd apps/api && pnpm vitest run test/routes/models-route.test.ts -t "fields"` | No -- Wave 0 |
| FOUND-03 | No internal fields leaked (capability, costType, pricing) | unit | `cd apps/api && pnpm vitest run test/routes/models-route.test.ts -t "no internal"` | No -- Wave 0 |
| FOUND-04 | Retrieve known model returns single object | unit | `cd apps/api && pnpm vitest run test/routes/models-route.test.ts -t "retrieve"` | No -- Wave 0 |
| FOUND-04 | Retrieve unknown model returns 404 in error format | unit | `cd apps/api && pnpm vitest run test/routes/models-route.test.ts -t "404"` | No -- Wave 0 |
| FOUND-03/04 | OpenAI SDK client.models.list() and .retrieve() work | integration | `cd apps/api && pnpm vitest run test/v1/models-sdk.test.ts` | No -- Wave 0 |

### Sampling Rate
- **Per task commit:** `cd apps/api && pnpm vitest run test/routes/models-route.test.ts`
- **Per wave merge:** `cd apps/api && pnpm test`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `apps/api/test/routes/models-route.test.ts` -- unit tests for list and retrieve endpoints (covers FOUND-03, FOUND-04)
- [ ] `apps/api/test/v1/models-sdk.test.ts` -- SDK integration tests using openai client (covers FOUND-03, FOUND-04)
- [ ] Test directory `apps/api/test/v1/` needs to be created for SDK integration tests

## Sources

### Primary (HIGH confidence)
- `docs/reference/openai-openapi.yml` lines 47861-47891 -- Model object schema: 4 required fields (id, object, created, owned_by)
- `docs/reference/openai-openapi.yml` lines 46479-46493 -- ListModelsResponse schema: object + data array
- `apps/api/src/domain/model-service.ts` -- Existing service with list() and findById()
- `apps/api/src/routes/models.ts` -- Existing route handler (needs fixing)
- `apps/api/src/schemas/models.ts` -- ModelsParamsSchema already defined
- `apps/api/src/routes/api-error.ts` -- sendApiError helper with STATUS_TO_TYPE mapping

### Secondary (MEDIUM confidence)
- OpenAI API documentation for model-not-found error type (training data suggests `invalid_request_error` not `not_found_error`)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - no new dependencies, all existing
- Architecture: HIGH - straightforward CRUD-like endpoint, patterns established in prior phases
- Pitfalls: HIGH - verified against OpenAI spec in repo, error handling pattern well-documented

**Research date:** 2026-03-18
**Valid until:** 2026-04-18 (stable -- static catalog, no moving parts)
