---
phase: 8
slug: payments-fx-and-compliance-checkout
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-10
---

# Phase 8 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — existing Go test infrastructure |
| **Quick run command** | `docker compose run --rm hive go test ./apps/control-plane/internal/payments/... -count=1 -short` |
| **Full suite command** | `docker compose run --rm hive go test ./... -count=1` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `docker compose run --rm hive go test ./apps/control-plane/internal/payments/... -count=1 -short`
- **After every plan wave:** Run `docker compose run --rm hive go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | Test Functions | Status |
|---------|------|------|-------------|-----------|-------------------|----------------|--------|
| 08-01-01 | 01 | 1 | BILL-03, BILL-04, BILL-07 | unit | `go test ./apps/control-plane/internal/payments/... -run "TestFetch\|TestCreate\|TestCalculateTax\|TestApplyTax" -count=1` | TestFetchUSDToBDT_*, TestCreateSnapshot_*, TestCalculateTax_*, TestApplyTax_* | ⬜ pending |
| 08-01-02 | 01 | 1 | BILL-03 | unit | `go test ./apps/control-plane/internal/payments/... -run "TestInitiate\|TestHandle\|TestConfirm\|TestPost" -count=1` | TestInitiateCheckout_*, TestHandleProviderEvent_*, TestConfirmPendingBDPayments_*, TestPostPurchaseGrant_* | ⬜ pending |
| 08-02-01 | 02 | 2 | BILL-03 | unit | `go test ./apps/control-plane/internal/payments/stripe/... -count=1` | TestStripeProcessEvent_*, TestStripeRailName | ⬜ pending |
| 08-02-02 | 02 | 2 | BILL-03 | unit | `go test ./apps/control-plane/internal/payments/bkash/... ./apps/control-plane/internal/payments/sslcommerz/... -count=1` | TestBkashInitiate_*, TestBkashProcessEvent_*, TestSSLCommerzInitiate_*, TestSSLCommerzProcessEvent_* | ⬜ pending |
| 08-03-01 | 03 | 3 | BILL-03, BILL-04, BILL-07 | unit+integration | `go test ./apps/control-plane/internal/payments/... -run "TestGet\|TestInitiate\|TestWebhook\|TestRouterIntegration" -count=1` | TestGetRails_*, TestInitiateCheckout_*, TestWebhook_*, TestRouterIntegration_* | ⬜ pending |
| 08-03-02 | 03 | 3 | BILL-03 | build | `go build ./apps/control-plane/...` | N/A (build verification) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `apps/control-plane/internal/payments/fx_test.go` — FX service tests (BILL-04)
- [ ] `apps/control-plane/internal/payments/tax_test.go` — Tax calculation tests (BILL-07)
- [ ] `apps/control-plane/internal/payments/service_test.go` — Service lifecycle tests (BILL-03)
- [ ] `apps/control-plane/internal/payments/stripe/rail_test.go` — Stripe rail tests (BILL-03)
- [ ] `apps/control-plane/internal/payments/bkash/rail_test.go` — bKash rail tests (BILL-03)
- [ ] `apps/control-plane/internal/payments/sslcommerz/rail_test.go` — SSLCommerz rail tests (BILL-03)
- [ ] `apps/control-plane/internal/payments/http_test.go` — HTTP handler + router integration tests (BILL-03, BILL-07)

*Existing Go test infrastructure covers framework needs. Wave 0 creates test file stubs via TDD in each plan task.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| bKash sandbox redirect flow | BILL-03 | Requires browser redirect to bKash sandbox | 1. Create payment intent with rail=bkash 2. Follow redirect URL 3. Complete sandbox payment 4. Verify callback received |
| SSLCommerz IPN callback | BILL-03 | Requires SSLCommerz sandbox environment | 1. Initiate SSLCommerz checkout 2. Complete sandbox payment 3. Verify IPN POST received and validated |
| Stripe checkout UI | BILL-03 | Requires Stripe test mode dashboard | 1. Create Stripe payment intent 2. Confirm via Stripe test card 3. Verify webhook delivery |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
