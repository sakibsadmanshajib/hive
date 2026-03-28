# Phase 12: Embeddings Alias Runtime Compliance - Research

**Researched:** 2026-03-22
**Domain:** OpenAI-compatible embeddings model identity, alias resolution, and real-runtime catalog wiring
**Confidence:** MEDIUM

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

### Public embeddings model identity
- Follow the OpenAI-facing public contract for embeddings model ids.
- For this phase, the primary public embeddings id is `text-embedding-3-small`, not `openai/text-embedding-3-small`.
- Public request handling, public catalog lookup, and response bodies should use OpenAI-shaped ids for embeddings.
- Provider namespacing is an internal/runtime concern, not the public API identity for embeddings.

### Embeddings alias surface
- Accept the standard OpenAI embeddings ids that are already represented in the local OpenAI reference for this surface.
- `text-embedding-3-small` must work end-to-end in the real runtime path.
- Keep `text-embedding-ada-002` as a compatibility alias that resolves to the supported current embeddings target rather than rejecting it up front.
- Do not invent new Hive-specific public embeddings ids in this phase.

### Response identity vs routed/provider identity
- API response bodies should expose the OpenAI-facing embeddings model id that Hive presents publicly.
- `CreateEmbeddingResponse.model` should not leak provider namespacing such as `openai/text-embedding-3-small`.
- Provider-specific identity belongs in routing internals and DIFF headers, especially `x-provider-model`.
- `x-model-routed` should remain consistent with the public routed model identity Hive is honoring for the request, while `x-provider-model` carries the upstream/provider-specific target.

### Unsupported embeddings ids
- Stay strict for embeddings model validation.
- Unsupported embeddings ids should fail through the normal OpenAI-compatible unknown-model path rather than silently falling through to arbitrary provider ids.
- The real runtime must not rely on mock-only catalog entries or test harness behavior to make unsupported ids appear valid.

### Regression coverage boundary
- Regression coverage must exercise the real runtime catalog and alias path, not only the mock SDK harness.
- The official `openai` npm SDK success case for `client.embeddings.create({ model: "text-embedding-3-small" })` is the primary acceptance boundary.
- Coverage should also protect against the prior blind spot where the mock test app accepted `text-embedding-3-small` directly even when the real runtime catalog did not.

### Claude's Discretion
- Exact helper placement for embeddings alias normalization across model-service/runtime wiring
- Whether the embeddings public id is represented by a dedicated catalog entry, serialization layer normalization, or both
- Exact regression test split between domain/runtime tests and SDK/e2e-style tests

### Deferred Ideas (OUT OF SCOPE)
- Full audit and cleanup of all non-embeddings public model ids to remove or normalize provider namespacing everywhere — separate phase if desired
- Dynamic synchronization of the public model catalog from upstream provider inventories
- Broader reconsideration of the Phase 4 alias-per-entry public catalog strategy outside the embeddings scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| DIFF-03 | Model aliasing — accept standard OpenAI model names and route to the best available provider. | Make `text-embedding-3-small` the canonical public embeddings catalog id, resolve `text-embedding-ada-002` centrally to that id, keep provider model ids internal, and add production-path regression coverage that exercises real `ModelService` and runtime wiring. |
</phase_requirements>

## Summary

The confirmed gap is exactly what the milestone audit reported: the real runtime catalog only exposes `openai/text-embedding-3-small`, while the OpenAI-facing request/SDK contract uses `text-embedding-3-small`. The current mock SDK harness hides that mismatch because [`test-app.ts`](../../../apps/api/test/helpers/test-app.ts) directly lists `text-embedding-3-small`, so Phase 11 stayed green while the real catalog path still rejected the standard model id.

The implementation should stay inside the existing architecture rather than inventing a second embeddings path. Keep alias normalization centralized in [`model-aliases.ts`](../../../apps/api/src/config/model-aliases.ts) and [`model-service.ts`](../../../apps/api/src/domain/model-service.ts), let the route remain thin, and preserve the DIFF header split between public routed model identity and provider execution identity. The key behavioral change is that the canonical public embeddings id becomes `text-embedding-3-small`, while provider namespacing remains internal and only surfaces in `x-provider-model`.

There is one additional risk not called out explicitly in the roadmap text but visible in code: embeddings dispatch currently falls through the shared provider-level `providerModelMap`, and the repo has no dedicated embeddings upstream-model env or test coverage for that path. Treat that as an implementation-time verification point, not a reason to widen scope. Fix the public catalog/alias issue first, but verify that the real runtime does not end up sending a chat-oriented fallback model to `/embeddings`.

**Primary recommendation:** Canonicalize embeddings on the public OpenAI id `text-embedding-3-small`, alias `text-embedding-ada-002` to it in one place, keep provider ids out of response bodies, and add at least one non-mock regression test that exercises `ModelService` plus the real runtime/provider registry path.

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `openai` | repo `^6.32.0`; npm latest `6.32.0` (published 2026-03-17) | Official SDK regression client | Success criteria explicitly require `client.embeddings.create()` against Hive's `/v1` surface. |
| `fastify` | repo `^5.1.0`; npm latest `5.8.2` (published 2026-03-07) | Live `/v1/embeddings` route, hooks, and reply headers | Existing route/plugin/auth patterns already enforce the API contract. |
| `@fastify/type-provider-typebox` | repo `^6.1.0`; npm latest `6.1.0` (published 2025-10-19) | Typed request validation | The embeddings request schema is already wired through this path; Phase 12 should preserve it, not replace it. |
| `vitest` | repo `^2.1.8`; npm latest `4.1.0` (published 2026-03-12) | Unit, runtime, and SDK regression tests | Existing API test runner and approved execution path. |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| Vendored OpenAI spec | `docs/reference/openai-openapi.yml` `info.version: 2.3.0` | Schema/examples for `/embeddings` | Source of truth for accepted request ids and Node SDK example payloads. |
| Generated OpenAI types | repo-generated `apps/api/src/types/openai.d.ts` | Compile-time request/response contract reference | Use when updating request model ids and response `model` identity expectations. |
| Existing OpenAI-compatible provider transport | repo code in [`openai-compatible-client.ts`](../../../apps/api/src/providers/openai-compatible-client.ts) | Upstream `/embeddings` HTTP client | Reuse for provider dispatch instead of adding a one-off embeddings transport. |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Central alias normalization in `resolveModelAlias()` + `ModelService.findById()` | Route-local string translation in [`embeddings.ts`](../../../apps/api/src/routes/embeddings.ts) | Route-local fixes drift immediately from models list/retrieve and any other runtime callers. |
| Official `openai` SDK regression tests | `fetch`-only integration tests | `fetch` will not catch SDK parsing/contract mismatches that matter for this milestone. |
| Existing OpenAI-compatible provider client | Bespoke embeddings-only HTTP helper | Duplicates auth, retry, and error-shaping logic with no phase-level benefit. |

**Installation:**
```bash
pnpm install
```

No new packages are required for this phase.

**Version verification:** Verified on 2026-03-22 with `npm view ... version time --json`.
- `openai`: `6.32.0`, published 2026-03-17
- `vitest`: `4.1.0`, published 2026-03-12
- `fastify`: `5.8.2`, published 2026-03-07
- `@fastify/type-provider-typebox`: `6.1.0`, published 2025-10-19

## Architecture Patterns

### Recommended Project Structure

```text
apps/api/src/
├── config/model-aliases.ts       # Public model-id normalization
├── domain/model-service.ts       # Public catalog + canonical lookup
├── runtime/services.ts           # Real embeddings execution + usage/header shaping
├── providers/registry.ts         # Provider dispatch + provider/public identity split
└── routes/embeddings.ts          # Thin route; no alias logic

apps/api/test/
├── openai-sdk-regression.test.ts # Official SDK acceptance coverage
├── helpers/test-app.ts           # Mock harness; useful, but insufficient alone
├── domain/                       # Add runtime/model catalog coverage here
└── providers/                    # Add registry embeddings coverage here
```

### Pattern 1: Canonical Public Embeddings ID

**What:** Treat `text-embedding-3-small` as the canonical public catalog id and alias `text-embedding-ada-002` to it before catalog lookup. Do not keep `openai/text-embedding-3-small` as the public canonical id.

**When to use:** Everywhere public embeddings ids cross the API boundary: request lookup, models serialization, response bodies, and `x-model-routed`.

**Example:**
```typescript
// Source: docs/reference/openai-openapi.yml + apps/api/src/config/model-aliases.ts + apps/api/src/domain/model-service.ts
const PUBLIC_EMBEDDING_MODEL_ID = "text-embedding-3-small";

export const MODEL_ALIASES: Record<string, string> = {
  "text-embedding-ada-002": PUBLIC_EMBEDDING_MODEL_ID,
};

const MODELS: GatewayModel[] = [
  {
    id: PUBLIC_EMBEDDING_MODEL_ID,
    object: "model",
    capability: "embedding",
    costType: "variable",
    created: 1705948997,
    pricing: { inputTokensPer1m: 2 },
  },
];
```

### Pattern 2: Resolve Once, in the Model Service

**What:** Keep request handlers thin. Alias resolution should happen through `ModelService.findById()`, so route handlers, runtime services, and any future model retrieval logic all agree on the same canonical id.

**When to use:** Any runtime path that accepts a public model id from a caller.

**Example:**
```typescript
// Source: apps/api/src/runtime/services.ts + apps/api/src/domain/model-service.ts
const model = this.models.findById(body.model);
if (!model || model.capability !== "embedding") {
  return { error: `Unknown embedding model: ${body.model}`, statusCode: 400 as const };
}

providerResult = await this.providerRegistry.embeddings(model.id, {
  input: body.input,
  encodingFormat: body.encoding_format,
  dimensions: body.dimensions,
  user: body.user,
});
```

### Pattern 3: Public Response Identity, Internal Provider Identity

**What:** `CreateEmbeddingResponse.model` and `x-model-routed` should expose the public id Hive is honoring. `x-provider-model` is the only place the provider-specific model id belongs.

**When to use:** On every successful embeddings response.

**Example:**
```typescript
// Source: phase context + apps/api/src/runtime/services.ts + apps/api/src/providers/registry.ts
return {
  statusCode: 200,
  body: {
    ...providerResult.body,
    model: model.id,
  },
  headers: {
    ...providerResult.headers,
    "x-model-routed": model.id,
    "x-provider-model": providerResult.providerModel,
    "x-actual-credits": String(creditsCost),
  },
};
```

### Pattern 4: Production-Path Regression Coverage

**What:** Use the official SDK against a Fastify app or runtime wiring that uses the real `ModelService` catalog. Mock provider responses if needed, but do not mock the model catalog for this acceptance boundary.

**When to use:** For the acceptance tests that prove the blind spot is closed.

**Example:**
```typescript
// Source: docs/reference/openai-openapi.yml node.js example + apps/api/test/openai-sdk-regression.test.ts
const client = new OpenAI({
  apiKey: "valid-api-key",
  baseURL: `${baseUrl}/v1`,
  maxRetries: 0,
});

const result = await client.embeddings.create({
  model: "text-embedding-3-small",
  input: "hello world",
});

expect(result.object).toBe("list");
expect(result.model).toBe("text-embedding-3-small");
```

### Anti-Patterns to Avoid

- **Route-local alias fixes:** Do not special-case `text-embedding-3-small` inside [`embeddings.ts`](../../../apps/api/src/routes/embeddings.ts); it will drift from models list/retrieve and other runtime callers.
- **Provider-model leakage in response bodies:** Do not pass `providerResult.body.model` through unchanged if it is provider-namespaced.
- **Mock-only acceptance evidence:** Do not accept a green SDK test that still depends on [`createMockServices`](../../../apps/api/test/helpers/test-app.ts) listing `text-embedding-3-small` directly.
- **Second catalog for embeddings only:** Do not create a parallel embeddings lookup table outside [`model-service.ts`](../../../apps/api/src/domain/model-service.ts).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Public alias handling | Inline `if (model === "...")` branches in routes/services | `resolveModelAlias()` + `ModelService.findById()` | Centralizes canonicalization and keeps models list/retrieve aligned with runtime lookup. |
| Provider/public identity mapping | Ad hoc header/body rewrites in each route | Runtime/service return shape that sets `model`, `x-model-routed`, and `x-provider-model` deliberately | Prevents provider ids from leaking into the public API and keeps DIFF headers coherent. |
| SDK compatibility verification | `fetch`-only happy-path tests | Official `openai` SDK regression tests | The milestone is about OpenAI SDK compatibility, not just HTTP reachability. |
| Upstream embeddings transport | A custom embeddings-only fetch layer | Existing [`OpenAICompatibleProviderClient.embeddings()`](../../../apps/api/src/providers/openai-compatible-client.ts) | Reuses auth, timeout, retry, and JSON parsing behavior already present in the provider layer. |

**Key insight:** The dangerous complexity is not vector math or request parsing. It is identity normalization across catalog, runtime, headers, and tests. One-off fixes create exactly the kind of drift that caused the current gap.

## Common Pitfalls

### Pitfall 1: Alias Map and Catalog Canonical ID Drift

**What goes wrong:** `resolveModelAlias()` and `MODELS[]` disagree about which id is canonical, so one path returns 200 while another returns 400 or leaks a provider id.

**Why it happens:** The alias map and the public catalog are currently maintained separately.

**How to avoid:** Define one canonical public embeddings id and assert both alias resolution and `ModelService.findById()` against it.

**Warning signs:** `CreateEmbeddingResponse.model` differs from the requested public id; `text-embedding-ada-002` works but `text-embedding-3-small` fails, or vice versa.

### Pitfall 2: Mock Harness Masks the Real Failure

**What goes wrong:** The SDK regression stays green because [`test-app.ts`](../../../apps/api/test/helpers/test-app.ts) directly exposes `text-embedding-3-small`, even though the real runtime catalog does not.

**Why it happens:** The Phase 11 regression app mounts `v1Plugin` with mock services and a mock model catalog.

**How to avoid:** Add at least one regression that uses the real `ModelService` catalog and runtime lookup path.

**Warning signs:** A test passes only when using `createMockServices`, or only the mock helper knows about `text-embedding-3-small`.

### Pitfall 3: Provider Model Leaks Across the API Boundary

**What goes wrong:** The response body or `x-model-routed` returns `openai/text-embedding-3-small`, violating the locked public contract.

**Why it happens:** [`ProviderRegistry.embeddings()`](../../../apps/api/src/providers/registry.ts) currently builds `body.model` from `result.providerModel`, not from the public catalog model id.

**How to avoid:** Treat provider model id as debug/internal metadata only; overwrite public response identity in the runtime service if necessary.

**Warning signs:** `x-provider-model` and `model` are identical provider-namespaced strings on success.

### Pitfall 4: Shared Provider Default Model Is Not Embeddings-Safe

**What goes wrong:** The runtime resolves the public embedding model correctly but then dispatches `/embeddings` using the provider's generic fallback model, which may be chat-oriented.

**Why it happens:** The repo has no dedicated embeddings env/model mapping today. [`providerModelMap`](../../../apps/api/src/runtime/services.ts) is provider-wide, and the README documents `OPENROUTER_MODEL` as chat configuration.

**How to avoid:** During implementation, verify the upstream model actually used for embeddings. If the shared provider model is not embeddings-capable, add a narrowly scoped embeddings mapping rather than assuming the alias fix alone is enough.

**Warning signs:** `x-provider-model` resolves to `openrouter/auto` or another chat model during embeddings requests, or the provider returns a 4xx complaining about model capability.

### Pitfall 5: No Targeted Runtime or Registry Tests

**What goes wrong:** A future refactor re-breaks embeddings aliasing because the only coverage lives in fixture tests or the mock SDK harness.

**Why it happens:** There are currently no dedicated embeddings tests under `apps/api/test/domain` or `apps/api/test/providers`.

**How to avoid:** Add one targeted model/runtime test and one provider-registry/provider-path test in addition to updating the SDK regression.

**Warning signs:** Search results show only schema/error tests and the mock SDK test touching `/v1/embeddings`.

## Code Examples

Verified patterns from official sources and current repo architecture:

### OpenAI Contract Example

```typescript
// Source: docs/reference/openai-openapi.yml (/embeddings x-oaiMeta node.js example)
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env["OPENAI_API_KEY"],
});

const createEmbeddingResponse = await client.embeddings.create({
  input: "The quick brown fox jumped over the lazy dog",
  model: "text-embedding-3-small",
});
```

### Central Alias + Public Catalog Canonicalization

```typescript
// Source: docs/reference/openai-openapi.yml + apps/api/src/config/model-aliases.ts + apps/api/src/domain/model-service.ts
const PUBLIC_EMBEDDING_MODEL_ID = "text-embedding-3-small";

export const MODEL_ALIASES: Record<string, string> = {
  "text-embedding-ada-002": PUBLIC_EMBEDDING_MODEL_ID,
};

findById(modelId: string): GatewayModel | undefined {
  const resolved = resolveModelAlias(modelId);
  return this.enabledModels().find((model) => model.id === resolved);
}
```

### SDK Regression That Actually Guards the Gap

```typescript
// Source: apps/api/test/openai-sdk-regression.test.ts + milestone audit guidance
const client = new OpenAI({
  apiKey: "valid-api-key",
  baseURL: `${baseUrl}/v1`,
  maxRetries: 0,
});

const result = await client.embeddings.create({
  model: "text-embedding-3-small",
  input: "hello world",
});

expect(result.object).toBe("list");
expect(result.model).toBe("text-embedding-3-small");
expect(result.data[0]?.object).toBe("embedding");
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Provider-namespaced embeddings ids exposed publicly | OpenAI-native public ids, provider ids only in debug/routing internals | Locked by Phase 12 context on 2026-03-22; matches OpenAI `/embeddings` examples | SDK callers can use standard ids without provider knowledge. |
| Mock-only SDK acceptance | Production-path runtime/catalog acceptance plus SDK regression | Milestone audit on 2026-03-22 exposed the blind spot | Prevents mock catalog drift from hiding real-runtime failures. |
| Provider-wide fallback model assumed safe for all capabilities | Capability-aware upstream verification for embeddings | Needed now; current code does not prove embeddings-safe upstream mapping | Prevents chat defaults from being sent to `/embeddings`. |

**Deprecated/outdated:**
- Publicly treating `openai/text-embedding-3-small` as the canonical embeddings id for Hive.
- Accepting Phase 11-style green coverage as sufficient when the only passing embeddings path is the mock catalog.

## Open Questions

1. **Does the real runtime need a dedicated embeddings upstream-model mapping?**
   - What we know: [`providerModelMap`](../../../apps/api/src/runtime/services.ts) is provider-wide, README only documents `OPENROUTER_MODEL` as chat configuration, and OpenAI provider code does not implement embeddings.
   - What's unclear: Whether deployed `OPENROUTER_MODEL` already points at an embeddings-capable model, or whether a narrow embeddings-specific mapping is required.
   - Recommendation: Verify the live/default upstream model during implementation. If it is chat-oriented, add a minimal embeddings-specific mapping without redesigning all provider configuration.

2. **Should `text-embedding-3-large` be accepted in this phase?**
   - What we know: The vendored OpenAI spec lists it as a valid request enum, but the phase context only locks `text-embedding-3-small` and compatibility alias `text-embedding-ada-002`.
   - What's unclear: Whether Hive has a configured production provider target and acceptable cost posture for `text-embedding-3-large`.
   - Recommendation: Keep it unsupported in Phase 12 unless a real provider target already exists; fail through the standard unknown-model path rather than guessing.

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | `vitest` (repo `^2.1.8`, npm latest `4.1.0`) |
| Config file | none — use [`apps/api/package.json`](../../../apps/api/package.json) test script |
| Quick run command | `pnpm --filter @hive/api exec vitest run src/config/__tests__/model-aliases.test.ts test/openai-sdk-regression.test.ts` |
| Full suite command | `pnpm --filter @hive/api test` |

### Phase Requirements -> Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| DIFF-03 | `resolveModelAlias()` keeps `text-embedding-3-small` canonical and maps `text-embedding-ada-002` to it | unit | `pnpm --filter @hive/api exec vitest run src/config/__tests__/model-aliases.test.ts` | `✅` update existing |
| DIFF-03 | Real `ModelService` lookup accepts `text-embedding-3-small` and resolves compatibility alias through the production catalog | unit | `pnpm --filter @hive/api exec vitest run test/domain/runtime-embeddings-alias.test.ts` | `❌` Wave 0 |
| DIFF-03 | Runtime success path returns public response identity (`model`, `x-model-routed`) while preserving provider identity in `x-provider-model` | integration | `pnpm --filter @hive/api exec vitest run test/domain/runtime-embeddings-alias.test.ts` | `❌` Wave 0 |
| DIFF-03 | Official SDK `client.embeddings.create({ model: "text-embedding-3-small" })` succeeds through the non-mock catalog/runtime path | integration | `pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts -t \"embeddings.create\"` | `✅` needs rewrite |

### Sampling Rate

- **Per task commit:** `pnpm --filter @hive/api exec vitest run src/config/__tests__/model-aliases.test.ts test/domain/runtime-embeddings-alias.test.ts`
- **Per wave merge:** `pnpm --filter @hive/api test`
- **Phase gate:** Full API suite green plus Docker API build green: `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`

### Wave 0 Gaps

- [ ] `apps/api/test/domain/runtime-embeddings-alias.test.ts` — covers public catalog lookup, runtime response identity, and alias acceptance without the mock catalog.
- [ ] `apps/api/test/providers/provider-registry-embeddings.test.ts` — covers embeddings dispatch and provider/public model identity split.
- [ ] `apps/api/test/openai-sdk-regression.test.ts` — existing file must stop relying on the mock `models.findById()` list for embeddings acceptance.

## Sources

### Primary (HIGH confidence)

- `docs/reference/openai-openapi.yml` — vendored OpenAI `/embeddings` schema, examples, and accepted model ids
- `apps/api/src/types/openai.d.ts` — generated OpenAI request type shows `text-embedding-ada-002`, `text-embedding-3-small`, and `text-embedding-3-large`
- `apps/api/src/config/model-aliases.ts` — current alias map and canonicalization entry point
- `apps/api/src/domain/model-service.ts` — current public catalog id and alias-aware lookup behavior
- `apps/api/src/runtime/services.ts` — real embeddings runtime path and provider registry wiring
- `apps/api/src/providers/registry.ts` — current response/body/header shaping for embeddings dispatch
- `.planning/v1.0-MILESTONE-AUDIT.md` — confirmed cross-phase evidence for the real-runtime alias gap
- https://platform.openai.com/docs/api-reference/embeddings/create — official embeddings endpoint reference
- https://platform.openai.com/docs/models/text-embedding-3-large — current official embeddings model documentation
- https://www.npmjs.com/package/openai — package page; current version verified via `npm view openai version time --json`
- https://www.npmjs.com/package/vitest — package page; current version verified via `npm view vitest version time --json`
- https://www.npmjs.com/package/fastify — package page; current version verified via `npm view fastify version time --json`
- https://www.npmjs.com/package/@fastify/type-provider-typebox — package page; current version verified via `npm view @fastify/type-provider-typebox version time --json`
- https://github.com/openai/openai-node — official JavaScript/TypeScript SDK repository
- https://openrouter.ai/docs/api/reference/overview — official OpenRouter docs; model routing expects provider-prefixed upstream model ids

### Secondary (MEDIUM confidence)

- `README.md` provider environment section — confirms there is no dedicated embeddings env mapping today
- `.planning/phases/11-real-openai-sdk-regression-tests-ci-style-e2e/11-VERIFICATION.md` — useful background on current SDK test shape and blind spots

### Tertiary (LOW confidence)

- None

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - verified from repo manifests plus live `npm view` package metadata on 2026-03-22
- Architecture: MEDIUM - core alias/catalog gap is confirmed, but the embeddings upstream-model wiring risk is an inference from current runtime code and env docs rather than a live runtime capture
- Pitfalls: HIGH - supported by the milestone audit, current code paths, and missing targeted test coverage

**Research date:** 2026-03-22
**Valid until:** 2026-03-29
