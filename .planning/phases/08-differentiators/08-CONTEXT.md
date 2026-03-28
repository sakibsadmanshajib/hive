# Phase 8: Differentiators - Context

**Gathered:** 2026-03-18
**Status:** Ready for planning

<domain>
## Phase Boundary

Add Hive-specific metadata headers to ALL `/v1/*` responses for transparency and debugging. Implement `x-request-id` generation, ensure consistent header propagation across all endpoints, and add model aliasing so standard OpenAI model names (e.g., `gpt-4o`, `gpt-4o-mini`) are accepted and routed to the best available provider. Credit cost stays in response headers.

</domain>

<decisions>
## Implementation Decisions

### Request ID Generation
- Use `crypto.randomUUID()` — built-in Node.js, no additional dependencies
- Generate per-request in v1-plugin hook so every request gets a unique ID before route handlers run
- Attach to reply as `x-request-id` header

[auto] Request ID generation → `crypto.randomUUID()` (recommended default)

### Header Propagation Scope
- ALL `/v1/*` endpoints must include: `x-request-id`, `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits`
- Add `x-request-id` in the v1-plugin onRequest/onSend hook (centralized, DRY) — not per-route
- Other headers (`x-model-routed`, etc.) already set in AiService methods — verify all routes set them consistently

[auto] Header propagation scope → v1-plugin hook for x-request-id; verify remaining headers across all endpoints (recommended default)

### Model Aliasing
- Static alias map in a dedicated config file (e.g., `src/config/model-aliases.ts`)
- Map standard OpenAI model names to Hive's routing targets:
  - `gpt-4o` → best available GPT-4o equivalent
  - `gpt-4o-mini` → best available mini equivalent
  - `gpt-3.5-turbo` → best available fast model
- Apply alias resolution early in AiService before provider dispatch
- Pass-through if model name not in alias map (no breaking change)

[auto] Model aliasing → Static alias map (recommended default)

### Credit Cost Transparency
- Credit cost exposed via `x-actual-credits` response header — already partially implemented in some routes
- No changes to usage object fields (keep it OpenAI-compatible)
- Ensure `x-actual-credits` is set on ALL endpoints (not just chat completions)

[auto] Credit cost → Response headers only via `x-actual-credits` (recommended default)

### Claude's Discretion
- Exact format/precision of credit values in `x-actual-credits`
- Whether alias map is stored as a plain object or typed map
- Specific model alias mappings beyond gpt-4o and gpt-4o-mini

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements
- `.planning/REQUIREMENTS.md` — DIFF-01 through DIFF-04 requirements (headers, credit cost, model aliasing, request ID)
- `.planning/ROADMAP.md` §Phase 8 — Success criteria: all headers on all endpoints, model aliasing, SDK compatibility

### Existing Implementation
- `apps/api/src/routes/v1-plugin.ts` — v1 route registration and auth hooks; add x-request-id here
- `apps/api/src/domain/ai-service.ts` — Where x-model-routed, x-provider-used, x-provider-model, x-actual-credits are set; verify all service methods set them
- `apps/api/src/routes/chat-completions.ts` — Header propagation pattern to replicate
- `apps/api/src/providers/registry.ts` — Provider dispatch; model alias resolution should happen before this

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `v1-plugin.ts` auth hooks: Existing onRequest hook pattern — add x-request-id generation here (centralized)
- `AiService` header-setting code: Already sets x-model-routed, x-provider-used, x-provider-model, x-actual-credits in some methods — reuse pattern, extend to all
- `apps/api/src/routes/api-error.ts` `sendApiError()`: Verify error responses also include headers

### Established Patterns
- TypeBox schema validation: All routes use TypeBox — no changes needed for headers (headers are set on reply, not schema)
- Provider dispatch pipeline: Route → AiService → ProviderRegistry → provider client; alias resolution fits at AiService entry point
- Header propagation: `reply.header('x-model-routed', ...)` pattern already in use

### Integration Points
- v1-plugin.ts onRequest/onSend: Best place to inject x-request-id uniformly for ALL endpoints
- AiService methods (chatCompletions, embeddings, imageGeneration, responses): Each must set all 4 non-request-id headers
- ProviderRegistry.chat/embeddings/imageGeneration: Source of truth for provider name and model — values flow back up to AiService

</code_context>

<specifics>
## Specific Ideas

- Headers must not break OpenAI SDK parsing — SDKs ignore unknown headers, so x-* headers are safe
- Model alias map should be pass-through by default (unknown model names pass as-is to provider)

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 08-differentiators*
*Context gathered: 2026-03-18*
