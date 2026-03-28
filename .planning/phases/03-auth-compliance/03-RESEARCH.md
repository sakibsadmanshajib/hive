# Phase 3: Auth Compliance - Research

**Researched:** 2026-03-17
**Domain:** Bearer token authentication, Content-Type headers, OpenAI SDK compatibility
**Confidence:** HIGH

## Summary

Phase 3 simplifies the `/v1/*` auth path to accept only `Authorization: Bearer <api-key>`, removing the Supabase JWT session lookup and `x-api-key` header fallback. The current `resolvePrincipal()` function in `auth.ts` has a three-step resolution chain (JWT session, x-api-key header, bearer-as-API-key fallback) that must be collapsed to a single path: extract bearer token, resolve as Hive API key via `services.users.resolveApiKey()`, return 401 if missing or invalid.

A Fastify `onSend` hook in `v1Plugin` will enforce `Content-Type: application/json` on all non-streaming responses. SDK integration tests using the official `openai` npm package (v6.x) against a real Fastify server via `app.listen()` on an ephemeral port will provide the highest-confidence validation.

**Primary recommendation:** Create a dedicated `resolveV1Principal()` function (bearer-only, no JWT, no x-api-key) and wire it into a `preHandler` hook on v1Plugin, then add the `onSend` Content-Type hook in the same plugin.

<user_constraints>

## User Constraints (from CONTEXT.md)

### Locked Decisions
- `/v1/*` routes accept **only** `Authorization: Bearer <api-key>` as auth
- Bearer token resolved as a **Hive API key** (not Supabase JWT)
- Supabase Auth session lookup (`getSessionPrincipal`) **removed** from `/v1/*` auth path
- `x-api-key` header fallback **removed** from `/v1/*` routes
- 401 error uses `code: "invalid_api_key"` with `type: "authentication_error"`
- Messages: no header = `"No API key provided"`, invalid token = `"Incorrect API key provided"`
- `onSend` hook in `v1Plugin` sets `Content-Type: application/json` on non-streaming replies
- Streaming `Content-Type: text/event-stream` enforcement deferred to Phase 6
- SDK verification uses real `openai` npm SDK against real Fastify server with test DB

### Claude's Discretion
- How to structure the test helper (server bootstrap, DB seeding utilities)
- Whether to create a dedicated `v1-auth` middleware function or inline the simplified logic in `auth.ts`
- Exact Fastify hook type for Content-Type enforcement (`onSend` vs `preSerialization`)

### Deferred Ideas (OUT OF SCOPE)
- `/app/*` web authentication path
- Streaming `Content-Type: text/event-stream` enforcement (Phase 6)
- Go SDK and Python SDK integration tests

</user_constraints>

<phase_requirements>

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| FOUND-02 | Bearer token auth (`Authorization: Bearer <key>`) verified compatible with official OpenAI SDKs -- no edge cases with x-api-key fallback | Simplified `resolvePrincipal` removing JWT/x-api-key paths; SDK integration tests with `openai` npm package |
| FOUND-05 | All `/v1/*` endpoints return correct `Content-Type` headers (`application/json` for non-streaming, `text/event-stream` for streaming) | `onSend` hook in v1Plugin for non-streaming; streaming deferred to Phase 6 per CONTEXT.md |

</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| fastify | ^5.1.0 | HTTP framework | Already in use; provides hooks API for auth/content-type |
| openai | ^6.32.0 | SDK integration testing | Official OpenAI Node SDK; used as test client |
| vitest | ^2.1.8 | Test runner | Already in workspace devDependencies |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| @sinclair/typebox | ^0.34.48 | Schema validation | Already in use for request schemas |

**Installation:**
```bash
cd apps/api && pnpm add -D openai
```

**Note:** `openai` is a devDependency only -- used for integration tests, not production code.

## Architecture Patterns

### Auth Simplification Strategy

The current `resolvePrincipal()` in `auth.ts:64-106` has this resolution order:
1. Try bearer token as Supabase JWT session (if `authEnabled`)
2. Fall back to `x-api-key` header, or bearer token as API key
3. Try dev-user prefix
4. Try `resolveApiKey()` DB lookup

For `/v1/*`, this collapses to:
1. Extract bearer token (`readBearerToken()` -- already exists at line 52)
2. If no token: 401 with `"No API key provided"`
3. Resolve via `services.users.resolveApiKey(token)`
4. If not found: 401 with `"Incorrect API key provided"`
5. Return `AuthPrincipal` with `authType: "apiKey"`

### Pattern 1: Dedicated v1 Auth Function
**What:** A new `resolveV1Principal()` function alongside the existing `resolvePrincipal()`
**When to use:** All `/v1/*` routes
**Why not inline in preHandler:** Keeps the function testable in isolation, matches existing pattern of `requireApiPrincipal()` calling `resolvePrincipal()`

```typescript
// In auth.ts -- new function for v1 routes only
async function resolveV1Principal(
  request: FastifyRequest,
  services: RuntimeServices,
): Promise<AuthPrincipal | null> {
  const bearerToken = readBearerToken(request);
  if (!bearerToken) return null; // caller differentiates "missing" vs "invalid"

  const resolved = await services.users.resolveApiKey(bearerToken);
  if (!resolved) return undefined; // sentinel: token present but invalid

  return {
    userId: resolved.userId,
    authType: "apiKey",
    scopes: resolved.scopes,
    apiKeyId: resolved.apiKeyId,
  };
}
```

**Alternative considered:** Using a `preHandler` hook on v1Plugin to centralize auth. This would remove the per-route `requireApiPrincipal()` call but changes the existing handler pattern too much for this phase. Better to update `requireApiPrincipal` to call the new resolver for v1 context.

### Pattern 2: onSend Hook for Content-Type
**What:** Fastify `onSend` hook registered in `v1Plugin` to force `Content-Type: application/json`
**When to use:** All non-streaming `/v1/*` responses

```typescript
// In v1-plugin.ts
app.addHook('onSend', async (_request, reply, payload) => {
  // Only override for non-streaming responses (streaming sets its own content-type)
  const ct = reply.getHeader('content-type');
  if (typeof ct === 'string' && ct.includes('text/event-stream')) {
    return payload;
  }
  reply.header('content-type', 'application/json; charset=utf-8');
  return payload;
});
```

**Why `onSend` over `preSerialization`:** `onSend` runs after serialization, so we can inspect the content-type that Fastify auto-set and override it. `preSerialization` runs before serialization and the content-type header may not be set yet. `onSend` is the safer choice.

### Pattern 3: SDK Integration Test with Ephemeral Server
**What:** Boot real Fastify app on port 0 (ephemeral), point `openai` SDK at it
**When to use:** Auth compliance tests

```typescript
import OpenAI from 'openai';
import { createApp } from '../../src/server';

let app: ReturnType<typeof createApp>;
let client: OpenAI;
let baseURL: string;

beforeAll(async () => {
  app = createApp(); // needs mock services for test
  const address = await app.listen({ port: 0 });
  baseURL = `${address}/v1`;
  client = new OpenAI({ apiKey: 'sk-test-valid-key', baseURL });
});

afterAll(async () => {
  await app.close();
});
```

**Key insight:** The `openai` npm SDK constructor accepts `baseURL` and `apiKey`. It sends `Authorization: Bearer <apiKey>` automatically. This is the exact behavior we need to validate.

### Recommended Test File Structure
```
apps/api/test/
  routes/
    v1-auth-compliance.test.ts    # SDK integration tests (new)
    auth-principal.test.ts         # Existing unit tests (update)
    api-error-format.test.ts       # Existing (no change)
```

### Anti-Patterns to Avoid
- **Modifying `resolvePrincipal()` in-place for v1 behavior:** The existing function serves web routes. Create a new v1-specific function instead of adding conditionals.
- **Using `app.inject()` for SDK compat tests:** `inject()` bypasses HTTP headers and transport. Use real HTTP via `app.listen()` + SDK client.
- **Removing `x-api-key` from CORS allowedHeaders:** The header may still be used by web routes. Only remove it from the auth resolution path for `/v1/*`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| HTTP client for testing | Custom fetch wrapper | `openai` npm SDK | Validates real SDK compatibility |
| Bearer token parsing | Custom regex | Existing `readBearerToken()` | Already handles edge cases (array headers, case-insensitive scheme) |
| Error response formatting | Manual JSON construction | `sendApiError()` from `api-error.ts` | Ensures consistent OpenAI error envelope |

## Common Pitfalls

### Pitfall 1: Fastify Content-Type Auto-Detection
**What goes wrong:** Fastify auto-sets `Content-Type` based on payload type. JSON objects get `application/json` but with varying charset parameters across versions.
**Why it happens:** Fastify's serialization pipeline sets headers before `onSend`.
**How to avoid:** Explicitly set `content-type: application/json; charset=utf-8` in `onSend` hook regardless of what Fastify auto-detected. The hook runs last and can override.
**Warning signs:** Tests pass with `inject()` but fail with real HTTP because header casing or charset differs.

### Pitfall 2: openai SDK Error Handling
**What goes wrong:** The `openai` SDK throws specific error classes (`AuthenticationError`, `APIError`) and parses the error body. If the error body shape doesn't match, the SDK wraps it differently.
**Why it happens:** The SDK expects `{ error: { message, type, param, code } }` exactly.
**How to avoid:** Use `sendApiError()` which already produces the correct shape. Test that `catch (e)` yields `e instanceof OpenAI.AuthenticationError`.
**Warning signs:** SDK throws generic `APIError` instead of `AuthenticationError` for 401s.

### Pitfall 3: Existing Tests Break After Auth Simplification
**What goes wrong:** Existing tests in `auth-principal.test.ts` test the x-api-key and JWT paths that are being removed from v1.
**Why it happens:** The old `resolvePrincipal()` still serves non-v1 routes.
**How to avoid:** Keep `resolvePrincipal()` intact for non-v1 routes. Create `resolveV1Principal()` as a separate function. Update `requireApiPrincipal()` to accept a context flag or create `requireV1ApiPrincipal()`.
**Warning signs:** Tests for web routes start failing after auth changes.

### Pitfall 4: Port Conflicts in CI
**What goes wrong:** Integration tests using `app.listen({ port: FIXED })` collide when run in parallel.
**Why it happens:** Vitest runs test files in parallel by default.
**How to avoid:** Always use `port: 0` for ephemeral port assignment. Extract the actual port from the returned address string.
**Warning signs:** Tests pass locally but fail intermittently in CI.

### Pitfall 5: Reply Already Sent
**What goes wrong:** `requireApiPrincipal` calls `sendApiError` which sends the reply, then the route handler also tries to send.
**Why it happens:** The existing pattern returns `undefined` as a sentinel, and the handler checks `if (!principal) return`.
**How to avoid:** Maintain the existing pattern exactly. The new v1 auth function should follow the same contract.
**Warning signs:** Fastify warns "Reply was already sent" in logs.

## Code Examples

### OpenAI SDK Client Constructor (verified behavior)
```typescript
// The openai SDK sends Authorization: Bearer <apiKey> automatically
const client = new OpenAI({
  apiKey: 'sk-test-key',
  baseURL: 'http://localhost:PORT/v1',
});

// This will send:
// POST http://localhost:PORT/v1/chat/completions
// Authorization: Bearer sk-test-key
// Content-Type: application/json
```

### OpenAI SDK Error Classes
```typescript
import OpenAI from 'openai';

try {
  await client.chat.completions.create({ model: 'test', messages: [] });
} catch (e) {
  if (e instanceof OpenAI.AuthenticationError) {
    // 401 responses -- e.status === 401
    // e.error === { message, type, param, code }
  }
}
```

### Fastify onSend Hook Registration
```typescript
// Source: Fastify docs -- hooks lifecycle
app.addHook('onSend', async (request, reply, payload) => {
  // payload is the serialized string/Buffer
  // reply headers can be modified here
  // return payload (or modified payload)
  reply.header('content-type', 'application/json; charset=utf-8');
  return payload;
});
```

### Current resolveApiKey Interface
```typescript
// From services.ts:511
async resolveApiKey(key: string): Promise<{
  userId: string;
  scopes: string[];
  apiKeyId?: string;
} | null>
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `x-api-key` + `Authorization: Bearer` dual path | Bearer-only for `/v1/*` | This phase | Eliminates header conflict edge cases |
| Supabase JWT in `/v1/*` path | Removed from `/v1/*` | This phase | API keys are the sole auth mechanism |
| Implicit Fastify Content-Type | Explicit `onSend` hook | This phase | Guarantees `application/json` header |

## Open Questions

1. **Test services mocking strategy for SDK integration tests**
   - What we know: Unit tests use inline mock objects cast with `as never`. SDK integration tests need a real Fastify server which calls `createRuntimeServices()`.
   - What's unclear: Whether to mock at the services level (fake `resolveApiKey`) or use a real test database.
   - Recommendation: Mock at the services level. Create a `createTestApp()` helper that injects mock services with a known API key. A real DB adds complexity without testing auth logic better. Claude's discretion per CONTEXT.md.

2. **Whether `requireApiPrincipal` should dispatch to v1 vs non-v1 resolver**
   - What we know: All `/v1/*` route handlers call `requireApiPrincipal()`. Changing its behavior would affect all callers.
   - What's unclear: Whether to create `requireV1ApiPrincipal()` as a separate export or parameterize the existing function.
   - Recommendation: Create a separate `requireV1ApiPrincipal()` function. Cleaner, no risk to existing routes. Update v1 route handlers to call the new function. Claude's discretion per CONTEXT.md.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | vitest ^2.1.8 |
| Config file | None for API app (uses vitest defaults) |
| Quick run command | `cd apps/api && pnpm test` |
| Full suite command | `cd apps/api && pnpm test` |

### Phase Requirements to Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| FOUND-02 | Valid Bearer token resolves to API key principal | integration | `cd apps/api && pnpm vitest run test/routes/v1-auth-compliance.test.ts -t "valid bearer"` | No -- Wave 0 |
| FOUND-02 | Missing Authorization header returns 401 | integration | `cd apps/api && pnpm vitest run test/routes/v1-auth-compliance.test.ts -t "missing"` | No -- Wave 0 |
| FOUND-02 | Invalid token returns 401 with correct body | integration | `cd apps/api && pnpm vitest run test/routes/v1-auth-compliance.test.ts -t "invalid"` | No -- Wave 0 |
| FOUND-02 | x-api-key ignored when Bearer present | integration | `cd apps/api && pnpm vitest run test/routes/v1-auth-compliance.test.ts -t "x-api-key ignored"` | No -- Wave 0 |
| FOUND-05 | Non-streaming response has Content-Type: application/json | integration | `cd apps/api && pnpm vitest run test/routes/v1-auth-compliance.test.ts -t "content-type"` | No -- Wave 0 |

### Sampling Rate
- **Per task commit:** `cd apps/api && pnpm test`
- **Per wave merge:** `cd apps/api && pnpm test`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `apps/api/test/routes/v1-auth-compliance.test.ts` -- SDK integration tests for auth + content-type
- [ ] `openai` npm package as devDependency -- `cd apps/api && pnpm add -D openai`
- [ ] Test helper for creating Fastify app with mock services (server bootstrap utility)

## Sources

### Primary (HIGH confidence)
- Project source code: `apps/api/src/routes/auth.ts`, `v1-plugin.ts`, `api-error.ts`, `server.ts`, `routes/index.ts`
- Existing tests: `apps/api/test/routes/auth-principal.test.ts`, `api-error-format.test.ts`
- Phase context: `.planning/phases/03-auth-compliance/03-CONTEXT.md`

### Secondary (MEDIUM confidence)
- Fastify hooks lifecycle: `onSend` runs after serialization, before response is sent. Can modify headers and payload.
- OpenAI SDK v6.x: Constructor accepts `baseURL` and `apiKey`. Sends `Authorization: Bearer` automatically. Throws typed error classes (`AuthenticationError` for 401).

### Tertiary (LOW confidence)
- None. All findings verified against project source code or well-established library behavior.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH -- all libraries already in use except `openai` SDK (verified current version 6.32.0)
- Architecture: HIGH -- based on direct reading of existing source code and established patterns
- Pitfalls: HIGH -- derived from code analysis of actual resolution chain and Fastify hook lifecycle

**Research date:** 2026-03-17
**Valid until:** 2026-04-17 (stable domain, no fast-moving dependencies)
