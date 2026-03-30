---
phase: 03-credits-ledger-usage-accounting
plan: 01
subsystem: billing-ledger
tags: [billing, control-plane, postgres, redis, go, current-account]

# Dependency graph
requires:
  - phase: 02-identity-account-foundation (plan 02)
    provides: "Authenticated current-account resolution and workspace membership semantics"
  - phase: 02-identity-account-foundation (plan 07)
    provides: "Control-plane pgx patterns and current-account profile routing conventions"
provides:
  - "Immutable credit ledger and idempotency schema in Supabase Postgres"
  - "Current-account balance and ledger-history APIs in the control-plane"
  - "Redis runtime wiring for future reservation and hot-path accounting helpers"
affects: [03-02, 03-03, 04-model-catalog-provider-routing, 05-api-keys-hot-path-enforcement]

# Tech tracking
tech-stack:
  added: [github.com/redis/go-redis/v9]
  patterns:
    - "Balance summaries are derived from immutable ledger entries instead of a mutable balance field"
    - "Financial mutation idempotency is enforced by account + operation type + idempotency key"
    - "Current-account credit routes resolve workspace context through accounts.Service, not request payload IDs"

key-files:
  created:
    - apps/control-plane/internal/platform/redis/client.go
    - supabase/migrations/20260330_01_credits_ledger.sql
    - apps/control-plane/internal/ledger/types.go
    - apps/control-plane/internal/ledger/repository.go
    - apps/control-plane/internal/ledger/service.go
    - apps/control-plane/internal/ledger/http.go
    - apps/control-plane/internal/ledger/service_test.go
    - apps/control-plane/internal/ledger/http_test.go
  modified:
    - .env.example
    - deploy/docker/docker-compose.yml
    - apps/control-plane/go.mod
    - apps/control-plane/go.sum
    - apps/control-plane/internal/platform/config/config.go
    - apps/control-plane/internal/platform/http/router.go
    - apps/control-plane/cmd/server/main.go

key-decisions:
  - "Reservation holds use negative ledger deltas and releases use positive deltas so reserved balance can be derived from the absolute net hold/release sum"
  - "Ledger idempotency is anchored in Postgres tables immediately, while Redis is introduced as runtime plumbing for later hot-path helpers rather than as balance truth"
  - "Credit balance and ledger inspection stay current-account scoped and authenticated through the existing accounts.Service resolver"

patterns-established:
  - "Ledger repository pattern: transactional idempotency-key insert, immutable ledger insert, then idempotency row linkage"
  - "Credit read API pattern: GET current-account balance and ledger endpoints share the profiles-style account resolution helper"
  - "Verification pattern: live-workspace Go checks run in the toolchain container because the runtime control-plane image snapshots source at build time"

requirements:
  - BILL-01

# Metrics
duration: 9min
completed: 2026-03-30
---

# Phase 03 Plan 01: Ledger Foundation Summary

**Immutable workspace credit ledger tables, Redis runtime plumbing, and authenticated current-account balance APIs for Phase 3 billing correctness**

## Performance

- **Duration:** 9 min
- **Started:** 2026-03-30T13:33:01-04:00
- **Completed:** 2026-03-30T13:41:56-04:00
- **Tasks:** 2/2 complete
- **Files modified:** 15

## Accomplishments
- Added Redis configuration and Docker wiring so Phase 3 can rely on a Docker-native Redis dependency during control-plane execution and future hot-path accounting work
- Created the immutable `credit_ledger_entries` and `credit_idempotency_keys` schema needed for append-only financial mutations and duplicate-safe posting
- Added a new `internal/ledger` package with pgx-backed posting, derived balance reads, current-account ledger history reads, and HTTP coverage for balance and ledger endpoints
- Wired the control-plane server and router to expose authenticated `GET /api/v1/accounts/current/credits/balance` and `GET /api/v1/accounts/current/credits/ledger`

## Task Commits

Each task was committed atomically:

1. **Task 1: Add Redis plumbing and the immutable ledger schema** - `ff0deaf` (feat)
2. **Task 2: Create the ledger package and current-account credit read APIs** - `a037aec` (feat)

## Files Created/Modified
- `.env.example` - Adds the Phase 3 `REDIS_URL` runtime key
- `deploy/docker/docker-compose.yml` - Adds the Redis service and wires it into the control-plane container lifecycle
- `apps/control-plane/internal/platform/config/config.go` - Carries optional Redis configuration through the control-plane runtime config
- `apps/control-plane/internal/platform/redis/client.go` - Provides shared Redis client creation and ping helpers
- `supabase/migrations/20260330_01_credits_ledger.sql` - Defines immutable ledger entries and idempotency tracking tables with privacy-safe comments
- `apps/control-plane/internal/ledger/repository.go` - Implements pgx-backed idempotent posting, derived balance reads, and current-account ledger listing
- `apps/control-plane/internal/ledger/service.go` - Adds grant/adjust validation plus balance and ledger read orchestration
- `apps/control-plane/internal/ledger/http.go` - Exposes authenticated current-account balance and ledger routes
- `apps/control-plane/internal/platform/http/router.go` - Registers the new current-account credit routes behind auth
- `apps/control-plane/cmd/server/main.go` - Initializes Redis when configured and wires the ledger handler into the server

## Decisions Made
- Represented reservation holds as negative deltas and releases as positive deltas so reserved-credit math stays derivable from immutable events without a separate mutable balance counter
- Kept idempotency truth in Postgres from the first ledger implementation rather than coupling correctness to Redis availability
- Reused the current-account resolution pattern from profiles so credit reads never trust client-supplied account IDs

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- The local shell does not provide `go`, so Go verification ran through Docker containers.
- The `control-plane` runtime image snapshots source at build time and does not bind-mount the workspace, so live-source TDD verification used the `toolchain` container instead.

## Next Phase Readiness

- `03-02` can now persist request-attempt and usage-event records against a real current-account ledger and stable idempotency primitives.
- `03-03` can layer reservation, release, charge, and refund flows on top of the immutable ledger entry model established here.

## Self-Check

- [x] `.env.example` contains `REDIS_URL=redis://redis:6379/0`
- [x] `supabase/migrations/20260330_01_credits_ledger.sql` contains `create table public.credit_ledger_entries`
- [x] `supabase/migrations/20260330_01_credits_ledger.sql` contains `create table public.credit_idempotency_keys`
- [x] `apps/control-plane/internal/ledger/service.go` contains `func (s *Service) GetBalance`
- [x] `apps/control-plane/internal/ledger/service.go` contains `GrantCredits`
- [x] `apps/control-plane/internal/ledger/http.go` contains `/api/v1/accounts/current/credits/balance`
- [x] `apps/control-plane/internal/ledger/http.go` contains `/api/v1/accounts/current/credits/ledger`
- [x] `apps/control-plane/internal/ledger/service_test.go` contains `TestDuplicateGrantReturnsExistingEntry`
- [x] `apps/control-plane/internal/ledger/http_test.go` contains `TestListLedgerEntriesDefaultsLimit`
- [x] `docker compose -f deploy/docker/docker-compose.yml --profile tools run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/ledger/... -count=1"` passed
- [x] `docker compose -f deploy/docker/docker-compose.yml --profile tools run --rm toolchain "cd /workspace/apps/control-plane && go test ./... -count=1"` passed
