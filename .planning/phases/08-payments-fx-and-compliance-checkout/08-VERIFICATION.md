---
phase: 08-payments-fx-and-compliance-checkout
verified: 2026-04-10T00:00:00Z
status: passed
score: 8/8 must-haves verified
re_verification: false
---

# Phase 08: Payments FX and Compliance Checkout — Verification Report

**Phase Goal:** Let customers buy credits safely across global and Bangladesh-local rails with reproducible FX and tax math.
**Verified:** 2026-04-10
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | PaymentRail interface exists with Stripe, bKash, SSLCommerz implementations | VERIFIED | `rail.go` defines 3-method interface; all three `*Rail` types implement `RailName/Initiate/ProcessEvent` |
| 2 | FX service fetches USD/BDT with Redis cache and admin override fallback chain | VERIFIED | `fx.go` 208 lines: `FXCache` interface, `redisFXCache` adapter, `SetAdminOverride`, `CreateSnapshot` with `math/big` |
| 3 | Tax calculation covers BD VAT 15%, BD reverse-charge, non-BD no-tax | VERIFIED | `tax.go`: `CalculateTax` with three branches; `ApplyTax` for inclusive VAT extraction |
| 4 | Payment intent state machine with 8 statuses and race-safe transitions | VERIFIED | `types.go` defines 8 `IntentStatus` constants; `repository.go` `CompareAndSetStatus` uses `RETURNING id` pattern |
| 5 | Payment intent service orchestrates full lifecycle including BD 3-minute confirming delay | VERIFIED | `service.go`: `InitiateCheckout`, `HandleProviderEvent` (BD->confirming, Stripe->completed), `ConfirmPendingBDPayments` with `cutoff := time.Now().Add(-3 * time.Minute)` |
| 6 | HTTP handler exposes checkout, initiation, and webhook endpoints | VERIFIED | `http.go` 251 lines: `GET /api/v1/accounts/current/checkout/rails`, `POST /checkout/initiate`, 6 webhook routes |
| 7 | Routes wired in platform router with auth on checkout, no auth on webhooks | VERIFIED | `router.go`: checkout routes behind `AuthMiddleware.Require`; webhook routes registered directly |
| 8 | main.go wires conditional rail registration and BD background goroutine | VERIFIED | `main.go`: conditional stripe/bkash/sslcommerz registration on env presence; 60s ticker calling `ConfirmPendingBDPayments`; `accountsResolverAdapter` bridges accounts service |

**Score:** 8/8 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/payments/types.go` | State machine types, enums, constants | VERIFIED | 169 lines, 8 statuses, 3 rails, monetary constants, sentinel errors |
| `internal/payments/rail.go` | PaymentRail interface | VERIFIED | 16 lines, 3-method interface |
| `internal/payments/fx.go` | FXService with FXCache interface | VERIFIED | 208 lines, `math/big` rate computation, admin override, Redis+cache fallback |
| `internal/payments/tax.go` | CalculateTax and ApplyTax | VERIFIED | 71 lines, all three tax treatment branches |
| `internal/payments/repository.go` | Repository interface + pgx impl | VERIFIED | 241 lines, 10 methods, `CompareAndSetStatus` with `RETURNING id` |
| `internal/payments/service.go` | Service with full lifecycle | VERIFIED | 424 lines, all 4 public methods present |
| `internal/payments/http.go` | HTTP handler | VERIFIED | 251 lines, 3 route groups, webhook always-200 pattern |
| `internal/payments/stripe/rail.go` | Stripe PaymentRail impl | VERIFIED | 109 lines, `ConstructEventWithOptions(IgnoreAPIVersionMismatch: true)` |
| `internal/payments/bkash/rail.go` | bKash PaymentRail impl | VERIFIED | 236 lines, grant->create->execute flow |
| `internal/payments/sslcommerz/rail.go` | SSLCommerz PaymentRail impl | VERIFIED | 237 lines, form-encoded IPN, HMAC verify, server-side validation |
| `supabase/migrations/20260410_01_payment_intents.sql` | payment_intents + payment_events tables | VERIFIED | 8-status check constraint, 3 indexes, all required columns |
| `supabase/migrations/20260410_02_fx_snapshots.sql` | fx_snapshots table | VERIFIED | `source_api` check constraint (`xe/cache/admin_override`), account index |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `main.go` | `payments.NewService` | conditional env-var block | WIRED | Lines 169-193: FXService, rails map, `NewPgxRepository`, `NewService`, `NewHandler` |
| `main.go` | `payments.ConfirmPendingBDPayments` | 60s background goroutine | WIRED | Line 203: goroutine with `time.Tick(60*time.Second)` |
| `main.go` | `payments.AccountResolver` | `accountsResolverAdapter` | WIRED | Lines 37-44: adapter struct bridges `accounts.Service.EnsureViewerContext` |
| `router.go` | `payments.Handler` | `RouterConfig.PaymentsHandler` | WIRED | Line 32: field declaration; lines 103-117: checkout + webhook registration |
| `main.go` | `RouterConfig.PaymentsHandler` | `paymentsHandler` | WIRED | Line 223: `PaymentsHandler: paymentsHandler` in struct literal |
| `service.go` | `ledger.GrantCredits` | `LedgerGranter` interface | WIRED | `PostPurchaseGrant` calls `s.ledger.GrantCredits` with idempotency key `payment:purchase:{intentID}` |
| `service.go` | `profiles.BillingProfile` | `ProfileReader` interface | WIRED | `InitiateCheckout` calls `s.profiles.GetBillingProfile` for tax calculation gate |
| `stripe/rail.go` | `payments.PaymentRail` | struct method set | WIRED | `RailName`, `Initiate`, `ProcessEvent` all implemented on `*Rail` |
| `bkash/rail.go` | `payments.PaymentRail` | struct method set | WIRED | `RailName`, `Initiate`, `ProcessEvent` all implemented on `*Rail` |
| `sslcommerz/rail.go` | `payments.PaymentRail` | struct method set | WIRED | `RailName`, `Initiate`, `ProcessEvent` all implemented on `*Rail` |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|---------|
| BILL-03 | 08-01 | Payment intent state machine | SATISFIED | 8-status enum, `CompareAndSetStatus` race-safe transitions |
| BILL-04 | 08-01 | FX rate with reproducible math | SATISFIED | `math/big` rate computation, FXSnapshot persisted per intent |
| BILL-07 | 08-01 | BD VAT compliance | SATISFIED | `CalculateTax` with bd_vat_15/bd_reverse_charge/no_tax branches |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | None found | — | — |

All `return nil` occurrences are legitimate error-path returns in repository/service methods and test stub implementations. No TODO, FIXME, HACK, PLACEHOLDER, or empty implementation patterns found across 14 production Go files.

### Human Verification Required

#### 1. Stripe Webhook Signature Verification in Production

**Test:** Deploy with a real Stripe webhook secret and send a test event from the Stripe dashboard.
**Expected:** Event is accepted and intent transitions correctly; tampered signatures receive 200 but log an error.
**Why human:** Requires live Stripe credentials and webhook infrastructure — cannot verify HMAC correctness from static analysis alone.

#### 2. bKash Token Grant Flow Against Sandbox

**Test:** Configure bKash sandbox credentials and initiate a checkout for a BD account.
**Expected:** `grantToken` returns a fresh token, `createPayment` returns `paymentID`, redirect URL is returned to caller.
**Why human:** Requires live bKash sandbox environment — hand-rolled HTTP client correctness cannot be confirmed without an actual endpoint responding.

#### 3. BD 3-Minute Confirming Delay End-to-End

**Test:** Trigger a `payment.succeeded` webhook for a bKash intent, wait 3+ minutes, verify `ConfirmPendingBDPayments` transitions the intent to `completed` and credits are granted.
**Expected:** Intent status moves `provider_processing` -> `confirming` -> `completed`; ledger shows credit grant.
**Why human:** Requires real database, real time passage, and ledger integration — cannot verify the full pipeline from static analysis.

### Gaps Summary

No gaps. All 8 observable truths verified. All 12 required artifacts exist, are substantive, and are wired. All 3 requirements (BILL-03, BILL-04, BILL-07) are satisfied. No anti-patterns found across 14 production files and 7 test files (61 test functions total, exceeding the 55 claimed in the final summary).

---

_Verified: 2026-04-10_
_Verifier: Claude (gsd-verifier)_
