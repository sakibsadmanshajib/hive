# Codebase Concerns

**Analysis Date:** 2026-03-16

## Critical Issues

### 1. MVP AI Service Placeholder (Severity: Critical)

The `AiService` base class in `apps/api/src/domain/ai-service.ts` returns hardcoded responses instead of calling actual AI providers:

- `chatCompletions()` returns `"MVP response: ${text || 'Your request was processed.'}"` (line ~71)
- `responses()` returns `"MVP output: ${input || 'No input provided.'}"` (line ~95)

**Impact:** The domain-layer AI service never calls real providers. The `RuntimeAiService` subclass in `apps/api/src/runtime/services.ts` extends this and overrides with actual provider calls, but the base class is misleading and any code path that calls the base class methods directly will return fake data.

**Fix:** Remove hardcoded responses from the base class. Make methods abstract or throw "not implemented" errors to force subclass implementation.

### 2. Non-Cryptographic Random IDs in Payment Service (Severity: High)

`apps/api/src/domain/payment-service.ts` (line ~53) generates payment intent IDs using:

```typescript
`intent_${Math.random().toString(36).slice(2, 12)}`
```

**Impact:** `Math.random()` is not cryptographically secure. Payment intent IDs are predictable, creating a potential vector for intent ID guessing attacks. An attacker could enumerate valid intent IDs and attempt to claim payments.

**Fix:** Replace with `crypto.randomUUID()` or `crypto.randomBytes()` for intent ID generation. The rest of the codebase already uses `randomUUID()` from `node:crypto` (see `ai-service.ts`, `langfuse.ts`).

## Architectural Concerns

### 3. Monolithic services.ts (~1130 Lines) (Severity: Medium)

`apps/api/src/runtime/services.ts` is the composition root at approximately 1130 lines. It contains:

- The `createRuntimeServices()` factory function
- The `RuntimeAiService` class (extends `AiService`)
- Type definitions for all store interfaces (`ApiKeyStore`, `BillingStore`, `UserStore`, `GuestStore`, `ChatHistoryStore`)
- `RuntimeServices` type with all service references
- Provider wiring, rate limiter setup, reconciliation scheduler
- Multiple helper types and inline logic

**Impact:**
- High risk of merge conflicts when multiple developers modify service composition
- Difficult to navigate and reason about dependencies
- Store interface types are co-located with implementation, making them harder to reuse
- Testing the composition itself requires understanding the entire file

**Fix:** Extract into focused modules:
- `apps/api/src/runtime/store-types.ts` - Store interface definitions
- `apps/api/src/runtime/runtime-ai-service.ts` - `RuntimeAiService` class
- `apps/api/src/runtime/service-factory.ts` - `createRuntimeServices()` orchestration
- Keep `services.ts` as a thin re-export barrel

### 4. InMemoryRateLimiter Memory Leak (Severity: Medium)

`apps/api/src/domain/rate-limiter.ts` stores timestamp arrays in a `Map<string, number[]>`. While timestamps within the window are filtered on each `allow()` call, the Map keys (unique client identifiers) are never evicted.

```typescript
private readonly events = new Map<string, number[]>();
```

**Impact:** In a high-traffic environment with many unique keys (especially guest rate limiting with `guest:{id}:{ip}` composite keys), the Map grows unboundedly. Each unique guest/IP combination adds a new entry that persists for the lifetime of the process.

**Mitigating factor:** The `RedisRateLimiter` in `apps/api/src/runtime/redis-rate-limiter.ts` is the production rate limiter when Redis is available. The in-memory limiter is primarily a fallback. Redis entries auto-expire via TTL.

**Fix:** Add periodic cleanup of stale keys (e.g., remove entries with no timestamps within the window). Alternatively, use a bounded LRU cache.

## Type Safety Issues

### 5. Payment Webhook Body Type Safety (Severity: Medium)

`apps/api/src/routes/payment-webhook.ts` defines a `PaymentWebhookBody` type but relies on runtime checks rather than schema validation:

```typescript
type PaymentWebhookBody = {
  provider: "bkash" | "sslcommerz";
  intent_id: string;
  provider_txn_id: string;
  verified: boolean;
};
```

**Issues:**
- No Fastify schema validation applied; the type annotation provides compile-time safety only.
- `request.body` fields accessed with optional chaining (`request.body?.provider`), suggesting the body might not match the declared type at runtime.
- The `verified` field comes from the external webhook payload but is trusted directly. While signature verification exists in `webhook-signatures.ts`, the route handler checks `request.body?.verified` as a boolean from the incoming payload rather than computing verification server-side.
- The `rawBody` access uses unsafe type assertion: `request as unknown as { rawBody?: unknown }`.

**Fix:** Add Fastify JSON schema validation to the route. Compute verification status server-side using `verifyBkashSignature()` / `verifySslcommerzSignature()` rather than trusting the `verified` field from the payload.

### 6. Error Mapping Relies on String Matching (Severity: Low)

`apps/api/src/routes/payment-webhook.ts` maps errors to HTTP status codes by checking error message strings:

```typescript
if (message.includes("intent not found")) { return { status: 404, ... }; }
if (message.includes("duplicate") || message.includes("provider mismatch")) { return { status: 409, ... }; }
```

**Impact:** Fragile error handling. If error message text changes in `PaymentService`, the route handler silently falls through to a generic 500 response.

**Fix:** Use typed error classes (e.g., `PaymentIntentNotFoundError`, `DuplicatePaymentError`) or error codes instead of string matching.

## Performance Concerns

### 7. Provider Metrics Cache TTL (Severity: Low)

`apps/api/src/providers/registry.ts` uses a 5-second cache TTL (`METRICS_STATUS_CACHE_TTL_MS = 5000`) for provider status responses. If the status endpoint is hit frequently, this may cause unnecessary provider health checks.

**Impact:** Minor latency impact on frequent status polling. The short TTL means the cache provides minimal benefit under sustained load.

**Fix:** Make the TTL configurable via environment variable. Consider increasing the default to 15-30 seconds for production.

### 8. Synchronous Service Initialization (Severity: Low)

`createRuntimeServices()` in `apps/api/src/runtime/services.ts` initializes all services sequentially in a single synchronous-looking function (with some async stores). Independent services could be initialized in parallel.

**Impact:** Slower cold starts. Not critical for long-running server processes but affects development restart cycles and container startup times.

**Fix:** Use `Promise.all()` for independent async initializations (e.g., Redis connection, Supabase client creation, Langfuse setup).

## Security Considerations

### 9. Demo Payment Confirm Endpoint (Severity: Low)

`apps/api/src/routes/payment-demo-confirm.ts` provides a `/v1/payments/demo/confirm` endpoint gated by the `ALLOW_DEMO_PAYMENT_CONFIRM` environment variable. This endpoint allows confirming payment intents without actual payment processor verification.

**Impact:** If accidentally enabled in production, it would allow free credit minting.

**Mitigating factor:** Requires `ALLOW_DEMO_PAYMENT_CONFIRM=true` explicitly. Also requires authenticated user with `billing:write` permission.

**Fix:** Add additional safeguards such as logging/alerting when this endpoint is used, or restrict it to development environments only via `NODE_ENV` check.

### 10. Guest Token as Shared Secret (Severity: Low)

The `WEB_INTERNAL_GUEST_TOKEN` is a single shared secret between the web frontend and API backend, used to authorize guest chat requests. It is not rotated per-request and not time-limited.

**Impact:** If the token leaks, any client can make unlimited guest chat requests by including the token in headers.

**Mitigating factor:** Guest requests are also rate-limited by `guest:{id}:{ip}` composite key.

**Fix:** Consider implementing short-lived signed tokens (JWTs) for guest sessions with expiration, or use request signing with timestamps.

## Technical Debt Summary

| # | Concern | Severity | Location | Effort |
|---|---------|----------|----------|--------|
| 1 | MVP AI service placeholder | Critical | `apps/api/src/domain/ai-service.ts` | Low |
| 2 | Non-crypto random payment IDs | High | `apps/api/src/domain/payment-service.ts` | Low |
| 3 | Monolithic services.ts | Medium | `apps/api/src/runtime/services.ts` | Medium |
| 4 | Rate limiter memory leak | Medium | `apps/api/src/domain/rate-limiter.ts` | Low |
| 5 | Webhook body type safety | Medium | `apps/api/src/routes/payment-webhook.ts` | Medium |
| 6 | String-based error mapping | Low | `apps/api/src/routes/payment-webhook.ts` | Low |
| 7 | Metrics cache TTL | Low | `apps/api/src/providers/registry.ts` | Low |
| 8 | Sequential service init | Low | `apps/api/src/runtime/services.ts` | Medium |
| 9 | Demo payment endpoint | Low | `apps/api/src/routes/payment-demo-confirm.ts` | Low |
| 10 | Shared guest token | Low | Multiple files | Medium |
