# Hive

Hive is an AI inference platform with:
- OpenAI-compatible API endpoints for chat, responses, and model access
- provider routing across Ollama, OpenRouter, OpenAI, Groq, Gemini, and Anthropic, with mock fallback where configured
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

Hive local development runs two Docker-managed systems in conjunction:

- the **Hive app stack**, started by this repo's Docker Compose files
- the **Supabase local stack**, started by the Supabase CLI

Both will appear in `docker ps`, but they are managed by different tools and you need both for the full app to work.

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
4. starts the local `ollama` service and pulls the default `OLLAMA_MODEL` plus `OLLAMA_FREE_MODEL` when it differs

`pnpm stack:dev` is the canonical daily-development entry point. It:

1. starts or verifies the Supabase local stack
2. reads the live local Supabase keys from `npx supabase status -o env`
3. injects the required auth env vars for Hive
4. injects a local-only `WEB_INTERNAL_GUEST_TOKEN` for guest web-chat proxying
5. exports `OLLAMA_FREE_MODEL` for the local zero-cost guest route when you have not set one explicitly
6. starts the full Hive stack with hot reload for `api` and `web`

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
- `supabase/migrations/` is the live schema path used by the Supabase CLI for local bootstrap and CI resets
- `pnpm bootstrap:local` owns first-time local schema/bootstrap
- `pnpm stack:dev` is the standardized daily-development command because it handles both lifecycles together

The GitHub web smoke workflow follows the same rule: it starts the Supabase CLI stack, resets the local schema from repo migrations, pulls a small local Ollama model for `guest-free`, then starts the Hive Docker app stack on top of that state.

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

If localhost behavior does not match the current source tree or recent test results, rebuild the production-style Docker containers from the current working tree before debugging further:

```bash
docker compose up --build -d api web
docker compose ps
```

The `api` and `web` containers run compiled artifacts, so a stale container can keep serving old behavior even when local source and unit tests are already fixed.

For web auth/chat/billing smoke verification, use the rebuilt Docker-local stack on `http://127.0.0.1:3000` and the runbook at `docs/runbooks/active/web-e2e-smoke.md`. Do not treat standalone local app servers or alternate local ports as the normal verification path.

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
- Providers: Ollama plus hosted OpenRouter, OpenAI, Groq, Gemini, and Anthropic adapters, all behind a provider registry with circuit breaker and mock fallback where configured.
- Legacy Python MVP and in-house `PostgresStore` have been fully removed.

## Product Direction

- Primary audience: developers and small teams integrating inference into applications.
- Secondary audience: operators and end users using Hive's web workspace.
- Positioning: inference-platform core first, with local payment rails and prepaid credits as a market-entry advantage rather than the whole product story.
- Current strengths: routing, billing controls, provider safety boundaries, and OSS contributor hygiene.
- Current expansion gaps: broader provider catalog, richer analytics, file capabilities, and stronger admin/deployment tooling.

## Current Web Flow

- `/` is the primary chat workspace for both guests and authenticated users.
- Guests stay on `/` in guest mode and are limited to `costType: "free"` chat models.
- `guest-free` is a provider-backed zero-cost chat model. It routes to configured zero-cost offers only and fails closed if no healthy free offer is available.
- Guest and authenticated chat conversations are persisted server-side in `chat_sessions` and `chat_messages`. The sidebar and active transcript survive reloads, and guest-owned sessions are claimed for the user on sign-in/link.
- The model picker keeps paid chat models visible to guests, but renders them as locked with the reason `Requires account and credits`.
- If the guest model catalog load fails or returns only paid chat models, the web app fails closed instead of inventing a free model or selecting a locked paid model.
- Selecting a locked paid model opens a dismissible combined auth modal on `/`; dismissing it keeps the current guest conversation intact.
- Successful authentication from that modal closes it in place and immediately unlocks paid models on `/` without navigating away.
- The web app bootstraps a signed `httpOnly` guest cookie through `/api/guest-session` and mirrors a browser-visible guest session object for UI state and analytics.
- Guest chat and session history flow through same-origin Next.js routes (`/api/chat/guest` for legacy completion, `/api/chat/guest/sessions` for list/create/get/send); the browser never calls the API guest runtime directly.
- `/auth` hosts login/signup (backed by Supabase Auth).
- `/chat` redirects to `/` (legacy compatibility).
- Chat workspace includes:
  - left conversation navigation
  - top-right avatar menu with `Settings`, `Developer Panel`, `Billing`, and `Log out`
- Developer Panel supports managed API key creation with nickname and optional expiry, current key status visibility, lifecycle activity, and a windowed usage analytics summary.

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
- `GET /v1/usage` — raw usage events plus summary analytics by day, model, endpoint, channel (`api` vs `web`), and API key where applicable
- `GET /v1/analytics/internal` — admin-only traffic snapshot across API and web channels, including per-key API usage and guest conversion metrics
- `POST /v1/payments/intents`
- `POST /v1/payments/demo/confirm` (demo-only top-up)
- `POST /v1/payments/webhook`

### Provider Health

- `GET /v1/providers/status` — public, sanitized availability
- `GET /v1/providers/status/internal` — admin-only with `x-admin-token`
- `GET /v1/providers/metrics` — public, provider-level request/error/latency summary
- `GET /v1/providers/metrics/internal` — admin-only JSON with provider diagnostics
- `GET /v1/providers/metrics/internal/prometheus` — admin-only Prometheus text scrape
- `GET /v1/support/users/{userId}` — admin-only single-user troubleshooting snapshot with credits, usage, and API key state

The API also performs a zero-token startup readiness sweep for configured provider models. Missing or unreachable provider models are logged for operators and exposed only through internal status detail; public status remains sanitized.

### Auth

Authentication is handled by **Supabase Auth** for session-based web flows and by Hive API keys for developer-facing inference routes. The web home `/` supports guest chat through Next.js server routes in `apps/web`, which mint a signed guest cookie, mirror a browser-visible guest session object, and forward guest chat plus guest-to-user linking to internal API endpoints using a server-only token. Those web routes accept only same-origin browser traffic and forward the client IP so guest rate limiting still applies per visitor. The direct public inference endpoint `/v1/chat/completions` remains authenticated. Inference endpoints accept either `x-api-key` or `Authorization: Bearer <api-key>`, while the web's session-authenticated management routes validate Supabase bearer tokens through `SupabaseAuthService.getSessionPrincipal()`.

OpenAI-compatible behavior is the API product contract, not the web chat product contract. Reporting now distinguishes the two businesses explicitly:

- `api` traffic covers the OpenAI-compatible API surface and records the stable API key id when a key-backed call is used
- `web` traffic covers the chat product, including guest chat and session-authenticated web chat inferred from trusted browser-origin signals while the runtime is still shared

Issue `#19` now includes the guest-first conversion UX on `/`: guests can keep chatting on free models, see paid models as locked, and authenticate from an in-place modal to unlock them. Authenticated web chat still shares the public inference runtime path today; the deeper runtime split remains tracked separately in GitHub issue `#57`.

Model metadata now includes a policy-oriented `costType` (`free`, `fixed`, `variable`). Guest web chat can use only `free` chat models, while authenticated users can access credit-charging models.

Guest web chat and attribution proxying require a server-only `WEB_INTERNAL_GUEST_TOKEN` in both the web and API runtimes. `pnpm stack:dev` and the GitHub smoke workflow inject a local-only development token explicitly, but base Compose, staging, and production must set a real secret themselves. Without that token, guest chat is unavailable.

When the API runs inside Docker, its Ollama target must stay on the Docker service hostname `http://ollama:11434`; using `http://127.0.0.1:11434` inside the API container points back at the API container itself and leaves Ollama degraded.

Guest attribution is persisted in dedicated Supabase tables:

- `guest_sessions`
- `guest_usage_events`
- `guest_user_links`

The web auth/session sync links a validated `guestId` to the first authenticated user session through the internal guest-link route, which makes later signup and payment conversion analysis queryable through `guestId -> userId`.

Session-authenticated developer key management endpoints:

- `GET /v1/users/me`
- `GET /v1/users/api-keys`
- `POST /v1/users/api-keys`
- `POST /v1/users/api-keys/{id}/revoke`

Operator-only support endpoint:

- `GET /v1/support/users/{userId}` with `x-admin-token`

## Runtime Components

### API Runtime

| Component | File | Description |
|-----------|------|-------------|
| Env config | `src/config/env.ts` | Validates all env vars including Supabase and Langfuse |
| Auth service | `src/runtime/supabase-auth-service.ts` | Validates bearer tokens via Supabase Auth |
| User store | `src/runtime/supabase-user-store.ts` | User profiles and settings via Supabase |
| API key store | `src/runtime/supabase-api-key-store.ts` | Hashed API key persistence plus lifecycle audit events via Supabase |
| Billing store | `src/runtime/supabase-billing-store.ts` | Credits, ledger, and payment events via Supabase |
| Guest attribution store | `src/runtime/supabase-guest-attribution-store.ts` | Guest sessions, guest usage events, and guest-to-user conversion links via Supabase |
| Payment reconciliation | `src/runtime/payment-reconciliation.ts`, `src/runtime/payment-reconciliation-scheduler.ts` | Recent billing drift detection and opt-in scheduler |
| Authorization | `src/runtime/authorization.ts` | RBAC via Supabase `user_roles`/`role_permissions` tables |
| User settings | `src/runtime/user-settings.ts` | Feature gates (apiEnabled, generateImage, etc.) |
| Rate limiter | `src/runtime/redis-rate-limiter.ts` | Redis-based rate limiting |
| Service composition | `src/runtime/services.ts` | Wires all services together |

### Provider Clients

- `src/providers/ollama-client.ts` — Ollama adapter
- `src/providers/groq-client.ts` — Groq adapter
- `src/providers/openai-compatible-client.ts` — shared OpenAI-compatible hosted chat transport
- `src/providers/openrouter-client.ts` — OpenRouter chat adapter
- `src/providers/openai-client.ts` — OpenAI chat and image adapter
- `src/providers/gemini-client.ts` — Gemini OpenAI-compatible chat adapter
- `src/providers/anthropic-client.ts` — Anthropic native Messages adapter exposed through OpenAI-compatible API responses
- `src/providers/mock-client.ts` — Mock fallback adapter
- `src/providers/provider-offers.ts` — internal provider-offer catalog for virtual-model routing
- `src/providers/registry.ts` — Orchestration with circuit breaker and fallback

## Provider Routing

- Public model ids stay stable and OpenAI-compatible at the API boundary. Internal routing chooses provider offers behind those public ids.
- `guest-free` routes only to configured zero-cost offers from `ollama`, `openrouter`, `openai`, `groq`, `gemini`, and `anthropic`. It never falls through to paid providers and never consumes credits.
- `fast-chat` → primary `ollama`, fallback `groq`, then `mock`
- `smart-reasoning` → primary `groq`, fallback `ollama`, then `mock`
- `image-basic` → primary `openai`, fallback `mock`
- Explicit image requests honor the caller-selected image model id when one is supplied instead of silently rewriting them to the default image model.

Chat, responses, and image response headers: `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits`.

## Environment Variables

Use `.env.example` as the template. Key variables:

### Core
- `NODE_ENV`, `PORT`, `REDIS_URL`, `RATE_LIMIT_PER_MINUTE`
- `ADMIN_STATUS_TOKEN`, `ALLOW_DEMO_PAYMENT_CONFIRM`, `ALLOW_DEV_API_KEY_PREFIX`
- `WEB_INTERNAL_GUEST_TOKEN` — server-only shared secret for guest web chat and guest attribution proxying; `pnpm stack:dev` and the GitHub smoke workflow inject a local dev token, but base Compose, staging, and production must provide a real secret explicitly

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
- `OPENROUTER_API_KEY`, `OPENROUTER_BASE_URL`, `OPENROUTER_MODEL`, `OPENROUTER_FREE_MODEL`, `OPENROUTER_TIMEOUT_MS`, `OPENROUTER_MAX_RETRIES` — hosted OpenRouter chat configuration and optional zero-cost offer
- `GROQ_API_KEY`, `GROQ_BASE_URL`, `GROQ_MODEL`, `GROQ_FREE_MODEL`, `GROQ_TIMEOUT_MS`, `GROQ_MAX_RETRIES` — hosted Groq chat configuration and optional zero-cost offer
- `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_CHAT_MODEL`, `OPENAI_IMAGE_MODEL`, `OPENAI_FREE_MODEL`, `OPENAI_TIMEOUT_MS`, `OPENAI_MAX_RETRIES` — hosted OpenAI chat/image configuration and optional zero-cost offer
- `GEMINI_API_KEY`, `GEMINI_BASE_URL`, `GEMINI_MODEL`, `GEMINI_FREE_MODEL`, `GEMINI_TIMEOUT_MS`, `GEMINI_MAX_RETRIES` — hosted Gemini chat configuration and optional zero-cost offer
- `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`, `ANTHROPIC_FREE_MODEL`, `ANTHROPIC_TIMEOUT_MS`, `ANTHROPIC_MAX_RETRIES` — hosted Anthropic chat configuration and optional zero-cost offer
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
docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"   # API build (Docker only)
docker compose exec web sh -c "cd /app && pnpm --filter @hive/web build"   # Web build (Docker only)
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

The live Supabase CLI migration source of truth is `supabase/migrations/`:
- `20260223000001_auth_user_tables.sql` — User profiles, roles, permissions, settings
- `20260223000002_api_keys.sql` — Hashed API key metadata
- `20260223000003_billing_tables.sql` — Credit accounts, ledger, payment intents/events
- `20260223000004_billing_rpcs.sql` — Billing RPCs
- `20260313052000_refund_credits_rpc.sql` — Refund credits RPC
- `20260314000100_api_key_lifecycle.sql` — API key stable ids, nickname, expiration, and audit events
- `20260314000200_guest_attribution.sql` — Guest sessions, guest usage events, and guest-to-user links
- `20260315000000_chat_history.sql` — Chat sessions and messages for guest and authenticated web chat persistence
- `20260314000300_usage_reporting_channels.sql` — Usage event channel and stable API key attribution fields
