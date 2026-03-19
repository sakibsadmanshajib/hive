---
phase: 07-surface-expansion
plan: 01
subsystem: api
tags: [embeddings, openai-api, typebox, provider-pipeline]

requires:
  - phase: 05-non-streaming-chat
    provides: route-service-provider pipeline pattern, credit tracking, usage recording
provides:
  - POST /v1/embeddings endpoint with full provider pipeline
  - ProviderEmbeddingsRequest/Response types
  - EmbeddingsBodySchema TypeBox validation
  - embedding capability type on GatewayModel
  - openai/text-embedding-3-small model in catalog
affects: [07-surface-expansion, testing]

tech-stack:
  added: []
  patterns: [embeddings-pipeline mirroring chat-completions pattern]

key-files:
  created:
    - apps/api/src/schemas/embeddings.ts
    - apps/api/src/routes/embeddings.ts
  modified:
    - apps/api/src/domain/types.ts
    - apps/api/src/providers/types.ts
    - apps/api/src/providers/openai-compatible-client.ts
    - apps/api/src/providers/registry.ts
    - apps/api/src/domain/ai-service.ts
    - apps/api/src/runtime/services.ts
    - apps/api/src/routes/v1-plugin.ts
    - apps/api/src/domain/model-service.ts

key-decisions:
  - "Embeddings method added to RuntimeAiService (runtime/services.ts) following the established real-provider pattern, not just domain AiService"
  - "ProviderEmbeddingsExecutionResult includes providerUsed/providerModel fields alongside statusCode/body/headers for consistency with other execution results"

patterns-established:
  - "Embeddings pipeline: route -> RuntimeAiService.embeddings() -> ProviderRegistry.embeddings() -> client.embeddings()"

requirements-completed: [SURF-01]

duration: 6min
completed: 2026-03-19
---

# Phase 7 Plan 1: Embeddings Endpoint Summary

**Full POST /v1/embeddings pipeline with TypeBox validation, provider dispatch via OpenAI-compatible client, credit tracking, and compliant CreateEmbeddingResponse shape**

## Performance

- **Duration:** 6 min
- **Started:** 2026-03-19T01:46:50Z
- **Completed:** 2026-03-19T01:53:00Z
- **Tasks:** 2
- **Files modified:** 10

## Accomplishments
- Complete embeddings pipeline from route through service to provider, following the same pattern as chat completions
- TypeBox schema with additionalProperties:false for request validation
- Compliant response shape: object:"list", data[].object:"embedding", data[].embedding, data[].index, model, usage
- Credit pre-charging with refund on provider failure, fire-and-forget usage tracking

## Task Commits

Each task was committed atomically:

1. **Task 1: Add embedding capability type, provider types, and embedding model to catalog** - `93494c3` (feat)
2. **Task 2: Create embeddings schema, provider client method, registry dispatch, service method, route, and register in v1-plugin** - `c7aa353` (feat)

## Files Created/Modified
- `apps/api/src/schemas/embeddings.ts` - TypeBox schema for CreateEmbeddingRequest
- `apps/api/src/routes/embeddings.ts` - POST /v1/embeddings route handler with auth and rate limiting
- `apps/api/src/domain/types.ts` - GatewayModel capability expanded to include "embedding"
- `apps/api/src/providers/types.ts` - ProviderEmbeddingsRequest/Response types and optional interface method
- `apps/api/src/providers/openai-compatible-client.ts` - embeddings() method POSTing to /embeddings
- `apps/api/src/providers/registry.ts` - embeddings() dispatch with circuit breaker and fallback chain
- `apps/api/src/domain/ai-service.ts` - Domain-level embeddings() mock method
- `apps/api/src/runtime/services.ts` - RuntimeAiService.embeddings() with real provider pipeline
- `apps/api/src/routes/v1-plugin.ts` - Route registration
- `apps/api/src/domain/model-service.ts` - openai/text-embedding-3-small model entry and updated signatures

## Decisions Made
- Added embeddings() to RuntimeAiService in runtime/services.ts (not just domain AiService) since that is where real provider integration lives
- ProviderEmbeddingsExecutionResult carries providerUsed/providerModel alongside statusCode/body/headers for header propagation consistency

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Added embeddings to RuntimeAiService (runtime/services.ts)**
- **Found during:** Task 2 (pipeline wiring)
- **Issue:** Plan specified adding embeddings() to domain/ai-service.ts, but the actual real service used by routes is RuntimeAiService in runtime/services.ts
- **Fix:** Added embeddings() to both domain AiService (mock) and RuntimeAiService (real provider pipeline)
- **Files modified:** apps/api/src/runtime/services.ts, apps/api/src/domain/ai-service.ts
- **Verification:** TypeScript compiles clean, all 273 existing tests pass
- **Committed in:** c7aa353 (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 missing critical)
**Impact on plan:** Essential for correct route-to-provider wiring. No scope creep.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Embeddings endpoint is wired and ready for integration testing
- Provider client supports any OpenAI-compatible embeddings API
- Ready for plan 07-02 (surface expansion tests) or next phase work

---
*Phase: 07-surface-expansion*
*Completed: 2026-03-19*
