---
status: complete
phase: 05-api-keys-hot-path-enforcement
source: [05-01-SUMMARY.md, 05-02-SUMMARY.md, 05-03-SUMMARY.md, 05-04-SUMMARY.md, 05-05-SUMMARY.md, 05-06-SUMMARY.md]
started: 2026-04-02T04:10:59-04:00
updated: 2026-04-02T04:38:08-04:00
---

## Current Test

[testing complete]

## Tests

### 1. Cold Start Smoke Test
expected: Kill any running server/service. Clear ephemeral state (temp DBs, caches, lock files). Start the application from scratch (`docker compose up -d`). The control-plane and edge-api boot without errors, database migrations complete, and `/health` on both services returns live status.
result: pass
notes: |
  Verified on 2026-04-02 with `docker compose --env-file .env -f deploy/docker/docker-compose.yml up -d --force-recreate --build redis control-plane edge-api`.
  The first cold-start attempt exposed a real gap: `deploy/docker/Dockerfile.edge-api` did not copy `apps/control-plane/go.mod`, so `go mod download` failed against the shared `go.work` file.
  After fixing that Dockerfile seam, the rebuilt stack started cleanly and in-network health checks returned `{"status":"ok"}` from both `control-plane:8081/health` and `edge-api:8080/health`.

### 2. API Key Issuance And Safe Key Views
expected: Creating a key returns a raw `hk_...` secret exactly once. Listing and detail endpoints show the key with policy-backed `expiration_summary`, `budget_summary`, and `allowlist_summary`, but never expose the raw secret again.
result: pass
notes: |
  `POST /api/v1/accounts/current/api-keys` returned HTTP 201 with a one-time `hk_...` secret for key `c554c3d0-8e8d-4e26-bdbd-19fa4552b9a6`.
  `GET /api/v1/accounts/current/api-keys` and `GET /api/v1/accounts/current/api-keys/{key_id}` returned the expected summary fields and omitted `secret` while preserving the redacted suffix.

### 3. Edge Authorization For Models
expected: Calling `/v1/models` with a valid API key succeeds. Missing, invalid, or disallowed key usage is rejected before routing with an OpenAI-style authorization error instead of a successful model list.
result: pass
notes: |
  `GET /v1/models` with the live secret returned HTTP 200 and the Hive model list.
  `GET /v1/models` with `hk_invalid_secret` returned HTTP 401 with OpenAI-style JSON: `type=invalid_request_error`, `code=invalid_api_key`, and no rate-limit headers.

### 4. Separate Rate Limits And Retry Headers
expected: When account or key thresholds are exceeded, the edge returns HTTP 429 `rate_limit_exceeded` using the active policy values. Retry metadata headers appear only on true 429 rate-limit denials, not on successful or permanent-auth responses.
result: pass
notes: |
  Created fresh key `7c9861e0-c5d2-4762-a818-455eb9a9edb3`, then set durable rate policies to `account_rpm=10` and `key_rpm=1`.
  The first `GET /v1/models` with that key returned HTTP 200; the second returned HTTP 429 with `code=rate_limit_exceeded`, message `Limit: 1 / min.`, and retry metadata headers (`retry-after`, `x-ratelimit-limit-requests`, `x-ratelimit-remaining-requests`, `x-ratelimit-reset-requests`).
  Earlier invalid-key responses remained header-clean, so retry metadata only appeared on the true 429 path.

### 5. Usage Attribution And Last Used Timestamp
expected: A successful API-key-backed request records the `api_key_id` on request and usage data, emits a completed usage event, and updates the key `last_used_at` timestamp.
result: pass
notes: |
  Created reservation `fbd9087d-4b5d-472e-be78-e264b30f782f` for request `phase5-live-usage-1775118907824` with `api_key_id=c554c3d0-8e8d-4e26-bdbd-19fa4552b9a6`, then finalized it successfully.
  `GET /api/v1/accounts/current/request-attempts?request_id=phase5-live-usage-1775118907824` showed `status=completed` with the same `api_key_id`.
  `GET /api/v1/accounts/current/usage-events?request_id=phase5-live-usage-1775118907824` returned both `reservation_created` and `completed` events with `api_key_id`, and `GET /api/v1/accounts/current/api-keys` showed `last_used_at=2026-04-02T08:35:11Z`.

### 6. Disable And Re-Enable Key
expected: Disabling an active key makes subsequent edge requests fail immediately. Re-enabling that same key restores successful access without issuing a new secret.
result: pass
notes: |
  `POST /api/v1/accounts/current/api-keys/c554c3d0-8e8d-4e26-bdbd-19fa4552b9a6/disable` returned HTTP 200 and changed the key status to `disabled`.
  The next `GET /v1/models` with the same secret returned HTTP 401 with `Incorrect API key provided: API key is disabled`.
  `POST /enable` restored the key to `active`, and the next `GET /v1/models` with the unchanged secret returned HTTP 200.

### 7. Immediate Rotate And Revoke Enforcement
expected: Rotating a key returns a new raw secret and the old secret stops working immediately. Revoking the replacement key blocks it immediately on the next edge request.
result: pass
notes: |
  `POST /rotate` on key `c554c3d0-8e8d-4e26-bdbd-19fa4552b9a6` returned a fresh secret for replacement key `89ad616c-24fb-4b0e-abaf-448b8faebcbe`.
  The rotated-away secret failed on the very next `GET /v1/models` with HTTP 401 and `API key is revoked`, while the replacement secret succeeded with HTTP 200.
  `POST /revoke` on the replacement key returned HTTP 200, and the next `GET /v1/models` with that secret also returned HTTP 401 immediately.

## Summary

total: 7
passed: 7
issues: 0
pending: 0
skipped: 0

## Bugs Fixed During Verification

- `deploy/docker/Dockerfile.edge-api` did not copy `apps/control-plane/go.mod` and `go.work` therefore broke `go mod download` during the edge image build. Fixed by copying the control-plane manifest into the edge build stage before dependency resolution.

## Gaps

[none yet]
