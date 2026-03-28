# Testing

**Analysis Date:** 2026-03-16

## Frameworks

| Framework | Version | Purpose | Config File |
|-----------|---------|---------|-------------|
| Vitest | 2.1.8 | Unit and integration tests | `apps/web/vitest.config.ts`, inline in `apps/api/package.json` |
| Playwright | 1.58.2 | End-to-end browser tests | `apps/web/playwright.config.ts` |
| Testing Library React | 16.3.0 | Component rendering and queries | Used in web test files |
| Jest DOM | 6.8.0 | DOM assertion matchers | Extended via `@testing-library/jest-dom` |
| jsdom | 26.1.0 | DOM emulation environment | Vitest environment for web tests |

## Test Structure

### API Tests (`apps/api/test/`)

Tests are organized by layer, mirroring the source directory structure:

```
apps/api/test/
├── domain/                        # Domain layer unit tests
│   ├── ai-service.test.ts         # AI service behavior
│   ├── api-key-service.test.ts    # API key validation
│   ├── authorization-matrix.test.ts # RBAC permission matrix
│   ├── chat-history-service.test.ts # Chat history persistence
│   ├── chat-history-store.test.ts # Chat history store
│   ├── credits-ledger.test.ts     # Credit ledger operations
│   ├── env.test.ts                # Environment config parsing
│   ├── guest-attribution.test.ts  # Guest session tracking
│   ├── model-service.test.ts      # Model catalog
│   ├── payment-reconciliation-scheduler.test.ts
│   ├── payment-reconciliation.test.ts
│   ├── payment-service.test.ts    # Payment intent lifecycle
│   ├── persistent-usage-service.test.ts
│   ├── persistent-user-service.test.ts
│   ├── rate-limiter.test.ts       # Rate limiter behavior
│   ├── refund-policy.test.ts      # Credit refund logic
│   ├── routing-engine.test.ts     # Model routing
│   ├── runtime-chat-billing.test.ts # Chat + billing integration
│   ├── runtime-image-generation.test.ts
│   ├── runtime-image-provider-wiring.test.ts
│   ├── runtime-services.test.ts   # Service composition
│   ├── supabase-api-key-store.test.ts
│   ├── supabase-auth-service.test.ts
│   ├── supabase-user-store.test.ts
│   ├── user-settings-gates.test.ts
│   └── webhook-signatures.test.ts # Signature verification
├── providers/                     # Provider client tests
│   ├── anthropic-client.test.ts
│   ├── groq-client.test.ts
│   ├── mock-client.test.ts
│   ├── ollama-client.test.ts
│   ├── openai-client.test.ts
│   ├── provider-circuit-breaker.test.ts
│   ├── provider-fallback.test.ts
│   ├── provider-http-client.test.ts
│   ├── provider-registry.test.ts
│   └── provider-status.test.ts
└── routes/                        # Route handler tests
    ├── analytics-route.test.ts
    ├── auth-principal.test.ts
    ├── chat-completions-route.test.ts
    ├── chat-sessions-route.test.ts
    ├── cors-route.test.ts
    ├── guest-attribution-route.test.ts
    ├── guest-chat-route.test.ts
    ├── guest-chat-sessions-route.test.ts
    ├── images-generations-route.test.ts
    ├── models-route.test.ts
    ├── payment-demo-confirm.test.ts
    ├── payment-webhook-route.test.ts
    ├── providers-metrics-route.test.ts
    ├── providers-status-route.test.ts
    ├── rbac-settings-enforcement.test.ts
    ├── responses-route.test.ts
    └── support-route.test.ts (and others)
```

### Web Tests (`apps/web/test/`)

```
apps/web/test/
├── __mocks__/
│   └── select-mock.tsx            # Mock for select component
├── app-shell.test.tsx             # Layout component tests
├── auth-page.test.tsx             # Auth page rendering
├── auth-session.test.ts           # Auth session state
├── billing-page.test.tsx          # Billing page rendering
├── chat-auth-gate.test.tsx        # Chat auth guard
├── chat-guest-mode.test.tsx       # Guest chat behavior
├── chat-mobile-layout.test.tsx    # Responsive layout
├── chat-polish.test.tsx           # Chat UI polish
├── chat-reducer.test.ts           # Chat state reducer
├── chat-shortcuts.test.ts         # Keyboard shortcuts
├── google-login-ui.test.tsx       # Google login button
├── guest-chat-history-route.test.ts
├── guest-chat-route.test.ts
├── guest-session-link-route.test.ts
├── guest-session-route.test.ts (and others)
└── ...
```

### E2E Tests (`apps/web/e2e/`)

```
apps/web/e2e/
├── fixtures/
│   └── auth.ts                    # Auth test fixtures/helpers
└── smoke-auth-chat-billing.spec.ts # Main smoke test suite
```

## Test Patterns

### Unit Test Structure (Vitest)

Tests follow a consistent `describe` / `it` pattern with clear naming:

```typescript
import { describe, expect, it, vi, afterEach } from "vitest";

describe("ServiceName", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does something specific", () => {
    // Arrange - create instance with dependencies
    // Act - call method
    // Assert - verify outcome
  });
});
```

### Mocking Patterns

**Constructor injection:** Domain services accept dependencies via constructor, making them easy to test with stubs:

```typescript
// Example from payment-service.test.ts
const ledger = new CreditLedger();
const service = new PaymentService(ledger);
```

**Time injection:** Services that depend on time accept a `nowFn` parameter:

```typescript
// Example from rate-limiter.test.ts
const times = [1000, 1001, 1002];
const limiter = new InMemoryRateLimiter(2, 60, () => times.shift() ?? 0);
```

**Vitest mocking:** `vi.restoreAllMocks()` in `afterEach` for clean state between tests.

**Web component mocks:** `apps/web/test/__mocks__/select-mock.tsx` provides mock implementations for UI components that are complex to render in test environments.

### Route Tests

Route handler tests create a minimal `RuntimeServices` mock object and test Fastify route behavior:
- Create Fastify app with route registered
- Inject requests with appropriate headers/body
- Assert response status codes and body content
- Test auth enforcement (missing tokens, invalid permissions)

### Provider Tests

Provider client tests verify:
- Request formatting and API contract compliance
- Error handling and timeout behavior
- Circuit breaker integration
- Fallback chain behavior

## E2E Configuration

Playwright configuration in `apps/web/playwright.config.ts`:

- **Test directory:** `./e2e`
- **Parallelism:** Disabled (`fullyParallel: false`, `workers: 1`)
- **Timeout:** 60 seconds per test, 10 seconds for assertions
- **Browser:** Chromium only (Desktop Chrome device)
- **Base URL:** `http://127.0.0.1:3000` (configurable via `E2E_BASE_URL`)
- **Tracing:** Retained on failure
- **Retries:** 1 in CI, 0 locally
- **Reporters:** List (console) and HTML (non-interactive)

## Vitest Configuration

Web Vitest config in `apps/web/vitest.config.ts`:
- **Environment:** jsdom
- **Setup files:** For Testing Library Jest DOM matchers
- **Path aliases:** Mapped to match Next.js `@/` paths

API tests use Vitest configuration via `package.json` or inline config.

## Coverage

- No explicit coverage thresholds configured in available config files.
- Coverage reporting not explicitly configured (no `coverage` section in Vitest configs).
- Test coverage is broad across layers:
  - Domain layer: Comprehensive (all major services have dedicated test files)
  - Provider layer: All 6 providers + registry + circuit breaker + fallback tested
  - Route layer: All major API routes have corresponding test files
  - Web: Component tests, state management tests, API route proxy tests
  - E2E: Smoke tests covering auth, chat, and billing flows

## Running Tests

```bash
# API unit tests
pnpm --filter @hive/api test

# Web unit tests
pnpm --filter @hive/web test

# E2E tests (requires running stack)
pnpm --filter @hive/web test:e2e
```
