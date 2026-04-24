---
phase: 05-api-keys-hot-path-enforcement
plan: 05
subsystem: control-plane
tags: [api-keys, accounting, usage, authz, go]
provides:
  - Live per-key budget-window projection in control-plane auth snapshots
  - Separate account-tier and key-tier rate-policy projection for the edge contract
  - Budget-affecting snapshot invalidation tied to reservation and usage finalization flows
affects: [05-06, 05-verification]
tech-stack:
  added: []
  patterns:
    - "API-key snapshots project durable budget windows instead of hard-coded zero totals"
    - "Account and key rate policies remain distinct sources all the way into the edge JSON contract"
key-files:
  created:
    - .planning/phases/05-api-keys-hot-path-enforcement/05-05-SUMMARY.md
  modified:
    - apps/control-plane/internal/accounting/service.go
    - apps/control-plane/internal/accounting/service_test.go
    - apps/control-plane/internal/usage/types.go
    - apps/control-plane/internal/usage/repository.go
    - apps/control-plane/internal/apikeys/types.go
    - apps/control-plane/internal/apikeys/repository.go
    - apps/control-plane/internal/apikeys/service.go
    - apps/control-plane/internal/apikeys/service_test.go
    - apps/control-plane/internal/apikeys/http_test.go
key-decisions:
  - "Budget truth stays derived from the shared-wallet/accounting flow instead of introducing any per-key balance table"
  - "Snapshot invalidation errors remain user-visible because stale Redis auth budget truth is a correctness bug"
patterns-established:
  - "Reservation and finalization flows update durable per-key budget windows, then invalidate the cached snapshot for the affected key"
duration: 42min
completed: 2026-04-02
---

# Phase 05 Plan 05: Live Budget And Rate Projection Summary

**Control-plane auth snapshots now include live key budget windows plus separate account and key rate-policy objects, and budget-affecting mutations invalidate stale Redis snapshots**

## Performance

- **Duration:** 42 min
- **Tasks:** 2
- **Files modified:** 9

## Accomplishments

- Reused the existing `ac95f2d` accounting/usage projection baseline and completed the missing snapshot-projection work in `506edfb`.
- Added `RatePolicy`, `BudgetWindow`, and separate `account_rate_policy` / `key_rate_policy` fields to the control-plane auth snapshot contract.
- Changed reservation/finalization flows to resolve the configured budget window kind inside the API-key service, update durable budget windows, and invalidate cached auth snapshots on success.
- Added tests for live budget-window reads, separate rate-policy projection, configured monthly-window mutation behavior, invalidate-on-delta, and internal resolver JSON coverage.

## Task Commits

1. **Task 1: Extend accounting and usage to maintain live per-key budget windows and rollups** - `ac95f2d`
2. **Task 2: Resolve live budget totals and separate account/key rate policies into the auth snapshot** - `506edfb`

## Files Created/Modified

- `apps/control-plane/internal/accounting/service.go` - resolves budget kind through the API-key service instead of hard-coding lifetime windows.
- `apps/control-plane/internal/accounting/service_test.go` - verifies configured window-kind behavior from reservation/finalize flows.
- `apps/control-plane/internal/usage/types.go` - preserves API-key attribution through usage records.
- `apps/control-plane/internal/usage/repository.go` - writes per-event `api_key_id` attribution into durable usage rows.
- `apps/control-plane/internal/apikeys/types.go` - adds rate-policy and budget-window projection types.
- `apps/control-plane/internal/apikeys/repository.go` - loads budget windows plus distinct account/key rate-policy sources.
- `apps/control-plane/internal/apikeys/service.go` - projects live budget totals into `ResolveSnapshot` and invalidates stale snapshots after budget-affecting deltas.
- `apps/control-plane/internal/apikeys/service_test.go` - covers live-budget, separate-rate-policy, configured-monthly-window, and invalidation behavior.
- `apps/control-plane/internal/apikeys/http_test.go` - verifies the internal resolver emits the new budget/rate fields.

## Decisions & Deviations

- The resumed workspace already contained Task 1 in committed form, so this session focused on finishing the missing Task 2 projection/invalidation work and verifying the full seam.
- No extra wallet semantics were introduced; all new counters stay projection-only.

## Next Phase Readiness

- `05-06` can now enforce account and key thresholds independently on the edge without guessing at budget totals or reusing the wrong policy source.

## Self-Check

- [x] `apps/control-plane/internal/apikeys/types.go` contains `json:"account_rate_policy,omitempty"`
- [x] `apps/control-plane/internal/apikeys/types.go` contains `json:"key_rate_policy,omitempty"`
- [x] `apps/control-plane/internal/apikeys/service_test.go` contains `TestBudgetAffectingDeltaInvalidatesSnapshot`
- [x] `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/apikeys -count=1"` passed
- [x] `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/accounting/... ./apps/control-plane/internal/usage/... ./apps/control-plane/cmd/server -count=1"` passed
