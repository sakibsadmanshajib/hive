---
phase: 05-api-keys-hot-path-enforcement
plan: 02
subsystem: control-plane
tags: [api-keys, policies, authz, go, postgres]
provides:
  - Durable per-key policy storage with default launch-safe model groups and no default budget cap
  - Customer-visible key list/detail summaries derived from durable policy state
  - Internal `/internal/apikeys/resolve` contract for Redis-safe edge auth snapshots
affects: [05-03, 05-05, 05-06]
tech-stack:
  added: []
  patterns:
    - "Default API-key policy rows are created alongside key issuance"
    - "Customer-safe key views are projected from durable key and policy state"
key-files:
  created: []
  modified:
    - supabase/migrations/20260331_03_api_key_policies.sql
    - apps/control-plane/internal/apikeys/types.go
    - apps/control-plane/internal/apikeys/repository.go
    - apps/control-plane/internal/apikeys/service.go
    - apps/control-plane/internal/apikeys/http.go
    - apps/control-plane/internal/apikeys/service_test.go
    - apps/control-plane/internal/apikeys/http_test.go
key-decisions:
  - "The edge rehydrates auth snapshots through `/internal/apikeys/resolve` instead of gaining any direct database path"
  - "Default keys stay on the curated `default` group until an owner explicitly customizes policy"
patterns-established:
  - "Allowlist resolution unions group members with explicit aliases, then subtracts denied aliases"
  - "Customer-visible summaries are emitted without ever re-exposing raw key material"
duration: 35min
completed: 2026-04-02
---

# Phase 05 Plan 02: Durable Key Policy Projection Summary

**API keys now have durable model/budget policy truth, customer-visible list/detail summaries, and a narrow internal auth snapshot contract for the edge**

## Performance

- **Duration:** 35 min
- **Tasks:** 2
- **Files modified:** 7

## Accomplishments

- Re-verified the existing `a830f96` implementation that added durable `api_key_policies`, policy-backed key summary projections, and the `/internal/apikeys/resolve` contract.
- Completed the missing list/detail response projection flow in the control-plane handler so policy-backed `expiration_summary`, `budget_summary`, and `allowlist_summary` are emitted consistently.
- Added focused tests for default policy creation, group-member resolution, all-model expansion, expired snapshot projection, and customer-visible key summaries.

## Task Commits

1. **Task 1: Add durable key-policy storage, default policy creation, and snapshot hydration** - `a830f96`
2. **Task 2: Expose policy-backed key summaries and the internal resolver contract cleanly through the handler/tests** - verified and finalized in the resumed workspace, with downstream projection work continuing in `506edfb`

## Files Created/Modified

- `supabase/migrations/20260331_03_api_key_policies.sql` - durable policy tables, policy groups, and seeded memberships.
- `apps/control-plane/internal/apikeys/types.go` - key policy, summary, and auth snapshot types.
- `apps/control-plane/internal/apikeys/repository.go` - policy reads/writes, group-member expansion, and token-hash lookup helpers.
- `apps/control-plane/internal/apikeys/service.go` - default policy creation, key views, and snapshot hydration.
- `apps/control-plane/internal/apikeys/http.go` - current-account list/detail/create/rotate responses backed by projected key views.
- `apps/control-plane/internal/apikeys/service_test.go` - policy resolution and summary projection coverage.
- `apps/control-plane/internal/apikeys/http_test.go` - handler coverage for summary-bearing list/detail responses and internal resolve output.

## Decisions & Deviations

- The original code landed before this execute-phase resumed, so this summary captures both the existing `a830f96` baseline and the verification/cleanup needed to make the plan explicitly complete.
- No behavior was widened beyond the plan; follow-up budget/rate projection work was deferred to `05-05`.

## Next Phase Readiness

- `05-03` can consume the resolver contract without direct Postgres access.
- `05-05` can extend the same snapshot shape with live budget-window totals and separate account/key rate policies.

## Self-Check

- [x] `apps/control-plane/internal/apikeys/service.go` contains `ResolveSnapshot`
- [x] `apps/control-plane/internal/apikeys/types.go` contains `type KeyView`
- [x] `apps/control-plane/internal/apikeys/service_test.go` contains `TestListKeyViewsExposeDefaultSummaries`
- [x] `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/apikeys -count=1"` passed
