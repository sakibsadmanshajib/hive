---
phase: 10-routing-storage-critical-fixes
plan: 09
subsystem: api
tags: [go, edge-api, accounting, batches, images, audio]

# Dependency graph
requires:
  - phase: 10-routing-storage-critical-fixes
    provides: Verified route and storage fixes from plans 10-03 through 10-08
provides:
  - Accepted strict reservation policy modes for image, audio, and batch accounting calls
  - Batch model-alias propagation from JSONL validation into reservation creation
  - Batch JSONL validation that rejects missing or mixed body.model attribution
affects: [edge-api, accounting, batches, phase-10-gap-closure]

# Tech tracking
tech-stack:
  added: []
  patterns: [strict reservation policy mode, batch model-alias validation, reservation attribution propagation]

key-files:
  created:
    - .planning/phases/10-routing-storage-critical-fixes/10-09-SUMMARY.md
  modified:
    - apps/edge-api/internal/images/accounting_adapter.go
    - apps/edge-api/internal/images/accounting_adapter_test.go
    - apps/edge-api/internal/audio/accounting_adapter.go
    - apps/edge-api/internal/audio/accounting_adapter_test.go
    - apps/edge-api/internal/batches/accounting_adapter.go
    - apps/edge-api/internal/batches/accounting_adapter_test.go
    - apps/edge-api/internal/batches/handler.go
    - apps/edge-api/internal/batches/handler_test.go

key-decisions:
  - "Image, audio, and batch reservations use policy_mode strict because control-plane accounting accepts strict today and prepaid paths must not overrun credits."
  - "Batch reservation attribution derives the model alias from JSONL body.model and rejects missing or mixed aliases before reservation creation."

patterns-established:
  - "Reservation adapters must send only accounting policy modes accepted by control-plane validation."
  - "Batch create validates model attribution before reserving credits so downstream accounting can roll up spend by model."

requirements-completed: [ROUT-02, API-05, API-06, API-07]

# Metrics
duration: 7 min
completed: 2026-04-21
---

# Phase 10 Plan 09: Edge Reservation Contract Repair Summary

**Strict accounting policy modes for image and audio flows plus batch model-alias propagation and JSONL validation for reservation attribution.**

## Performance

- **Duration:** 7 min
- **Started:** 2026-04-20T20:54:38-04:00
- **Completed:** 2026-04-20T21:01:22-04:00
- **Tasks:** 3
- **Files modified:** 8

## Accomplishments

- Changed image and audio reservation adapters to send `policy_mode:"strict"` and proved the request contract with focused adapter tests.
- Propagated batch `model_alias` into reservations and updated batch JSONL validation to reject missing or mixed `body.model` values before credits are reserved.
- Ran the focused image, audio, and batch package regression suites and confirmed the rejected reservation values are gone from the touched sources.

## Task Commits

Each task was committed atomically:

1. **Task 1: Use strict policy mode for image and audio reservations** - `867605b`, `446a8df` (test, fix)
2. **Task 2: Derive and pass batch model alias into reservations** - `034f2d8`, `c1b2557` (test, fix)
3. **Task 3: Run edge reservation contract regression checks** - verification only (no code changes)

**Plan metadata:** final docs commit created after state updates.

## Files Created/Modified

- `apps/edge-api/internal/images/accounting_adapter.go` - Sends strict accounting policy mode for image reservations.
- `apps/edge-api/internal/images/accounting_adapter_test.go` - Verifies image reservations send strict policy, expected API key, model alias, and estimated credits.
- `apps/edge-api/internal/audio/accounting_adapter.go` - Sends strict accounting policy mode for audio reservations.
- `apps/edge-api/internal/audio/accounting_adapter_test.go` - Verifies audio reservations send strict policy, expected API key, model alias, and estimated credits.
- `apps/edge-api/internal/batches/accounting_adapter.go` - Passes the derived model alias into batch reservations and uses strict policy mode.
- `apps/edge-api/internal/batches/accounting_adapter_test.go` - Verifies batch reservations send model alias and strict policy mode.
- `apps/edge-api/internal/batches/handler.go` - Returns the batch model alias from JSONL validation and rejects missing or mixed `body.model` values.
- `apps/edge-api/internal/batches/handler_test.go` - Covers reservation attribution propagation and missing or mixed model-alias validation failures.
- `.planning/phases/10-routing-storage-critical-fixes/10-09-SUMMARY.md` - Records the plan outcome, verification, and self-check.

## Test Evidence

- Strict media reservation adapter tests:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/edge-api/internal/images ./apps/edge-api/internal/audio -run TestAccountingAdapterCreateReservationUsesStrictPolicy -count=1'`
  - Result: exited 0.
  - Evidence: image and audio packages returned `ok`.

- Batch reservation attribution tests:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/edge-api/internal/batches -run "TestAccountingAdapterCreateReservationUsesStrictPolicyAndModelAlias|TestBatchCreatePassesModelAliasToReservation|TestBatchCreateRejectsMissingModelAlias|TestBatchCreateRejectsMixedModelAliases|TestBatchCreate" -count=1'`
  - Result: exited 0.
  - Evidence: batches package returned `ok`.

- Focused edge reservation regression suite:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/edge-api/internal/images ./apps/edge-api/internal/audio ./apps/edge-api/internal/batches -count=1'`
  - Result: exited 0.
  - Evidence: images, audio, and batches packages returned `ok`.

- Rejected-value source grep:
  `rg -n 'PolicyMode:\s+"soft"|ModelAlias:\s+""' apps/edge-api/internal/images/accounting_adapter.go apps/edge-api/internal/audio/accounting_adapter.go apps/edge-api/internal/batches/accounting_adapter.go apps/edge-api/internal/batches/handler.go`
  - Result: exited 1 with no matches.
  - Evidence: no touched reservation source still contains rejected `soft` policy mode or blank batch model alias literals.

## Decisions Made

- Used `strict` rather than `temporary_overage` for all touched reservation paths so prepaid admission stays conservative and matches current control-plane validation.
- Made batch model attribution a validation-time requirement instead of letting reservation creation fail downstream with an empty alias.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- Unrelated local changes in `.env.example`, `.gitignore`, and `.claude/` were present before execution and were intentionally left untouched.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Plan 10-09 closes the reservation-contract gap found in verification and leaves batch attribution data ready to persist through the internal batch create path in 10-10.

## Self-Check: PASSED

- Found `10-09-SUMMARY.md` plus the eight touched edge-api files on disk.
- Found task commits `867605b`, `446a8df`, `034f2d8`, and `c1b2557` in git history.
- All focused image, audio, and batch verification commands exited 0, and the rejected-value grep returned no matches.

---
*Phase: 10-routing-storage-critical-fixes*
*Completed: 2026-04-21*
