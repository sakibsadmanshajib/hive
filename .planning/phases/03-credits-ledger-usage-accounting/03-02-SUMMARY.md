---
phase: 03-credits-ledger-usage-accounting
plan: 02
subsystem: usage-accounting
tags: [billing, control-plane, usage, privacy, postgres, go]

# Dependency graph
requires:
  - phase: 03-credits-ledger-usage-accounting (plan 01)
    provides: "Immutable ledger foundations, current-account handler patterns, and Redis-aware control-plane wiring"
provides:
  - "Durable request-attempt records keyed by account, request_id, and attempt_number"
  - "Privacy-safe usage events with recursive metadata redaction and provider-blind customer responses"
  - "Authenticated current-account request-attempt and usage-event inspection APIs"
affects: [03-03, 05-api-keys-hot-path-enforcement, 06-core-text-embeddings-api, 09-developer-console-operational-hardening]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Request-accounting separates customer-visible request IDs from durable attempt identities"
    - "Usage-event persistence redacts sensitive prompt/message/input/response/completion keys recursively before storage"
    - "Customer-visible usage APIs remain provider-blind even when internal records keep provider diagnostics"

key-files:
  created:
    - supabase/migrations/20260330_02_usage_accounting.sql
    - apps/control-plane/internal/usage/types.go
    - apps/control-plane/internal/usage/repository.go
    - apps/control-plane/internal/usage/service.go
    - apps/control-plane/internal/usage/http.go
    - apps/control-plane/internal/usage/service_test.go
    - apps/control-plane/internal/usage/http_test.go
  modified:
    - apps/control-plane/cmd/server/main.go
    - apps/control-plane/internal/platform/http/router.go

key-decisions:
  - "Usage accounting keeps both request_id and attempt_number so retries and interrupted executions stay reconcilable without inventing a second wallet model"
  - "Recursive redaction removes prompt, message, input, response, completion, content, and output_text keys before internal metadata is persisted"
  - "Customer-visible usage responses omit provider_request_id and internal_metadata even though the internal event store can keep those diagnostics"

patterns-established:
  - "Usage repository pattern: account-scoped list queries with optional request_id filtering and JSONB tag/metadata decoding"
  - "Privacy boundary pattern: usage service redacts before persistence and usage HTTP handlers shape a provider-blind response model"
  - "Current-account inspection pattern: request-attempt and usage-event routes reuse the accounts.Service resolver instead of trusting request params for account identity"

requirements:
  - BILL-01
  - BILL-02
  - PRIV-01

# Metrics
duration: 5min
completed: 2026-03-30
---

# Phase 03 Plan 02: Usage Accounting Summary

**Durable request-attempt and usage-event records with recursive transcript-field redaction and provider-blind current-account inspection APIs**

## Performance

- **Duration:** 5 min
- **Started:** 2026-03-30T13:51:08-04:00
- **Completed:** 2026-03-30T13:56:13-04:00
- **Tasks:** 2/2 complete
- **Files modified:** 9

## Accomplishments
- Added the `request_attempts` and `usage_events` schema needed to explain billing outcomes without storing prompt or response payloads at rest
- Created a new `internal/usage` repository and service layer for durable attempt creation, status changes, event recording, and account-scoped history reads
- Implemented recursive metadata redaction so sensitive prompt/message/input/response/completion keys are stripped before usage metadata is persisted
- Exposed authenticated `GET /api/v1/accounts/current/request-attempts` and `GET /api/v1/accounts/current/usage-events` routes, with customer-visible usage responses kept provider-blind

## Task Commits

Each task was committed atomically:

1. **Task 1: Add request-attempt and privacy-safe usage-event schema** - `4ec4b11` (feat)
2. **Task 2: Add metadata redaction rules and current-account usage inspection APIs** - `fbee697` (feat)

## Files Created/Modified
- `supabase/migrations/20260330_02_usage_accounting.sql` - Defines durable request-attempt and usage-event tables with privacy-safe comments and account-scoped indexes
- `apps/control-plane/internal/usage/types.go` - Declares request-attempt, usage-event, and input/filter DTOs plus shared status/event enums
- `apps/control-plane/internal/usage/repository.go` - Implements pgx-backed attempt creation, status updates, usage-event recording, and account-scoped list queries
- `apps/control-plane/internal/usage/service.go` - Adds validation, recursive metadata redaction, and request-attempt / usage-event orchestration
- `apps/control-plane/internal/usage/http.go` - Exposes current-account usage inspection routes and strips provider-only fields from API responses
- `apps/control-plane/internal/platform/http/router.go` - Registers current-account request-attempt and usage-event routes behind auth
- `apps/control-plane/cmd/server/main.go` - Wires the usage repository, service, and handler into the control-plane server

## Decisions Made
- Kept request attempts and usage events account-scoped with optional attribution fields so later API-key, team, and service-account reporting can layer on top of the shared workspace wallet
- Applied redaction at the service boundary before persistence so repository callers cannot accidentally store transcript-like metadata
- Returned provider-blind usage-event payloads from the customer-facing route while preserving internal provider diagnostics for future reconciliation work

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- The local shell still lacks `go`, so verification continued through the live-workspace Docker toolchain container.
- Only the public Supabase URL and publishable browser key were available during this step; service-role, DB, and Redis envs remained unset, which was acceptable for the pure unit-test verification performed here.

## Next Phase Readiness

- `03-03` can now create reservations against durable request attempts, record lifecycle events like `reservation_created` and `released`, and update attempt statuses for streaming, completion, failure, cancellation, and interruption paths.
- Later billing analytics and hot-path key enforcement work can query account-scoped usage history without relying on transcript storage.

## Self-Check

- [x] `supabase/migrations/20260330_02_usage_accounting.sql` contains `create table public.request_attempts`
- [x] `supabase/migrations/20260330_02_usage_accounting.sql` contains `create table public.usage_events`
- [x] `supabase/migrations/20260330_02_usage_accounting.sql` does not define columns named `prompt`, `messages`, `response_body`, or `completion_text`
- [x] `apps/control-plane/internal/usage/service.go` contains `func RedactMetadata`
- [x] `apps/control-plane/internal/usage/http.go` contains `/api/v1/accounts/current/request-attempts`
- [x] `apps/control-plane/internal/usage/http.go` contains `/api/v1/accounts/current/usage-events`
- [x] `apps/control-plane/internal/usage/http.go` omits `provider_request_id` and `internal_metadata`
- [x] `apps/control-plane/internal/usage/service_test.go` contains `TestRecordEventRedactsPromptFields`
- [x] `apps/control-plane/internal/usage/http_test.go` contains `TestListRequestAttemptsDefaultsLimit`
- [x] `docker compose -f deploy/docker/docker-compose.yml --profile tools run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/usage/... -count=1"` passed
- [x] `docker compose -f deploy/docker/docker-compose.yml --profile tools run --rm toolchain "cd /workspace/apps/control-plane && go test ./... -count=1"` passed
