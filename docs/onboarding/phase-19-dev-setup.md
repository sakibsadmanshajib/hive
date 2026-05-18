# Phase 19 — Local Development Setup

This document covers Supabase test-project setup, OAuth test-account
configuration, and environment variable wiring for Phase 19 E2E runs.

## 1. Supabase test project

* Create a Supabase project named `hive-dev-<your-handle>`.
* Enable Google OAuth: **Auth → Providers → Google**. Create OAuth
  credentials in Google Cloud Console with the redirect URIs:
  - `https://<project>.supabase.co/auth/v1/callback`
  - `http://localhost:3002/oauth/Hive/callback` (Open WebUI direct)
  - `http://localhost:3000/auth/callback` (web-console)
* Apply migrations from `supabase/migrations/20260516_*.sql` and
  `supabase/migrations/20260518_*.sql` in filename order.
* In **Auth → Hooks → Custom Access Token**, set the function to
  `public.custom_access_token_hook` and enable it.

## 2. Test accounts

You need three Google accounts (Workspace or personal):

| Variable | Purpose |
| --- | --- |
| `E2E_USER_A_EMAIL` / `_PASSWORD` | Tenant A, role MEMBER. |
| `E2E_USER_B_EMAIL` / `_PASSWORD` | Tenant B, role MEMBER. |
| `OWUI_E2E_EMAIL` / `_PASSWORD` | Any of the above; chat surface tester. |

Manually run the OAuth flow once for each account to materialise the
`auth.users` row and the `tenant_users` membership. Note the resolved
tenant uuids for `E2E_TENANT_B_ID` and `E2E_USER_A_SECOND_TENANT_ID`.

Pre-mint two test JWTs:

* `E2E_EXPIRED_JWT` — a Supabase JWT signed with the project's secret
  whose `exp` claim is in the past.
* `E2E_ORPHAN_JWT` — a JWT for a user that has zero `tenant_users` rows.

Both can be generated with the Supabase admin script:

```bash
node tools/dev-mint-jwt.mjs --exp -3600 > E2E_EXPIRED_JWT.txt
node tools/dev-mint-jwt.mjs --orphan   > E2E_ORPHAN_JWT.txt
```

`tools/dev-mint-jwt.mjs` is a small wrapper around `pg.Client` plus the
Supabase service-role key. Add it the first time a Plan 04 user-flow spec
needs the orphan / expired tokens — the OAuth setup above does not
require it.

## 3. .env

Copy `.env.example` to `.env`. Populate the Phase 19 variables introduced
in Plan 03 Task 2 plus the E2E variables above. Use `.env.ci` for CI runs
where only secrets differ.

## 4. Smoke test

```bash
cd deploy/docker
docker compose --env-file ../../.env --profile local up -d --build

# Wait until edge-api + control-plane + Open WebUI are healthy, then:
curl -fsSL http://localhost:3003/health   # OWUI via Caddy

cd ../../apps/web-console
pnpm install
pnpm e2e:phase-19
```

If Playwright reports green, your stack is ready. For the Open WebUI
direct E2E suite:

```bash
pnpm e2e:owui
pnpm e2e:owui:perf
```

## 5. Common pitfalls

* `AUTH_JWT_INVALID` audit rows on every chat — the OWUI pipeline filter
  did not pick up the user JWT. Confirm `PIPELINES_URLS` points at the
  mounted file. Adopt the Go sidecar fallback (see Phase 19 spec §6) if
  the OWUI build does not honour `__metadata.upstream_auth`.
* `403 NO_TENANT` after first sign-in — the Supabase Database Webhook did
  not fire. Inspect Supabase dashboard → Database → Webhooks; ensure the
  `auth.users INSERT` webhook is registered against
  `${CONTROL_PLANE_URL}/internal/auth/user-created`.
* Open WebUI native admin still visible at `/admin` — confirm you are
  hitting port `3003` (Caddy) not `3002` (OWUI direct). The Caddy block
  is the user-facing surface.
