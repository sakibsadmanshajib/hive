# API Key Lifecycle Runbook

## Purpose

This runbook documents managed user API keys with nicknames, optional expiration, revocation, and immutable lifecycle audit events.

## Data Model

- Current key state lives in `public.api_keys`
- Lifecycle history lives in `public.api_key_events`
- Raw API keys are returned only once at creation time and are never stored in plaintext

Current key metadata includes:

- stable key `id`
- non-secret `key_prefix`
- `nickname`
- `scopes`
- `created_at`
- optional `expires_at`
- `revoked` and optional `revoked_at`

Lifecycle events currently include:

- `created`
- `revoked`
- `expired_observed`

## Status Semantics

- `active` means not revoked and not expired
- `revoked` means `revoked = true`
- `expired` means `expires_at` is in the past and the key is no longer valid for auth

Expired keys are rejected during normal auth resolution. The first observed expired access records an `expired_observed` audit event.

## Management API

These routes require a valid Supabase bearer token plus `users:manage_api_keys` permission and `apiEnabled` setting access:

- `GET /v1/users/me`
- `GET /v1/users/api-keys`
- `POST /v1/users/api-keys`
- `POST /v1/users/api-keys/:id/revoke`

Creation rules:

- `nickname` is required
- `scopes` is required
- `expiresAt`, when present, must be a valid future ISO timestamp

## Verification

```bash
pnpm --filter @hive/api exec vitest run test/domain/supabase-api-key-store.test.ts
pnpm --filter @hive/api exec vitest run test/domain/persistent-user-service.test.ts
pnpm --filter @hive/api exec vitest run test/routes/user-api-keys-route.test.ts
pnpm --filter @hive/api test
docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"
docker compose exec web sh -c "cd /app && pnpm --filter @hive/web build"
```

## Operator Notes

- Treat copied raw keys as secrets; rotate immediately if exposed.
- Revoke keys by stable key id from the management API, not by re-submitting the raw secret.
- Duplicate `expired_observed` events across restarts indicate audit dedupe drift and should be investigated.
