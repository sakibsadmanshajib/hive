# Hive

Hive is a Bangladesh-focused AI API gateway with:
- OpenAI-compatible API surface
- prepaid AI credits with local payment integration
- provider routing across Ollama, Groq, and mock fallback
- Supabase for auth, user data, API keys, and billing persistence
- self-hosted Langfuse for LLM observability
- Dockerized API + web + Redis + Ollama + Langfuse

## Start Here

- Full docs index: `docs/README.md`
- Agent operating guide: `AGENTS.md`
- Changelog: `CHANGELOG.md`
- Engineering standards: `docs/engineering/git-and-ai-practices.md`
- System architecture: `docs/architecture/system-architecture.md`

## Current Status

- Stack: TypeScript monorepo (API + web) is the only active runtime.
- Auth: Supabase Auth handles all user registration, login, OAuth, and MFA.
- Persistence: Supabase Postgres via `@supabase/supabase-js` for user profiles, API keys, billing/credits, and settings.
- Observability: Self-hosted Langfuse v2 for LLM tracing and analytics.
- Providers: Ollama + Groq behind a provider registry with circuit breaker and fallback to mock.
- Legacy Python MVP and in-house `PostgresStore` have been fully removed.

## Current Web Flow

- `/` is the primary chat workspace (auth-guarded).
- Unauthenticated users are redirected from `/` to `/auth`.
- `/auth` hosts login/signup (backed by Supabase Auth).
- `/chat` redirects to `/` (legacy compatibility).
- Chat workspace includes:
  - left conversation navigation
  - top-right avatar menu with `Settings`, `Developer Panel`, `Billing`, and `Log out`

## Business Rules

- Base top-up conversion: `1 BDT = 100 AI Credits`
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

### Auth

Authentication is fully handled by **Supabase Auth**. There are no custom auth endpoints in the Hive API. Users authenticate via Supabase's client SDKs (email/password, OAuth, MFA), and the API validates bearer tokens against Supabase using `SupabaseAuthService.getSessionPrincipal()`.

## Runtime Components

### API Runtime

| Component | File | Description |
|-----------|------|-------------|
| Env config | `src/config/env.ts` | Validates all env vars including Supabase and Langfuse |
| Auth service | `src/runtime/supabase-auth-service.ts` | Validates bearer tokens via Supabase Auth |
| User store | `src/runtime/supabase-user-store.ts` | User profiles and settings via Supabase |
| API key store | `src/runtime/supabase-api-key-store.ts` | Hashed API key persistence via Supabase |
| Billing store | `src/runtime/supabase-billing-store.ts` | Credits, ledger, and payment events via Supabase |
| Authorization | `src/runtime/authorization.ts` | RBAC via Supabase `user_roles`/`role_permissions` tables |
| User settings | `src/runtime/user-settings.ts` | Feature gates (apiEnabled, generateImage, etc.) |
| Rate limiter | `src/runtime/redis-rate-limiter.ts` | Redis-based rate limiting |
| Service composition | `src/runtime/services.ts` | Wires all services together |

### Provider Clients

- `src/providers/ollama-client.ts` — Ollama adapter
- `src/providers/groq-client.ts` — Groq adapter
- `src/providers/mock-client.ts` — Mock fallback adapter
- `src/providers/registry.ts` — Orchestration with circuit breaker and fallback

## Provider Routing

- `fast-chat` → primary `ollama`, fallback `groq`, then `mock`
- `smart-reasoning` → primary `groq`, fallback `ollama`, then `mock`
- `image-basic` → `mock` (placeholder)

Chat response headers: `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits`.

## Environment Variables

Use `.env.example` as the template. Key variables:

### Core
- `NODE_ENV`, `PORT`, `REDIS_URL`, `RATE_LIMIT_PER_MINUTE`
- `ADMIN_STATUS_TOKEN`, `ALLOW_DEMO_PAYMENT_CONFIRM`, `ALLOW_DEV_API_KEY_PREFIX`

### Supabase
- `SUPABASE_URL` — Supabase API endpoint (default: `http://127.0.0.1:54321`)
- `SUPABASE_SERVICE_ROLE_KEY` — Service role key for admin operations
- `SUPABASE_AUTH_ENABLED`, `SUPABASE_USER_REPO_ENABLED`, `SUPABASE_API_KEYS_ENABLED`, `SUPABASE_BILLING_STORE_ENABLED` — Feature flags (all `true` for production)

### Langfuse
- `LANGFUSE_ENABLED` — Enable LLM tracing (`true`)
- `LANGFUSE_BASE_URL` — Langfuse endpoint (default: `http://langfuse:3000` in Docker)
- `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY` — Project credentials

### Providers
- `OLLAMA_BASE_URL`, `OLLAMA_MODEL`
- `GROQ_API_KEY`, `GROQ_BASE_URL`, `GROQ_MODEL`
- `PROVIDER_TIMEOUT_MS` (default `4000`), `PROVIDER_MAX_RETRIES` (default `1`)

### Payments
- `BKASH_WEBHOOK_SECRET`, `SSLCOMMERZ_WEBHOOK_SECRET`

## Docker Setup

The compose stack includes:

| Service | Port | Description |
|---------|------|-------------|
| `redis` | 6379 | Rate limiting |
| `ollama` | 11434 | Local LLM inference |
| `api` | 8080 | Hive API server |
| `web` | 3000 | Next.js frontend |
| `langfuse` | 3030 | LLM observability dashboard |
| `langfuse-db` | 5434 | Langfuse's dedicated Postgres |

> **Note:** Supabase runs separately via the Supabase CLI (`npx supabase start`). The API container communicates with it over `host.docker.internal:54321`.

### Getting Started

```bash
# 1. Start Supabase locally
npx supabase start

# 2. Apply database migrations
npx supabase db reset

# 3. Start the Docker stack
docker compose up --build -d

# 4. Pull Ollama model
docker compose exec ollama ollama pull llama3.1:8b

# 5. Verify everything is running
docker compose ps
curl -s http://127.0.0.1:8080/v1/providers/status
```

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
pnpm --filter @hive/api test        # Run API tests (23 suites, 64 tests)
pnpm --filter @hive/api build       # TypeScript build check
pnpm --filter @hive/web build       # Web build
pnpm --filter @hive/api dev         # Run API locally
pnpm --filter @hive/web dev         # Run web locally
```

## Test Coverage

- **Domain tests**: Supabase stores, authorization, user settings, credits, payments, rate limiting, routing
- **Provider tests**: HTTP client, fallback, circuit breaker, registry, status
- **Route tests**: Auth principal resolution, payment webhooks, provider status, RBAC enforcement
- **E2E smoke**: Auth → chat → billing flow via Playwright

## Security Notes

- Do not expose `/v1/providers/status/internal` without `ADMIN_STATUS_TOKEN`
- Do not commit real API keys; rotate immediately on accidental exposure
- Public provider status intentionally omits internal error details
- All Supabase tables use Row Level Security (RLS)

## Database Migrations

Supabase migrations are located in `supabase/migrations/`:
- `20260223000001_auth_user_tables.sql` — User profiles, roles, permissions, settings
- `20260223000002_api_keys.sql` — Hashed API key metadata
- `20260223000003_billing_tables.sql` — Credit accounts, ledger, payment intents/events
