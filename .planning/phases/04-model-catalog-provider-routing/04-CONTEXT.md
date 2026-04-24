# Phase 4: Model Catalog & Provider Routing - Context

**Gathered:** 2026-03-31
**Status:** Ready for planning

<domain>
## Phase Boundary

Expose Hive-controlled public model aliases, pricing metadata, capability metadata, internal routing policies, and cache-aware usage attribution while keeping provider selection internal and provider-blind. This phase defines how models are named, presented, and routed; it does not expand into API-key governance, broader endpoint implementation, or full developer-console catalog UX from later phases.

</domain>

<decisions>
## Implementation Decisions

### Public alias identity
- Public aliases may use familiar vendor-or-model-family style IDs when that improves recognizability, but they must not reveal the actual routed provider or transport handle.
- Internal route handles such as `openrouter/free` and `openrouter/auto` stay private. Public convenience aliases like `hive-default` and `hive-auto` should be used instead where a raw upstream handle would leak routing details or pricing rationale.
- Hive should support an internal alias map so multiple public aliases can point to the same upstream model or route.
- Alias behavior should stay very stable over time. Hive may swap internals behind an alias, but only when the advertised behavior class remains materially the same.
- The launch catalog should stay curated rather than broad.

### Catalog and pricing surface
- Keep `GET /v1/models` OpenAI-compatible and lean. Do not add Hive-specific pricing payloads directly to the OpenAI model response shape.
- Publish richer Hive pricing and capability metadata through a separate Hive catalog surface or documentation path, with `/catalog` as the likely long-term public location.
- Public pricing should feel transparent, understandable, and predictable in the way users expect from OpenAI and other popular vendors.
- Public pricing should be denominated in Hive Credits only.
- Public visibility may include vetted aliases plus preview models; it should not dump every routable internal option into the public catalog.

### Capability metadata
- Public capability metadata should use curated badges rather than a dense feature matrix.
- Even when canonical IDs are model-like, guidance and descriptions should help users choose by outcome, such as cost, latency, stability, or reasoning quality.
- Capability claims must stay aligned with both the public support matrix and the internal provider capability matrix. Hive should not advertise capabilities that routing cannot reliably satisfy.

### Routing policy profiles
- Routing policy is configurable per alias rather than globally.
- Some aliases may be pinned to a specific internal route, while others may target named policy profiles such as `cost`, `latency`, `stability`, or a weighted composite like `weighted-h010`.
- For dynamic aliases, default behavior should be a preferred provider order with health-based failover rather than manual-only pinning or fully unconstrained per-request reshuffling.
- Cost, latency, stability, and weighted composite policies are all valid first-class routing modes, selected on an alias-by-alias basis.

### Fallback and failure handling
- First-layer fallback should preserve the alias contract. Automatic failover should only choose routes that keep the same advertised behavior class.
- A second fallback layer may widen selection when the alias policy explicitly allows it. The conservative default is to widen within the same price class before broader best-effort routing.
- If all eligible routes fail, Hive should return a provider-blind OpenAI-style error after internal fallback is exhausted.
- Do not silently degrade to a visibly weaker contract unless the alias policy explicitly allows that behavior.

### Cache-aware billing visibility
- Cache-aware billing should be publicly visible anywhere pricing is explained, not treated as an internal-only concern.
- Customer-facing usage reporting should expose separate `cache_read_tokens` and `cache_write_tokens` fields when they exist.
- For aliases or providers without cache-aware semantics, cache fields should appear only when applicable rather than always showing zeros.
- Public pricing and documentation should fully explain cache-aware billing behavior for eligible aliases.
- Cache-aware presentation should remain understandable: simple primary pricing plus explicit cache pricing or notes, not an opaque blended total that hides why billing changed.

### Claude's Discretion
- Exact schema shape for the alias map, route-policy configuration, and provider capability matrix.
- Exact policy-scoring inputs and thresholds inside named weighted policies such as `weighted-h010`.
- Exact preview labels, badge taxonomy, and catalog rendering details.
- Exact separation between runtime catalog endpoints and docs pages, so long as `/v1/models` remains OpenAI-compatible and richer Hive metadata is public somewhere in Phase 4 scope.
- Exact error copy and sanitization wording, so long as provider identity remains hidden from customers.

</decisions>

<specifics>
## Specific Ideas

- Example public convenience aliases: `hive-default`, `hive-auto`.
- Example policy-oriented aliases or profiles: `cost`, `latency`, `stability`, `weighted-h010`.
- Internal routing may use pooled or provider-specific routes such as `openrouter/free` or `openrouter/auto`, but those route handles and the underlying "free route" rationale must stay internal.
- Public pricing should be legible and predictable enough for cost-sensitive users to understand quickly, but still remain in Hive Credits rather than mixed currency displays.
- Cache-aware pricing should be explained publicly and paired with explicit cache token fields in usage views when supported.

</specifics>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope and product requirements
- `.planning/ROADMAP.md` § "Phase 4: Model Catalog & Provider Routing" — Defines the phase goal, success criteria, and plan breakdown for aliases, routing policy, capability matrices, and cache-aware attribution.
- `.planning/REQUIREMENTS.md` § "Model Catalog & Routing" — Defines `ROUT-01`, `ROUT-02`, and `ROUT-03`.
- `.planning/PROJECT.md` § "Context" — Defines public Hive aliases, provider-blind posture, pricing expectations, reasoning support expectations, and catalog visibility as part of the launch product.
- `.planning/PROJECT.md` § "Constraints" — Locks compatibility, provider abstraction, privacy, performance, hosted Supabase, and Docker-only workflow expectations.
- `.planning/STATE.md` § "Accumulated Context" — Carries forward accepted decisions about provider-blind customer surfaces, privacy-safe reporting, and current focus on Phase 4.

### Carry-forward context from earlier phases
- `.planning/phases/01-contract-compatibility-harness/01-CONTEXT.md` — Establishes OpenAI-compatible public behavior, explicit support classification, and provider-blind unsupported/error handling that Phase 4 must preserve.
- `.planning/phases/03-credits-ledger-usage-accounting/03-CONTEXT.md` — Establishes `model_alias` as a billing/reporting dimension, provider-blind customer reporting, and cache-token-aware usage goals that Phase 4 now needs to operationalize.

### Compatibility and architecture guidance
- `packages/openai-contract/upstream/openapi.yaml` § `/models` — Canonical OpenAI `GET /v1/models` contract that Hive should preserve rather than extend with Hive-only pricing fields.
- `.planning/research/SUMMARY.md` § "Phase 3: Model Catalog and Routing Policy" — Explains why aliasing and routing policy must land before public inference routing broadens.
- `.planning/research/ARCHITECTURE.md` § "Key Data Flows" item 3 — Defines the catalog flow that syncs upstream model metadata and provider costs into stable Hive aliases and pricing.
- `.planning/research/STACK.md` — Recommends LiteLLM-backed provider abstraction and explicitly rejects exposing raw provider model IDs as the public contract.
- `.planning/research/PITFALLS.md` § "Pitfall 3: Provider Leakage Through Public Surface" — Explains why raw upstream names, headers, and error strings cannot leak into public behavior.
- `.planning/research/PITFALLS.md` § "Surface expansion without capability matrix" — Reinforces that routing must consult an explicit provider capability matrix rather than treating support as binary.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `apps/edge-api/cmd/server/main.go`: Already exposes an OpenAI-shaped `/v1/models` list envelope, so Phase 4 can replace the empty data source without redefining the public response shape.
- `apps/edge-api/internal/matrix/types.go`: The endpoint support-matrix pattern already exists and can inform how an internal provider capability matrix is structured conceptually, while remaining separate from the public endpoint matrix.
- `apps/control-plane/internal/usage/types.go`: Usage events and request attempts already carry `model_alias`, `cache_read_tokens`, and `cache_write_tokens`.
- `apps/control-plane/internal/usage/http.go`: Customer-visible usage responses already expose `model_alias` and cache token fields, which gives Phase 4 a ready-made place to surface cache-aware attribution.
- `apps/control-plane/internal/accounting/types.go` and `apps/control-plane/internal/accounting/http.go`: Reservations are already keyed by `model_alias`, so alias stability directly affects reservation, settlement, and future policy work.

### Established Patterns
- Control-plane modules follow repository/service/http layering and account-scoped handlers resolved through `accounts.Service`.
- Control-plane persistence currently uses direct SQL repositories against `public.*` tables in Supabase Postgres; there is no in-repo migration framework yet.
- Edge API keeps the public compatibility layer thin and explicit. `/v1/models` is still a placeholder, and unsupported behavior is mediated by compat headers plus the support matrix middleware.
- Customer-visible reporting is already provider-blind by default, even when internal records retain richer diagnostics.

### Integration Points
- Phase 4 will replace the empty `/v1/models` placeholder with alias-backed data while preserving the Phase 1 compatibility contract.
- Alias and pricing decisions from Phase 4 become direct inputs to Phase 5 model allowlists/budgets and Phase 9 catalog, usage, and spend UX.
- Cache-aware billing decisions plug into the existing request-attempt, reservation, and usage-event records; Phase 4 should extend that path rather than inventing a separate accounting channel.
- The routing/catalog source of truth created here will be consumed later by edge dispatch and provider adapters, so Phase 4 needs a control-plane-owned alias and capability model rather than scattered provider config.

</code_context>

<deferred>
## Deferred Ideas

- Rich standalone developer-console catalog UX, browsing, and model discovery beyond the minimal public catalog needed in Phase 4 — later console/catalog work, likely Phase 9.
- Per-key model allowlists, model budgets, and key-level entitlement governance — Phase 5.
- Broader media, file, audio, and other endpoint-specific alias families beyond the initial curated Phase 4 catalog — Phases 6 and 7.
- Any customer-visible explanation of internal pooled-provider economics or "free route" rationale — explicitly out of scope for the public product surface.

</deferred>

---

*Phase: 04-model-catalog-provider-routing*
*Context gathered: 2026-03-31*
