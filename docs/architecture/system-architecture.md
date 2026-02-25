# System Architecture

## Purpose

BD AI Gateway provides an API-first aggregation layer over multiple LLM providers with Bangladesh-focused billing and payment workflows.

## High-Level Components

1. API Service (`apps/api`)
   - Fastify HTTP server
   - OpenAI-compatible endpoints (`/v1/chat/completions`, `/v1/responses`, `/v1/images/generations`)
   - Billing and payment endpoints
   - Provider status endpoints

2. Web App (`apps/web`)
   - Next.js App Router UI
   - Chat and billing surfaces for quick operator/user testing

3. Data and Infra
   - PostgreSQL: source of truth for balances, ledger, usage, payment intents/events
   - Redis: rate limiting and short-window traffic control
   - Ollama: local model inference runtime

4. External Provider Integrations
   - Groq API for hosted inference
   - bKash/SSLCOMMERZ webhooks and optional provider-side verification

## Request Lifecycle (Chat)

1. Auth and scope check (`x-api-key`)
2. Redis rate-limit check
3. Model selection (`fast-chat`, `smart-reasoning`, etc.)
4. Credit debit attempt in Postgres
5. Provider registry execution with fallback chain
6. Usage event persisted
7. Response returned with routing headers:
   - `x-model-routed`
   - `x-provider-used`
   - `x-provider-model`
   - `x-actual-credits`

## Billing and Ledger Architecture

- Credits are tracked as application entitlements (not wallet cash balance)
- Conversion and refund policy:
  - top-up: `1 BDT = 100 AI Credits`
  - refund: `100 AI Credits = 0.9 BDT`
  - refundable only if unused purchased credits and within configured window
- Payment events are idempotent via provider transaction event keys
- Usage debits are tied to request IDs and endpoint/model metadata

## Provider Routing Architecture

Current provider mapping:

- `fast-chat` -> `ollama` -> `groq` -> `mock`
- `smart-reasoning` -> `groq` -> `ollama` -> `mock`
- `image-basic` -> `mock` (placeholder implementation)

Routing orchestration lives in:
- `apps/api/src/providers/registry.ts`
- `apps/api/src/runtime/services.ts`

## Provider Circuit Breaker

The Provider Registry implements a circuit breaker pattern to protect against cascading failures and reduce latency when a provider is repeatedly failing.

- **Thresholds**: Configurable via `PROVIDER_CB_THRESHOLD` (failure count) and `PROVIDER_CB_RESET_MS` (timeout).
- **States**: 
  - `CLOSED`: Normal operation, calls the provider.
  - `OPEN`: Provider is skipped for all requests until the reset timeout expires.
  - `HALF_OPEN`: Allows a single test request to check if the provider has recovered.
- **Observability**: Circuit state is exposed in `/v1/providers/status` (as `circuit-open` state) and in detail via `/v1/providers/status/internal`.

## Provider Status Endpoints

- Public status endpoint: `GET /v1/providers/status`
  - sanitized availability only
- Internal status endpoint: `GET /v1/providers/status/internal`
  - includes detailed provider diagnostics
  - protected with `x-admin-token`

## Security Boundaries

- Public endpoint never returns internal provider error details
- Internal provider diagnostics require `ADMIN_STATUS_TOKEN`
- Production should avoid default secrets (`BKASH_WEBHOOK_SECRET`, `SSLCOMMERZ_WEBHOOK_SECRET`)
- Never commit real API keys; rotate immediately on accidental exposure

## Operational Dependencies

The API requires reachable:
- Postgres (`POSTGRES_URL`)
- Redis (`REDIS_URL`)

Provider health and behavior depends on:
- Ollama availability and pulled model
- Groq API key validity and network reachability
