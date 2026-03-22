---
phase: 12-embeddings-alias-runtime-compliance
verified: 2026-03-22T09:19:45Z
status: passed
score: "8/8 must-haves verified"
---

# Phase 12: Embeddings Alias Runtime Compliance Verification Report

**Phase Goal:** Close the remaining embeddings alias gap so standard OpenAI SDK model IDs resolve in the real runtime catalog.
**Verified:** 2026-03-22T09:19:45Z
**Status:** passed
**Re-verification:** No — initial verification
**Tracking artifacts:** Preserved later-phase history. `.planning/ROADMAP.md` and `.planning/STATE.md` were not modified.

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
| --- | --- | --- | --- |
| 1 | The real model catalog accepts `text-embedding-3-small` as the canonical public embeddings id and still accepts `text-embedding-ada-002` as a compatibility alias. | ✓ VERIFIED | `apps/api/src/config/model-aliases.ts:6-14` maps the alias to `text-embedding-3-small`; `apps/api/src/domain/model-service.ts:145-181` catalogs only `text-embedding-3-small` and resolves aliases before lookup; `apps/api/test/domain/model-service.test.ts:35-45` covers both ids. |
| 2 | Successful embeddings responses expose `model: text-embedding-3-small` and `x-model-routed: text-embedding-3-small` while `x-provider-model` keeps the upstream provider id `openai/text-embedding-3-small`. | ✓ VERIFIED | `apps/api/src/providers/registry.ts:296-325` sends `openai/text-embedding-3-small` upstream but returns `model: modelId`, `x-model-routed: modelId`, and `x-provider-model: result.providerModel ?? providerModel`; `apps/api/test/providers/provider-registry.test.ts:63-74` asserts the split. |
| 3 | The public catalog and static compliance fixtures no longer present `openai/text-embedding-3-small` as the public response identity. | ✓ VERIFIED | `apps/api/src/domain/model-service.ts:145-150` uses `id: "text-embedding-3-small"`; `apps/api/src/routes/__tests__/embeddings-compliance.test.ts:6-13` and `apps/api/src/routes/__tests__/differentiators-headers.test.ts:82-86` use the canonical public id; no non-test route file exposes `openai/text-embedding-3-small` via `rg`. |
| 4 | Focused unit and provider regressions fail if the alias map, public catalog id, or provider/public model split regresses. | ✓ VERIFIED | `apps/api/src/config/__tests__/model-aliases.test.ts:17-47`, `apps/api/test/domain/model-service.test.ts:35-53`, and `apps/api/test/providers/provider-registry.test.ts:33-75` lock the alias, catalog, and provider-boundary behavior; the focused pack passed with 59/59 tests. |
| 5 | The official OpenAI Node SDK succeeds on `client.embeddings.create({ model: "text-embedding-3-small" })` against a Fastify app backed by the real `ModelService` and `RuntimeAiService` path. | ✓ VERIFIED | `apps/api/test/openai-sdk-regression.test.ts:114-181` builds a dedicated app with `new ModelService()`, `new RuntimeAiService(...)`, and `createTestAppWithServices(...)`; `pnpm --filter @hive/api exec vitest run ... test/openai-sdk-regression.test.ts` passed. |
| 6 | The embeddings SDK success case no longer depends on `createMockServices()` listing `text-embedding-3-small` directly. | ✓ VERIFIED | `apps/api/test/helpers/test-app.ts:275-286` lists only `mock-chat` and `dall-e-3`; `apps/api/test/helpers/test-app.ts:299-312` provides `createTestAppWithServices()` so the SDK regression uses real runtime services instead of helper-owned embeddings catalog entries. |
| 7 | The regression proves the provider call still uses the upstream id `openai/text-embedding-3-small` while the SDK-visible response model stays `text-embedding-3-small`. | ✓ VERIFIED | `apps/api/test/openai-sdk-regression.test.ts:169-178` asserts `result.model === "text-embedding-3-small"` and that the fake provider client received `model: "openai/text-embedding-3-small"`. |
| 8 | The updated regression suite, full API suite, and Docker-only API build pass after the blind spot is removed. | ✓ VERIFIED | `pnpm --filter @hive/api exec vitest run src/config/__tests__/model-aliases.test.ts test/domain/model-service.test.ts test/providers/provider-registry.test.ts src/routes/__tests__/embeddings-compliance.test.ts src/routes/__tests__/differentiators-headers.test.ts test/openai-sdk-regression.test.ts` passed (`6` files, `59` tests); `pnpm --filter @hive/api test` passed (`69` files, `368` tests); `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` passed. |

**Score:** 8/8 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `apps/api/src/config/model-aliases.ts` | Canonical embeddings alias map | ✓ VERIFIED | Exists, contains `"text-embedding-ada-002": "text-embedding-3-small"`, and is consumed by `ModelService.findById()`. |
| `apps/api/src/domain/model-service.ts` | Canonical public embeddings catalog entry | ✓ VERIFIED | Exists, catalogs `id: "text-embedding-3-small"`, and resolves aliases before catalog lookup. |
| `apps/api/src/providers/registry.ts` | Embeddings provider/public model separation | ✓ VERIFIED | Exists, contains `EMBEDDING_PROVIDER_MODEL_MAP`, dispatches embeddings upstream on `openai/text-embedding-3-small`, and returns the public id externally. |
| `apps/api/test/providers/provider-registry.test.ts` | Embeddings upstream-model regression coverage | ✓ VERIFIED | Exists, substantively asserts upstream request model, public response model, `x-model-routed`, `x-provider-model`, and `providerModel`. |
| `apps/api/src/runtime/services.ts` | Exported `RuntimeAiService` with test-instantiable dependency surface | ✓ VERIFIED | Exists, exports `RuntimeAiService`, defines the narrowed collaborator aliases, and calls `providerRegistry.embeddings(model.id, ...)` from the real runtime path. |
| `apps/api/test/helpers/test-app.ts` | Shared Fastify helper that accepts provided services without masking the embeddings catalog | ✓ VERIFIED | Exists, exposes `createTestAppWithServices()`, registers `v1Plugin`, and no longer lists `text-embedding-3-small` in the helper-owned model catalog. |
| `apps/api/test/openai-sdk-regression.test.ts` | Real-runtime embeddings SDK regression | ✓ VERIFIED | Exists, wires `ModelService`, `ProviderRegistry`, and `RuntimeAiService` together and exercises `client.embeddings.create()` through the real route. |

### Key Link Verification

| From | To | Via | Status | Details |
| --- | --- | --- | --- | --- |
| `apps/api/src/domain/model-service.ts` | `apps/api/src/config/model-aliases.ts` | `findById` resolves aliases before catalog lookup | ✓ WIRED | `const resolved = resolveModelAlias(modelId)` at `apps/api/src/domain/model-service.ts:179-181`. |
| `apps/api/src/providers/registry.ts` | upstream embeddings provider model | embeddings-specific upstream model mapping | ✓ WIRED | `EMBEDDING_PROVIDER_MODEL_MAP` at `apps/api/src/providers/registry.ts:82-84` feeds `providerModel` at `apps/api/src/providers/registry.ts:296-301`. |
| `apps/api/test/providers/provider-registry.test.ts` | `apps/api/src/providers/registry.ts` | real `ProviderRegistry.embeddings()` assertions | ✓ WIRED | The test at `apps/api/test/providers/provider-registry.test.ts:33-75` calls `registry.embeddings("text-embedding-3-small", ...)` and verifies the upstream/public split. |
| `apps/api/test/openai-sdk-regression.test.ts` | `apps/api/src/runtime/services.ts` | `new RuntimeAiService(...)` | ✓ WIRED | `apps/api/test/openai-sdk-regression.test.ts:141-160` instantiates the real runtime service for the embeddings regression. |
| `apps/api/test/openai-sdk-regression.test.ts` | `apps/api/src/domain/model-service.ts` | `new ModelService()` | ✓ WIRED | `apps/api/test/openai-sdk-regression.test.ts:115-116` uses the real model catalog instead of helper-owned mock catalog entries. |
| `apps/api/test/helpers/test-app.ts` | `apps/api/src/routes/v1-plugin.ts` | shared Fastify registration helper | ✓ WIRED | `await app.register(v1Plugin, { services: services as RuntimeServices })` at `apps/api/test/helpers/test-app.ts:299-310` wires provided services into the actual v1 routes. |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| --- | --- | --- | --- | --- |
| `DIFF-03` | `12-01-PLAN.md`, `12-02-PLAN.md` | Model aliasing — accept standard OpenAI model names and route to the best available provider. | ✓ SATISFIED | Alias resolution is implemented in `apps/api/src/config/model-aliases.ts:6-14` and `apps/api/src/domain/model-service.ts:179-181`; runtime/provider routing is enforced in `apps/api/src/providers/registry.ts:296-325` and `apps/api/src/runtime/services.ts:978-1031`; real-runtime SDK coverage exists in `apps/api/test/openai-sdk-regression.test.ts:114-181`; focused tests, full API tests, and Docker build all passed. |

No orphaned Phase 12 requirements were found in `.planning/REQUIREMENTS.md`. The traceability entry maps only `DIFF-03` to Phase 12, and both phase plans declare `DIFF-03`.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| None | - | No TODO/FIXME/placeholder/console-only stubs detected in touched implementation or regression files. | ℹ️ Info | No blocker or warning anti-patterns found during verification. |

### Human Verification Required

None. The phase goal is covered by static code verification, targeted regression coverage, the full API suite, and the Docker-container API build.

### Gaps Summary

None. The codebase now accepts the standard embeddings id `text-embedding-3-small` through the real runtime catalog, preserves `text-embedding-ada-002` as a compatibility alias, keeps provider namespacing behind `x-provider-model`, and proves the behavior through a real-runtime OpenAI SDK regression.

---

_Verified: 2026-03-22T09:19:45Z_
_Verifier: Claude (gsd-verifier)_
