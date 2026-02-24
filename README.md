# BD AI Gateway

Bangladesh-focused AI API gateway with:
- OpenAI-compatible API surface
- prepaid AI credits
- local payment intent + verified webhook flow
- provider routing across Ollama, Groq, and mock fallback
- Dockerized API + web + Postgres + Redis + Ollama

This document captures the current implementation state so you can continue in a new chat with full context.

## Start Here

- Full docs index: `docs/README.md`
- Agent operating guide: `AGENTS.md`
- Engineering standards (Git + AI): `docs/engineering/git-and-ai-practices.md`

## Current Status

- Stack: TypeScript monorepo (API + web) is the active implementation.
- Runtime: production-style API wiring uses Postgres + Redis + provider clients.
- Providers: Ollama + Groq integrated behind a provider registry with fallback to mock.
- Public provider health endpoint exists.
- Internal provider diagnostics endpoint exists and is admin-token protected.
- Python MVP still exists in repo as a legacy reference implementation.

## Business Rules Implemented

- Base top-up conversion: `1 BDT = 100 AI Credits`
- Refund conversion: `100 AI Credits = 0.9 BDT`
- Refund eligibility: unused purchased credits within 30 days
- Promo credits: non-refundable
- Consumed credits: non-refundable by default

## Repo Structure

- `apps/api` - Fastify API, domain logic, runtime integrations
- `apps/web` - Next.js app (chat + billing pages)
- `packages/openapi/openapi.yaml` - OpenAPI contract for TS stack
- `app/` + `tests/` - legacy Python MVP and tests
- `docs/` - runbooks, release checklists, planning docs

## Implemented API Endpoints

### Core API

- `GET /health`
- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/images/generations`

### User Management

- `POST /v1/users/register`
- `POST /v1/users/login`
- `GET /v1/users/me`
- `POST /v1/users/api-keys`
- `DELETE /v1/users/api-keys`
- `GET /v1/users/settings`
- `PATCH /v1/users/settings`

### Auth and Security

- `GET /v1/auth/google/start`
- `GET /v1/auth/google/callback`
- `POST /v1/auth/logout`
- `GET /v1/auth/session`
- `POST /v1/auth/2fa/enroll/start`
- `POST /v1/auth/2fa/enroll/verify`
- `POST /v1/auth/2fa/challenge/start`
- `POST /v1/auth/2fa/challenge/verify`

### Billing and Usage

- `GET /v1/credits/balance`
- `GET /v1/usage`
- `POST /v1/payments/intents`
- `POST /v1/payments/demo/confirm` (demo-only top-up confirmation)
- `POST /v1/payments/webhook`

### Provider Health

- `GET /v1/providers/status` (public, sanitized)
  - returns: `name`, `enabled`, `healthy`, `state`
- `GET /v1/providers/status/internal` (admin-only)
  - requires header: `x-admin-token: <ADMIN_STATUS_TOKEN>`
  - returns provider `detail` fields for diagnostics

## Routing and Provider Behavior

Model mapping currently:
- `fast-chat` -> primary `ollama`, fallback `groq`, then `mock`
- `smart-reasoning` -> primary `groq`, fallback `ollama`, then `mock`
- `image-basic` -> `mock` (placeholder path)

Chat response headers include:
- `x-model-routed`
- `x-provider-used`
- `x-provider-model`
- `x-actual-credits`

## Runtime Components

### API Runtime

- Env loading/validation: `apps/api/src/config/env.ts`
- Postgres persistence: `apps/api/src/runtime/postgres-store.ts`
- Redis rate limit: `apps/api/src/runtime/redis-rate-limiter.ts`
- Payment provider adapters: `apps/api/src/runtime/provider-adapters.ts`
- Runtime service composition: `apps/api/src/runtime/services.ts`

### Provider Clients

- `apps/api/src/providers/ollama-client.ts`
- `apps/api/src/providers/groq-client.ts`
- `apps/api/src/providers/mock-client.ts`
- `apps/api/src/providers/registry.ts`

Inference providers follow an adapter design pattern:
- shared adapter contract: `ProviderClient` (`apps/api/src/providers/types.ts`)
- concrete adapters: `OllamaProviderClient`, `GroqProviderClient`, `MockProviderClient`
- orchestration/fallback via `ProviderRegistry`

## Environment Variables

Use `.env.example` as the template.

Important variables:
- `NODE_ENV`
- `PORT`
- `POSTGRES_URL`
- `REDIS_URL`
- `RATE_LIMIT_PER_MINUTE`
- `ADMIN_STATUS_TOKEN`
- `BKASH_WEBHOOK_SECRET`
- `SSLCOMMERZ_WEBHOOK_SECRET`
- `OLLAMA_BASE_URL`
- `OLLAMA_MODEL`
- `GROQ_API_KEY`
- `GROQ_BASE_URL`
- `GROQ_MODEL`
- `ALLOW_DEMO_PAYMENT_CONFIRM`
- `ALLOW_DEV_API_KEY_PREFIX`
- `GOOGLE_CLIENT_ID`
- `GOOGLE_CLIENT_SECRET`
- `GOOGLE_REDIRECT_URI`
- `AUTH_SESSION_TTL_MINUTES`
- `ENFORCE_2FA_FOR_SENSITIVE_ACTIONS`
- `LANGFUSE_ENABLED`
- `LANGFUSE_BASE_URL`
- `LANGFUSE_PUBLIC_KEY`
- `LANGFUSE_SECRET_KEY`

Provider optional verification settings are also available in `.env.example`.

## Docker Setup

The compose stack includes:
- `postgres` (`:5432`)
- `redis` (`:6379`)
- `ollama` (`:11434`)
- `api` (`:8080`)
- `web` (`:3000`)

Start stack:

```bash
docker compose up --build -d
```

Check status:

```bash
docker compose ps
```

## Ollama and Groq API-First Testing

1. Pull model into Ollama container:

```bash
docker compose exec ollama ollama pull llama3.1:8b
```

2. Set `GROQ_API_KEY` in your shell or `.env`.

3. Recreate stack if env changed:

```bash
docker compose up -d --build
```

4. Check provider readiness:

```bash
curl -s http://127.0.0.1:8080/v1/providers/status
```

5. Internal provider diagnostics:

```bash
curl -s http://127.0.0.1:8080/v1/providers/status/internal \
  -H "x-admin-token: <ADMIN_STATUS_TOKEN>"
```

6. Create a user and get API key:

```bash
curl -s -X POST http://127.0.0.1:8080/v1/users/register \
  -H "content-type: application/json" \
  -d '{"email":"demo@example.com","password":"password123","name":"Demo"}'
```

7. Test chat routing (replace `<API_KEY>`):

```bash
curl -i -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "content-type: application/json" \
  -H "x-api-key: <API_KEY>" \
  -d '{"model":"fast-chat","messages":[{"role":"user","content":"hello"}]}'
```

Inspect headers to confirm provider path.

## Dev Commands

Install dependencies:

```bash
pnpm install
```

API tests:

```bash
pnpm --filter @bd-ai-gateway/api test
```

API build:

```bash
pnpm --filter @bd-ai-gateway/api build
```

Web build:

```bash
pnpm --filter @bd-ai-gateway/web build
```

Run API locally:

```bash
pnpm --filter @bd-ai-gateway/api dev
```

Run web locally:

```bash
pnpm --filter @bd-ai-gateway/web dev
```

## Tests Currently Present

- Domain tests under `apps/api/test/domain/*`
- Provider tests under `apps/api/test/providers/*`
- Route-level test for provider status endpoints:
  - `apps/api/test/routes/providers-status-route.test.ts`

## Security and Operations Notes

- Do not expose `/v1/providers/status/internal` without an `ADMIN_STATUS_TOKEN`.
- Do not commit real API keys.
- If a key is accidentally shared, rotate/revoke immediately.
- Public provider status intentionally avoids detailed internal error strings.

## What Is Done vs. Next

Done:
- Stable Dockerized platform for API-first provider testing
- Provider routing + fallback + status visibility
- Local-payment top-up and credit accounting paths
- User registration/login with persistent API keys
- ChatGPT-like multi-conversation demo UI and billing dashboard
- Optional Langfuse tracing hooks in runtime

Likely next engineering steps:
- Replace placeholder/mock image pipeline with real image providers
- Add migrations/versioned schema management (instead of bootstrap SQL in code)
- Add observability dashboards and alerts for provider failures
