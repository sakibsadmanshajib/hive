# Directory Structure

**Analysis Date:** 2026-03-16

## Monorepo Layout

```
hive/                              # pnpm monorepo root
├── apps/
│   ├── api/                       # Fastify backend API
│   └── web/                       # Next.js frontend
├── packages/
│   ├── openapi/                   # OpenAPI specification
│   └── shared/                    # Shared TypeScript utilities
├── supabase/                      # Supabase configuration and migrations
├── docs/                          # Project documentation
├── tools/                         # Development and CI scripts
└── .planning/                     # Codebase analysis documents
```

**Workspace config:** `pnpm-workspace.yaml` defines `apps/*` and `packages/*` as workspace members.

## apps/api (Fastify Backend)

```
apps/api/
├── src/
│   ├── index.ts                   # Entry point: creates app and listens on port 8080
│   ├── server.ts                  # createApp(): Fastify instance setup and route registration
│   ├── config/
│   │   └── env.ts                 # Environment variable parsing and validation (AppEnv type)
│   ├── domain/                    # Pure business logic (no infra dependencies)
│   │   ├── ai-service.ts          # AiService base class (chat, responses, image gen)
│   │   ├── api-key-service.ts     # API key validation and management
│   │   ├── credit-service.ts      # Credit balance checks and consumption
│   │   ├── credits-conversion.ts  # bdtToCredits() currency conversion
│   │   ├── credits-ledger.ts      # In-memory credit ledger
│   │   ├── model-service.ts       # Model catalog and routing
│   │   ├── payment-service.ts     # Payment intent lifecycle and webhooks
│   │   ├── rate-limiter.ts        # InMemoryRateLimiter (sliding window)
│   │   ├── refund-policy.ts       # Credit refund on usage failure
│   │   ├── routing-engine.ts      # Model-to-provider routing
│   │   ├── services.ts            # Domain service interfaces
│   │   ├── types.ts               # Shared domain types
│   │   ├── usage-service.ts       # Usage tracking
│   │   └── webhook-signatures.ts  # HMAC-SHA256 signature verification
│   ├── providers/                 # AI provider client implementations
│   │   ├── anthropic-client.ts    # Anthropic API client
│   │   ├── gemini-client.ts       # Google Gemini API client
│   │   ├── groq-client.ts         # Groq API client
│   │   ├── ollama-client.ts       # Ollama local client
│   │   ├── openai-client.ts       # OpenAI API client
│   │   ├── openai-compatible-client.ts  # Base class for OpenAI-compatible APIs
│   │   ├── openrouter-client.ts   # OpenRouter API client
│   │   ├── mock-client.ts         # Mock provider for testing
│   │   ├── circuit-breaker.ts     # Per-provider circuit breaker
│   │   ├── http-client.ts         # Shared HTTP client utilities
│   │   ├── provider-metrics.ts    # Latency/status tracking
│   │   ├── provider-offers.ts     # Dynamic offer catalog builder
│   │   ├── registry.ts            # ProviderRegistry: routing, failover, circuit breaking
│   │   └── types.ts               # Provider-level type definitions
│   ├── runtime/                   # Infrastructure adapters and composition
│   │   ├── services.ts            # createRuntimeServices() - DI composition root (~1130 lines)
│   │   ├── authorization.ts       # AuthorizationService RBAC
│   │   ├── chat-history-service.ts # PersistentChatHistoryService
│   │   ├── cors-origins.ts        # CORS origin validation
│   │   ├── langfuse.ts            # LangfuseClient for tracing
│   │   ├── payment-reconciliation.ts          # Payment reconciliation logic
│   │   ├── payment-reconciliation-scheduler.ts # Periodic reconciliation
│   │   ├── provider-adapters.ts   # Provider-to-runtime adapters
│   │   ├── redis-rate-limiter.ts  # RedisRateLimiter adapter
│   │   ├── security.ts            # Cryptographic key generation
│   │   ├── supabase-client.ts     # Supabase admin client factory
│   │   ├── supabase-auth-service.ts          # Supabase JWT verification
│   │   ├── supabase-api-key-store.ts         # API key persistence
│   │   ├── supabase-billing-store.ts         # Credits/payment persistence
│   │   ├── supabase-chat-history-store.ts    # Chat session persistence
│   │   ├── supabase-guest-attribution-store.ts # Guest session tracking
│   │   ├── supabase-user-store.ts            # User profile persistence
│   │   └── user-settings.ts       # UserSettingsService feature gates
│   └── routes/                    # Fastify route handlers
│       ├── index.ts               # Central route registration
│       ├── auth.ts                # requirePrincipal() auth middleware
│       ├── admin-auth.ts          # Admin authentication
│       ├── analytics.ts           # Usage analytics endpoint
│       ├── chat-completions.ts    # /v1/chat/completions
│       ├── chat-sessions.ts       # Authenticated chat session CRUD
│       ├── credits-balance.ts     # /v1/credits/balance
│       ├── guest-attribution.ts   # /v1/internal/guest/session
│       ├── guest-chat.ts          # /v1/internal/chat/guest
│       ├── guest-chat-sessions.ts # Guest chat session management
│       ├── health.ts              # /health
│       ├── images-generations.ts  # /v1/images/generations
│       ├── models.ts              # /v1/models
│       ├── payment-demo-confirm.ts # /v1/payments/demo/confirm
│       ├── payment-intents.ts     # /v1/payments/intents
│       ├── payment-webhook.ts     # /v1/payments/webhook
│       ├── providers-metrics.ts   # /v1/providers/metrics
│       ├── providers-status.ts    # /v1/providers/status
│       ├── responses.ts           # /v1/responses
│       ├── support.ts             # Support snapshot
│       ├── usage.ts               # /v1/usage
│       └── users.ts               # User management
├── test/                          # Test files (mirrors src/ structure)
│   ├── domain/                    # Domain unit tests
│   ├── providers/                 # Provider client tests
│   └── routes/                    # Route handler tests
├── dist/                          # Compiled JavaScript output
├── supabase/                      # Local Supabase config
├── Dockerfile                     # API container build
├── package.json                   # API dependencies and scripts
└── tsconfig.json                  # TypeScript configuration
```

## apps/web (Next.js Frontend)

```
apps/web/
├── src/
│   ├── app/                       # Next.js App Router pages
│   │   ├── page.tsx               # Home page
│   │   ├── layout.tsx             # Root layout
│   │   ├── auth/                  # Auth page (page.tsx, layout.tsx)
│   │   ├── chat/                  # Chat page (page.tsx, chat-reducer.ts, chat-types.ts)
│   │   ├── billing/               # Billing page (page.tsx)
│   │   ├── developer/             # Developer tools page (page.tsx)
│   │   ├── settings/              # Settings page (page.tsx)
│   │   └── api/                   # Next.js API routes (guest proxies)
│   │       ├── chat/guest/        # Guest chat proxy routes
│   │       └── guest-session/     # Guest session management
│   ├── features/                  # Feature-based modules
│   │   ├── auth/                  # Auth feature
│   │   │   ├── auth-session.ts    # Auth session state management
│   │   │   ├── guest-session.ts   # Guest session utilities
│   │   │   ├── google-login-button.tsx
│   │   │   └── components/        # auth-experience.tsx, auth-modal.tsx
│   │   ├── chat/                  # Chat feature
│   │   │   ├── use-chat-session.ts # Chat session hook
│   │   │   ├── components/        # chat-shell, workspace-shell, message-*, conversation-list, etc.
│   │   │   └── hooks/             # use-chat-shortcuts.ts
│   │   ├── billing/               # Billing feature
│   │   │   └── components/        # billing-shell, topup-panel, usage-cards
│   │   ├── account/               # Account feature
│   │   │   └── components/        # profile-menu.tsx
│   │   ├── developer/             # Developer feature
│   │   │   └── components/        # developer-shell.tsx
│   │   └── settings/              # Settings feature
│   │       ├── user-settings-panel.tsx
│   │       └── components/        # settings-shell.tsx
│   ├── components/                # Shared components
│   │   ├── layout/                # app-header, app-shell, app-sidebar, theme-toggle
│   │   ├── ui/                    # Radix-based primitives (button, card, input, select, etc.)
│   │   └── theme/                 # theme-provider.tsx
│   └── lib/                       # Shared utilities
│       ├── api.ts                 # API client
│       ├── supabase-client.ts     # Supabase browser client
│       └── utils.ts               # General utilities
├── e2e/                           # Playwright E2E tests
│   ├── fixtures/auth.ts           # Auth test fixtures
│   └── smoke-auth-chat-billing.spec.ts  # Main smoke test
├── test/                          # Vitest unit tests
│   ├── __mocks__/                 # Mock modules (select-mock.tsx)
│   ├── app-shell.test.tsx
│   ├── auth-page.test.tsx
│   ├── auth-session.test.ts
│   ├── billing-page.test.tsx
│   ├── chat-*.test.ts(x)         # Chat-related tests
│   ├── google-login-ui.test.tsx
│   ├── guest-*.test.ts            # Guest flow tests
│   └── ...
├── playwright.config.ts           # Playwright configuration
├── vitest.config.ts               # Vitest configuration
├── Dockerfile                     # Web container build
├── next.config.mjs                # Next.js configuration
├── tailwind.config.ts             # Tailwind CSS configuration
├── package.json                   # Web dependencies and scripts
└── tsconfig.json                  # TypeScript configuration
```

## packages/

```
packages/
├── openapi/
│   └── openapi.yaml               # OpenAPI specification
└── shared/
    ├── src/                       # Shared TypeScript source
    ├── package.json
    └── tsconfig.json
```

## supabase/ (Root)

```
supabase/
├── config.toml                    # Local Supabase CLI configuration
├── migrations/                    # Database migrations (chronologically ordered)
│   ├── 20260223000001_auth_user_tables.sql
│   ├── 20260223000002_api_keys.sql
│   ├── 20260223000003_billing_tables.sql
│   ├── 20260223000004_billing_rpcs.sql
│   ├── 20260313052000_refund_credits_rpc.sql
│   ├── 20260314000100_api_key_lifecycle.sql
│   ├── 20260314000200_guest_attribution.sql
│   ├── 20260314000300_usage_reporting_channels.sql
│   ├── 20260315000000_chat_history.sql
│   └── 20260315000100_auth_user_sync_trigger.sql
└── snippets/                      # SQL snippet utilities
```

## docs/

```
docs/
├── architecture/                  # System architecture docs
├── audits/                        # Code and platform audits
├── design/                        # Feature design docs (active/, archive/)
├── engineering/                   # Engineering practices
├── plans/                         # Implementation plans (active/, archive/, completed/)
├── release/                       # Release documentation
└── runbooks/                      # Operational runbooks (active/, archive/)
```

## tools/

```
tools/
├── dev/                           # Development scripts
│   ├── bootstrap-local.sh         # Local environment setup
│   ├── stack-dev.sh               # Start dev stack
│   └── stack-dev-latest.sh        # Start dev stack with latest images
└── github/                        # GitHub management
    ├── labels.json                # Issue label definitions
    ├── milestones.json            # Milestone definitions
    └── sync-github-meta.sh       # Sync labels/milestones to GitHub
```

## Root Configuration Files

| File | Purpose |
|------|---------|
| `package.json` | Root workspace package (pnpm scripts) |
| `pnpm-workspace.yaml` | Workspace member definitions |
| `pnpm-lock.yaml` | Dependency lockfile |
| `tsconfig.base.json` | Base TypeScript config (extended by packages) |
| `eslint.config.mjs` | ESLint configuration |
| `docker-compose.yml` | Production-like multi-service orchestration |
| `docker-compose.dev.yml` | Development Docker overrides |
| `AGENTS.md` | AI agent configuration |
| `CHANGELOG.md` | Version changelog |

## Naming Conventions

- **Route files:** `kebab-case.ts` with verb-noun registration functions (e.g., `registerChatCompletionsRoute()`)
- **Test files:** Mirror source path with `.test.ts` / `.test.tsx` suffix
- **Component files:** `kebab-case.tsx` with PascalCase exports
- **Feature directories:** Singular noun (`auth/`, `chat/`, `billing/`)
- **Supabase stores:** `supabase-{entity}-store.ts` pattern
- **Provider clients:** `{provider}-client.ts` pattern
