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

4. Export the API and web base URLs used by fixtures and browser navigation:

```bash
export E2E_API_BASE_URL=http://127.0.0.1:8080
export E2E_BASE_URL=http://127.0.0.1:3000
export E2E_SUPABASE_ANON_KEY=<your-local-supabase-anon-key>
```

`E2E_API_BASE_URL` is used by auth fixtures that call the API directly, and `E2E_BASE_URL` is used by Playwright `baseURL` for browser navigation.
If you override the web port or host, set both variables to matching values for that environment.
You can also place both exports in your local shell profile or env file used during Playwright runs.
`E2E_SUPABASE_ANON_KEY` is required for the default fixture path, which creates a real Supabase user through the Auth REST API and seeds the shared web auth-session storage key.
The smoke/dev fallback path also seeds that same browser auth-session key, so auth-session sync code must not clear it just because Supabase initially reports no browser session.

Optional smoke-only fallback:

```bash
export E2E_ALLOW_DEV_TOKEN_FALLBACK=true
```

Enable this only in disposable local or CI smoke environments when Supabase signup is unavailable. It allows the fixture to seed a synthetic token instead of failing immediately.

If Linux host libraries are missing locally:

```bash
pnpm --filter @hive/web exec playwright install-deps chromium
```

## Local Run

Use `pnpm stack:dev` for normal daily development. It is not the preferred target for this smoke runbook, because this runbook is meant to validate production-bundle and hydration behavior rather than `next dev`.

For local smoke validation on a fresh environment, run the one-time bootstrap first:

```bash
pnpm bootstrap:local
```

Do not rerun `pnpm bootstrap:local` as a routine smoke step unless you explicitly want to reset your local Supabase data and repull the default Ollama model.

Then use the non-destructive production-style build/serve flow below.

Build the production web bundle with the required public envs:

```bash
ANON_KEY=<your-local-supabase-anon-key>

NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 \
NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 \
NEXT_PUBLIC_SUPABASE_ANON_KEY=$ANON_KEY \
pnpm --filter @hive/web build
```

Get the real local Supabase anon key from:

```bash
npx supabase status -o env | rg '^(ANON_KEY|API_URL|SERVICE_ROLE_KEY)='
```

Start the production-style stack:

```bash
npx supabase start
NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 \
NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 \
NEXT_PUBLIC_SUPABASE_ANON_KEY=$ANON_KEY \
docker compose up --build -d
docker compose ps
```

If you are validating a local fix outside Docker, run the rebuilt production app instead of `next dev`:

```bash
ANON_KEY=<your-local-supabase-anon-key>

NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 \
NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 \
NEXT_PUBLIC_SUPABASE_ANON_KEY=$ANON_KEY \
pnpm --filter @hive/web exec next start -p 3000
```

Run smoke e2e:

```bash
pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts
```

This matters for auth/session fixes because production hydration can expose redirect races and session-clearing bugs that are easy to miss in unit tests or `next dev`.

## CI Run

Workflow: `.github/workflows/web-e2e-smoke.yml`

Workflow installs browser dependencies, starts Docker stack, waits for readiness, and runs the smoke spec.
The CI workflow currently enables `E2E_ALLOW_DEV_TOKEN_FALLBACK=true` so smoke runs can proceed in ephemeral environments even if direct Supabase signup is unavailable.

## Troubleshooting

- Missing browser executable: rerun the Playwright browser install command.
- Missing `E2E_SUPABASE_ANON_KEY`: export the local Supabase anon key or opt into `E2E_ALLOW_DEV_TOKEN_FALLBACK=true` for smoke-only runs.
- API/web readiness timeout: run `docker compose ps` and inspect logs via `docker compose logs api web`.
- Smoke run accidentally executed against `pnpm stack:dev`: stop the dev stack with `pnpm stack:down` and rerun the production-style commands above.
- Missing local Linux shared libraries: use `playwright install-deps` or rely on CI runner provisioning.
- Smoke redirects authenticated fixtures back to `/auth`: verify the browser auth-session mirror waits for client hydration and does not clear seeded sessions before a real Supabase session has been observed.
