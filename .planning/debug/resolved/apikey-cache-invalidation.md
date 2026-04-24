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

## Fix Applied

**Date:** 2026-04-09

**Status:** Already implemented — fix was delivered in commit `506edfb` (feat(05-05): finish live key auth snapshot projections). No code changes were required in this session.

**What was verified (by code reading):**

- `apps/control-plane/internal/apikeys/service.go`
  - `RevokeKey` (line 249): calls `s.invalidateSnapshot(ctx, updated.TokenHash)` after the Postgres mutation succeeds.
  - `RotateKey` (line 303): calls `s.invalidateSnapshots(ctx, old.TokenHash, created.TokenHash)` — invalidates both the replaced key and the newly created replacement key.
  - `RefreshSnapshot` (lines 407–413): real implementation — looks up key by ID, then calls `invalidateSnapshot`. Not a no-op.
  - `DisableKey` and `EnableKey` also invalidate.
  - `invalidateSnapshot` delegates to `SnapshotCache.InvalidateSnapshot` which calls `redis.Del("auth:key:{tokenHash}")`.

- `apps/control-plane/cmd/server/main.go` (line 101): `apikeys.NewService(apikeysRepo, apikeys.NewRedisSnapshotCache(redisClient))` — the Redis cache is injected at startup.

- `apps/edge-api/internal/authz/client.go` (line 90): uses key format `auth:key:{tokenHash}` — exactly matches the control-plane's `snapshotRedisKey` function (line 630 of service.go).

- `apps/control-plane/internal/apikeys/service_test.go`:
  - `TestRevokeKeyInvalidatesCachedSnapshot`: asserts revoke invalidates the correct token hash.
  - `TestRevokeKeyReturnsErrorWhenSnapshotInvalidationFails`: asserts Postgres state is durable even if Redis fails.
  - `TestRotateKeyInvalidatesOldAndNewSnapshots`: asserts both old and new hashes are invalidated in order.
  - `TestUpdatePolicyInvalidatesCachedSnapshot`: asserts policy changes also flush the cache.

**Root cause was correctly identified.** The fix was already applied before this debug session was continued. The debug session's state was stale relative to the codebase.

## Resolution

**Status:** resolved (2026-04-09)
**Fixed by:** Commit 506edfb (feat(05-05): finish live key auth snapshot projections)
**Verification:** Requires live Docker stack for E2E confirmation.
