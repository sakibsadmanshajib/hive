---
phase: 08-payments-fx-and-compliance-checkout
plan: 01
subsystem: payments
tags: [payments, fx, tax, stripe, bkash, sslcommerz, pgx, redis, bdvat, ledger]

requires:
  - phase: 03-credits-ledger-usage-accounting
    provides: ledger.Service.GrantCredits for credit posting on payment completion
  - phase: 02-identity-account-foundation
    provides: profiles.Service.GetBillingProfile and GetAccountProfile for billing gate and tax data

provides:
  - payments package with PaymentIntent state machine types and constants
  - PaymentRail interface for provider implementations (Stripe, bKash, SSLCommerz)
  - FXService with XE API, FXCache interface (Redis + in-memory), admin override fallback chain
  - CalculateTax and ApplyTax for BD VAT 15% / BD reverse-charge / no-tax logic
  - Repository interface + pgx implementation for payment_intents, payment_events, fx_snapshots
  - Service orchestrating InitiateCheckout, HandleProviderEvent, ConfirmPendingBDPayments, PostPurchaseGrant
  - Supabase migrations for payment_intents, payment_events, fx_snapshots tables

affects:
  - 08-02-stripe-rail
  - 08-03-bkash-sslcommerz-rails
  - any phase integrating checkout or billing

tech-stack:
  added: []
  patterns:
    - FXCache interface over *redis.Client for testable cache abstraction
    - PaymentRail interface for provider-agnostic rail implementations
    - LedgerGranter/ProfileReader/FXProvider interfaces for clean dependency injection in Service
    - CompareAndSetStatus for race-safe intent state machine transitions
    - math/big for all FX rate arithmetic to avoid float64 corruption
    - int64 micro-units for all monetary amounts (credits, USD cents, paisa)

key-files:
  created:
    - apps/control-plane/internal/payments/types.go
    - apps/control-plane/internal/payments/rail.go
    - apps/control-plane/internal/payments/fx.go
    - apps/control-plane/internal/payments/fx_test.go
    - apps/control-plane/internal/payments/tax.go
    - apps/control-plane/internal/payments/tax_test.go
    - apps/control-plane/internal/payments/repository.go
    - apps/control-plane/internal/payments/service.go
    - apps/control-plane/internal/payments/service_test.go
    - supabase/migrations/20260410_01_payment_intents.sql
    - supabase/migrations/20260410_02_fx_snapshots.sql
  modified: []

key-decisions:
  - "FXService uses FXCache interface (not *redis.Client directly) — enables in-memory test double without a real Redis server in unit tests"
  - "FXService.newFXServiceWithBaseURL accepts FXCache not *redis.Client — test helper uses same cache abstraction"
  - "BD rails (bkash, sslcommerz) transition to confirming on payment.succeeded; Stripe transitions directly to completed — BD payment clearing requires 3-minute delay before ledger grant"
  - "PostPurchaseGrant idempotency key is payment:purchase:{intentID} — deterministic and scoped to prevent double-crediting across retries"
  - "amountLocal (paisa) = amountUSD_cents * effectiveRate using math/big — avoids float64 rounding errors on currency conversion"
  - "AvailableRails returns all three rails for BD, Stripe-only for others — BD-specific rails gated at service layer not HTTP layer"

patterns-established:
  - "FXCache interface: Get(ctx, key) (string, error) / Set(ctx, key, value, ttl) error — use this abstraction for all cache stores needing test substitution"
  - "CompareAndSetStatus: returns (false, nil) on pgx.ErrNoRows (already transitioned) — idempotent state machine updates"
  - "PaymentRail interface: RailName/Initiate/ProcessEvent — all provider implementations must satisfy this contract"

requirements-completed: [BILL-03, BILL-04, BILL-07]

duration: 35min
completed: 2026-04-10
---

# Phase 08 Plan 01: Payments Domain Foundation Summary

**Payments domain foundation with PaymentIntent state machine, FX service (XE/Redis/admin override), BD VAT tax logic, pgx repository, and payment intent service orchestrating initiate/webhook/grant lifecycle across Stripe and BD rails**

## Performance

- **Duration:** 35 min
- **Started:** 2026-04-10T23:25:37Z
- **Completed:** 2026-04-10T23:59:00Z
- **Tasks:** 2
- **Files modified:** 11 created, 0 modified

## Accomplishments

- Payments package with 8-state intent state machine, Rail enum, FXSnapshot, TaxResult, InitiateInput/Result, RailEvent types and all sentinel errors
- FXService fetching USD/BDT from XE API with configurable FXCache fallback (Redis in production, in-memory in tests) and admin override
- CalculateTax correctly classifies BD individual (15% inclusive VAT), BD B2B with VAT# (reverse-charge, 0%), non-BD (no tax), with ApplyTax for integer-safe extraction
- PaymentRail interface enabling provider-agnostic Stripe/bKash/SSLCommerz implementations in Plans 02-03
- Repository interface + pgx implementation with CompareAndSetStatus (race-safe via RETURNING id pattern)
- Service orchestrating full lifecycle: billing profile gate, FX snapshot for BD rails, rail.Initiate, provider detail persistence, status transitions, BD 3-minute confirming delay, idempotent ledger grants
- Two Supabase migrations: payment_intents (8-status enum, indexes on idempotency key + confirming status), fx_snapshots (with source_api check constraint)
- 22 unit tests passing across fx_test.go, tax_test.go, service_test.go

## Task Commits

1. **Task 1: Types, migrations, PaymentRail interface, FX service, tax calculation** - `13bcd78` (feat)
2. **Task 2: Payment intent repository, service, and service tests** - `2420e3c` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `apps/control-plane/internal/payments/types.go` - IntentStatus/Rail enums, PaymentIntent/FXSnapshot/TaxResult structs, constants, ValidatePurchaseAmount, AvailableRails, sentinel errors
- `apps/control-plane/internal/payments/rail.go` - PaymentRail interface: RailName/Initiate/ProcessEvent
- `apps/control-plane/internal/payments/fx.go` - FXService, FXCache interface, redisFXCache, XE API fetch, Redis cache fallback, admin override, CreateSnapshot with math/big effective rate
- `apps/control-plane/internal/payments/fx_test.go` - 5 tests: admin override, XE success, cache fallback, all sources fail, effective rate computation
- `apps/control-plane/internal/payments/tax.go` - CalculateTax (BD individual/B2B/non-BD), ApplyTax (inclusive VAT extraction)
- `apps/control-plane/internal/payments/tax_test.go` - 6 tests: all tax treatment branches, inclusive VAT extraction, reverse charge
- `apps/control-plane/internal/payments/repository.go` - Repository interface + pgxRepository with all 10 methods
- `apps/control-plane/internal/payments/service.go` - Service with LedgerGranter/ProfileReader/FXProvider interfaces, InitiateCheckout/HandleProviderEvent/ConfirmPendingBDPayments/PostPurchaseGrant
- `apps/control-plane/internal/payments/service_test.go` - 11 tests with stub implementations: happy paths, error cases, BD rail flow, idempotency
- `supabase/migrations/20260410_01_payment_intents.sql` - payment_intents (8-status check, 3 indexes) + payment_events tables
- `supabase/migrations/20260410_02_fx_snapshots.sql` - fx_snapshots table with source_api check constraint

## Decisions Made

- **FXCache interface over *redis.Client**: Wrapping Redis in a `FXCache` interface (Get/Set) allows in-memory test doubles without requiring a running Redis in unit tests. The `redisFXCache` adapter bridges production usage.
- **FXService.newFXServiceWithBaseURL accepts FXCache**: The test helper takes the interface directly rather than constructing a Redis client internally — consistent with the abstraction boundary.
- **BD rails confirming delay**: bKash and SSLCommerz payment webhooks transition to `confirming` rather than `completed`. A `ConfirmPendingBDPayments` background job completes them after 3 minutes, matching BD payment clearing realities.
- **PostPurchaseGrant idempotency key `payment:purchase:{intentID}`**: Deterministic key prevents double-crediting if the grant function is retried after a transient failure.
- **amountLocal computed with math/big**: Currency conversion from USD cents to BDT paisa uses `big.Rat` multiplication to avoid float64 rounding errors on financial amounts.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] FXService tests used real Redis instead of test double**
- **Found during:** Task 1 (FX service tests — TDD GREEN run)
- **Issue:** `TestFetchUSDToBDT_XEFailsFallsBackToCache` pre-populated Redis at localhost:6379, which doesn't exist in the toolchain container. Test failed with `ErrFXUnavailable` instead of testing the cache fallback path.
- **Fix:** Introduced `FXCache` interface (`Get`/`Set`) and `redisFXCache` adapter. Replaced `*redis.Client` field with `FXCache` in `FXService`. Updated `newFXServiceWithBaseURL` to accept `FXCache`. Added `memFXCache` in-memory test double. Removed Redis dependency from all 3 XE tests.
- **Files modified:** `fx.go`, `fx_test.go`
- **Verification:** All 5 FX tests pass with zero external service dependencies
- **Committed in:** `13bcd78` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — bug in test design requiring real Redis)
**Impact on plan:** Fix improved test isolation and removed runtime Redis dependency from unit tests. No scope change. The production `NewFXService` still accepts `*redis.Client` and wraps it transparently.

## Issues Encountered

- Docker toolchain container I/O: `docker compose run` output was not flowing back to the shell. Resolved by using `docker run` directly with the pre-built `docker-toolchain:latest` image and passing the command string as an argument to the `/bin/sh -c` entrypoint.

## User Setup Required

None — no external service configuration required for this plan. XE API credentials (`XE_ACCOUNT_ID`, `XE_API_KEY`) will be wired in the HTTP handler plan.

## Next Phase Readiness

- All types, interfaces, repository, and service are ready for Plans 02 and 03
- Plan 02 (Stripe rail) can import `payments.PaymentRail`, `InitiateInput`, `RailEvent` and implement `Initiate`/`ProcessEvent`
- Plan 03 (bKash/SSLCommerz rails) follows the same interface contract
- `Service.InitiateCheckout` and `Service.HandleProviderEvent` are wired — Plans 02-03 only need to add HTTP handlers and inject real rail implementations
- No blockers

---
*Phase: 08-payments-fx-and-compliance-checkout*
*Completed: 2026-04-10*
