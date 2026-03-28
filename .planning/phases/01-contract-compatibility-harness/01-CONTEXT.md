# Phase 1: Contract & Compatibility Harness - Context

**Gathered:** 2026-03-28
**Status:** Ready for planning

<domain>
## Phase Boundary

Define Hive's public OpenAI-facing compatibility contract for the launch-era product, import and version the source contract, publish an explicit endpoint support matrix for the public non-org/admin surface, enforce OpenAI-style unsupported behavior for anything outside the currently supported subset, generate Swagger/OpenAPI docs for the implemented surface, and create a compatibility harness that proves official SDK behavior instead of approximating it.

</domain>

<decisions>
## Implementation Decisions

### Launch coverage bar
- Publish an endpoint-by-endpoint matrix for the full public non-org/admin OpenAI surface rather than a partial or family-only summary.
- Treat the long-term launch target as a near-full public mirror, even though the `supported now` subset in Phase 1 will remain narrow.
- Use four explicit statuses in the public matrix: `supported now`, `planned for launch`, `explicitly unsupported at launch`, and `out of scope` for org/admin endpoints.
- The support matrix must distinguish future launch intent from current implementation status; Phase 1 must not blur those together.

### Unsupported behavior contract
- Any public endpoint marked as not currently supported must return strict OpenAI-style unsupported errors rather than best-effort fallbacks or generic placeholder failures.
- Unsupported parameters, modes, or feature combinations on otherwise-supported endpoints must also fail explicitly with OpenAI-style errors instead of being silently ignored.
- Error messaging should be customer-clear but provider-blind: explain what capability is unavailable without exposing upstream provider identity or internal routing constraints.
- The published support matrix is authoritative for runtime behavior; if the matrix says a capability is unsupported or only planned, the runtime must reject it consistently until the matrix changes.

### Compatibility proof bar
- Phase 1 should use a deep compatibility verification standard for official OpenAI JavaScript/TypeScript, Python, and Java SDKs rather than minimal smoke tests.
- Streaming compatibility must be proven with golden regression cases that cover event ordering, chunk shape, terminal events, and interruption or failure behavior.
- Compatibility proof must include error-path and unsupported-path fidelity, including HTTP status behavior, error object shape, compatibility headers, and explicit unsupported responses.
- A failing compatibility harness blocks Hive from claiming the affected endpoint or status as supported.

### Docs and support matrix format
- Public documentation should expose an endpoint-by-endpoint reference table rather than relying on prose or family-only summaries.
- Each matrix row should include the endpoint or method, current status, brief support notes, and later-phase linkage when full implementation belongs to a later phase.
- Endpoint support and model support should be treated as separate views; model-level readiness or health must not be mixed into the endpoint matrix.
- Swagger/OpenAPI is the source for request and response shape, but the support matrix is the authoritative source of support status.

### Claude's Discretion
- No additional product-scope decisions were delegated during discussion.
- Downstream agents may choose the exact codegen tools, test harness structure, documentation rendering approach, and internal implementation details as long as they preserve the matrix/status model, provider-blind unsupported behavior, and the high compatibility proof bar above.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope and requirements
- `.planning/ROADMAP.md` § "Phase 1: Contract & Compatibility Harness" — Defines the phase goal, success criteria, and plan breakdown for the compatibility harness.
- `.planning/REQUIREMENTS.md` § "Compatibility & Contract" — Defines `COMP-01`, `COMP-02`, and `COMP-03`.
- `.planning/REQUIREMENTS.md` § "Inference Surface" — Defines `API-08`, which requires explicit unsupported behavior for public endpoints outside the implemented launch subset.
- `.planning/PROJECT.md` § "Context" — States the product promise of mirroring the public OpenAI API surface except org/admin endpoints.
- `.planning/PROJECT.md` § "Constraints" — Locks Docker-only development, provider abstraction, privacy posture, and compatibility expectations.
- `.planning/STATE.md` § "Accumulated Context" — Carries forward project-level constraints already accepted for the current phase.

### Research that should shape planning
- `.planning/research/SUMMARY.md` — Recommends a contract-first compatibility architecture and explains why the support matrix and SDK regression harness must come first.
- `.planning/research/ARCHITECTURE.md` — Describes the recommended `packages/openai-contract` and `packages/sdk-tests` structure and the contract-first public edge approach.
- `.planning/research/STACK.md` — Recommends the Docker-only toolchain, Go OpenAPI codegen options, and SDK compatibility harness tooling expectations.
- `.planning/research/PITFALLS.md` — Highlights "compatibility by approximation" as the main Phase 1 failure mode and reinforces explicit unsupported behavior.
- `.planning/research/FEATURES.md` — Explains why official SDK compatibility and endpoint capability classification are launch-critical dependencies.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- No application code exists yet; the repository is currently a planning and research scaffold.
- The most reusable current assets are the research documents that already define the contract-first approach, recommended structure, and compatibility risks.

### Established Patterns
- The project is greenfield, so there are no existing implementation patterns to preserve at the code level.
- Project-level decisions already lock a Docker-only developer workflow and a contract-first architecture for public API compatibility.
- The repo's planning structure is already organized around phased delivery, so Phase 1 outputs should become the canonical contract baseline for later phases.

### Integration Points
- Phase 1 decisions will shape the first implementation work in the future `packages/openai-contract` area described in `.planning/research/ARCHITECTURE.md`.
- The compatibility harness and regression suites should seed the future `packages/sdk-tests` area described in `.planning/research/ARCHITECTURE.md`.
- The support matrix and generated Swagger/OpenAPI docs will become the contract boundary that later endpoint implementation phases must obey.

</code_context>

<specifics>
## Specific Ideas

- The support matrix should cover the full public non-org/admin surface even when current implementation is narrow.
- Public support status must distinguish `supported now` from `planned for launch`; the matrix cannot flatten those into one bucket.
- Public errors and future model-support views must remain provider-blind.

</specifics>

<deferred>
## Deferred Ideas

- Add a provider-blind per-model health or support view separate from the endpoint matrix. This is valuable, but it is a separate capability from Phase 1's endpoint contract and documentation work.

</deferred>

---

*Phase: 01-contract-compatibility-harness*
*Context gathered: 2026-03-28*
