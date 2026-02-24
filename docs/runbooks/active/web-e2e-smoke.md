# Web E2E Smoke Runbook

This runbook covers smoke validation for the guarded auth -> chat -> billing/settings flow in `apps/web`.

## Scope

Smoke suite file: `apps/web/e2e/smoke-auth-chat-billing.spec.ts`

Covered scenarios:

- unauthenticated `/` redirects to `/auth`
- register happy path reaches `/` chat workspace
- chat success and failure messaging
- billing access from profile menu
- top-up failure messaging in settings

## Prerequisites

1. Docker is running.
2. Dependencies are installed:

```bash
pnpm install
```

3. Playwright browser is installed:

```bash
pnpm --filter @hive/web exec playwright install chromium
```

4. Export the API base URL used by fixtures that call the API directly:

```bash
export E2E_API_BASE_URL=http://127.0.0.1:8080
```

You can also place `E2E_API_BASE_URL=http://127.0.0.1:8080` in your local shell profile or env file used during Playwright runs.

If Linux host libraries are missing locally:

```bash
pnpm --filter @hive/web exec playwright install-deps chromium
```

## Local Run

Start stack:

```bash
docker compose up --build -d
docker compose ps
```

Run smoke e2e:

```bash
pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts
```

## CI Run

Workflow: `.github/workflows/web-e2e-smoke.yml`

Workflow installs browser dependencies, starts Docker stack, waits for readiness, and runs the smoke spec.

## Troubleshooting

- Missing browser executable: rerun the Playwright browser install command.
- API/web readiness timeout: run `docker compose ps` and inspect logs via `docker compose logs api web`.
- Missing local Linux shared libraries: use `playwright install-deps` or rely on CI runner provisioning.
