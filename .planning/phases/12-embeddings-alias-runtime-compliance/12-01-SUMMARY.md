---
phase: 12-embeddings-alias-runtime-compliance
plan: 01
subsystem: api
tags: [embeddings, model-aliases, provider-registry, openai-compatibility, vitest]
requires:
  - phase: 07-surface-expansion
    provides: Embeddings route/runtime/provider pipeline
  - phase: 08-differentiators
    provides: Alias resolution and DIFF header conventions
  - phase: 11-real-openai-sdk-regression-tests-ci-style-e2e
    provides: SDK regression harness and runtime gap evidence
provides:
  - Canonical public embeddings catalog id `text-embedding-3-small`
  - Compatibility alias from `text-embedding-ada-002` to the canonical embeddings id
  - Embeddings-only upstream provider model mapping that keeps provider namespacing internal
  - Focused alias/runtime/provider regression coverage for DIFF-03
affects: [12-02, DIFF-03, embeddings, openai-sdk-regression]
tech-stack:
  added: []
  patterns:
    - Public embeddings identity stays in the catalog and response body while provider identity stays in DIFF headers
    - ProviderRegistry can use capability-specific upstream model maps without changing chat or image routing
key-files:
  created: []
  modified:
    - apps/api/src/config/model-aliases.ts
    - apps/api/src/domain/model-service.ts
    - apps/api/src/providers/registry.ts
    - apps/api/src/config/__tests__/model-aliases.test.ts
    - apps/api/test/domain/model-service.test.ts
    - apps/api/test/providers/provider-registry.test.ts
    - apps/api/src/routes/__tests__/embeddings-compliance.test.ts
    - apps/api/src/routes/__tests__/differentiators-headers.test.ts
key-decisions:
  - "Canonicalized the public embeddings catalog on `text-embedding-3-small` while keeping `text-embedding-ada-002` as a compatibility alias."
  - "Mapped embeddings to `openai/text-embedding-3-small` only inside ProviderRegistry so response bodies and `x-model-routed` stay on the public id."
patterns-established:
  - "Embeddings follow the DIFF boundary: body.model and x-model-routed are public ids, x-provider-model is the upstream provider id."
  - "Focused plan-task regression packs can be committed separately from the repo-required full-suite and Docker build completion gates."
requirements-completed: [DIFF-03]
duration: 6 min
completed: 2026-03-22
---

# Phase 12 Plan 01: Canonicalize public embeddings IDs and keep provider namespacing behind the API boundary Summary

**Canonical public embeddings identity on `text-embedding-3-small` with internal-only upstream routing to `openai/text-embedding-3-small` and focused DIFF-03 regression coverage**

## Performance

- **Duration:** 6 min
- **Started:** 2026-03-22T08:46:59Z
- **Completed:** 2026-03-22T08:52:14Z
- **Tasks:** 3
- **Files modified:** 8

## Accomplishments
- Re-keyed the public alias map and model catalog so embeddings now resolve on `text-embedding-3-small` while `text-embedding-ada-002` remains supported.
- Updated ProviderRegistry so embeddings requests route upstream as `openai/text-embedding-3-small` without leaking that provider id into public response bodies.
- Added focused regression coverage for alias resolution, public catalog lookup, provider boundary behavior, and public embeddings fixtures.

## Task Commits

Each task was committed atomically:

1. **Task 1: Re-key the public embeddings alias map and catalog entry to text-embedding-3-small** - `417d40b` (fix)
2. **Task 2: Route embeddings to the provider model internally while returning the public model id externally** - `852ac9c` (fix)
3. **Task 3: Run the focused alias/runtime regression pack and leave the full API suite plus Docker-only build as explicit plan gates** - `87d6d7a` (test)

**Plan metadata:** summary/tracking recovered after execution; code commits already existed before this artifact pass

## Files Created/Modified
- `apps/api/src/config/model-aliases.ts` - Embeddings compatibility alias now resolves to the canonical public id.
- `apps/api/src/domain/model-service.ts` - Public embeddings catalog entry now uses `text-embedding-3-small`.
- `apps/api/src/providers/registry.ts` - Embeddings dispatch now maps to the upstream provider id while returning the public response id.
- `apps/api/src/config/__tests__/model-aliases.test.ts` - Locks the embeddings alias and pass-through canonical id behavior.
- `apps/api/test/domain/model-service.test.ts` - Verifies canonical lookup, compatibility alias lookup, and list output.
- `apps/api/test/providers/provider-registry.test.ts` - Verifies embeddings upstream-provider routing and public/provider model separation.
- `apps/api/src/routes/__tests__/embeddings-compliance.test.ts` - Static embeddings fixture now reflects the public id.
- `apps/api/src/routes/__tests__/differentiators-headers.test.ts` - Embeddings header fixture now uses the public request model id.

## Decisions Made
- Made `text-embedding-3-small` the only public embeddings catalog id in this plan because the real runtime and response contract need to match the OpenAI-facing API surface.
- Kept `openai/text-embedding-3-small` as an internal upstream routing target exposed only through `x-provider-model` so public response bodies stay provider-agnostic.
- Used a dedicated embeddings-only upstream model map instead of changing shared chat or image provider dispatch logic.

## Verification Run
- `pnpm --filter @hive/api exec vitest run src/config/__tests__/model-aliases.test.ts test/domain/model-service.test.ts test/providers/provider-registry.test.ts src/routes/__tests__/embeddings-compliance.test.ts src/routes/__tests__/differentiators-headers.test.ts` - passed (`5` files, `44` tests)
- `pnpm --filter @hive/api test` - passed (`69` files, `368` tests)
- `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` - passed

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Ready for `12-02-PLAN.md`; the public catalog, alias path, provider boundary, focused regressions, full API suite, and Docker-only API build all passed.
- Phase 12 remains in progress because `12-02` is still pending, and Phase 13 stays recorded as the current completed phase to avoid regressing out-of-order milestone state.

---
*Phase: 12-embeddings-alias-runtime-compliance*
*Completed: 2026-03-22*
