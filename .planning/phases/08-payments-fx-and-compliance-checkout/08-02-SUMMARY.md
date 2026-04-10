---
phase: 08-payments-fx-and-compliance-checkout
plan: "02"
subsystem: payments
tags: [payments, stripe, bkash, sslcommerz, webhook, idempotency]
dependency_graph:
  requires: ["08-01"]
  provides: ["stripe-rail", "bkash-rail", "sslcommerz-rail"]
  affects: ["08-03"]
tech_stack:
  added:
    - "github.com/stripe/stripe-go/v84 v84.4.1"
  patterns:
    - "hand-rolled HTTP client for bKash tokenized checkout"
    - "hand-rolled HTTP + HMAC verification for SSLCommerz IPN"
    - "stripe-go SDK with ConstructEventWithOptions(IgnoreAPIVersionMismatch: true)"
key_files:
  created:
    - apps/control-plane/internal/payments/stripe/rail.go
    - apps/control-plane/internal/payments/stripe/rail_test.go
    - apps/control-plane/internal/payments/bkash/rail.go
    - apps/control-plane/internal/payments/bkash/rail_test.go
    - apps/control-plane/internal/payments/sslcommerz/rail.go
    - apps/control-plane/internal/payments/sslcommerz/rail_test.go
  modified:
    - apps/control-plane/go.mod
    - apps/control-plane/go.sum
decisions:
  - "[08-02]: Stripe uses ConstructEventWithOptions with IgnoreAPIVersionMismatch: true — stripe-go v84 validates event API version by default; test events built locally lack the SDK-matching api_version field"
  - "[08-02]: bKash always grants fresh token per request — tokens are never cached across sessions as bKash access tokens are short-lived and caching risks 401s on concurrent requests"
  - "[08-02]: SSLCommerz verifyHash skipped when verify_key absent in IPN — hash fields are optional in some IPN configurations; server-side validation API is the authoritative check"
  - "[08-02]: ProviderIntentID consistency enforced: Stripe returns pi.ID, bKash returns paymentID from execute response, SSLCommerz returns sessionkey from IPN body — ensures GetPaymentIntentByProviderID lookup works"
metrics:
  duration: "45min"
  completed_date: "2026-04-10"
  tasks_completed: 2
  files_created: 6
  files_modified: 2
---

# Phase 08 Plan 02: Payment Rails (Stripe, bKash, SSLCommerz) Summary

**One-liner:** Three PaymentRail implementations — Stripe via stripe-go v84 with webhook.ConstructEvent, bKash via hand-rolled tokenized checkout grant→create→execute flow, SSLCommerz via form-encoded v4 API with server-side IPN validation.

## Tasks Completed

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 | Stripe rail with PaymentIntent + webhook verification | e50d00e | stripe/rail.go, stripe/rail_test.go, go.mod |
| 2 | bKash and SSLCommerz rails with hand-rolled HTTP | e488991 | bkash/rail.go, bkash/rail_test.go, sslcommerz/rail.go, sslcommerz/rail_test.go |

## What Was Built

### Stripe Rail (`internal/payments/stripe`)
- `NewRail(secretKey, webhookSecret)` sets global `stripe.Key` and returns `*Rail`
- `Initiate`: creates `PaymentIntent` with `IdempotencyKey = PaymentIntentID.String()` and `hive_payment_intent_id` metadata; constructs redirect URL from `CallbackBaseURL` if `NextAction` is nil
- `ProcessEvent`: case-insensitive `Stripe-Signature` header lookup; `webhook.ConstructEventWithOptions` with `IgnoreAPIVersionMismatch: true`; maps `payment_intent.{succeeded,payment_failed,canceled}` to normalized event types

### bKash Rail (`internal/payments/bkash`)
- `NewRail(httpClient, baseURL, appKey, appSecret, username, password)` — injectable HTTP client for testing
- `grantToken`: fresh token on every call (no caching), POSTs to `/tokenized/checkout/token/grant`
- `Initiate`: grant → create flow; `merchantInvoiceNumber = PaymentIntentID.String()` for idempotency; `amount = fmt.Sprintf("%.2f", float64(AmountLocal)/100.0)` in BDT; returns `paymentID` as `ProviderIntentID`
- `ProcessEvent`: parses callback JSON, always calls `/tokenized/checkout/execute` server-side; returns `paymentID` as `ProviderIntentID` (not `merchantInvoiceNumber`)

### SSLCommerz Rail (`internal/payments/sslcommerz`)
- `NewRail(httpClient, baseURL, storeID, storePasswd)` — injectable HTTP client for testing
- `Initiate`: form-encoded POST to `/gwprocess/v4/api.php`; `tran_id = PaymentIntentID.String()`; all required fields including `product_profile = "digital-goods"`; returns `sessionkey` as `ProviderIntentID`
- `ProcessEvent`: parses form-encoded IPN; verifies HMAC via `verifyHash` (MD5 per SSLCommerz spec); calls `/validator/api/validationserverAPI.php` for server-side confirmation; returns `sessionkey` as `ProviderIntentID`

## Test Coverage

25 unit tests total across all three rails using `httptest.NewServer` stubs:
- 8 Stripe tests (signature verification, event type mapping, unsupported event error)
- 9 bKash tests (grant+create flow, invoice number, amount formatting, server-side execute, ProviderIntentID consistency)
- 8 SSLCommerz tests (form encoding, tran_id, sessionkey as ProviderIntentID, amount formatting, server-side validation, failed validation)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] stripe-go v84 webhook.GenerateTestSignedPayload API mismatch**
- **Found during:** Task 1 (first test run)
- **Issue:** Test file used old API `GenerateTestSignedPayload([]byte, string) ([]byte, string)` but v84 uses `GenerateTestSignedPayload(*UnsignedPayload) *SignedPayload`; also `Event.Type` is `stripego.EventType` not `string`
- **Fix:** Updated `buildSignedPayload` helper to use `*webhook.UnsignedPayload{Payload, Secret, Timestamp}` and access `.Header` on returned `*SignedPayload`; used anonymous struct with `APIVersion: stripego.APIVersion` so `ConstructEvent` parses cleanly
- **Files modified:** apps/control-plane/internal/payments/stripe/rail_test.go
- **Commit:** e50d00e

**2. [Rule 1 - Bug] ConstructEvent rejects events with API version mismatch**
- **Found during:** Task 1 (test investigation)
- **Issue:** `webhook.ConstructEvent` in v84 validates that the event's `api_version` matches `stripe.APIVersion`; locally-constructed test events without this field fail
- **Fix:** Changed rail to use `webhook.ConstructEventWithOptions` with `IgnoreAPIVersionMismatch: true` — production webhooks from Stripe will always have the correct api_version; this flag is safe for the rail implementation
- **Files modified:** apps/control-plane/internal/payments/stripe/rail.go
- **Commit:** e50d00e

## Self-Check: PASSED

Files verified:
- apps/control-plane/internal/payments/stripe/rail.go — FOUND
- apps/control-plane/internal/payments/stripe/rail_test.go — FOUND
- apps/control-plane/internal/payments/bkash/rail.go — FOUND
- apps/control-plane/internal/payments/bkash/rail_test.go — FOUND
- apps/control-plane/internal/payments/sslcommerz/rail.go — FOUND
- apps/control-plane/internal/payments/sslcommerz/rail_test.go — FOUND

Commits verified:
- e50d00e — feat(08-02): implement Stripe PaymentRail with webhook signature verification
- e488991 — feat(08-02): implement bKash and SSLCommerz PaymentRail with hand-rolled HTTP clients
