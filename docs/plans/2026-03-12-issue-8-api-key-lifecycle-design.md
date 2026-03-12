# Issue #8 API Key Lifecycle Design

## Goal

Define a production-safe design for issue `#8` that adds API key lifecycle auditability and operator-visible key management with nicknames, revocation, expiration, and developer-page visibility.

## Scope

- Support multiple API keys per user.
- Add non-secret key metadata for `nickname` and optional expiration.
- Preserve hashed-at-rest key storage and one-time raw key return at creation.
- Add immutable audit events for API key lifecycle actions.
- Expose current key metadata and lifecycle visibility through authenticated user-facing API routes.
- Add developer-page UI for key creation, listing, status visibility, and revocation.

## Current State

- `public.api_keys` stores `key_hash`, `user_id`, `key_prefix`, `scopes`, `revoked`, `created_at`, and `revoked_at`.
- `SupabaseApiKeyStore` resolves and lists keys by current row state only.
- `PersistentUserService.me()` returns only prefix-like identifier, scopes, created date, and revoked flag.
- API-key management is partially implied by the web UI, but there is no lifecycle event model, nickname support, expiration handling, or developer-page history surface.
- Auth resolution rejects revoked keys, but there is no expiration check.

## Recommended Approach

Keep `api_keys` as the current-state table for fast auth lookup and add a separate append-only `api_key_events` table for lifecycle audit history.

This separates two concerns cleanly:

- request-time validation remains a single-row lookup against `api_keys`
- lifecycle reporting and operator traceability come from immutable event records

This approach keeps the auth path simple while making future lifecycle features, including explicit rotation or admin investigations, straightforward.

## Data Model

### `api_keys` table changes

Extend the existing table with:

- `id uuid primary key default gen_random_uuid()` or equivalent stable row identifier
- `nickname text not null`
- `expires_at timestamptz null`

Retain:

- `key_hash` as a unique lookup field
- `key_prefix`
- `scopes`
- `revoked`
- `created_at`
- `revoked_at`

Derived status for reads:

- `active` when not revoked and not expired
- `revoked` when `revoked = true`
- `expired` when `expires_at < now()` and not revoked

### `api_key_events` table

Add an immutable event table keyed by event id with:

- `id uuid`
- `api_key_id uuid`
- `user_id uuid`
- `event_type text`
- `event_at timestamptz`
- `metadata jsonb default '{}'`

Initial event types:

- `created`
- `revoked`
- `expired_observed`

`expired_observed` should be emitted when the backend first detects an expired key during a management read or auth resolution, not by a scheduler in this issue. That keeps the design minimal while still producing audit evidence.

## API Design

### Create key

Use the authenticated session-backed management surface:

- `POST /v1/users/api-keys`

Request fields:

- `nickname`
- `scopes`
- optional `expiresAt` or a constrained duration field

Response:

- raw API key returned once
- stable key id
- key prefix
- nickname
- scopes
- created timestamp
- expires timestamp
- current status

### List keys

Add or normalize:

- `GET /v1/users/api-keys`

Response includes current key metadata only:

- `id`
- `keyPrefix`
- `nickname`
- `scopes`
- `createdAt`
- `expiresAt`
- `revokedAt`
- `status`

### Revoke key

Add:

- `POST /v1/users/api-keys/:id/revoke`

This avoids requiring the raw secret for management operations and makes revocation idempotent.

### User snapshot

`GET /v1/users/me` should continue to provide a summary view, but its API key payload should be enriched enough for dashboard cards to show active vs revoked vs expired counts without requiring secret material.

## Runtime Behavior

### Auth resolution

`SupabaseApiKeyStore.resolve()` must reject:

- unknown keys
- revoked keys
- expired keys

If an expired key is observed and no expiration event exists yet, the store should persist a single `expired_observed` audit event. Auth failure behavior should remain equivalent to invalid credentials.

### Creation and revocation

Key creation should write:

1. current-state row in `api_keys`
2. `created` event in `api_key_events`

Revocation should:

1. mark `revoked = true`
2. set `revoked_at`
3. write a `revoked` event exactly once

## Developer Page

Upgrade `/developer` from a simple “create extra key” action into a key-management surface:

- create-key form with nickname, scopes, and optional expiration
- current key list with status badges
- revoke action per key
- lifecycle activity section or per-key event timeline

The page should continue using the browser Supabase session bearer token, not an API key, for management requests.

## Alternatives Considered

### 1. Extend `api_keys` only

Pros:

- fewer tables
- smallest schema change

Cons:

- weak audit trail
- lifecycle changes overwrite evidence
- hard to explain or inspect who/what changed a key over time

### 2. Version every key row

Pros:

- strong historical reconstruction

Cons:

- unnecessary complexity for current MVP
- makes auth lookup and management semantics harder than needed

## Verification Strategy

- Migration and store checks:
  `sed -n '1,260p' apps/api/supabase/migrations/20260312_004_api_key_lifecycle.sql`
  `pnpm --filter @hive/api exec vitest run test/domain/supabase-api-key-store.test.ts`
- Auth and expiry checks:
  `pnpm --filter @hive/api exec vitest run test/routes/auth-principal.test.ts`
  `pnpm --filter @hive/api exec vitest run test/domain/supabase-api-key-store.test.ts`
- Service and route flows:
  `pnpm --filter @hive/api exec vitest run test/domain/persistent-user-service.test.ts`
  `pnpm --filter @hive/api exec vitest run test/routes/user-api-keys-route.test.ts`
- Web contract and UI:
  `rg -n "/v1/users/me|/v1/users/api-keys|nickname|expiresAt|revokedAt|status" packages/openapi/openapi.yaml`
  `pnpm --filter @hive/web build`
- Full touched-scope verification:
  `pnpm --filter @hive/api test`
  `pnpm --filter @hive/api build`
  `pnpm --filter @hive/web build`

Example manual API exercise after deployment:

```bash
API_URL=http://127.0.0.1:8080
ACCESS_TOKEN="<supabase-access-token>"

curl -s "$API_URL/v1/users/me" -H "authorization: Bearer $ACCESS_TOKEN"
curl -s "$API_URL/v1/users/api-keys" -H "authorization: Bearer $ACCESS_TOKEN"
curl -s -X POST "$API_URL/v1/users/api-keys" \
  -H "authorization: Bearer $ACCESS_TOKEN" \
  -H "content-type: application/json" \
  -d '{"nickname":"deploy","scopes":["chat","usage"],"expiresAt":"2026-12-01T00:00:00.000Z"}'
```

## Risks and Mitigations

- Risk: expiration checks add side effects to auth reads.
  Mitigation: only append a single `expired_observed` event when needed and keep key validity derived from `expires_at`, not from the event.
- Risk: developer-page management APIs expose too much metadata.
  Mitigation: return only non-secret fields and continue returning raw key material only at creation time.
- Risk: route surface drifts from the currently implied web behavior.
  Mitigation: formalize the user/API-key management routes in API tests and update docs/openapi accordingly.
- Risk: current code drift around unregistered user routes masks regressions.
  Mitigation: include route registration and end-to-end route tests in implementation.

## Out of Scope

- automatic key rotation flows
- scheduler-based expiration sweeps
- admin cross-user key management
- scope model redesign
