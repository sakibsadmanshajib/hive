# System Architecture

**Analysis Date:** 2026-03-16

## Overview

Hive is a pnpm monorepo containing a Fastify API backend (`apps/api`) and a Next.js web frontend (`apps/web`), supported by shared packages. The system provides AI chat completions, image generation, billing/credits, and user management.

## Layered Architecture (API)

The API follows a strict layered architecture with unidirectional dependencies:

```
Config  -->  Domain  -->  Providers  -->  Runtime  -->  Routes  -->  Server
```

### Layer 1: Config
- **File:** `apps/api/src/config/env.ts`
- Parses and validates all environment variables at startup.
- Returns a typed `AppEnv` object consumed by all other layers.
- No dependencies on other layers.

### Layer 2: Domain
- **Directory:** `apps/api/src/domain/`
- Pure business logic with no infrastructure dependencies.
- Key domain services:
  - `ai-service.ts` - `AiService` base class with chat, responses, and image generation methods.
  - `credit-service.ts` - `CreditService` for balance checks and consumption.
  - `credits-ledger.ts` - `CreditLedger` in-memory ledger for credit tracking.
  - `credits-conversion.ts` - `bdtToCredits()` BDT-to-credit conversion.
  - `model-service.ts` - `ModelService` for model catalog and routing.
  - `payment-service.ts` - `PaymentService` for intent lifecycle and webhook handling.
  - `usage-service.ts` - `UsageService` for usage tracking.
  - `rate-limiter.ts` - `InMemoryRateLimiter` sliding window implementation.
  - `refund-policy.ts` - Credit refund logic on usage tracking failure.
  - `routing-engine.ts` - Model-to-provider routing decisions.
  - `api-key-service.ts` - API key validation and management.
  - `webhook-signatures.ts` - HMAC-SHA256 signature verification for Bkash/SSLCommerz.
  - `types.ts` - Shared domain types (`CreditBalance`, `UsageChannel`, `PersistentApiKey`, `PersistentPaymentIntent`, etc.).
  - `services.ts` - Domain service interfaces/contracts.

### Layer 3: Providers
- **Directory:** `apps/api/src/providers/`
- External AI provider integrations (OpenRouter, Groq, OpenAI, Gemini, Anthropic, Ollama, Mock).
- `registry.ts` - `ProviderRegistry` manages client routing, circuit breaking, and failover.
- `circuit-breaker.ts` - Per-provider circuit breaker with configurable threshold/reset.
- `provider-offers.ts` - Dynamic offer catalog builder based on env configuration.
- `provider-metrics.ts` - Latency/status tracking per provider.
- `http-client.ts` - Shared HTTP client utilities.
- `openai-compatible-client.ts` - Base class for OpenAI-compatible API providers.
- `types.ts` - Provider-level types (`ProviderClient`, `ProviderName`, `ProviderChatMessage`, etc.).

### Layer 4: Runtime
- **Directory:** `apps/api/src/runtime/`
- Infrastructure adapters and service composition.
- **Service composition:** `services.ts` (`createRuntimeServices()`) is the central dependency injection point (~1130 lines). It instantiates all domain services, connects them to infrastructure adapters, and returns a `RuntimeServices` object.
- Key runtime components:
  - `supabase-client.ts` - Supabase admin client factory.
  - `supabase-auth-service.ts` - Supabase JWT verification.
  - `supabase-billing-store.ts` - Persistent credit/payment storage.
  - `supabase-api-key-store.ts` - Persistent API key storage.
  - `supabase-user-store.ts` - User profile persistence.
  - `supabase-chat-history-store.ts` - Chat session persistence.
  - `supabase-guest-attribution-store.ts` - Guest session tracking.
  - `redis-rate-limiter.ts` - Redis-backed rate limiter.
  - `langfuse.ts` - Langfuse tracing client.
  - `authorization.ts` - `AuthorizationService` RBAC permission matrix.
  - `user-settings.ts` - `UserSettingsService` per-user feature gates.
  - `chat-history-service.ts` - `PersistentChatHistoryService` with AI integration.
  - `payment-reconciliation.ts` - Payment reconciliation logic.
  - `payment-reconciliation-scheduler.ts` - Periodic reconciliation runner.
  - `provider-adapters.ts` - Adapters bridging providers to runtime.
  - `cors-origins.ts` - CORS origin validation.
  - `security.ts` - Cryptographic key generation.

### Layer 5: Routes
- **Directory:** `apps/api/src/routes/`
- Fastify route handlers. Each file exports a `register*Route()` function.
- Route registration is centralized in `apps/api/src/routes/index.ts`.
- Auth middleware: `apps/api/src/routes/auth.ts` exports `requirePrincipal()` and `requireApiPrincipal()`.
- Key routes:
  - `chat-completions.ts` - `/v1/chat/completions`
  - `responses.ts` - `/v1/responses`
  - `images-generations.ts` - `/v1/images/generations`
  - `models.ts` - `/v1/models`
  - `guest-chat.ts` - `/v1/internal/chat/guest`
  - `guest-attribution.ts` - `/v1/internal/guest/session`
  - `guest-chat-sessions.ts` - Guest chat session management
  - `chat-sessions.ts` - Authenticated chat session management
  - `payment-intents.ts` - `/v1/payments/intents`
  - `payment-webhook.ts` - `/v1/payments/webhook`
  - `payment-demo-confirm.ts` - `/v1/payments/demo/confirm`
  - `credits-balance.ts` - `/v1/credits/balance`
  - `usage.ts` - `/v1/usage`
  - `users.ts` - User management
  - `providers-status.ts` - `/v1/providers/status`
  - `providers-metrics.ts` - `/v1/providers/metrics`
  - `analytics.ts` - Usage analytics
  - `support.ts` - Support snapshot
  - `health.ts` - `/health`
  - `admin-auth.ts` - Admin authentication

### Layer 6: Server
- **File:** `apps/api/src/server.ts` - `createApp()` creates and configures the Fastify instance.
- **Entry point:** `apps/api/src/index.ts` - Calls `createApp()` and starts listening on port 8080.

## Service Composition (Dependency Injection)

The `createRuntimeServices()` function in `apps/api/src/runtime/services.ts` acts as the composition root:

1. Loads env config.
2. Creates Supabase admin client (if enabled).
3. Instantiates persistent stores (billing, API keys, users, guests, chat history).
4. Creates domain services (models, credits, usage, payments).
5. Builds provider registry with all AI provider clients.
6. Composes `RuntimeAiService` (extends `AiService`) with provider registry and Langfuse.
7. Creates auth, authorization, and user settings services.
8. Sets up rate limiter (Redis or in-memory fallback).
9. Starts payment reconciliation scheduler (if enabled).
10. Returns a `RuntimeServices` typed object consumed by all routes.

The `RuntimeServices` type exposes: `env`, `ai`, `models`, `credits`, `usage`, `payments`, `users`, `apiKeys`, `rateLimiter`, `auth`, `authorization`, `userSettings`, `guests`, `chatHistory`, `providerRegistry`, `langfuse`, `reconciliation`.

## Web Application Architecture

### Framework
- Next.js 15.1.1 with App Router (`apps/web/src/app/`).
- React 19.0.0 with client-side rendering for interactive features.

### Feature-Based Organization
Features are organized under `apps/web/src/features/`:
- `auth/` - Authentication (Supabase auth, Google login, guest sessions, auth modal).
- `chat/` - Chat interface (workspace shell, message composer, message list, conversation list, typing indicator, markdown rendering).
- `billing/` - Billing UI (billing shell, top-up panel, usage cards).
- `account/` - Account management (profile menu).
- `developer/` - Developer tools (developer shell).
- `settings/` - User settings (settings shell, user settings panel).

### Pages (App Router)
- `/` - Home page (`apps/web/src/app/page.tsx`)
- `/auth` - Auth page with layout (`apps/web/src/app/auth/`)
- `/chat` - Chat page with reducer (`apps/web/src/app/chat/`)
- `/billing` - Billing page
- `/developer` - Developer page
- `/settings` - Settings page

### Shared Components
- Layout: `apps/web/src/components/layout/` (app-header, app-shell, app-sidebar, theme-toggle)
- UI primitives: `apps/web/src/components/ui/` (Radix-based: button, card, input, select, sheet, etc.)
- Theme: `apps/web/src/components/theme/theme-provider.tsx`

### API Layer (Web)
- API client: `apps/web/src/lib/api.ts`
- Supabase client: `apps/web/src/lib/supabase-client.ts`
- Guest session proxy routes under `apps/web/src/app/api/guest-session/` and `apps/web/src/app/api/chat/guest/`

## Guest vs Authenticated Flows

### Guest Flow
1. Web app creates guest session via `/api/guest-session` route.
2. Guest ID stored client-side in `guest-session.ts`.
3. Chat requests proxied through Next.js API routes to backend `/v1/internal/chat/guest`.
4. Backend validates `x-web-guest-token` header and `x-guest-id`.
5. Guest-free offer policy restricts to `costClass: "zero"` models only.
6. Rate limiting applied per `guest:{id}:{ip}` key.

### Authenticated Flow
1. User authenticates via Supabase Auth (email/password or Google OAuth).
2. JWT token included in API requests as Bearer token.
3. `requirePrincipal()` in auth middleware resolves principal with userId, scopes, permissions.
4. RBAC enforced via `AuthorizationService` permission matrix.
5. Per-user feature gates checked via `UserSettingsService`.
6. Full model catalog available based on credit balance.

### Guest-to-User Transition
- Guest sessions can be linked to authenticated users via `/api/guest-session/link`.
- Chat history persists across the transition via `PersistentChatHistoryService`.

## Data Flow: Chat Completion Request

```
Client --> Next.js API Route (guest) or Direct API (auth)
  --> Fastify Route Handler (chat-completions.ts / guest-chat.ts)
    --> requirePrincipal() / guest token validation
    --> RuntimeAiService.chatCompletions() / guestChatCompletions()
      --> ModelService.resolve() (model lookup)
      --> CreditService.check() / consume() (billing)
      --> ProviderRegistry.chat() (AI provider call with circuit breaking)
      --> UsageService.add() (usage tracking)
      --> LangfuseClient.trace() (observability, non-blocking)
    --> Response with x-model-routed, x-actual-credits headers
```

## Infrastructure

- **Docker Compose:** `docker-compose.yml` orchestrates API, web, Redis, Langfuse, and Langfuse DB.
- **Dev overrides:** `docker-compose.dev.yml` for local development.
- **Supabase local:** `supabase/config.toml` for local Supabase CLI.
- **Dev scripts:** `tools/dev/bootstrap-local.sh`, `tools/dev/stack-dev.sh`, `tools/dev/stack-dev-latest.sh`.
