# Debug Session: API Key Cache Invalidation

## Symptom

Test 6 in Phase 05 UAT failed.

Expected:
- Revoking an active key immediately blocks subsequent edge-api calls.
- Rotating a key returns a new raw secret and the old key stops working.

Actual:
- The control-plane marks the old key revoked after rotate.
- The control-plane marks the replacement key revoked after explicit revoke.
- The edge still returns HTTP 200 for both revoked secrets on `/v1/models`.

## Reproduction

1. Create a key through `POST /api/v1/accounts/current/api-keys`.
2. Call `GET /v1/models` with that secret and confirm HTTP 200.
3. Rotate the key through `POST /api/v1/accounts/current/api-keys/{key_id}/rotate`.
4. Confirm the control-plane list endpoint shows the old key as `revoked`.
5. Call `GET /v1/models` with the rotated-away secret.
6. Revoke the replacement key through `POST /api/v1/accounts/current/api-keys/{key_id}/revoke`.
7. Call `GET /v1/models` with the revoked replacement secret.

Observed on 2026-04-01:
- Both revoked secrets still returned HTTP 200.

## Evidence

- [apps/edge-api/internal/authz/client.go](/home/sakib/hive/apps/edge-api/internal/authz/client.go)
  `Resolve` reads `auth:key:{tokenHash}` from Redis first and writes fetched snapshots back with a one-hour TTL.
- [apps/control-plane/internal/apikeys/service.go](/home/sakib/hive/apps/control-plane/internal/apikeys/service.go)
  `RevokeKey` and `RotateKey` update durable key state, but they never invalidate or refresh Redis snapshots.
- [apps/control-plane/internal/apikeys/service.go](/home/sakib/hive/apps/control-plane/internal/apikeys/service.go)
  `RefreshSnapshot` is an explicit no-op placeholder.

## Root Cause

The edge hot path trusts stale Redis snapshots for up to one hour. Revoke and rotate mutate Postgres state, but no code invalidates or rewrites the cached `auth:key:{tokenHash}` entry, so revoked secrets continue authorizing until TTL expiry.

## Fix Direction

- Add a real snapshot invalidation/refresh path in the control-plane API-key service.
- Trigger it from revoke, rotate, disable, enable, expiration-sensitive policy changes, and any mutation that changes authorization truth.
- Add end-to-end tests proving a revoked or rotated-away secret fails on the very next edge request.
