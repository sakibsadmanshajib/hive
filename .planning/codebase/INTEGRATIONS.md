# External Integrations

**Analysis Date:** 2026-03-16

## AI / LLM Providers

Six provider clients live under `apps/api/src/providers/`, each implementing the `ProviderClient` interface from `apps/api/src/providers/types.ts`. They are wired together by `ProviderRegistry` (`apps/api/src/providers/registry.ts`).

| Provider | Client File | Base URL Env | Model Env | Default Model |
|-----------|-------------|-------------|-----------|---------------|
| OpenRouter | `openrouter-client.ts` | `OPENROUTER_BASE_URL` | `OPENROUTER_MODEL` | `openrouter/auto` |
| Groq | `groq-client.ts` | `GROQ_BASE_URL` | `GROQ_MODEL` | `llama-3.1-8b-instant` |
| OpenAI | `openai-client.ts` | `OPENAI_BASE_URL` | `OPENAI_CHAT_MODEL` / `OPENAI_IMAGE_MODEL` | `gpt-4o-mini` / `gpt-image-1` |
| Gemini | `gemini-client.ts` | `GEMINI_BASE_URL` | `GEMINI_MODEL` | `gemini-3-flash-preview` |
| Anthropic | `anthropic-client.ts` | `ANTHROPIC_BASE_URL` | `ANTHROPIC_MODEL` | `claude-sonnet-4-20250514` |
| Ollama | `ollama-client.ts` | `OLLAMA_BASE_URL` | `OLLAMA_MODEL` | `llama3.1:8b` |

- A `MockProviderClient` (`mock-client.ts`) exists for testing.
- All cloud providers accept an API key via `<PROVIDER>_API_KEY` env var.
- Each provider supports `timeoutMs` and `maxRetries` configuration.
- Every provider may expose a `freeModel` variant (e.g. `OPENROUTER_FREE_MODEL`) used for the `guest-free` offer policy.
- Common HTTP layer: `apps/api/src/providers/http-client.ts` and OpenAI-compatible base class `openai-compatible-client.ts`.

**Provider Resilience:**
- Circuit breaker per provider (`apps/api/src/providers/circuit-breaker.ts`), configured via `PROVIDER_CB_THRESHOLD` and `PROVIDER_CB_RESET_MS`.
- Fallback ordering defined in `apps/api/src/runtime/services.ts`.
- Provider offer catalog built dynamically by `apps/api/src/providers/provider-offers.ts` (`buildProviderOfferCatalog()`).

**Provider Metrics:**
- `apps/api/src/providers/provider-metrics.ts` tracks latency and status per provider.
- Exposed via routes `apps/api/src/routes/providers-metrics.ts` and `apps/api/src/routes/providers-status.ts`.

## Data Storage

### PostgreSQL
- Primary relational store via Supabase-hosted PostgreSQL.
- Connection string: `POSTGRES_URL` env var.
- Driver: `pg` 8.13.1.
- Migrations managed under `supabase/migrations/` (10 migration files covering auth, API keys, billing, chat history, guest attribution).

### Supabase
- `@supabase/supabase-js` 2.57.4 used in both API and web.
- Admin client created in `apps/api/src/runtime/supabase-client.ts` (`createSupabaseAdminClient()`).
- Persistent stores backed by Supabase:
  - `apps/api/src/runtime/supabase-api-key-store.ts` - API key CRUD
  - `apps/api/src/runtime/supabase-auth-service.ts` - Auth verification
  - `apps/api/src/runtime/supabase-billing-store.ts` - Credits, payment intents, payment events
  - `apps/api/src/runtime/supabase-chat-history-store.ts` - Chat session persistence
  - `apps/api/src/runtime/supabase-guest-attribution-store.ts` - Guest session tracking
  - `apps/api/src/runtime/supabase-user-store.ts` - User profiles and settings
- Feature flags gate Supabase usage: `SUPABASE_AUTH_ENABLED`, `SUPABASE_USER_REPO_ENABLED`, `SUPABASE_API_KEYS_ENABLED`, `SUPABASE_BILLING_STORE_ENABLED`.
- Web app SSR integration via `@supabase/ssr` 0.9.0.

### Redis
- `ioredis` 5.4.2 for rate limiting.
- Connection string: `REDIS_URL` env var.
- Used in `apps/api/src/runtime/redis-rate-limiter.ts` (`RedisRateLimiter`).
- Falls back to in-memory rate limiter (`apps/api/src/domain/rate-limiter.ts`) when Redis is unavailable.

## Authentication

### Supabase Auth
- Server-side token verification in `apps/api/src/runtime/supabase-auth-service.ts`.
- Web client uses `apps/web/src/lib/supabase-client.ts` with `@supabase/ssr`.
- Session sync hook: `useSupabaseAuthSessionSync()` in web app.
- Auth session management: `apps/web/src/features/auth/auth-session.ts`.

### Google OAuth
- Configured via `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` env vars.
- Login button component: `apps/web/src/features/auth/google-login-button.tsx`.
- Auth experience component: `apps/web/src/features/auth/components/auth-experience.tsx`.
- Dedicated auth page: `apps/web/src/app/auth/page.tsx`.

### Guest Tokens
- Internal guest token: `WEB_INTERNAL_GUEST_TOKEN` env var shared between web and API.
- Guest session creation: `apps/web/src/app/api/guest-session/route.ts`.
- Guest identity passed via `x-guest-id` and `x-web-guest-token` headers.
- Guest session files: `apps/web/src/features/auth/guest-session.ts`.
- Guest chat route: `apps/api/src/routes/guest-chat.ts`.
- Guest attribution tracking: `apps/api/src/routes/guest-attribution.ts`.
- Guest-to-user linking: `apps/web/src/app/api/guest-session/link/route.ts`.

### API Keys
- Service: `apps/api/src/domain/api-key-service.ts`.
- Persistent store: `apps/api/src/runtime/supabase-api-key-store.ts`.
- Secure key generation: `apps/api/src/runtime/security.ts` (`createApiKey()`).
- Auth principal resolution in `apps/api/src/routes/auth.ts` (`requirePrincipal()`).

### RBAC & Authorization
- Permission matrix: `apps/api/src/runtime/authorization.ts` (`AuthorizationService`).
- User settings gates: `apps/api/src/runtime/user-settings.ts` (`UserSettingsService`).
- Scope-to-permission mapping via `mapScopeToPrimaryPermission()`.

## Payments

### Bkash
- Webhook signature verification: `verifyBkashSignature()` in `apps/api/src/domain/webhook-signatures.ts`.
- Uses HMAC-SHA256 with `X-BKash-Signature` and `X-BKash-Timestamp` headers.
- Secret configured via `BKASH_WEBHOOK_SECRET` env var.

### SSLCommerz
- Webhook signature verification: `verifySslcommerzSignature()` in `apps/api/src/domain/webhook-signatures.ts`.
- Canonical key-value pair signing with HMAC-SHA256.
- Secret configured via `SSLCOMMERZ_WEBHOOK_SECRET` env var.

### Payment Infrastructure
- Payment service: `apps/api/src/domain/payment-service.ts` (`PaymentService`).
- Payment intent creation route: `apps/api/src/routes/payment-intents.ts`.
- Webhook route: `apps/api/src/routes/payment-webhook.ts`.
- Demo payment confirmation: `apps/api/src/routes/payment-demo-confirm.ts` (gated by `ALLOW_DEMO_PAYMENT_CONFIRM`).
- Credit conversion: `apps/api/src/domain/credits-conversion.ts` (`bdtToCredits()`).
- Payment reconciliation: `apps/api/src/runtime/payment-reconciliation.ts` and scheduler in `apps/api/src/runtime/payment-reconciliation-scheduler.ts`.
- Reconciliation config: `PAYMENT_RECONCILIATION_ENABLED`, `PAYMENT_RECONCILIATION_INTERVAL_MS`, `PAYMENT_RECONCILIATION_LOOKBACK_HOURS`.

## Observability

### Langfuse
- Client: `apps/api/src/runtime/langfuse.ts` (`LangfuseClient`).
- Sends trace events to Langfuse ingestion API (`/api/public/ingestion`) using Basic auth.
- Configured via `LANGFUSE_ENABLED`, `LANGFUSE_BASE_URL`, `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY`.
- Traces include: userId, model, provider, endpoint, credits, promptPreview.
- Non-blocking: errors silently caught (telemetry must not break requests).
- Docker Compose runs Langfuse 2 with its own PostgreSQL instance on port 3030.

### Prometheus
- `prom-client` 15.1.3 available as a dependency.
- Provider metrics exposed via `apps/api/src/providers/provider-metrics.ts`.
- Route: `apps/api/src/routes/providers-metrics.ts`.
- Admin access gated by `ADMIN_STATUS_TOKEN`.

## Configuration Reference

All environment configuration is centralized in `apps/api/src/config/env.ts`, using helper functions:
- `required(key, default)` - required with fallback
- `optional(key)` - optional, returns undefined if missing
- `parseBoolean(key, default)` - boolean flags
- `parsePositiveInteger(key, default)` - positive integer values
- `parseNonNegativeInteger(key, default)` - non-negative integer values
