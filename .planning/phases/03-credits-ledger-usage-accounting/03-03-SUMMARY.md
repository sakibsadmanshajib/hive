---
phase: 03-credits-ledger-usage-accounting
plan: 03
subsystem: reservation-accounting
tags: [billing, control-plane, reservations, usage, postgres, go]

# Dependency graph
requires:
  - phase: 03-credits-ledger-usage-accounting (plan 01)
    provides: "Immutable ledger entries, idempotent credit posting, and current-account credit routing conventions"
  - phase: 03-credits-ledger-usage-accounting (plan 02)
    provides: "Durable request-attempt records, privacy-safe usage events, and usage status transitions"
provides:
  - "Durable reservation, reservation-event, and reconciliation-job schema"
  - "Accounting service rules for reserve, expand, finalize, release, and ambiguous-stream reconciliation"
  - "Authenticated current-account reservation lifecycle endpoints in the control-plane"
affects: [04-model-catalog-provider-routing, 05-api-keys-hot-path-enforcement, 06-core-text-embeddings-api, 09-developer-console-operational-hardening]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Reservation lifecycle state is stored durably in Postgres while immutable ledger entries remain the financial source of truth"
    - "Ambiguous settlement defaults to customer-favoring release behavior plus a reconciliation job instead of assuming full reserve consumption"
    - "Current-account reservation routes resolve workspace context through accounts.Service rather than trusting body-supplied account IDs"

key-files:
  created:
    - supabase/migrations/20260330_03_credit_reservations.sql
    - apps/control-plane/internal/accounting/types.go
    - apps/control-plane/internal/accounting/repository.go
    - apps/control-plane/internal/accounting/service.go
    - apps/control-plane/internal/accounting/http.go
    - apps/control-plane/internal/accounting/service_test.go
    - apps/control-plane/internal/accounting/http_test.go
  modified:
    - apps/control-plane/internal/ledger/service.go
    - apps/control-plane/internal/usage/service.go
    - apps/control-plane/internal/platform/http/router.go
    - apps/control-plane/cmd/server/main.go

key-decisions:
  - "Reservation events and reconciliation jobs are stored separately from immutable ledger entries so operational recovery can evolve without mutating financial history"
  - "Temporary overage is enforced as a bounded 10,000-credit policy overlay rather than a separate wallet or balance type"
  - "Release calls are idempotent at the service layer so repeated cancellations do not emit duplicate ledger releases or reservation events"

patterns-established:
  - "Accounting orchestration pattern: usage attempts, reservation rows, and ledger entries are coordinated by a dedicated accounting service"
  - "Settlement pattern: terminal usage charges actual credits, releases unused reserve, and ambiguous streams additionally create reconciliation jobs"
  - "Current-account mutation pattern: POST reservation endpoints mirror existing control-plane auth and account-resolution behavior"

requirements:
  - BILL-02

# Metrics
duration: 73min
completed: 2026-03-30
---

# Phase 03 Plan 03: Reservation Accounting Summary

**Durable reservation state, customer-favoring settlement rules, and authenticated current-account reservation lifecycle APIs for Phase 3 billing correctness**

## Performance

- **Duration:** 73 min
- **Started:** 2026-03-30T14:23:38-04:00
- **Completed:** 2026-03-30T15:36:46-04:00
- **Tasks:** 2/2 complete
- **Files modified:** 11

## Accomplishments
- Added the durable reservation, reservation-event, and reconciliation-job schema required to settle credits without mutating ledger history
- Built a new `internal/accounting` package that enforces strict versus temporary-overage reservation policy, finalizes real usage, and marks ambiguous interruptions for reconciliation
- Exposed authenticated `POST /api/v1/accounts/current/credits/reservations*` endpoints and wired them into the control-plane server

## Task Commits

Each task was committed atomically:

1. **Task 1: Add reservation and reconciliation schema plus accounting service rules** - `8d2baf8` (feat)
2. **Task 2: Add current-account reservation lifecycle endpoints and wire the accounting package into the control-plane** - `e586fc7` (feat)

## Files Created/Modified
- `supabase/migrations/20260330_03_credit_reservations.sql` - Defines durable reservation state, immutable reservation events, and reconciliation jobs
- `apps/control-plane/internal/accounting/service.go` - Implements reserve, expand, finalize, release, and reconciliation orchestration on top of ledger and usage services
- `apps/control-plane/internal/accounting/http.go` - Exposes authenticated current-account reservation lifecycle endpoints
- `apps/control-plane/internal/ledger/service.go` - Adds reservation hold, release, usage charge, and refund helper methods
- `apps/control-plane/internal/usage/service.go` - Adds typed request-attempt status updates used by reservation settlement flows
- `apps/control-plane/internal/platform/http/router.go` - Registers the current-account reservation endpoints behind auth
- `apps/control-plane/cmd/server/main.go` - Wires the accounting repository, service, and handler into the control-plane server

## Decisions Made
- Kept reservation lifecycle state explicit in Postgres instead of inferring everything from ledger events alone, so ambiguous streams can be queued for reconciliation without rewriting financial history
- Defaulted ambiguous interruptions to customer-favoring settlement by releasing unused reserve immediately and creating a reconciliation job for any later upstream evidence
- Reused the current-account HTTP pattern from the rest of the control-plane so reservation routes never trust caller-supplied account IDs

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- The local shell still lacks `go`, so verification ran through the live-workspace Docker toolchain container.
- The toolchain runner only surfaced stderr in this environment, so successful test verification relied on clean exit codes plus local acceptance checks.

## Next Phase Readiness

- Phase 4 can now rely on reservation creation and settlement hooks when provider routing starts dispatching real billable requests.
- Phase 5 hot-path enforcement can attach per-key budgets and rate limits to a real workspace-level reservation and usage-accounting substrate.

## Self-Check

- [x] `supabase/migrations/20260330_03_credit_reservations.sql` contains `create table public.credit_reservations`
- [x] `supabase/migrations/20260330_03_credit_reservations.sql` contains `create table public.credit_reconciliation_jobs`
- [x] `apps/control-plane/internal/accounting/service.go` contains `FinalizeReservation`
- [x] `apps/control-plane/internal/accounting/service_test.go` contains `TestFinalizeReservationMarksAmbiguousStreamForReconciliation`
- [x] `apps/control-plane/internal/accounting/service_test.go` contains `TestReleaseReservationWritesReleaseEventsOnlyOnce`
- [x] `apps/control-plane/internal/accounting/http_test.go` contains `TestCreateReservationUsesCurrentAccount`
- [x] `apps/control-plane/internal/accounting/http_test.go` contains `TestReleaseReservationReturnsPolicyError`
- [x] `apps/control-plane/internal/ledger/service.go` contains `ChargeUsage`
- [x] `docker compose -f deploy/docker/docker-compose.yml --profile tools run --rm toolchain sh -lc 'cd /workspace/apps/control-plane && go test ./internal/accounting -count=1'` passed
- [x] `docker compose -f deploy/docker/docker-compose.yml --profile tools run --rm toolchain sh -lc 'cd /workspace/apps/control-plane && go test ./... -count=1'` passed
