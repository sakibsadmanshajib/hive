---
status: diagnosed
phase: 05-api-keys-hot-path-enforcement
source: [05-01-PLAN.md, 05-02-PLAN.md, 05-03-PLAN.md]
started: 2026-03-31T09:21:25-04:00
updated: 2026-04-01T16:21:32-04:00
---

## Current Test

[testing complete]

## Tests

### 1. Cold Start Smoke Test
expected: Kill any running server/service. Clear ephemeral state (temp DBs, caches, lock files). Start the application from scratch (`docker compose up -d`). Server boots without errors, database migrations complete, and checking the `/health` endpoint of both control-plane and edge-api returns live status.
result: pass
notes: |
  Re-ran the phase 5 stack on 2026-04-01:
  - `docker compose --env-file .env -f deploy/docker/docker-compose.yml up -d redis control-plane edge-api`
  - control-plane `/health` => `{"status":"ok"}`
  - edge-api `/health` => `{"status":"ok"}`

### 2. Retrieve Control Plane API Key
expected: Creating a new API key through the control-plane returns the raw secret once. Listing keys shows the key but DOES NOT reveal the raw secret (only a 6-char suffix).
result: pass
notes: |
  Verified against the live control-plane on 2026-04-01:
  - `POST /api/v1/accounts/current/api-keys` returned a one-time `hk_...` secret
  - `GET /api/v1/accounts/current/api-keys` returned only the key metadata plus `redacted_suffix`
  - the suffix matched the final 6 characters of the secret and the list payload did not contain the raw secret

### 3. Edge API Authorization (Valid Key)
expected: Sending a `/v1/models` request to the edge-api with a valid API key passes authorization and reaches the downstream handler. An invalid or missing key returns a 401 OpenAI error `invalid_api_key`.
result: pass
notes: |
  Verified against the live edge-api on 2026-04-01:
  - valid key => HTTP 200 with model list
  - invalid key => HTTP 401 with OpenAI-style `invalid_api_key`

### 4. Dynamic Per-Key Rate Limiting (RPM)
expected: Firing >10 requests per minute with an API key whose policy sets `rate_limit_rpm: 10` returns an OpenAI-compatible 429 `rate_limit_exceeded` error with the dynamic limit in the message.
result: pass
notes: |
  Verified via 12-request burst test:
  - Requests 1-10 => authorized
  - Requests 11-12 => HTTP 429 `"Rate limit reached for requests. Limit: 10 / min."`
  - Redis Lua sliding-window script correctly reads per-key RPM/TPM from `AuthSnapshot.Policy`
  - Error messages dynamically reflect policy values instead of hardcoded defaults

### 5. Usage Attribution
expected: After a successful proxied request, the usage event includes the `api_key_id` and the `last_used_at` timestamp on the key is updated.
result: issue
reported: "The live reservation/finalize flow completed without any API-key attribution: the request attempt had no api_key_id, the usage-events response exposed no api_key_id, and the active key still had no last_used_at after the successful finalize."
severity: major
notes: |
  Evidence from 2026-04-01:
  - `POST /api/v1/accounts/current/credits/reservations` succeeded only after switching to `temporary_overage` because the account balance is 0
  - `GET /api/v1/accounts/current/request-attempts?request_id=phase5-uat-usage-2` returned a completed attempt with no `api_key_id`
  - `GET /api/v1/accounts/current/usage-events?request_id=phase5-uat-usage-2` returned only `reservation_created` and no `api_key_id`
  - `GET /api/v1/accounts/current/api-keys` still showed the active key without `last_used_at`

### 6. Key Revocation & Rotation
expected: Revoking an active key immediately prevents subsequent edge-api calls. Rotating a key returns a new raw secret and the old key stops working.
result: issue
reported: "Rotation and revocation update control-plane state, but the edge still accepts both the rotated-away secret and the explicitly revoked secret with HTTP 200 responses."
severity: blocker
notes: |
  Evidence from 2026-04-01:
  - rotate returned a fresh raw secret and the list endpoint showed the old key as `revoked`
  - `GET /v1/models` with the rotated-away secret still returned HTTP 200
  - `POST /api/v1/accounts/current/api-keys/{new}/revoke` marked the replacement key revoked
  - `GET /v1/models` with the explicitly revoked replacement secret still returned HTTP 200

## Summary

total: 6
passed: 4
issues: 2
pending: 0
skipped: 0

## Bugs Fixed During Verification

- `rate_limit_exceeded` returned HTTP 401 instead of 429 -> fixed in `apps/edge-api/cmd/server/main.go`
- Rate-limit error messages were hardcoded (`Limit: 60 / min`) -> now dynamic via `fmt.Sprintf` with policy values
- `CheckRateLimit` used hardcoded 60 RPM / 120000 TPM -> now reads from `AuthSnapshot.Policy`
- `AuthSnapshot` in edge-api lacked `Policy` struct -> added for rate-limit deserialization

## Gaps

- truth: "A successful API-key-backed request records `api_key_id` in usage data and updates `last_used_at` on the key."
  status: failed
  reason: "User reported: The live reservation/finalize flow completed without any API-key attribution: the request attempt had no api_key_id, the usage-events response exposed no api_key_id, and the active key still had no last_used_at after the successful finalize."
  severity: major
  test: 5
  root_cause: "The public accounting HTTP contract never accepts or forwards `api_key_id` into `CreateReservationInput`, the finalize path only emits a usage event for release/reconciliation cases, and the usage-events handler omits `api_key_id` from its response body. The live API therefore has no end-to-end path that both carries key identity into accounting and surfaces the attributed result back out."
  artifacts:
    - path: "apps/control-plane/internal/accounting/http.go"
      issue: "createReservationRequest lacks `api_key_id`, so `CreateReservationInput.APIKeyID` is always nil on the public HTTP path."
    - path: "apps/control-plane/internal/accounting/service.go"
      issue: "FinalizeReservation records no completed usage event on the common exact-charge success path."
    - path: "apps/control-plane/internal/usage/http.go"
      issue: "handleListEvents drops `api_key_id` from customer-visible usage event JSON even though the model carries it."
  missing:
    - "Accept and validate `api_key_id` on the public accounting request path or ensure the edge/internal caller injects it before accounting starts."
    - "Record a completed usage event on successful finalize flows, not only on release/reconciliation branches."
    - "Expose `api_key_id` in usage event responses and verify `MarkLastUsed` runs when an attributed request settles."
  debug_session: ".planning/debug/apikey-usage-attribution.md"

- truth: "Revoking an active key immediately blocks edge requests, and rotating a key invalidates the old secret right away."
  status: failed
  reason: "User reported: Rotation and revocation update control-plane state, but the edge still accepts both the rotated-away secret and the explicitly revoked secret with HTTP 200 responses."
  severity: blocker
  test: 6
  root_cause: "The edge resolver caches `AuthSnapshot` documents in Redis for one hour, but the control-plane revoke/rotate flows never invalidate or refresh those cached snapshots. `RefreshSnapshot` is still a no-op, so stale cached entries continue authorizing revoked secrets until TTL expiry."
  artifacts:
    - path: "apps/edge-api/internal/authz/client.go"
      issue: "Resolve caches snapshots under `auth:key:{tokenHash}` with a one-hour TTL and reuses them before consulting the control plane."
    - path: "apps/control-plane/internal/apikeys/service.go"
      issue: "RevokeKey and RotateKey update Postgres state but never trigger cache invalidation; `RefreshSnapshot` is explicitly a no-op."
  missing:
    - "Invalidate or overwrite the Redis auth snapshot for a key whenever revoke, rotate, disable, enable, expiration, or policy changes occur."
    - "Add end-to-end coverage proving old secrets fail immediately after rotate/revoke."
    - "Re-check edge authorization against the post-mutation snapshot rather than trusting stale cache entries."
  debug_session: ".planning/debug/apikey-cache-invalidation.md"
