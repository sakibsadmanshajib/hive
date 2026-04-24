---
phase: 08-payments-fx-and-compliance-checkout
plan: "03"
subsystem: payments-http
tags: [payments, http, checkout, webhooks, routing, wiring]
dependency_graph:
  requires: ["08-01", "08-02"]
  provides: ["payment HTTP API", "checkout initiation", "webhook endpoints", "BD confirmation background loop"]
  affects: ["apps/control-plane/internal/payments", "apps/control-plane/internal/platform/http", "apps/control-plane/cmd/server"]
tech_stack:
  added: []
  patterns:
    - "Accept interfaces pattern for PaymentService and AccountResolver in http.go"
    - "accountsResolverAdapter bridges accounts.Service to narrow payments.AccountResolver interface"
    - "Conditional rail registration — missing env vars skip that rail at startup without crashing"
    - "Webhook always-200 pattern — log errors but never fail providers (prevents duplicate retries)"
    - "Router integration tests using real auth middleware + test HTTP server to verify auth wiring"
key_files:
  created:
    - apps/control-plane/internal/payments/http.go
    - apps/control-plane/internal/payments/http_test.go
  modified:
    - apps/control-plane/internal/payments/service.go
    - apps/control-plane/internal/platform/http/router.go
    - apps/control-plane/cmd/server/main.go
    - deploy/docker/docker-compose.yml
decisions:
  - "PaymentService and AccountResolver interfaces defined in http.go — follows accept-interfaces pattern; stubs in tests avoid importing full service"
  - "accountsResolverAdapter in main.go bridges the 3-arg accounts.Service.EnsureViewerContext to the narrow 1-arg payments.AccountResolver interface — isolates payments from accounts internals"
  - "Router integration tests use real auth.Middleware backed by a test HTTP server that always returns 401 — confirms webhook routes bypass auth and checkout routes enforce it without mocking middleware internals"
  - "Webhook routes registered before /api/v1/ wildcard in router to prevent the wildcard from absorbing /webhooks/* paths"
  - "ledgerSvc/profilesSvc/accountsSvc hoisted to var declarations outside if pool != nil block so payments wiring block can reference them without re-entering the block"
metrics:
  duration: 9min
  completed: "2026-04-10"
  tasks: 2
  files: 6
---

# Phase 08 Plan 03: Payments HTTP Handler, Router Wiring, and main.go Summary

HTTP checkout and webhook API surface for the payments system, wired into the control-plane server with conditional rail registration and a BD confirmation background goroutine.

## What Was Built

**Task 1: Payments HTTP handler + GetCheckoutOptions service method**

`http.go` implements `Handler` with three route groups:
- `GET /api/v1/accounts/current/checkout/rails` — authenticated; returns available rails via `GetCheckoutOptions` with per-rail min/max credit limits and predefined tiers
- `POST /api/v1/accounts/current/checkout/initiate` — authenticated; validates rail/credits/idempotency_key, calls `InitiateCheckout`, returns 201 with payment_intent_id and redirect_url
- `POST /webhooks/{stripe,bkash/callback,sslcommerz/ipn,sslcommerz/success,sslcommerz/fail,sslcommerz/cancel}` — unauthenticated; reads raw body first (before any parsing), collects lowercase headers, calls `HandleProviderEvent`, always returns 200

`service.go` gains `GetCheckoutOptions` (reads account profile → `AvailableRails` → maps each to `RailOption` with min/max limits) plus `CheckoutOptions` and `RailOption` types.

`router.go` gains `PaymentsHandler *payments.Handler` in `RouterConfig` and registers checkout routes behind `AuthMiddleware.Require` and webhook routes directly (no auth).

`http_test.go` includes 14 unit tests (stub service/resolver) and 3 router integration tests that spin up a real `auth.Middleware` backed by a test HTTP server to verify auth wiring is correct at the router level.

**Task 2: main.go wiring + docker-compose env vars**

`main.go` gains:
- `accountsResolverAdapter` struct that implements `payments.AccountResolver` by extracting viewer from context and calling `accounts.Service.EnsureViewerContext`
- Env var reading for all 12 payment/FX credentials with empty defaults
- Default sandbox URLs for bKash and SSLCommerz when not configured
- Payments wiring block: FXService → conditional rail registration (stripe/bkash/sslcommerz based on env presence) → `NewPgxRepository` → `NewService` → `NewHandler`
- Background goroutine ticking every 60s to call `ConfirmPendingBDPayments`
- `PaymentsHandler: paymentsHandler` in `RouterConfig` struct literal

`docker-compose.yml` gains 13 env vars under the control-plane service, all with `${VAR:-}` empty defaults so the service starts without payment credentials configured.

## Test Results

All 55 tests pass across 4 packages:
- `payments`: 39 tests (unit + router integration)
- `payments/bkash`: 9 tests
- `payments/sslcommerz`: 8 tests  
- `payments/stripe`: 7 tests

Full `go build ./apps/control-plane/...` succeeds.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Router PaymentsHandler field added in Task 1 instead of Task 2**

- **Found during:** Task 1 test writing
- **Issue:** The router integration tests in `http_test.go` import `platformhttp` and reference `RouterConfig.PaymentsHandler`. Without that field, Task 1 tests would not compile.
- **Fix:** Added `PaymentsHandler *payments.Handler` to `RouterConfig` and the route registrations in Task 1. Task 2 skipped the router.go changes (already done) and focused on main.go.
- **Files modified:** `apps/control-plane/internal/platform/http/router.go`
- **Commit:** 500685a

**2. [Rule 1 - Bug] ledgerSvc/profilesSvc/accountsSvc hoisted out of if-pool block**

- **Found during:** Task 2 main.go wiring
- **Issue:** Original code declared `accountsSvc`, `ledgerSvc`, `profilesSvc` with `:=` inside the first `if pool != nil` block, making them inaccessible in the second payments wiring block.
- **Fix:** Hoisted to `var` declarations before the `if pool != nil` block; changed inner assignments from `:=` to `=`.
- **Files modified:** `apps/control-plane/cmd/server/main.go`
- **Commit:** 96d219c

## Self-Check: PASSED

- `apps/control-plane/internal/payments/http.go` — FOUND
- `apps/control-plane/internal/payments/http_test.go` — FOUND
- `apps/control-plane/internal/payments/service.go` — FOUND (GetCheckoutOptions, CheckoutOptions, RailOption)
- `apps/control-plane/internal/platform/http/router.go` — FOUND (PaymentsHandler, checkout routes, webhook routes)
- `apps/control-plane/cmd/server/main.go` — FOUND (payments.NewService, ConfirmPendingBDPayments, 60s ticker)
- Commit 500685a — verified in git log
- Commit 96d219c — verified in git log
- All 55 payment package tests pass
- Full control-plane build succeeds
