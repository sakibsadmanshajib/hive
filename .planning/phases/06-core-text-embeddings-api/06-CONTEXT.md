# Phase 6: Core Text & Embeddings API - Context

**Gathered:** 2026-04-02
**Status:** Ready for planning

<domain>
## Phase Boundary

Deliver the main OpenAI-compatible inference endpoints used by agents and developer workflows: `POST /v1/responses`, `POST /v1/chat/completions`, `POST /v1/completions`, and `POST /v1/embeddings`, with compatible streaming, structured outputs, tool calling, and reasoning behavior where the OpenAI contract exposes them. This phase does not expand into stored retrieval/update/delete flows for `responses` or `chat.completions`, and it does not broaden into file, media, audio, or async APIs from later phases.

</domain>

<decisions>
## Implementation Decisions

### Text capabilities and request-surface fidelity
- The initial Phase 6 rollout includes function and tool calling on supported text endpoints; this is not deferred to a later hardening pass.
- Structured outputs are first-class from the start. JSON-mode and JSON-schema-shaped behavior should be supported anywhere the OpenAI endpoint contract exposes them.
- Hive should support the broader OpenAI text request surface up front rather than shipping an artificially narrowed "plain text only" subset.
- Capability mismatches must hard-fail with OpenAI-style errors. Hive should not silently degrade a request to plain text or weaker behavior when the requested contract cannot be honored.
- Model capability tags and public capability communication should stay authoritative enough that users can tell which aliases support which behaviors, while remaining OpenAI-compliant and provider-blind.

### Endpoint-family posture
- Hive should mirror OpenAI behavior per endpoint family instead of inventing a Hive-specific convergence layer across `responses`, `chat/completions`, and `completions`.
- Where OpenAI exposes meaningful differences between `responses`, `chat/completions`, and legacy `completions`, Hive should preserve those differences instead of flattening them.
- Alias exposure and runtime enforcement should follow the practical OpenAI/OpenRouter posture the user wants: expose broadly where that is normal, but fail strictly on unsupported endpoint-capability combinations.
- Stored retrieval, update, delete, and input-item listing flows for `responses` and `chat.completions` remain out of scope for this phase even though create/inference flows are in scope.

### Streaming and reasoning behavior
- On supported routes, Hive should aim for the richest OpenAI-style streaming behavior from day one, including usage-bearing terminal chunks and lifecycle-style events where the endpoint contract supports them.
- Reasoning-visible behavior must mirror OpenAI semantics rather than leaking provider-native reasoning traces or extra vendor-specific fields.
- Requests that ask for reasoning controls or reasoning-visible output on unsupported aliases should follow OpenAI-compatible strict behavior, not Hive-defined fallback behavior.
- Interrupted and failed stream termination should replicate OpenAI's terminal behavior as closely as possible, even when upstream providers are messy internally.

### Embeddings compatibility
- `/v1/embeddings` should follow the OpenAI request surface rather than a reduced Hive-specific subset, including the common input shapes and options OpenAI exposes.
- Options such as `dimensions` and `encoding_format` should be supported wherever the OpenAI contract allows them, and should hard-fail in OpenAI style when a selected alias cannot honor them.
- Alias exposure versus request-time failure should follow the same practical posture chosen for text endpoints: broad exposure where appropriate, strict runtime failure for unsupported combinations.
- Embeddings capability visibility should track the practical OpenAI-level behavior the user expects, not a custom Hive-specific compatibility story.

### Claude's Discretion
- Downstream agents can choose the exact internal translation layer, adapter shims, reservation/finalization orchestration, and SSE normalization internals as long as the public behavior stays OpenAI-compatible and provider-blind.
- Downstream agents can choose the exact catalog/tag schema and customer-facing capability wording as long as the resulting public surface remains compatible, explicit about supported behaviors, and faithful to the underlying alias capability matrix.
- Downstream agents can decide how much behavior is passed through versus translated per provider, provided the user-facing contract still matches OpenAI semantics and unsupported combinations fail explicitly.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope and product requirements
- `.planning/ROADMAP.md` § "Phase 6: Core Text & Embeddings API" — Defines the phase goal, success criteria, and the 06-01 through 06-03 plan split.
- `.planning/REQUIREMENTS.md` § "Inference Surface" — Defines `API-01`, `API-02`, `API-03`, and `API-04`.
- `.planning/PROJECT.md` § "Context" — Locks the drop-in OpenAI-compatible product promise, provider-blind public aliases, reasoning support expectations, and the no-transcript-storage rule.
- `.planning/PROJECT.md` § "Constraints" — Locks compatibility, privacy, provider abstraction, hot-path performance, hosted Supabase, and Docker-only workflow expectations.
- `.planning/STATE.md` § "Accumulated Context" — Carries forward the already accepted constraints and the current routing/accounting/hot-path context.

### Carry-forward decisions from earlier phases
- `.planning/phases/01-contract-compatibility-harness/01-CONTEXT.md` — Establishes the compatibility proof bar, the authority of the support matrix, and strict OpenAI-style unsupported behavior for unsupported endpoints and parameters.
- `.planning/phases/03-credits-ledger-usage-accounting/03-CONTEXT.md` — Establishes reserve-then-finalize accounting, customer-favoring ambiguous settlement, provider-blind customer reporting, and privacy-safe usage capture.
- `.planning/phases/04-model-catalog-provider-routing/04-CONTEXT.md` — Establishes stable Hive aliases, public capability badges, the internal provider capability matrix, and provider-blind routing/error behavior.
- `.planning/phases/05-api-keys-hot-path-enforcement/05-CONTEXT.md` — Establishes hot-path allowlist, budget, and rate-limit enforcement plus compatibility-first public failure behavior.

### Contract and compatibility artifacts
- `packages/openai-contract/upstream/openapi.yaml` — Canonical imported OpenAI contract for `responses`, `chat/completions`, `completions`, `embeddings`, streaming semantics, reasoning fields, tools, and structured-output request/response shapes.
- `packages/openai-contract/generated/hive-openapi.yaml` — The generated Hive contract artifact that downstream work must keep aligned with the imported OpenAI surface.
- `packages/openai-contract/matrix/support-matrix.json` — Current support classification for Phase 6 endpoints and the explicit out-of-scope status of stored retrieval/update/delete surfaces.

### Research and planning guidance
- `.planning/research/SUMMARY.md` § "Phase 5: Core Inference Surfaces" and adjacent architecture notes — Explains why core inference endpoints are the main drop-in value path and why compatibility must stay contract-first.
- `.planning/research/FEATURES.md` § "Reasoning/'thinking' compatibility" and "Feature Dependencies" — Connects reasoning support to the capability matrix and response-translation layer.
- `.planning/research/PITFALLS.md` § "Compatibility by Approximation" and "Surface expansion without capability matrix" — Documents the main failure modes for Phase 6 and the need for explicit endpoint-provider capability classification.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `apps/edge-api/cmd/server/main.go`: The public edge shell already wires compatibility headers, unsupported-endpoint middleware, catalog routes, and alias-aware authorization. Phase 6 should plug new endpoint handlers into this shell rather than inventing a parallel server path.
- `apps/edge-api/internal/authz/authorizer.go`, `apps/edge-api/internal/authz/client.go`, and `apps/edge-api/internal/authz/ratelimit.go`: Hot-path key snapshot resolution, budget checks, and OpenAI-style rate-limit responses already exist and should remain the inference gate.
- `apps/control-plane/internal/routing/service.go` and `apps/control-plane/internal/routing/types.go`: Routing already models capability needs for `responses`, `chat/completions`, `embeddings`, `streaming`, and `reasoning`, which is the direct seam for Phase 6 request shaping and hard-fail checks.
- `apps/control-plane/internal/apikeys/service.go`: Auth snapshots already project alias allowlists, budget state, and rate policies to the edge, so Phase 6 can consume this instead of building endpoint-specific policy plumbing.
- `apps/control-plane/internal/accounting/*` and `apps/control-plane/internal/usage/*`: Request attempts, reservation lifecycle state, streaming/interruption statuses, and privacy-safe usage events already exist and should be reused for inference settlement and stream finalization.
- `packages/openai-contract/*`: The imported OpenAI contract, generated Hive contract, and support matrix already define the public shape and support posture.
- `packages/sdk-tests/*`: The current SDK suites already assert unsupported and compatibility-header behavior, giving Phase 6 a ready place to add supported endpoint and streaming regressions.

### Established Patterns
- The edge API keeps the public compatibility layer thin and explicit, with support-matrix middleware deciding whether a request reaches a handler at all.
- Control-plane code follows repository/service/http layering with internal JSON endpoints for catalog, routing, usage, accounting, and API-key snapshot resolution.
- Provider capability differences are already represented explicitly in catalog/routing data, not inferred ad hoc inside request handlers.
- Public failures are already expected to be OpenAI-shaped and provider-blind, even when internal routing and provider metadata are richer.
- Hot-path enforcement already resolves key policy first, then applies allowlist, budget, and rate-limit checks before route selection or upstream dispatch.

### Integration Points
- Phase 6 endpoint handlers belong in `apps/edge-api` under the existing `/v1/*` compatibility surface, behind the current authz and compatibility middleware stack.
- Route selection for text and embeddings requests should extend the existing control-plane routing selector rather than bypassing it.
- Request execution must hook into the existing request-attempt, reservation, finalize, and usage-event primitives so streaming, retries, failures, and reasoning-token accounting stay financially consistent.
- Public capability communication should extend the existing catalog and alias capability badge system from Phase 4 so users can tell which aliases support tools, structured outputs, streaming, reasoning, and embeddings behaviors.
- SDK regression coverage should extend the existing `packages/sdk-tests` suites so supported Phase 6 behavior is verified the same way earlier unsupported/contract behavior is already verified.

</code_context>

<specifics>
## Specific Ideas

- "Do exactly what the OpenAI API does" is the user's dominant preference across text endpoints, streaming, reasoning behavior, and embeddings behavior.
- The user explicitly wants function/tool calling included in the initial rollout, not deferred.
- The user explicitly wants structured outputs to be first-class in Phase 6.
- The user explicitly wants unsupported capability combinations to hard-fail with OpenAI-style errors rather than degrade silently.
- The user wants model capability tags to remain authoritative enough that customers can tell which aliases support which behaviors.
- The user wants practical alias exposure and failure behavior to feel like OpenAI/OpenRouter, while still keeping the Hive public surface provider-blind and OpenAI-compliant.

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope.

</deferred>

---

*Phase: 06-core-text-embeddings-api*
*Context gathered: 2026-04-02*
