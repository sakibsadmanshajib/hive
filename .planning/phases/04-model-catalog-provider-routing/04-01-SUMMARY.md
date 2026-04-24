---
phase: 04-model-catalog-provider-routing
plan: 01
subsystem: api
tags: [catalog, models, edge-api, control-plane, postgres, go]

# Dependency graph
requires:
  - phase: 03-credits-ledger-usage-accounting
    provides: "Control-plane server wiring, Docker-based SDK regression harnesses, and usage-accounting primitives reused by the catalog slice"
provides:
  - "Seeded public Hive aliases and pricing metadata in public.model_aliases"
  - "Unauthenticated control-plane catalog snapshot for internal consumers"
  - "Edge-backed `/v1/models` and `/catalog/models` projections driven by the same snapshot"
affects: [04-02, 04-03, 05-api-keys-hot-path-enforcement, 06-core-text-embeddings-api]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Public alias data is projected from one control-plane snapshot rather than duplicated at the edge"
    - "Public model-list failures fail closed with a provider-blind `catalog_unavailable` OpenAI error"

key-files:
  created:
    - supabase/migrations/20260331_01_model_catalog.sql
    - apps/control-plane/internal/catalog/types.go
    - apps/control-plane/internal/catalog/repository.go
    - apps/control-plane/internal/catalog/service.go
    - apps/control-plane/internal/catalog/http.go
    - apps/edge-api/internal/catalog/client.go
  modified:
    - apps/control-plane/cmd/server/main.go
    - apps/control-plane/internal/platform/http/router.go
    - apps/edge-api/cmd/server/main.go
    - .env.example
    - deploy/docker/docker-compose.yml
    - packages/sdk-tests/fixtures/golden/models-list.json
    - packages/sdk-tests/js/tests/models/list-models.test.ts

key-decisions:
  - "The control-plane snapshot is the single source of truth for both the OpenAI model list and the richer Hive catalog projection"
  - "Edge model-list failures return a provider-blind OpenAI-style `catalog_unavailable` error instead of leaking snapshot or provider details"

patterns-established:
  - "Internal snapshot pattern: edge consumers call `/internal/catalog/snapshot` for Hive-owned alias data"
  - "Provider-blind public projection pattern: `/v1/models` stays lean while `/catalog/models` exposes richer metadata from the same snapshot"

requirements-completed:
  - ROUT-01

# Metrics
duration: 30min
completed: 2026-03-31
---

# Phase 04 Plan 01: Public Model Catalog Summary

**Seeded Hive-owned model aliases, one control-plane snapshot source of truth, and edge-backed `/v1/models` plus `/catalog/models` public projections**

## Performance

- **Duration:** 30 min
- **Started:** 2026-03-31T03:00:01-04:00
- **Completed:** 2026-03-31T03:30:50-04:00
- **Tasks:** 2/2 complete
- **Files modified:** 19

## Accomplishments

- Added `public.model_aliases` with seeded `hive-default`, `hive-fast`, and `hive-auto` rows plus catalog pricing and badge metadata.
- Built a new control-plane `internal/catalog` package and unauthenticated `/internal/catalog/snapshot` endpoint so public model projections share one source of truth.
- Replaced the edge's empty models handler with a snapshot-backed OpenAI model list and a richer `/catalog/models` surface, then aligned the SDK golden fixture with the new alias contract.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the control-plane model catalog schema, seeded aliases, and internal snapshot endpoint** - `948c318` (test), `2ff971d` (feat)
2. **Task 2: Back the public edge model endpoints with the control-plane snapshot and provider-blind SDK fixtures** - `fc4b0e3` (test), `6ad8ae2` (feat)

## Files Created/Modified

- `supabase/migrations/20260331_01_model_catalog.sql` - Defines the durable alias catalog schema and seeds the public Hive aliases used by both projections.
- `apps/control-plane/internal/catalog/repository.go` - Loads public alias rows and assembles the internal snapshot payload.
- `apps/control-plane/internal/catalog/service.go` - Maps alias rows into OpenAI-style model objects and richer catalog entries.
- `apps/control-plane/internal/catalog/http.go` - Exposes `GET /internal/catalog/snapshot` for internal edge consumption.
- `apps/edge-api/internal/catalog/client.go` - Fetches and decodes the control-plane snapshot from the edge.
- `apps/edge-api/cmd/server/main.go` - Wires the snapshot client into `/v1/models` and the new `/catalog/models` route with provider-blind failure handling.
- `packages/sdk-tests/fixtures/golden/models-list.json` - Updates the golden model-list fixture to the three public Hive aliases.
- `packages/sdk-tests/js/tests/models/list-models.test.ts` - Asserts seeded alias presence and provider-name absence in the public model list.

## Decisions Made

- Kept alias truth in the control-plane so the public OpenAI model list and the richer Hive catalog projection cannot drift.
- Added an explicit `EDGE_CONTROL_PLANE_BASE_URL` contract so edge-api Docker wiring points at the control-plane service instead of embedding per-environment URLs in code.
- Returned `catalog_unavailable` through the standard OpenAI-style error envelope when the snapshot fetch fails, preserving a provider-blind public failure mode.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Fixed adjacent control-plane compile blockers surfaced while wiring the new catalog package**
- **Found during:** Task 1 (Add the control-plane model catalog schema, seeded aliases, and internal snapshot endpoint)
- **Issue:** The new catalog wiring exposed existing compile blockers in `internal/accounting/repository.go` and `internal/usage/service.go`, which prevented the control-plane package graph from building cleanly.
- **Fix:** Removed unused reservation scan results and restored the missing `time` import required by the usage service's status-update API.
- **Files modified:** `apps/control-plane/internal/accounting/repository.go`, `apps/control-plane/internal/usage/service.go`
- **Verification:** `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace/apps/control-plane && go test ./internal/catalog/... -count=1'`
- **Committed in:** `2ff971d`

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** The fix was required to make the planned catalog package buildable. No scope expansion beyond restoring adjacent compile health.

## Issues Encountered

- The plan's Docker verification commands needed environment-aware adjustments in this workspace: Compose had to be launched with `--env-file .env`, and the `sdk-tests-js` image runs from `/tests` with `npm` rather than `/workspace/...` with `pnpm`.

## User Setup Required

None - no external service configuration required beyond the existing repo `.env` used for Docker verification.

## Next Phase Readiness

- Phase 04-02 can now layer internal route metadata and policy selection on top of the seeded alias catalog without inventing a second public source of truth.
- Phase 04-03 can reuse the same provider-blind boundary now established at the edge when it adds sanitized upstream error translation.

## Self-Check

- [x] `supabase/migrations/20260331_01_model_catalog.sql` contains `create table public.model_aliases`
- [x] `supabase/migrations/20260331_01_model_catalog.sql` contains `hive-default`
- [x] `supabase/migrations/20260331_01_model_catalog.sql` contains `hive-fast`
- [x] `supabase/migrations/20260331_01_model_catalog.sql` contains `hive-auto`
- [x] `apps/control-plane/internal/catalog/types.go` contains `type CatalogSnapshot`
- [x] `apps/control-plane/internal/catalog/http.go` contains `/internal/catalog/snapshot`
- [x] `apps/control-plane/internal/platform/http/router.go` contains `/internal/catalog/snapshot`
- [x] `apps/control-plane/internal/catalog/service_test.go` contains `TestGetSnapshotOmitsInternalAliases`
- [x] `apps/edge-api/internal/catalog/client.go` contains `func (c *Client) FetchSnapshot`
- [x] `apps/edge-api/cmd/server/main.go` contains `/catalog/models`
- [x] `apps/edge-api/cmd/server/main.go` contains `catalog_unavailable`
- [x] `packages/sdk-tests/js/tests/models/list-models.test.ts` contains `openrouter|groq`
- [x] `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace/apps/control-plane && go test ./internal/catalog/... -count=1'` exited 0
- [x] `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain sh -lc 'cd /workspace && go test ./apps/edge-api/internal/catalog ./apps/edge-api/cmd/server -count=1'` exited 0
- [x] `docker compose --env-file .env -f deploy/docker/docker-compose.yml run --rm sdk-tests-js sh -lc 'cd /tests && npm test -- --run tests/models/list-models.test.ts'` passed
