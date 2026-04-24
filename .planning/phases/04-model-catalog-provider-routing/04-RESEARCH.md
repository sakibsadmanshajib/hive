# Phase 4: Model Catalog & Provider Routing - Research

**Researched:** 2026-03-31
**Domain:** Hive-owned model aliases, provider-blind catalog surfacing, internal routing policy, LiteLLM-backed fallback orchestration, and cache-aware usage attribution
**Confidence:** HIGH

## Summary

Phase 4 should establish a Hive-owned model catalog as a control-plane source of truth, then project only sanitized alias data into the public edge surface. The existing repo already has the two most important prerequisites:

1. `apps/edge-api` already owns the public OpenAI-compatible `/v1/models` route and compatibility headers, but it still returns an empty list.
2. `apps/control-plane` already owns account-scoped accounting and usage records, including `model_alias`, `cache_read_tokens`, and `cache_write_tokens`.

That means Phase 4 should not invent a separate product surface or a provider-facing public contract. It should:

1. Add a control-plane-owned catalog and routing schema that defines public aliases, internal route candidates, capability badges, pricing metadata, allowlist eligibility, and fallback policy.
2. Keep `GET /v1/models` strictly OpenAI-compatible by returning only model objects for public Hive aliases, not Hive-specific pricing payloads.
3. Expose richer Hive metadata through a separate catalog projection or documentation path, while using the same alias source of truth as `/v1/models`.
4. Normalize provider- or LiteLLM-specific routing and cache-usage details into Hive-controlled internal records and provider-blind customer-visible outputs.

**Primary recommendation:** plan Phase 4 as three linked deliverables matching the roadmap:

- `04-01`: create the alias catalog, pricing metadata, and public projections.
- `04-02`: build provider capability matrices, policy profiles, LiteLLM route groups, and fallback selection rules.
- `04-03`: map upstream cache token semantics into the existing usage/accounting schema and sanitize provider/model leakage in error and usage surfaces.

<user_constraints>

## User Constraints (from CONTEXT.md)

### Locked Decisions

- Public aliases may be recognizable, but they must not reveal the actual routed provider or raw transport handle.
- Internal route handles like `openrouter/free` and `openrouter/auto` stay private.
- Multiple public aliases may point to the same internal route or upstream model.
- Alias behavior should remain stable even if internals change.
- `GET /v1/models` must remain OpenAI-compatible and lean.
- Richer pricing and capability metadata should live in a separate Hive catalog/documentation surface, likely `/catalog`.
- Public capability metadata should be curated badges, not a dense provider matrix.
- Routing policy is configured per alias, not globally.
- Dynamic aliases should prefer ordered failover and health-based fallback over unconstrained per-request reshuffling.
- Automatic fallback must preserve the alias contract and only widen behavior when policy explicitly allows it.
- Provider failures must become provider-blind OpenAI-style errors.
- Customer-visible usage should expose `cache_read_tokens` and `cache_write_tokens` only when applicable.
- Public pricing and docs must explain cache-aware billing behavior for eligible aliases.

### Claude's Discretion

- Exact schema shape for alias tables, route-policy records, and provider capability matrices.
- Exact policy-profile scoring inputs for weighted strategies.
- Exact public catalog JSON shape outside the OpenAI `/v1/models` response.
- Exact cache attribution normalization logic for providers with different usage payloads.
- Exact wording of sanitized provider-blind error messages.

### Deferred Ideas (OUT OF SCOPE)

- Developer-console catalog browsing and richer UX beyond the minimal Phase 4 public catalog.
- Per-key model allowlists and budgets as user-configurable features.
- Broader endpoint-specific alias families for audio, image, file, and other surfaces.
- Customer-visible explanation of internal pooled-provider economics or "free route" rationale.

</user_constraints>

<phase_requirements>

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| ROUT-01 | Developer can list Hive-owned public model aliases, capabilities, and prices without seeing upstream provider identities. | Use a control-plane-owned alias catalog with a thin public projection. Keep `/v1/models` OpenAI-shaped and publish pricing/capability metadata through a separate Hive catalog surface or docs source generated from the same alias records. |
| ROUT-02 | Requests route only to internally approved providers and models that satisfy the alias capability matrix, fallback policy, and account or key allowlists. | Introduce explicit provider capability matrices, alias-level policy profiles, ordered fallback rules, and allowlist checks before any LiteLLM route group is selected. |
| ROUT-03 | When an upstream provider supports cache-aware billing semantics, Hive tracks and itemizes the related token categories without exposing the provider name. | Normalize provider-specific cache usage fields into the existing `cache_read_tokens` and `cache_write_tokens` fields already present in the control-plane usage schema, and keep provider identity internal-only. |

</phase_requirements>

## Repo Reality Check

### What already exists

- `apps/edge-api/cmd/server/main.go` serves `GET /v1/models`, but currently returns `{"object":"list","data":[]}`.
- `packages/openai-contract/upstream/openapi.yaml` already defines `/models` as returning a list of OpenAI model objects; the local SDK golden fixture for `models.list()` currently expects an empty list.
- `apps/control-plane/internal/usage/types.go` and `repository.go` already store `model_alias`, `cache_read_tokens`, and `cache_write_tokens`.
- `apps/control-plane/internal/accounting/*` already uses `model_alias` in reservation and finalization workflows.
- `deploy/docker/docker-compose.yml` already runs `edge-api`, `control-plane`, and `redis`, but no LiteLLM or provider adapter service exists yet.

### Important structural constraint

- `apps/edge-api` and `apps/control-plane` are separate Go modules listed in `go.work`.
- There is no shared `packages/domain` or internal service client today.
- `apps/edge-api` cannot and should not import `apps/control-plane/internal/...`.

**Implication:** Phase 4 planning must explicitly account for the cross-app contract between control-plane-owned catalog/routing state and the edge-owned `/v1/models` endpoint. Do not assume the edge can reach into control-plane internals directly.

## Standard Stack

### Core

| Technology | Version / Variant | Purpose | Why It Fits Phase 4 |
|------------|-------------------|---------|---------------------|
| Go | `1.24` in the current repo | Edge and control-plane implementation | Matches the current codebase and keeps Phase 4 in the existing services. |
| Hosted Supabase Postgres | Existing primary DB | Alias catalog, route candidates, policy profiles, and provider capability records | Catalog and routing policy are durable control-plane state, not ephemeral edge config. |
| LiteLLM Router / Proxy | Current official docs and project stack direction | Internal model-group abstraction, ordered fallback, retry, cooldown, and cost/routing helpers | Fits the repo's architecture research and reduces provider-specific routing code Hive must own. |
| Redis | Existing dependency and Compose service | Cooldowns, route health snapshots, or cached catalog snapshots if needed | Already present and aligned with LiteLLM router guidance. |
| Docker Compose | Existing repo workflow | Local edge/control-plane/provider-stack integration | Keeps routing and catalog verification inside the locked Docker-only workflow. |

### Supporting

| Library / Tool | Purpose | When to Use |
|----------------|---------|-------------|
| Existing OpenAI contract artifacts in `packages/openai-contract/` | Public `/v1/models` response shape and SDK behavior baseline | Reuse the existing contract source instead of hand-defining model-list payloads. |
| Existing `usage` and `accounting` packages | Cache-token and alias attribution persistence | Extend these types instead of inventing a second usage ledger. |
| LiteLLM config / route groups | Model-group aliasing, retries, and ordered fallback | Use internally after Hive alias policy has already chosen an eligible route set. |
| `go test` in edge/control-plane packages | Deterministic provider-blindness and routing-policy verification | Prefer package-level tests plus a thin integration layer over ad hoc manual checks. |

## Architecture Patterns

### Pattern 1: Control-Plane-Owned Catalog With Public Projections

**What:** Keep the canonical alias catalog in the control-plane and generate two projections from it:

- a minimal OpenAI-compatible `/v1/models` list for the edge
- a richer Hive catalog projection for pricing/capability metadata

**Why:** The context explicitly forbids stuffing Hive-specific pricing metadata into the OpenAI model object shape, while still requiring public alias and pricing visibility.

**Recommended data shape:**

- `model_aliases`
  - stable public alias ID
  - display name / summary
  - visibility state (`public`, `preview`, `internal`)
  - behavior class / support class
  - badge set and pricing metadata fields
- `model_routes`
  - alias-to-route-candidate mapping
  - provider identifier, provider model handle, LiteLLM group/model name
  - status / health / priority / price class / allowed fallback tier
- `provider_capabilities`
  - endpoint support
  - streaming, reasoning, cache semantics, structured output, tool use, etc.
- `routing_policies`
  - alias-level profile such as `cost`, `latency`, `stability`, or `weighted-*`
  - ordered preferred routes
  - widening rules and same-price-class boundaries

**Public projection rules:**

- `/v1/models` should return OpenAI model objects only.
- `id` must be the Hive alias, not the upstream provider model ID.
- `owned_by` should be Hive-controlled, not an upstream vendor name.
- richer pricing and badge metadata should come from a separate catalog endpoint or docs JSON generated from the same records.

### Pattern 2: Alias Contract First, Provider Choice Second

**What:** The request path should resolve the public alias into an internal behavior contract first, then choose an eligible provider route second.

**Why:** Provider-blindness only holds if public behavior is defined independently from the provider that happens to satisfy it.

**Recommended routing order:**

1. Validate requested alias exists and is allowed for the account or API key.
2. Load the alias policy profile and behavior class.
3. Filter internal route candidates by endpoint/capability requirements.
4. Filter again by account/key allowlists and route status.
5. Order the survivors by the alias policy profile.
6. Pass the chosen internal model group or route handle into LiteLLM.
7. If the route fails, use ordered fallback only within the allowed contract-preserving set.
8. If no eligible route succeeds, emit a provider-blind OpenAI-style failure.

**Important distinction:** LiteLLM's router and fallback support are useful implementation tools, but Hive still needs its own pre-LiteLLM capability and policy gate. Do not let a public alias map directly to "whatever LiteLLM can currently route."

### Pattern 3: Ordered Fallback, Not Silent Degradation

**What:** Use explicit ordered fallback groups for each alias, with widening only when the alias policy permits it.

**Why:** LiteLLM's current docs support ordered `fallbacks` from one model group to another after retries, but that mechanism alone does not enforce Hive's public contract. Hive must decide the eligible fallback set first.

**Recommendation:**

- represent fallback candidates as ordered route groups per alias
- group candidates by behavior class and price class
- default widening rule: stay inside the same behavior class and same price class
- optional widening: allow a second tier only when the alias explicitly permits it
- log the chosen internal route and fallback chain internally, never in customer-facing payloads

### Pattern 4: Explicit Cross-App Contract Between Edge and Control-Plane

**What:** Plan an explicit data path from control-plane catalog state to the edge's `/v1/models` implementation.

**Why:** The current repo has separate Go modules and no shared domain package or internal HTTP client.

**Recommended shape for Phase 4:**

- keep persistence and mutation logic in the control-plane
- add a narrow read projection for the edge, either:
  - an internal control-plane read endpoint consumed by an edge client with caching, or
  - a very small shared module dedicated to catalog projection types, added intentionally to `go.work`

**Preferred direction:** use a narrow control-plane read projection or snapshot client rather than teaching the edge to query Postgres directly. The edge should stay thin and should not become the owner of routing/catalog persistence.

### Pattern 5: Cache Token Normalization Into Existing Usage Schema

**What:** Normalize provider-specific cache usage fields into the control-plane's existing `cache_read_tokens` and `cache_write_tokens` fields.

**Why:** The repo already has a place to store cache token categories; Phase 4 should operationalize those fields instead of creating parallel accounting columns.

**Normalization rules:**

- map `cache_read_input_tokens` to `cache_read_tokens`
- map `cache_creation_input_tokens` to `cache_write_tokens`
- if a provider only exposes OpenAI-style `prompt_tokens_details.cached_tokens`, treat that as read-side cache attribution only
- only surface cache fields to customers when the underlying route/provider actually reported them
- keep raw provider usage payloads and provider names internal-only

**Practical implication:** route capability metadata should include whether a provider can emit cache-read and cache-write categories at all, so billing/reporting does not claim support that routing cannot satisfy.

### Pattern 6: Provider-Blind Error and Usage Translation

**What:** Any route or provider failure should be translated into Hive/OpenAI-style errors and provider-blind usage/reporting surfaces.

**Why:** The context and earlier phases already require provider identity to stay hidden from customers.

**Do not leak:**

- upstream vendor names in `/v1/models`
- provider model IDs in success payloads where Hive alias should appear
- raw upstream error bodies mentioning OpenRouter, Anthropic, Groq, etc.
- provider request IDs in customer-visible usage endpoints

**Safe internal retention:**

- provider route chosen
- provider request ID
- raw provider error and status
- fallback chain taken

## Recommended Plan Split

### Plan 04-01: Catalog source of truth and public projections

Focus on:

- schema or seeded config for alias catalog, pricing metadata, badges, and visibility
- a control-plane package that owns catalog reads
- edge `/v1/models` implementation backed by alias projection
- optional richer Hive catalog projection or docs JSON for pricing/capability metadata

### Plan 04-02: Capability matrix and routing policy engine

Focus on:

- provider capability records and route candidates
- alias-level policy profiles and allowlist evaluation
- LiteLLM route group definitions and ordered fallback rules
- tests proving provider selection happens only after policy/capability filtering

### Plan 04-03: Cache-aware attribution and provider sanitization

Focus on:

- normalization of cache token usage into the existing accounting pipeline
- internal-only provider diagnostics
- provider-blind error translation and usage/reporting responses
- regression tests that prevent provider leakage in catalog, usage, and error paths

## Common Pitfalls

### Pitfall 1: Using upstream model IDs as public alias IDs

That makes future provider changes customer-breaking and defeats the whole point of Phase 4.

### Pitfall 2: Letting `/v1/models` become a Hive-specific pricing document

The context is explicit that `/v1/models` must stay OpenAI-compatible and lean. Keep richer pricing/capability metadata separate.

### Pitfall 3: Routing without an explicit capability matrix

If routing only knows "preferred provider order," it will eventually pick a route that cannot satisfy the alias contract.

### Pitfall 4: Duplicating catalog truth across edge and control-plane

The current repo split means a cross-app contract is required. Do not hardcode alias data in the edge while a different version lives in control-plane or docs.

### Pitfall 5: Treating cache fields as always-zero generic columns

The context wants cache fields to appear only when applicable. The plan should avoid customer-visible "always present but meaningless" cache fields.

### Pitfall 6: Handing full fallback control to LiteLLM without Hive policy gating

LiteLLM can execute ordered fallback, but Hive must define which fallbacks preserve the alias contract before LiteLLM is invoked.

## Open Questions

These are planning questions, not blockers:

1. Should the richer Hive catalog surface be a control-plane JSON endpoint, an edge-served `/catalog` JSON projection, or a generated docs artifact?
   - Recommendation: choose one canonical machine-readable projection and generate docs from it, rather than maintaining parallel sources.
2. Should alias catalog data start as seeded SQL records or checked-in config files?
   - Recommendation: prefer durable control-plane records or seeded migrations, because Phase 5 and Phase 9 will need the same source of truth.
3. How should the edge consume catalog projections?
   - Recommendation: a narrow read projection with caching is better than direct database ownership in the edge.
4. How much LiteLLM configuration belongs in repo-managed config versus database-managed routing state?
   - Recommendation: keep Hive alias policy in control-plane-owned state; generate LiteLLM-facing route groups from that state rather than making raw LiteLLM config the primary business source of truth.

## Validation Architecture

### Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` for `apps/edge-api` and `apps/control-plane`, plus existing JS SDK list-model tests |
| **Config file** | `deploy/docker/docker-compose.yml` |
| **Quick run command** | `cd /home/sakib/hive && docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -count=1` |
| **Full suite command** | `cd /home/sakib/hive && docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -count=1 && docker compose -f deploy/docker/docker-compose.yml run --rm sdk-tests-js pnpm test -- --run tests/models/list-models.test.ts` |
| **Estimated runtime** | ~120 seconds once Phase 4 packages and fixtures exist |

### Phase Requirements to Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| ROUT-01 | `/v1/models` returns Hive alias IDs only and no upstream provider names | edge unit + SDK regression | `cd /home/sakib/hive && docker compose -f deploy/docker/docker-compose.yml run --rm sdk-tests-js pnpm test -- --run tests/models/list-models.test.ts` | Partial |
| ROUT-01 | richer catalog projection exposes Hive pricing and curated capability metadata without mutating the OpenAI model object shape | control-plane or edge unit | `cd /home/sakib/hive && docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -run TestCatalogProjection -count=1` | No -- Wave 0 |
| ROUT-02 | routing filters by alias capability rules, allowlists, and ordered fallback policy before choosing a LiteLLM model group | service unit | `cd /home/sakib/hive && docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -run TestSelectEligibleRoute -count=1` | No -- Wave 0 |
| ROUT-02 | fallback does not widen outside the alias contract unless explicitly allowed | service unit | `cd /home/sakib/hive && docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -run TestFallbackPolicyWidening -count=1` | No -- Wave 0 |
| ROUT-03 | provider cache usage fields are normalized into `cache_read_tokens` and `cache_write_tokens` | usage/accounting unit | `cd /home/sakib/hive && docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -run TestNormalizeCacheUsage -count=1` | No -- Wave 0 |
| ROUT-03 | customer-visible usage and error payloads remain provider-blind even when internal records contain provider data | usage/http + edge error tests | `cd /home/sakib/hive && docker compose -f deploy/docker/docker-compose.yml run --rm control-plane go test ./... -run TestProviderBlindUsageResponse -count=1` | No -- Wave 0 |

### Sampling Rate

- **Per task commit:** run the most specific package test for the touched catalog, routing, or usage code.
- **Per wave merge:** run the control-plane suite plus the JS SDK `models.list()` regression.
- **Before `$gsd-verify-work`:** full suite must be green.
- **Max feedback latency:** 120 seconds.

### Wave 0 Gaps

- [ ] No catalog or routing package exists in `apps/control-plane`.
- [ ] No persistent alias catalog, route candidate, or provider capability schema exists under `supabase/migrations/`.
- [ ] `apps/edge-api/cmd/server/main.go` still serves an empty `/v1/models` response.
- [ ] No cross-app projection/client exists between control-plane-owned catalog state and the edge.
- [ ] No LiteLLM service or config is present in Docker Compose.
- [ ] No regression tests currently assert that `/v1/models`, usage responses, or error payloads never leak provider names.
- [ ] No tests currently verify cache-read versus cache-write token normalization from provider usage payloads.

### Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Hive aliases remain understandable to a developer even though providers are hidden | ROUT-01 | Requires product judgment about naming and documentation clarity | Inspect the public model list and richer catalog output. Confirm a developer can choose a model by alias, pricing, and capability badges without seeing vendor internals. |
| Fallback preserves the advertised behavior class | ROUT-02 | Requires scenario review across multiple route candidates | Simulate a primary-route failure, inspect the fallback chain, and verify the selected fallback stays within the alias's allowed behavior/price class. |
| Cache-aware billing is understandable when shown to customers | ROUT-03 | Requires UX/content review in addition to raw field checks | Review customer-facing usage and catalog outputs for a cache-capable alias and confirm the read/write token categories explain billing changes without provider terminology. |

## Sources

### Primary (repo-local)

- `.planning/phases/04-model-catalog-provider-routing/04-CONTEXT.md`
- `.planning/ROADMAP.md`
- `.planning/REQUIREMENTS.md`
- `.planning/PROJECT.md`
- `.planning/STATE.md`
- `packages/openai-contract/upstream/openapi.yaml`
- `packages/sdk-tests/fixtures/golden/models-list.json`
- `packages/sdk-tests/js/tests/models/list-models.test.ts`
- `apps/edge-api/cmd/server/main.go`
- `apps/control-plane/internal/usage/types.go`
- `apps/control-plane/internal/usage/http.go`
- `apps/control-plane/internal/accounting/service.go`
- `deploy/docker/docker-compose.yml`

### External (official / primary)

- OpenAI API contract for `/v1/models` via the official OpenAI OpenAPI material already vendored in this repo
- LiteLLM routing docs: `https://docs.litellm.ai/docs/routing`
- LiteLLM fallback docs: `https://docs.litellm.ai/docs/proxy/reliability`
- LiteLLM response/usage format docs: `https://docs.litellm.ai/`
- Anthropic prompt caching docs: `https://docs.anthropic.com/fr/docs/build-with-claude/prompt-caching`

---
*Phase: 04-model-catalog-provider-routing*
*Research completed: 2026-03-31*
