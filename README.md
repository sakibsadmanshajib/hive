# Hive

Hive is an AI inference platform with:
- OpenAI-compatible API endpoints for chat, responses, and model access
- provider routing across Ollama, Groq, and mock fallback
- prepaid credits, billing persistence, and reconciliation controls
- Supabase-backed auth, user data, API keys, and settings
- self-hosted Langfuse for LLM observability
- a lightweight web workspace plus developer panel
- Bangladesh-native payment workflows as one strategic monetization wedge

## Start Here

- Full docs index: `docs/README.md`
- Agent operating guide: `AGENTS.md`
- Changelog: `CHANGELOG.md`
- Contributing guide: `CONTRIBUTING.md`
- Code of Conduct: `CODE_OF_CONDUCT.md`
- Security policy: `SECURITY.md`
- Support guide: `SUPPORT.md`
- Governance: `GOVERNANCE.md`
- Engineering standards: `docs/engineering/git-and-ai-practices.md`
- System architecture: `docs/architecture/system-architecture.md`

## Getting Started

Hive local development runs two Docker-managed systems:

- the **Hive app stack**, started by this repo's Docker Compose files
- the **Supabase local stack**, started by the Supabase CLI

Both will appear in `docker ps`, but they are managed by different tools.

### First-Time Setup

```bash
pnpm install
pnpm bootstrap:local
pnpm stack:dev
```

`pnpm bootstrap:local` is the explicit first-time setup command. It:

1. starts or verifies the local Supabase CLI stack
2. resets the local Supabase database
3. applies repo migrations to the local database
4. starts the local `ollama` service and pulls the default `OLLAMA_MODEL`

`pnpm stack:dev` is the canonical daily-development entry point. It:

1. starts or verifies the Supabase local stack
2. reads the live local Supabase keys from `npx supabase status -o env`
3. injects the required auth env vars for Hive
4. starts the full Hive stack with hot reload for `api` and `web`

### Daily Development

Use the same command for normal development:

```bash
pnpm stack:dev
```

Use these commands to stop or reset the local stack:

```bash
pnpm stack:down
pnpm stack:reset
```

Use `pnpm bootstrap:local` again when you need to rebuild the local Supabase schema from migrations or repopulate the default local Ollama model.

### What Starts

`pnpm stack:dev` starts:

- Hive Compose services:
  - `api`
  - `web`
  - `redis`
  - `ollama`
  - `langfuse`
  - `langfuse-db`
- Supabase CLI services:
  - `auth`
  - `db`
  - `kong`
  - `rest`
  - `studio`
  - and related local Supabase services

### Why Supabase Uses The CLI

Supabase is not defined in Hive's `docker-compose.yml`. Instead, Hive uses the Supabase CLI as the source of truth for the local Supabase stack.

That means:

- `docker compose up` alone is not enough for local auth
- `npx supabase start` alone is not enough for the Hive app stack
- `pnpm bootstrap:local` owns first-time local schema/bootstrap
- `pnpm stack:dev` is the standardized daily-development command because it handles both lifecycles together

### Why `api` And `web` Are Separate Containers

Hive keeps `api` and `web` separate even in local development because they are separate deployable applications with different runtime concerns.

This preserves:

- the real browser-to-API HTTP boundary
- correct separation of browser-safe `NEXT_PUBLIC_*` values from server-only secrets
- production-like wiring with hot reload still enabled in development

### Verification

Once the stack is up, these are the primary checks:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml ps
curl -s http://127.0.0.1:8080/health
curl -s http://127.0.0.1:8080/v1/providers/status
curl -s http://127.0.0.1:54321/auth/v1/health
curl -sI http://127.0.0.1:3000/auth
```

### Optional Production-Like Compose Mode

If you explicitly want the built production-style stack instead of hot reload, use:

```bash
npx supabase start
docker compose up --build -d
```

In that mode, you are responsible for ensuring the real local Supabase values are provided to the Hive containers.

## Contributing and Governance

Contributor and repository policy documents live at the repository root:

- `CONTRIBUTING.md`
- `CODE_OF_CONDUCT.md`
- `SECURITY.md`
- `SUPPORT.md`
- `GOVERNANCE.md`

Use these documents together with `AGENTS.md` and `docs/README.md` when proposing changes, reporting issues, or reviewing repository policy.

GitHub contributor intake and triage are repo-managed:

- Issue forms live under `.github/ISSUE_TEMPLATE/`
- The PR checklist lives in `.github/pull_request_template.md`
- Label and milestone metadata are managed by `tools/github/sync-github-meta.sh`
- Maintainer operating guidance lives in `docs/runbooks/active/github-triage.md`
- Maintainer issue state transitions, planning, and closeout workflow live in `docs/runbooks/active/issue-lifecycle.md`

## Current Status

- Stack: TypeScript monorepo (API + web) is the only active runtime.
- Auth: Supabase Auth handles all user registration, login, OAuth, and MFA.
- Persistence: Supabase Postgres via `@supabase/supabase-js` for user profiles, API keys, billing/credits, and settings.
- Observability: Self-hosted Langfuse v2 for LLM tracing and analytics.
- Providers: Ollama + Groq for chat, OpenAI-backed image generation, all behind a provider registry with circuit breaker and mock fallback where configured.
- Legacy Python MVP and in-house `PostgresStore` have been fully removed.

## Product Direction

- Primary audience: developers and small teams integrating inference into applications.
- Secondary audience: operators and end users using Hive's web workspace.
- Positioning: inference-platform core first, with local payment rails and prepaid credits as a market-entry advantage rather than the whole product story.
- Current strengths: routing, billing controls, provider safety boundaries, and OSS contributor hygiene.
- Current expansion gaps: broader provider catalog, richer analytics, file capabilities, and stronger admin/deployment tooling.

## Current Web Flow

- `/` is the primary chat workspace (auth-guarded).
- Unauthenticated users are redirected from `/` to `/auth`.
- `/auth` hosts login/signup (backed by Supabase Auth).
- `/chat` redirects to `/` (legacy compatibility).
- Chat workspace includes:
  - left conversation navigation
  - top-right avatar menu with `Settings`, `Developer Panel`, `Billing`, and `Log out`
- Developer Panel supports managed API key creation with nickname and optional expiry, current key status visibility, and lifecycle activity.

## Business Rules

- Base top-up conversion: `1 BDT = 100 AI Credits`
- Credit conversion implementation must stay decimal-safe for 2-decimal payment amounts such as `19.99 BDT -> 1999 credits`
- Refund conversion: `100 AI Credits = 0.9 BDT`
- Refund eligibility: unused purchased credits within 30 days
- Promo credits: non-refundable
- Consumed credits: non-refundable by default

## Repo Structure

- `apps/api` — Fastify API, domain logic, runtime integrations
- `apps/web` — Next.js app (chat-first workspace + developer panel + settings)
- `packages/openapi/openapi.yaml` — OpenAPI contract
- `supabase/migrations/` — Supabase database migrations
- `docs/` — architecture, runbooks, release checklists, planning docs

## API Endpoints

### Core AI

- `GET /health`
- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/images/generations`

### Billing and Usage

- `GET /v1/credits/balance`
- `GET /v1/usage`
- `POST /v1/payments/intents`
- `POST /v1/payments/demo/confirm` (demo-only top-up)
- `POST /v1/payments/webhook`

### Provider Health

- `GET /v1/providers/status` — public, sanitized availability
- `GET /v1/providers/status/internal` — admin-only with `x-admin-token`
- `GET /v1/providers/metrics` — public, provider-level request/error/latency summary
- `GET /v1/providers/metrics/internal` — admin-only JSON with provider diagnostics
- `GET /v1/providers/metrics/internal/prometheus` — admin-only Prometheus text scrape

The API also performs a zero-token startup readiness sweep for configured provider models. Missing or unreachable provider models are logged for operators and exposed only through internal status detail; public status remains sanitized.

### Auth

Authentication is handled by **Supabase Auth** for session-based web flows and by Hive API keys for developer-facing inference routes. Inference endpoints accept either `x-api-key` or `Authorization: Bearer <api-key>`, while the web's session-authenticated management routes validate Supabase bearer tokens through `SupabaseAuthService.getSessionPrincipal()`.

Session-authenticated developer key management endpoints:

- `GET /v1/users/me`
- `GET /v1/users/api-keys`
- `POST /v1/users/api-keys`
- `POST /v1/users/api-keys/{id}/revoke`

## Runtime Components

### API Runtime

| Component | File | Description |
|-----------|------|-------------|
| Env config | `src/config/env.ts` | Validates all env vars including Supabase and Langfuse |
| Auth service | `src/runtime/supabase-auth-service.ts` | Validates bearer tokens via Supabase Auth |
| User store | `src/runtime/supabase-user-store.ts` | User profiles and settings via Supabase |
| API key store | `src/runtime/supabase-api-key-store.ts` | Hashed API key persistence plus lifecycle audit events via Supabase |
| Billing store | `src/runtime/supabase-billing-store.ts` | Credits, ledger, and payment events via Supabase |
| Payment reconciliation | `src/runtime/payment-reconciliation.ts`, `src/runtime/payment-reconciliation-scheduler.ts` | Recent billing drift detection and opt-in scheduler |
| Authorization | `src/runtime/authorization.ts` | RBAC via Supabase `user_roles`/`role_permissions` tables |
| User settings | `src/runtime/user-settings.ts` | Feature gates (apiEnabled, generateImage, etc.) |
| Rate limiter | `src/runtime/redis-rate-limiter.ts` | Redis-based rate limiting |
| Service composition | `src/runtime/services.ts` | Wires all services together |

### Provider Clients

- `src/providers/ollama-client.ts` — Ollama adapter
- `src/providers/groq-client.ts` — Groq adapter
- `src/providers/openai-client.ts` — OpenAI-compatible hosted image adapter
- `src/providers/mock-client.ts` — Mock fallback adapter
- `src/providers/registry.ts` — Orchestration with circuit breaker and fallback

## Provider Routing

- `fast-chat` → primary `ollama`, fallback `groq`, then `mock`
- `smart-reasoning` → primary `groq`, fallback `ollama`, then `mock`
- `image-basic` → primary `openai`, fallback `mock`

Chat and image response headers: `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits`.

## Environment Variables

Use `.env.example` as the template. Key variables:

### Core
- `NODE_ENV`, `PORT`, `REDIS_URL`, `RATE_LIMIT_PER_MINUTE`
- `ADMIN_STATUS_TOKEN`, `ALLOW_DEMO_PAYMENT_CONFIRM`, `ALLOW_DEV_API_KEY_PREFIX`

### Supabase
- `SUPABASE_URL` — Supabase API endpoint (default: `http://127.0.0.1:54321`)
- `SUPABASE_SERVICE_ROLE_KEY` — Service role key for admin operations
- `SUPABASE_AUTH_ENABLED`, `SUPABASE_USER_REPO_ENABLED`, `SUPABASE_API_KEYS_ENABLED`, `SUPABASE_BILLING_STORE_ENABLED` — Feature flags (all `true` for production)
- `NEXT_PUBLIC_SUPABASE_URL`, `NEXT_PUBLIC_SUPABASE_ANON_KEY` — Required browser-side Supabase auth configuration for the web app
- `NEXT_PUBLIC_API_BASE_URL` — Required browser-side Hive API base URL for authenticated web requests

### Langfuse
- `LANGFUSE_ENABLED` — Enable LLM tracing (`true`)
- `LANGFUSE_BASE_URL` — Langfuse endpoint (default: `http://langfuse:3000` in Docker)
- `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY` — Project credentials

### Providers
- `OLLAMA_*` — local chat provider base URL, model, timeout, retries
- `GROQ_*` — hosted chat provider API key, base URL, model, timeout, retries
- `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_IMAGE_MODEL`, `OPENAI_TIMEOUT_MS`, `OPENAI_MAX_RETRIES` — hosted image provider configuration
- `PROVIDER_TIMEOUT_MS` (default `4000`), `PROVIDER_MAX_RETRIES` (default `1`)

### Payments
- `BKASH_WEBHOOK_SECRET`, `SSLCOMMERZ_WEBHOOK_SECRET`
- `PAYMENT_RECONCILIATION_ENABLED` (default `false`)
- `PAYMENT_RECONCILIATION_INTERVAL_MS` (default `3600000`)
- `PAYMENT_RECONCILIATION_LOOKBACK_HOURS` (default `24`)

Automated billing hardening:

- When enabled, the API runs a reconciliation scheduler that scans recent payment intents, verified payment events, and payment ledger entries.
- The scheduler runs an initial reconciliation immediately on start, then continues on the configured interval.
- Reconciliation treats `payment_intents.status` and `minted_credits` as insufficient by themselves; payment ledger evidence is also required.
- Lookback scans expand to all rows linked to affected `intent_id` values so boundary-adjacent events do not create false drift alerts.
- In multi-instance deployments, enable reconciliation on only one API instance until cross-instance coordination exists.
- Drift alerts are log-based and emitted only for actionable mismatches or scheduler failures.
- Operator workflow lives in `docs/runbooks/active/payments-reconciliation.md`.

## Docker Setup

Docker is used here to give Hive a reproducible local runtime for the parts that behave like deployed services:

- `api` runs the Fastify backend in a containerized environment close to production wiring
- `web` runs the built Next.js app as a separate HTTP service
- `redis` provides rate limiting
- `ollama` provides local inference
- `langfuse` and `langfuse-db` provide observability

The API and web are separate containers on purpose:

- they are separate deployable applications in the monorepo
- the web depends on the API over HTTP just like a real client would
- this catches environment, networking, and build/runtime mismatches that do not appear when everything is run as one in-process dev setup
- it keeps the boundary between browser code and server code explicit

Supabase is intentionally **not** defined in the Hive Compose file. In the current local architecture, Supabase is started separately by the Supabase CLI, which itself runs Supabase services as Docker containers. The API container reaches that stack over `host.docker.internal`.

The standardized path is `pnpm stack:dev`, which reads the live local Supabase keys and injects them for the Hive stack automatically.

The compose stack includes:

| Service | Port | Description |
|---------|------|-------------|
| `redis` | 6379 | Rate limiting |
| `ollama` | 11434 | Local LLM inference |
| `api` | 8080 | Hive API server |
| `web` | 3000 | Next.js frontend |
| `langfuse` | 3030 | LLM observability dashboard |
| `langfuse-db` | 5434 | Langfuse's dedicated Postgres |

> **Note:** Supabase runs as Docker containers too, but it is managed by the Supabase CLI instead of Hive's Compose file.

### Langfuse Dashboard

After starting the stack, access Langfuse at `http://localhost:3030`:
- Email: `admin@hive.local`
- Password: `admin123`

### Testing Chat

```bash
# With dev API key prefix enabled:
curl -i -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "content-type: application/json" \
  -H "x-api-key: dev-user-demo" \
  -d '{"model":"fast-chat","messages":[{"role":"user","content":"hello"}]}'
```

## Dev Commands

```bash
pnpm install                        # Install dependencies
pnpm stack:dev                     # Start full local stack with hot reload
pnpm stack:down                    # Stop the Hive dev stack
pnpm stack:reset                   # Stop Hive stack, remove Compose volumes, stop Supabase
pnpm --filter @hive/api test        # Run API tests (23 suites, 64 tests)
pnpm --filter @hive/api build       # TypeScript build check
pnpm --filter @hive/web build       # Web build
```

## Test Coverage

- **Domain tests**: Supabase stores, authorization, user settings, credits, payments, rate limiting, routing
- **Provider tests**: HTTP client, fallback, circuit breaker, registry, status
- **Route tests**: Auth principal resolution, payment webhooks, provider status, provider metrics, RBAC enforcement
- **E2E smoke**: Auth → chat → billing flow via Playwright

## Security Notes

- Do not expose `/v1/providers/status/internal` without `ADMIN_STATUS_TOKEN`
- Do not expose `/v1/providers/metrics/internal` or `/v1/providers/metrics/internal/prometheus` without `ADMIN_STATUS_TOKEN`
- Do not commit real API keys; rotate immediately on accidental exposure
- Public provider status intentionally omits internal error details
- Public provider status intentionally omits startup model readiness detail
- Public provider metrics intentionally omit provider diagnostic detail and raw circuit-breaker failure internals
- All Supabase tables use Row Level Security (RLS)

## Database Migrations

Supabase migrations are located in `supabase/migrations/`:
- `20260223000001_auth_user_tables.sql` — User profiles, roles, permissions, settings
- `20260223000002_api_keys.sql` — Hashed API key metadata
- `20260223000003_billing_tables.sql` — Credit accounts, ledger, payment intents/events
- `20260312_004_api_key_lifecycle.sql` — API key stable ids, nickname, expiration, and audit events
