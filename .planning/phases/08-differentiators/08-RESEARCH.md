# Phase 8: Differentiators - Research

**Researched:** 2026-03-18
**Domain:** HTTP response headers, model aliasing, request tracing
**Confidence:** HIGH

## Summary

Phase 8 adds Hive-specific transparency headers and model aliasing to all `/v1/*` endpoints. The codebase is already 80% there: `RuntimeAiService` methods (`chatCompletions`, `chatCompletionsStream`, `responses`, `imageGeneration`, `embeddings`) already return `x-model-routed`, `x-provider-used`, `x-provider-model`, and `x-actual-credits` headers. The route handlers already propagate these to Fastify replies. What remains is: (1) adding `x-request-id` via `crypto.randomUUID()` in a centralized v1-plugin hook, (2) auditing that ALL endpoints consistently set ALL 4 service-level headers (the MVP `AiService` is missing `x-provider-used` and `x-provider-model` on some methods), (3) implementing a static model alias map so `gpt-4o` etc. resolve correctly, and (4) writing compliance tests.

**Primary recommendation:** This is a surgical phase -- add one `onRequest` hook for `x-request-id`, create a model alias config, wire alias resolution into `ModelService.findById()`, audit/fix any header gaps in `AiService` (MVP fallback), and add compliance tests.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Request ID generation: `crypto.randomUUID()` -- built-in Node.js, no additional dependencies
- Generate per-request in v1-plugin hook so every request gets a unique ID before route handlers run
- Attach to reply as `x-request-id` header
- ALL `/v1/*` endpoints must include: `x-request-id`, `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits`
- Add `x-request-id` in v1-plugin onRequest/onSend hook (centralized, DRY) -- not per-route
- Other headers already set in AiService methods -- verify all routes set them consistently
- Static alias map in a dedicated config file (e.g., `src/config/model-aliases.ts`)
- Apply alias resolution early in AiService before provider dispatch
- Pass-through if model name not in alias map (no breaking change)
- Credit cost exposed via `x-actual-credits` response header only
- No changes to usage object fields (keep it OpenAI-compatible)

### Claude's Discretion
- Exact format/precision of credit values in `x-actual-credits`
- Whether alias map is stored as a plain object or typed map
- Specific model alias mappings beyond gpt-4o and gpt-4o-mini

### Deferred Ideas (OUT OF SCOPE)
None -- discussion stayed within phase scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| DIFF-01 | All `/v1/*` endpoints include `x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits` response headers | Header audit findings below; RuntimeAiService already returns all 4 on success paths; MVP AiService has gaps; models route has no headers (acceptable -- no AI call) |
| DIFF-02 | Usage object or response headers include actual credit cost | Already implemented via `x-actual-credits` header in all service methods; just needs audit for consistency |
| DIFF-03 | Model aliasing -- accept standard OpenAI model names and route to best provider | Model alias map pattern documented; `gpt-4o` and `gpt-4o-mini` already exist as model IDs in ModelService; `gpt-3.5-turbo` needs alias |
| DIFF-04 | All `/v1/*` responses include `x-request-id` header | Fastify onRequest hook pattern in v1-plugin.ts; `crypto.randomUUID()` verified available |
</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `node:crypto` | built-in | `randomUUID()` for x-request-id | Zero dependencies, RFC 4122 v4 compliant |
| Fastify hooks | 5.x (existing) | `onRequest` hook for centralized header injection | Already used for content-type via `onSend` |

### Supporting
No new dependencies required. This phase uses only existing project infrastructure.

## Architecture Patterns

### Current Header Flow (verified from codebase)

```
Request → v1-plugin hooks → Route handler → AiService method → ProviderRegistry
                                              ↓
                                        Returns { body, headers, statusCode }
                                              ↓
                                    Route handler: reply.header(k, v) for each
```

### Header Audit Results

**RuntimeAiService (production path):**
| Method | x-model-routed | x-provider-used | x-provider-model | x-actual-credits |
|--------|:-:|:-:|:-:|:-:|
| chatCompletions | YES | YES | YES | YES |
| chatCompletionsStream | YES | YES | YES | YES |
| responses | YES | YES | YES | YES |
| imageGeneration | YES | YES | YES | YES |
| imageGeneration (error) | YES | NO | NO | NO |
| embeddings | YES (via registry) | YES (via registry) | YES (via registry) | YES |

**MVP AiService (fallback path):**
| Method | x-model-routed | x-provider-used | x-provider-model | x-actual-credits |
|--------|:-:|:-:|:-:|:-:|
| chatCompletions | YES | NO | NO | YES |
| responses | NO headers | NO | NO | NO |
| imageGeneration | NO | NO | NO | YES |
| embeddings | YES | NO | NO | YES |

**Route-level gaps:**
- `GET /v1/models` and `GET /v1/models/:model`: No AI headers (no AI call -- x-request-id only needed)
- Error responses via `sendApiError()`: Do NOT include any x-* headers

### Pattern 1: Centralized x-request-id via onRequest Hook
**What:** Add `onRequest` hook in v1-plugin.ts to generate and attach `x-request-id` to every reply
**When to use:** Always -- this runs before any route handler
**Example:**
```typescript
app.addHook('onRequest', async (request, reply) => {
  const requestId = crypto.randomUUID();
  request.id = requestId; // available for logging
  reply.header('x-request-id', requestId);
});
```

### Pattern 2: Model Alias Resolution
**What:** Static map that translates OpenAI model names to Hive model IDs
**When to use:** Before model lookup in service methods
**Example:**
```typescript
// src/config/model-aliases.ts
export const MODEL_ALIASES: Record<string, string> = {
  'gpt-3.5-turbo': 'gpt-4o-mini',    // fast/cheap equivalent
  'gpt-4-turbo': 'gpt-4o',           // capable equivalent
  'gpt-4': 'gpt-4o',                 // latest GPT-4 class
  'text-embedding-ada-002': 'openai/text-embedding-3-small',
};

export function resolveModelAlias(modelId: string): string {
  return MODEL_ALIASES[modelId] ?? modelId;
}
```

**Key insight:** `gpt-4o` and `gpt-4o-mini` already exist as first-class model IDs in `ModelService.MODELS`, so they do NOT need aliases. Only legacy/alternate names like `gpt-3.5-turbo`, `gpt-4`, `gpt-4-turbo` need mapping.

### Pattern 3: MVP AiService Header Gaps Fix
**What:** Add missing `x-provider-used` and `x-provider-model` to MVP AiService return objects
**Example:**
```typescript
headers: {
  "x-model-routed": model.id,
  "x-provider-used": "hive-mvp",
  "x-provider-model": model.id,
  "x-actual-credits": String(credits),
},
```

### Anti-Patterns to Avoid
- **Per-route x-request-id generation:** Would require touching every route file; use centralized hook instead
- **Alias resolution in route handlers:** Would scatter logic; resolve in ModelService.findById() or a wrapper
- **Modifying usage object for credits:** Breaks OpenAI SDK compatibility; keep in headers only

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| UUID generation | Custom ID format | `crypto.randomUUID()` | RFC compliant, fast, built-in |
| Request tracing | Custom middleware | Fastify `onRequest` hook | Framework-native, runs before all routes |

## Common Pitfalls

### Pitfall 1: Error Responses Missing Headers
**What goes wrong:** `sendApiError()` bypasses route-level header setting, so error responses lack x-model-routed etc.
**Why it happens:** Error path exits before route handler sets headers from service result
**How to avoid:** `x-request-id` is safe (set in onRequest hook, always present). For other headers, only AI-invoking endpoints should have them; error responses before AI dispatch legitimately lack them.
**Warning signs:** Tests expecting headers on 400/401/402/429 responses

### Pitfall 2: Models Endpoint Has No AI Headers
**What goes wrong:** GET `/v1/models` doesn't call any AI service, so x-model-routed etc. don't apply
**Why it happens:** Models endpoint is a catalog listing, not an inference call
**How to avoid:** Only `x-request-id` should appear on models responses. Don't add dummy values for the other 4 headers.

### Pitfall 3: Alias Map Shadowing Real Model IDs
**What goes wrong:** If an alias key matches a real model ID, it could redirect incorrectly
**Why it happens:** Adding an alias for a model name that also exists in the MODELS array
**How to avoid:** Only alias model names that do NOT exist as first-class IDs. `gpt-4o` is a real model ID -- do NOT add it as an alias key.

### Pitfall 4: Header Values on Streaming Responses
**What goes wrong:** Headers set after streaming begins are silently dropped
**Why it happens:** HTTP headers must be sent before body
**How to avoid:** All x-* headers are already set before `reply.send(stream)` in chat-completions.ts. Maintain this pattern.

## Code Examples

### x-request-id Hook Integration (v1-plugin.ts)
```typescript
import { randomUUID } from 'node:crypto';

// Inside v1Plugin, before route registration:
app.addHook('onRequest', async (_request, reply) => {
  reply.header('x-request-id', randomUUID());
});
```

### Model Alias File Structure
```typescript
// src/config/model-aliases.ts
const MODEL_ALIASES: Record<string, string> = {
  'gpt-3.5-turbo': 'gpt-4o-mini',
  'gpt-4': 'gpt-4o',
  'gpt-4-turbo': 'gpt-4o',
  'text-embedding-ada-002': 'openai/text-embedding-3-small',
};

export function resolveModelAlias(modelId: string): string {
  return MODEL_ALIASES[modelId] ?? modelId;
}
```

### ModelService Integration Point
```typescript
// In ModelService.findById():
findById(modelId: string): GatewayModel | undefined {
  const resolved = resolveModelAlias(modelId);
  return this.enabledModels().find((model) => model.id === resolved);
}
```

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Vitest (existing) |
| Config file | apps/api/vitest.config.ts |
| Quick run command | `cd apps/api && npx vitest run src/routes/__tests__/differentiators-compliance.test.ts` |
| Full suite command | `cd apps/api && npx vitest run` |

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| DIFF-01 | All endpoints return 4 AI headers | unit | `npx vitest run src/routes/__tests__/differentiators-headers.test.ts -x` | Wave 0 |
| DIFF-02 | x-actual-credits present and numeric | unit | Same as DIFF-01 test file | Wave 0 |
| DIFF-03 | Alias model names resolve correctly | unit | `npx vitest run src/config/__tests__/model-aliases.test.ts -x` | Wave 0 |
| DIFF-04 | x-request-id present on all responses | unit | Same as DIFF-01 test file | Wave 0 |

### Sampling Rate
- **Per task commit:** `cd apps/api && npx vitest run src/routes/__tests__/differentiators-*.test.ts`
- **Per wave merge:** `cd apps/api && npx vitest run`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `src/routes/__tests__/differentiators-headers.test.ts` -- covers DIFF-01, DIFF-02, DIFF-04
- [ ] `src/config/__tests__/model-aliases.test.ts` -- covers DIFF-03
- [ ] `src/config/model-aliases.ts` -- alias map module (new file)

## Open Questions

1. **Which legacy model names need aliases beyond gpt-3.5-turbo and gpt-4?**
   - What we know: `gpt-4o` and `gpt-4o-mini` are already first-class model IDs
   - What's unclear: Whether `gpt-4-turbo`, `gpt-4-0125-preview`, `text-embedding-ada-002` etc. should be aliased
   - Recommendation: Start with a minimal set (`gpt-3.5-turbo`, `gpt-4`, `gpt-4-turbo`, `text-embedding-ada-002`), extend later based on user requests

2. **Should x-request-id be accepted from client request headers?**
   - What we know: Decision says generate in onRequest hook
   - What's unclear: Whether to honor incoming `x-request-id` if present (correlation ID pattern)
   - Recommendation: Always generate server-side for consistency; defer client correlation to future phase

## Sources

### Primary (HIGH confidence)
- Direct codebase inspection of v1-plugin.ts, ai-service.ts, runtime/services.ts, all route files
- Fastify hooks documentation (onRequest runs before route handlers)
- Node.js crypto.randomUUID() -- built-in since Node 19+

### Secondary (MEDIUM confidence)
- OpenAI SDK behavior: SDKs ignore unknown response headers (x-* prefix is safe)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH -- no new dependencies, all built-in Node.js + existing Fastify
- Architecture: HIGH -- patterns directly observed in codebase, minimal changes needed
- Pitfalls: HIGH -- identified from actual code audit of header gaps

**Research date:** 2026-03-18
**Valid until:** 2026-04-18 (stable domain, no external dependencies)
