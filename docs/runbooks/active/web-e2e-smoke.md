# Web E2E Smoke Runbook

This runbook covers smoke validation for the guest-first home, auth, chat, and billing/settings flow in `apps/web`.

## Scope

Smoke suite file: `apps/web/e2e/smoke-auth-chat-billing.spec.ts`

Covered scenarios:

- unauthenticated `/` stays on the guest-first chat workspace
- guest mode can send a real free-model chat message through the web guest route
- guest chat transcript persists after reload (server-backed history)
- guest mode fails closed if the catalog does not expose any unlocked guest chat model
- locked paid models open a dismissible auth modal for guests
- registering from that modal unlocks paid models in place
- chat success and failure messaging (session-based for authenticated users)
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

Use the Docker-local stack for smoke verification. Do not switch to standalone local app servers or alternate web ports for this runbook; the repo’s web-to-API path is origin-sensitive and smoke should reflect the normal Docker-local wiring on `http://127.0.0.1:3000`.

For local smoke validation on a fresh environment, run the one-time bootstrap first:

```bash
pnpm bootstrap:local
```

Do not rerun `pnpm bootstrap:local` as a routine smoke step unless you explicitly want to reset your local Supabase data and repull the default Ollama model.

Load the live local Supabase values used by the Docker-local stack:

```bash
npx supabase start
npx supabase status
set -a
# shellcheck disable=SC1090
source <(npx supabase status -o env)
set +a
export WEB_INTERNAL_GUEST_TOKEN=dev-web-guest-token
export E2E_SUPABASE_ANON_KEY="$ANON_KEY"
export OLLAMA_FREE_MODEL="${OLLAMA_FREE_MODEL:-${OLLAMA_MODEL:-llama3.1:8b}}"
```

Rebuild and start the Docker-local stack from the current working tree:

```bash
docker compose down
docker compose up --build -d
docker compose ps
```

If you only changed `api` or `web` code and want a faster rerun, rebuild those services explicitly:

```bash
docker compose up --build -d api web
docker compose ps
```

The Docker-local stack expects:

- `WEB_INTERNAL_GUEST_TOKEN` to be present for both `web` and `api` so guest session bootstrap and guest chat work
- `OLLAMA_FREE_MODEL` to point at a pulled local Ollama model so `guest-free` has a real zero-cost provider offer
- the API container to reach Ollama over `http://ollama:11434`, not `http://127.0.0.1:11434`

Verify readiness on the standard Docker-local ports:

```bash
curl -s http://127.0.0.1:8080/health
curl -sI http://127.0.0.1:3000/auth
```

Run smoke e2e:

```bash
pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts
```

This matters for auth/session fixes because the current smoke flow depends on the real Docker-local origin and web-to-API path, including guest-first `/`, locked-model auth modal behavior, and billing navigation from the profile menu.

## CI Run

Workflow: `.github/workflows/web-e2e-smoke.yml`

Workflow installs browser dependencies, starts the local Supabase CLI stack, resets the local schema from `supabase/migrations/`, exports the live local Supabase anon/service-role keys, pulls a small local Ollama model for `guest-free`, injects a local-only `WEB_INTERNAL_GUEST_TOKEN`, then starts the Docker app stack on top of that state and runs the smoke spec on `http://127.0.0.1:3000`.

CI service split:

- Supabase CLI provides the auth/database stack required for real guest-session persistence and real Auth REST signup fixtures
- Docker Compose provides `redis`, `langfuse-db`, `langfuse`, `ollama`, `api`, and `web`
- CI guest chat uses a pulled local Ollama free model so the smoke suite still sends one real guest chat message through the web boundary

## Troubleshooting

- Missing browser executable: rerun the Playwright browser install command.
- Missing `E2E_SUPABASE_ANON_KEY`: export the live local Supabase anon key from `npx supabase status -o env`.
- API/web readiness timeout: run `docker compose ps` and inspect logs via `docker compose logs api web`.
- Browser behavior still reflects an already-fixed bug: rebuild `api` and `web` from the current working tree, then confirm the running containers were recreated before trusting localhost results.
- CI smoke fails before the app stack is healthy: verify both orchestration layers are up, not just one of them. `npx supabase start` must be running alongside the Docker app services.
- CI guest bootstrap fails with missing tables such as `guest_sessions`: verify the workflow ran `npx supabase db reset --yes` before exporting the local Supabase env.
- Smoke run accidentally executed against `pnpm stack:dev` or a standalone local server: stop that flow and rerun the Docker-local commands above.
- Guest UI renders but guest chat fails immediately: verify `WEB_INTERNAL_GUEST_TOKEN` is present in both `web` and `api`, and confirm the API container is not pointing Ollama at `127.0.0.1`.
- Missing local Linux shared libraries: use `playwright install-deps` or rely on CI runner provisioning.
- Smoke redirects authenticated fixtures back to `/auth`: verify the browser auth-session mirror waits for client hydration and does not clear seeded sessions before a real Supabase session has been observed.
