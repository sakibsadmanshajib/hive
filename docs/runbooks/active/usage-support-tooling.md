# Usage Analytics and Support Snapshot Runbook

## Purpose

This runbook documents the first operator/support tooling slice for issue `#13`: richer user-scoped usage analytics and a single-user admin troubleshooting snapshot.

## User-Scoped Analytics

`GET /v1/usage`

Authentication:

- valid Hive API key via `x-api-key`
- or `Authorization: Bearer <api-key>`
- or an authenticated Supabase session on the web developer panel path

Response shape:

- `data`: recent raw usage events
- `summary.windowDays`: analytics window used for aggregation
- `summary.totalRequests`
- `summary.totalCredits`
- `summary.daily`
- `summary.byModel`
- `summary.byEndpoint`

Important boundary:

- `summary` is window-bounded
- `data` remains the raw recent event list and may include rows older than the summary window

## Admin Support Snapshot

`GET /v1/support/users/{userId}`

Authentication:

- requires `x-admin-token`
- returns `401` without a valid admin token

Response includes:

- basic user identity fields
- current credit balance
- usage summary plus recent raw usage events
- managed API keys
- API key lifecycle events

Example:

```bash
curl -s http://127.0.0.1:8080/v1/support/users/<USER_ID> \
  -H "x-admin-token: <ADMIN_STATUS_TOKEN>"
```

## Operator Guidance

- Use the support snapshot for one-user investigations only; do not treat it as a broad user search surface.
- Do not expose or proxy this endpoint to public clients.
- Treat API key metadata as sensitive operational data even though raw key secrets are not returned.
- If usage totals look inconsistent, verify whether you are comparing the windowed `summary` or the broader raw `data` list.

## Verification

```bash
pnpm --filter @hive/api exec vitest run test/domain/persistent-usage-service.test.ts
pnpm --filter @hive/api exec vitest run test/routes/usage-route.test.ts
pnpm --filter @hive/api exec vitest run test/routes/support-route.test.ts
pnpm --filter @hive/api test
docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"
docker compose exec web sh -c "cd /app && pnpm --filter @hive/web build"
```
