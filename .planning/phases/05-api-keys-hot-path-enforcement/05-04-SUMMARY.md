---
phase: 05-api-keys-hot-path-enforcement
plan: 04
subsystem: auth
tags: [api-keys, authz, redis, accounting, usage, go]

# Dependency graph
requires:
  - phase: 05-api-keys-hot-path-enforcement (plan 01)
    provides: "Durable API-key lifecycle routes and mutation handlers"
  - phase: 05-api-keys-hot-path-enforcement (plan 02)
    provides: "Redis-backed auth snapshot resolution on the edge hot path"
  - phase: 05-api-keys-hot-path-enforcement (plan 03)
    provides: "Per-key attribution fields in request attempts, usage events, and accounting projections"
provides:
  - "Immediate Redis auth-snapshot invalidation on revoke, rotate, disable, enable, and policy updates"
  - "End-to-end API-key attribution through reservation creation, finalize completion events, and customer-visible usage responses"
affects: [05-verification, 06-core-text-embeddings-api, 09-developer-console-operational-hardening]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Control-plane lifecycle mutations actively delete `auth:key:{tokenHash}` so edge authorization rehydrates from durable state on the next request"
    - "Accounting finalize always emits a completed usage event with `api_key_id` when the attempt was attributed to a key"

key-files:
  created: []
  modified:
    - apps/control-plane/cmd/server/main.go
    - apps/control-plane/internal/apikeys/service.go
    - apps/control-plane/internal/apikeys/service_test.go
    - apps/control-plane/internal/accounting/http.go
    - apps/control-plane/internal/accounting/http_test.go
    - apps/control-plane/internal/accounting/service.go
    - apps/control-plane/internal/accounting/service_test.go
    - apps/control-plane/internal/usage/http.go
    - apps/control-plane/internal/usage/http_test.go

key-decisions:
  - "Auth snapshot invalidation stays in the control-plane API-key service because it already owns lifecycle truth and the Redis client"
  - "Finalize writes a `completed` usage event even when `released_credits == 0`, so exact-charge flows still leave customer-visible attribution"

patterns-established:
  - "Key mutation pattern: durable lifecycle updates can succeed, but Redis snapshot invalidation must happen in the same service path before the API reports success"
  - "Usage attribution pattern: `api_key_id` enters at reservation creation, survives through attempt lookups, and is echoed back in usage-event JSON plus `last_used_at`"

requirements-completed:
  - KEY-01
  - KEY-04
  - KEY-05

# Metrics
duration: 73min
completed: 2026-04-01
---

# Phase 05 Plan 04: Immediate Auth Cache Invalidation And API-Key Attribution Summary

**Redis-backed auth snapshots now invalidate immediately on API-key mutations, and finalized accounting flows expose `api_key_id` plus `last_used_at` back to customers**

## Performance

- **Duration:** 73 min
- **Started:** 2026-04-01T20:26:00-04:00
- **Completed:** 2026-04-01T21:39:00-04:00
- **Tasks:** 2/2 complete
- **Files modified:** 9

## Accomplishments

- Injected the control-plane Redis client into the API-key service and invalidated `auth:key:{tokenHash}` on revoke, rotate, disable, enable, and policy changes.
- Added focused cache-invalidation tests that cover revoke, rotate, policy updates, and invalidate-failure handling.
- Plumbed `api_key_id` through reservation creation and finalize, emitted completed usage events on exact-charge success paths, and exposed `api_key_id` in customer-visible usage responses.
- Verified the fixes both with package-level Go tests and against the rebuilt live Docker stack.

## Task Commits

Each task was committed atomically:

1. **Task 1: Invalidate cached auth snapshots immediately on key mutations** - `53c56f6` (fix)
2. **Task 2: Plumb `api_key_id` through accounting settlement and surface attributed usage** - `4f63c22` (fix)

## Files Created/Modified

- `apps/control-plane/cmd/server/main.go` - Wires the live Redis client into `apikeys.NewService`.
- `apps/control-plane/internal/apikeys/service.go` - Adds Redis snapshot invalidation helpers and calls them from lifecycle and policy mutations.
- `apps/control-plane/internal/apikeys/service_test.go` - Covers revoke, rotate, policy update, and invalidate-failure behavior.
- `apps/control-plane/internal/accounting/http.go` - Accepts and validates optional `api_key_id` on reservation creation.
- `apps/control-plane/internal/accounting/service.go` - Carries key attribution through finalize, writes completed usage events, and updates per-key usage hooks.
- `apps/control-plane/internal/accounting/http_test.go` - Verifies HTTP propagation of `api_key_id`.
- `apps/control-plane/internal/accounting/service_test.go` - Verifies completed finalize events, per-key usage finalization, and `last_used_at` updates.
- `apps/control-plane/internal/usage/http.go` - Includes `api_key_id` in customer-visible usage-event payloads.
- `apps/control-plane/internal/usage/http_test.go` - Verifies `api_key_id` appears when present.

## Decisions Made

- Kept cache invalidation as a narrow `SnapshotCache` interface so tests can assert mutation behavior without real Redis.
- Reused existing attempt lookup APIs in accounting instead of widening the usage repository surface for this gap-only fix.
- Rebuilt the live `control-plane` and `edge-api` images during verification because those services are image-backed, not bind-mounted from the workspace.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- The first executor run stalled because its Docker test command opened an interactive shell instead of running the intended command string. Verification was re-run locally with the correct `docker compose ... run --rm toolchain "..."` form.
- Initial live revocation checks were misleading because the smoke sequence ran cache warm-up, mutation, and post-mutation reads in parallel. Redis `MONITOR` showed the expected `DEL auth:key:{...}` command, and sequential live checks then passed.
- Restarting the running containers was insufficient because `control-plane` and `edge-api` rebuild from baked image sources. A full `docker compose ... up -d --build control-plane edge-api` was required before live smoke tests reflected the patched code.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Phase 05 can now be re-verified against the original UAT gaps with both immediate edge invalidation and end-to-end key attribution in place.
- Later phases can rely on `api_key_id` being present in request attempts, completed usage events, and `last_used_at` surfaces for operational tooling and console views.

## Self-Check

- [x] `apps/control-plane/internal/apikeys/service.go` no longer contains the `RefreshSnapshot` no-op placeholder behavior
- [x] `apps/control-plane/cmd/server/main.go` injects Redis into `apikeys.NewService`
- [x] `apps/control-plane/internal/apikeys/service_test.go` covers revoke/rotate/policy cache invalidation
- [x] `apps/control-plane/internal/accounting/http.go` accepts `api_key_id`
- [x] `apps/control-plane/internal/accounting/service.go` records a completed usage event on exact-charge finalize
- [x] `apps/control-plane/internal/usage/http.go` includes `api_key_id` when present
- [x] `docker compose --env-file .env -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/apikeys ./apps/control-plane/internal/accounting ./apps/control-plane/internal/usage ./apps/control-plane/cmd/server -count=1"` passed
- [x] Rebuilt live `control-plane` and `edge-api` images returned HTTP 401 for cached secrets immediately after revoke and rotate
- [x] Live accounting readback for `request_id=phase5-live-usage-20260401` showed `api_key_id`, a `completed` usage event, and `last_used_at` on key `30dc3fb6-945e-4976-8207-1deab8af394a`
