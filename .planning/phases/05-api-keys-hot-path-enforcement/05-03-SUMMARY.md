---
phase: 05-api-keys-hot-path-enforcement
plan: 03
subsystem: edge-api
tags: [edge-api, authz, redis, api-keys, go]
provides:
  - Redis-first auth snapshot resolution with fallback to the control-plane internal resolver
  - Edge hot-path alias and projected-budget admission checks before request execution
  - Reusable authorizer wiring for public edge routes
affects: [05-06, api-compatibility]
tech-stack:
  added: []
  patterns:
    - "Edge auth state is cache-first and rehydrated through `/internal/apikeys/resolve`"
    - "Alias denial and budget denial happen before routing/upstream execution"
key-files:
  created:
    - .planning/phases/05-api-keys-hot-path-enforcement/05-03-SUMMARY.md
  modified:
    - apps/edge-api/internal/authz/authz.go
    - apps/edge-api/internal/authz/client.go
    - apps/edge-api/internal/authz/authorizer.go
    - apps/edge-api/cmd/server/main.go
    - apps/edge-api/cmd/server/main_test.go
key-decisions:
  - "The edge remains a consumer of control-plane snapshots instead of inventing its own durable auth storage"
  - "Projected-budget denials happen before upstream work starts, even for future billable handlers"
patterns-established:
  - "Public edge handlers can share one authorizer contract and supply endpoint-specific estimated cost values"
duration: 25min
completed: 2026-04-02
---

# Phase 05 Plan 03: Edge Snapshot Admission Summary

**The edge now resolves API-key auth snapshots from Redis/control-plane, rejects disallowed aliases in Hive alias space, and keeps `/v1/models` behind valid API-key admission**

## Performance

- **Duration:** 25 min
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- Re-verified the earlier `a830f96` edge snapshot consumer and authorizer baseline against the phase contract.
- Confirmed the server still routes `/v1/models` through API-key authorization and added a dedicated regression test so model listing cannot become public again.
- Kept the edge-side snapshot contract aligned with the control-plane auth projection while later `05-06` limiter work expanded the same path rather than replacing it.

## Task Commits

1. **Task 1: Add the edge auth snapshot contract and Redis-backed resolver client** - `a830f96`
2. **Task 2: Wire the authorizer into the edge server and preserve `/v1/models` admission** - contract re-verified here, with later limiter/header work layered in `10cd871`

## Files Created/Modified

- `apps/edge-api/internal/authz/authz.go` - edge-side auth snapshot contract and access checks.
- `apps/edge-api/internal/authz/client.go` - Redis-first snapshot resolution with control-plane fallback.
- `apps/edge-api/internal/authz/authorizer.go` - reusable authorization wrapper for public routes.
- `apps/edge-api/cmd/server/main.go` - model-list route authorization wiring.
- `apps/edge-api/cmd/server/main_test.go` - regression coverage for `/v1/models` requiring a valid API key.

## Decisions & Deviations

- This plan was mostly present before the execute-phase resumed, so completion here focused on verification and route-contract hardening rather than a from-scratch implementation.
- Rate limiting and retry metadata were intentionally left to `05-06` instead of being folded back into the base snapshot-admission slice.

## Next Phase Readiness

- `05-06` can layer account/key limiter behavior on top of the established snapshot-authorizer path without changing the core resolver contract.

## Self-Check

- [x] `apps/edge-api/internal/authz/client.go` uses `auth:key:{...}` cache keys
- [x] `apps/edge-api/internal/authz/client.go` falls back to `/internal/apikeys/resolve`
- [x] `apps/edge-api/cmd/server/main_test.go` contains `TestModelsRouteRequiresValidAPIKey`
- [x] `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz ./apps/edge-api/cmd/server -count=1"` passed
