---
phase: 08-differentiators
plan: 01
subsystem: api
tags: [headers, x-request-id, model-aliases, transparency]

requires:
  - phase: 07-surface-expansion
    provides: "All /v1/* endpoints (chat, embeddings, images, responses)"
provides:
  - "x-request-id UUID on all /v1/* responses including errors"
  - "All 4 AI headers on every MVP AiService method"
  - "Model alias resolution for legacy OpenAI model names"
affects: [08-differentiators, testing]

tech-stack:
  added: []
  patterns: ["onRequest hook for cross-cutting response headers", "static alias map with passthrough for unknown models"]

key-files:
  created:
    - apps/api/src/config/model-aliases.ts
  modified:
    - apps/api/src/routes/v1-plugin.ts
    - apps/api/src/domain/ai-service.ts
    - apps/api/src/domain/model-service.ts

key-decisions:
  - "x-request-id via onRequest hook ensures presence on all responses including errors/404s"
  - "hive-mvp as provider name for MVP AiService since no real provider dispatch"
  - "Model aliases use static map with passthrough for unknown names (no breaking change)"

patterns-established:
  - "onRequest hook pattern for cross-cutting headers in v1-plugin"
  - "Static config module pattern in src/config/ for alias maps"

requirements-completed: [DIFF-01, DIFF-02, DIFF-04]

duration: 1min
completed: 2026-03-19
---

# Phase 8 Plan 1: Differentiator Headers & Model Aliases Summary

**x-request-id UUID on all /v1/* responses, all 4 AI headers on every AiService method, and model alias resolution for legacy OpenAI names**

## Performance

- **Duration:** 1 min
- **Started:** 2026-03-19T02:35:22Z
- **Completed:** 2026-03-19T02:36:41Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments
- Centralized x-request-id header generation via onRequest hook in v1-plugin, covering all responses including errors
- Fixed all 4 MVP AiService methods to return complete AI header set (x-model-routed, x-provider-used, x-provider-model, x-actual-credits)
- Created model alias config mapping legacy OpenAI names (gpt-3.5-turbo, gpt-4, gpt-4-turbo, text-embedding-ada-002) to Hive models

## Task Commits

Each task was committed atomically:

1. **Task 1: Add x-request-id hook and create model alias config** - `ae78674` (feat)
2. **Task 2: Fix MVP AiService header gaps and wire model alias resolution** - `842e9cf` (feat)

## Files Created/Modified
- `apps/api/src/config/model-aliases.ts` - Static alias map and resolveModelAlias function for legacy OpenAI model names
- `apps/api/src/routes/v1-plugin.ts` - Added onRequest hook setting x-request-id UUID on all /v1/* responses
- `apps/api/src/domain/ai-service.ts` - All 4 methods now return complete 4-header set including x-provider-used and x-provider-model
- `apps/api/src/domain/model-service.ts` - findById resolves aliases before model lookup

## Decisions Made
- Used onRequest hook (not onSend) for x-request-id to ensure header is set before any handler runs, guaranteeing presence on error responses
- Used "hive-mvp" as x-provider-used value since MVP AiService does not dispatch to real providers
- Model aliases passthrough unknown names unchanged to avoid breaking existing model IDs

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- All differentiator headers in place for Phase 8 remaining plans
- Model alias infrastructure ready for expansion with additional aliases

---
*Phase: 08-differentiators*
*Completed: 2026-03-19*
