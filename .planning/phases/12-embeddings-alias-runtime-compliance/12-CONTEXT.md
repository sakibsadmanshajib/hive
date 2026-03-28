# Phase 12: Embeddings Alias Runtime Compliance - Context

**Gathered:** 2026-03-22
**Status:** Ready for planning

<domain>
## Phase Boundary

Close the remaining embeddings alias gap so the real runtime behaves like an OpenAI-compatible API for embeddings model ids. This phase covers public embeddings model identity, alias resolution, real-runtime request handling, response identity, and regression coverage for the official `openai` npm SDK path.

This phase does not broaden into a full cross-endpoint catalog redesign. It applies the OpenAI-first contract to embeddings specifically.

</domain>

<decisions>
## Implementation Decisions

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

</decisions>

<specifics>
## Specific Ideas

- "Do what OpenAI does" means the public embeddings contract should look OpenAI-native, not provider-native.
- The caller should be able to use `text-embedding-3-small` through the official OpenAI SDK without needing to know Hive's provider catalog internals.
- Provider names such as `openai/...` should stay behind the API boundary unless they are intentionally exposed through routing/debug headers.
- For embeddings specifically, the current real-runtime/provider catalog mismatch is the gap to close, not a reason to widen public provider namespacing.

</specifics>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements and roadmap
- `.planning/ROADMAP.md` §Phase 12 — Scope, gap-closure statement, and success criteria for embeddings alias runtime compliance
- `.planning/REQUIREMENTS.md` — DIFF-03 requirement mapping and milestone traceability

### Prior phase decisions
- `.planning/phases/04-models-endpoint/04-CONTEXT.md` — Earlier public-catalog identity decisions and OpenAI-facing model-id expectations
- `.planning/phases/07-surface-expansion/07-CONTEXT.md` — Embeddings route/runtime/provider pipeline and SDK compatibility boundary
- `.planning/phases/08-differentiators/08-CONTEXT.md` — Static alias-map decision, alias resolution before provider dispatch, and pass-through philosophy
- `.planning/phases/11-real-openai-sdk-regression-tests-ci-style-e2e/11-RESEARCH.md` — SDK regression strategy and intended real-client coverage
- `.planning/phases/11-real-openai-sdk-regression-tests-ci-style-e2e/11-VERIFICATION.md` — Evidence that the SDK suite existed while still leaving room for a real-runtime alias blind spot

### OpenAI reference
- `docs/reference/openai-openapi.yml` — OpenAI embeddings request/response schema and examples for `text-embedding-3-small` and `text-embedding-ada-002`
- `apps/api/src/types/openai.d.ts` — Generated OpenAI types showing accepted embeddings request ids and response contract

### Existing implementation
- `apps/api/src/config/model-aliases.ts` — Current alias map, including `text-embedding-ada-002`
- `apps/api/src/domain/model-service.ts` — Public catalog identity, alias-aware lookup, and current embeddings model entry
- `apps/api/src/runtime/services.ts` — Real runtime embeddings path that resolves the model then calls the provider registry
- `apps/api/src/providers/registry.ts` — Embeddings provider dispatch and DIFF header source values
- `apps/api/test/openai-sdk-regression.test.ts` — Current official SDK regression suite
- `apps/api/test/helpers/test-app.ts` — Mock catalog that currently masks the real-runtime embeddings alias gap

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `resolveModelAlias()` in `apps/api/src/config/model-aliases.ts` already provides a single alias entry point for model-id normalization.
- `ModelService.findById()` in `apps/api/src/domain/model-service.ts` already resolves aliases before catalog lookup.
- `RuntimeAiService.embeddings()` in `apps/api/src/runtime/services.ts` already follows the route -> model lookup -> provider registry pattern used by the real runtime.
- `ProviderRegistry.embeddings()` in `apps/api/src/providers/registry.ts` already separates routed-model identity from provider-model identity.

### Established Patterns
- Chat aliases already resolve to a canonical public-facing model id before the response body is built.
- DIFF headers separate routed model identity from provider execution details; this phase should reuse that boundary rather than inventing a new one.
- Unknown model ids fail at model lookup with OpenAI-compatible errors rather than being passed through unchecked.

### Integration Points
- `apps/api/src/domain/model-service.ts` is the critical public-catalog decision point for embeddings model identity.
- `apps/api/src/runtime/services.ts` is the real-runtime behavior that must accept the OpenAI-facing embeddings id.
- `apps/api/test/openai-sdk-regression.test.ts` and any real-runtime coverage added for this phase must prove the alias path through the production-style model/runtime wiring.
- `apps/api/test/helpers/test-app.ts` is a known masking point because its mock catalog currently includes `text-embedding-3-small` directly.

</code_context>

<deferred>
## Deferred Ideas

- Full audit and cleanup of all non-embeddings public model ids to remove or normalize provider namespacing everywhere — separate phase if desired
- Dynamic synchronization of the public model catalog from upstream provider inventories
- Broader reconsideration of the Phase 4 alias-per-entry public catalog strategy outside the embeddings scope

</deferred>

---

*Phase: 12-embeddings-alias-runtime-compliance*
*Context gathered: 2026-03-22*
