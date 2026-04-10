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
| **Quick run command** | `docker compose run --rm hive go test ./internal/payments/... -count=1 -short` |
| **Full suite command** | `docker compose run --rm hive go test ./... -count=1` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `docker compose run --rm hive go test ./internal/payments/... -count=1 -short`
- **After every plan wave:** Run `docker compose run --rm hive go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 08-01-01 | 01 | 1 | BILL-03 | unit | `go test ./internal/payments/... -run TestPaymentIntent` | ❌ W0 | ⬜ pending |
| 08-01-02 | 01 | 1 | BILL-03 | unit | `go test ./internal/payments/... -run TestPaymentRail` | ❌ W0 | ⬜ pending |
| 08-02-01 | 02 | 2 | BILL-03 | integration | `go test ./internal/payments/... -run TestStripeWebhook` | ❌ W0 | ⬜ pending |
| 08-02-02 | 02 | 2 | BILL-03 | integration | `go test ./internal/payments/... -run TestBkashFlow` | ❌ W0 | ⬜ pending |
| 08-02-03 | 02 | 2 | BILL-03 | integration | `go test ./internal/payments/... -run TestSSLCommerz` | ❌ W0 | ⬜ pending |
| 08-03-01 | 03 | 2 | BILL-04 | unit | `go test ./internal/payments/... -run TestFXSnapshot` | ❌ W0 | ⬜ pending |
| 08-03-02 | 03 | 2 | BILL-07 | unit | `go test ./internal/payments/... -run TestSurchargeCalc` | ❌ W0 | ⬜ pending |
| 08-03-03 | 03 | 2 | BILL-07 | unit | `go test ./internal/payments/... -run TestTaxEvidence` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/payments/payments_test.go` — stubs for payment intent and rail tests (BILL-03)
- [ ] `internal/payments/fx_test.go` — stubs for FX snapshot and surcharge tests (BILL-04, BILL-07)
- [ ] `internal/payments/webhook_test.go` — stubs for webhook idempotency tests (BILL-03)

*Existing Go test infrastructure covers framework needs. Wave 0 creates test file stubs only.*

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
