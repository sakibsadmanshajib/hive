# Debug Session: API Key Usage Attribution

## Symptom

Test 5 in Phase 05 UAT failed.

Expected:
- A successful API-key-backed request records `api_key_id` in usage data.
- `last_used_at` is updated on the key after settlement.

Actual:
- The live reservation/finalize flow completed, but the request attempt had no `api_key_id`.
- The customer-visible usage-events response exposed no `api_key_id`.
- The active key still had no `last_used_at`.

## Reproduction

1. Create an active key through `POST /api/v1/accounts/current/api-keys`.
2. Create a reservation through `POST /api/v1/accounts/current/credits/reservations`.
3. Finalize it through `POST /api/v1/accounts/current/credits/reservations/finalize`.
4. Read attempts through `GET /api/v1/accounts/current/request-attempts?request_id=...`.
5. Read events through `GET /api/v1/accounts/current/usage-events?request_id=...`.
6. List keys through `GET /api/v1/accounts/current/api-keys`.

Observed on 2026-04-01:
- The attempt was completed but had no `api_key_id`.
- The usage-events response only returned `reservation_created`.
- The active key still had no `last_used_at`.

## Evidence

- [apps/control-plane/internal/accounting/http.go](/home/sakib/hive/apps/control-plane/internal/accounting/http.go)
  `createReservationRequest` has no `api_key_id` field and never populates `CreateReservationInput.APIKeyID`.
- [apps/control-plane/internal/accounting/service.go](/home/sakib/hive/apps/control-plane/internal/accounting/service.go)
  `FinalizeReservation` only records a usage event when credits are released or reconciliation is needed, so the normal exact-charge success path emits no completed usage event.
- [apps/control-plane/internal/usage/http.go](/home/sakib/hive/apps/control-plane/internal/usage/http.go)
  `handleListEvents` does not include `api_key_id` in the response payload even though `usage.UsageEvent` carries it.

## Root Cause

The public accounting entrypoint has no way to carry API-key identity into the accounting flow, so attempts and settlement logic cannot attribute work to a key. Even if attribution existed in storage, the usage-events response currently strips `api_key_id`, and the common successful finalize path emits no completed usage event for customers to inspect.

## Fix Direction

- Accept and validate `api_key_id` in the public accounting request path, or ensure the edge/internal caller injects it before the accounting flow begins.
- Always emit a completed usage event on successful finalize, not only on release/reconciliation branches.
- Surface `api_key_id` in usage-event responses and verify `MarkLastUsed` updates the key on attributed settlement.
