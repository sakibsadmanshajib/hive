---
phase: 05-api-keys-hot-path-enforcement
plan: 01
subsystem: api
tags: [go, postgres, api-keys, control-plane, apikeys]
requires:
  - phase: 02-identity-account-foundation
    provides: Verified-owner viewer gates and current-account resolution via accounts.Service.
  - phase: 03-credits-ledger-usage-accounting
    provides: Account-scoped control-plane route patterns and durable api_key_id attribution seams.
  - phase: 04-model-catalog-provider-routing
    provides: Launch-safe model-group defaults that the API-key summaries reference.
provides:
  - Durable API-key lifecycle schema with one-time secret hashing and per-key rotation/revocation.
  - Authenticated current-account API-key create, list, detail, rotate, disable, enable, and revoke routes.
  - Customer-visible expiration, budget, and allowlist summaries that never re-expose raw secrets.
affects: [05-02, 05-03, 09-01, api-compatibility]
tech-stack:
  added: []
  patterns:
    - pgx repository/service/http layering for API-key lifecycle management
    - hk_ secret generation with SHA-256 hash-at-rest storage
    - current-account route authorization via accounts.Service viewer-context resolution
key-files:
  created:
    - .planning/phases/05-api-keys-hot-path-enforcement/05-01-SUMMARY.md
  modified:
    - supabase/migrations/20260331_02_api_keys.sql
    - apps/control-plane/internal/apikeys/types.go
    - apps/control-plane/internal/apikeys/repository.go
    - apps/control-plane/internal/apikeys/service.go
    - apps/control-plane/internal/apikeys/service_test.go
    - apps/control-plane/internal/apikeys/http.go
    - apps/control-plane/internal/apikeys/http_test.go
    - apps/control-plane/cmd/server/main.go
    - apps/control-plane/internal/platform/http/router.go
key-decisions:
  - "API-key mutations remain gated by accounts.Service.EnsureViewerContext and CanManageAPIKeys instead of trusting client ownership claims."
  - "List/detail/create/rotate responses share a customer-safe serializer that applies expiry projection and never re-emits secrets after issuance."
patterns-established:
  - "Control-plane key routes resolve the authenticated viewer first, then derive the current account before any lookup or mutation."
  - "API-key lifecycle state is durable in Postgres while customer responses expose only hashed-secret metadata plus safe summaries."
requirements-completed: [KEY-01, KEY-03]
duration: 10min
completed: 2026-04-02
---

# Phase 05 Plan 01: API key lifecycle foundation Summary

**Durable API-key issuance and per-key lifecycle routes with one-time `hk_` secrets, verified-owner gating, and customer-safe summary responses**

## Performance

- **Duration:** 10 min
- **Started:** 2026-04-02T00:04:47Z
- **Completed:** 2026-04-02T00:15:10Z
- **Tasks:** 2
- **Files modified:** 9

## Accomplishments
- Re-verified the existing 05-01 lifecycle foundation commit that added the durable `api_keys`/`api_key_events` schema, hashed-secret storage, and per-key rotation semantics.
- Added the missing authenticated detail route for `GET /api/v1/accounts/current/api-keys/{key_id}` with the same expiry projection used by list responses.
- Filled the customer-visible response contract with `expiration_summary`, `budget_summary`, and `allowlist_summary` fields while keeping secrets create/rotate-only.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the API-key schema and lifecycle service with one-time secret handling** - `6c59970` (feat)
2. **Task 2: Add authenticated current-account API-key endpoints and control-plane wiring** - `ba5f669` (feat)

**Plan metadata:** included in the follow-up docs metadata commit for this plan

## Files Created/Modified
- `supabase/migrations/20260331_02_api_keys.sql` - durable API-key and event tables with raw-secret-at-rest prohibitions
- `apps/control-plane/internal/apikeys/types.go` - lifecycle types and exposed key states
- `apps/control-plane/internal/apikeys/repository.go` - pgx-backed key CRUD, transitions, and replacement-key transaction
- `apps/control-plane/internal/apikeys/service.go` - one-time secret creation, per-key lifecycle rules, and single-key expiry projection
- `apps/control-plane/internal/apikeys/service_test.go` - lifecycle and secret-handling tests
- `apps/control-plane/internal/apikeys/http.go` - current-account API-key routes, verified-owner gate, detail reads, and summary responses
- `apps/control-plane/internal/apikeys/http_test.go` - route tests covering create/list/detail behavior and secret omission
- `apps/control-plane/cmd/server/main.go` - API-key repository/service/handler wiring into the control-plane server
- `apps/control-plane/internal/platform/http/router.go` - authenticated current-account API-key route registration

## Decisions Made
- Reused the existing `accounts.Service.EnsureViewerContext` + `CanManageAPIKeys` gate as the only authority for API-key management.
- Added a dedicated `Service.GetKey` helper so list and detail reads expose expired keys consistently without mutating durable state.

## Deviations from Plan

None - plan executed against the existing 05-01 workspace baseline, and no Rule 1-4 auto-fixes were required.

## Issues Encountered

- The workspace already contained a prior 05-01 lifecycle commit plus unrelated phase-05 changes in adjacent files. Execution was kept scoped to the missing detail-route and summary-response delta, and unrelated edits were left untouched.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Plan 05-02 can build on the current list/detail/create/rotate response contract and replace the default budget/allowlist summaries with durable policy-backed summaries.
- KEY-02, KEY-04, and KEY-05 remain open for later Phase 5 plans.

## Self-Check: PASSED

- FOUND: `.planning/phases/05-api-keys-hot-path-enforcement/05-01-SUMMARY.md`
- FOUND: `6c59970`
- FOUND: `ba5f669`
