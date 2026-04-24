# Phase 5: API Keys & Hot-Path Enforcement - Context

**Gathered:** 2026-03-31
**Status:** Ready for planning

<domain>
## Phase Boundary

Give account owners safe multi-key management and cheap hot-path enforcement for key validity, model access, budgets, and rate limits. This phase covers API key issuance, rotation, revocation, pause/resume behavior, per-key model governance, per-key budget policy, per-account and per-key rate limiting, and request-time authorization. It does not expand into per-user enforcement, broad console UX, spend alerts, or later reporting and observability phases.

</domain>

<decisions>
## Implementation Decisions

### Key lifecycle and secret handling
- Hive-issued API secrets use an `hk_...` prefix.
- A raw secret is shown exactly once at creation time.
- Rotating a key issues a brand-new key and immediately revokes the old key rather than keeping a migration overlap window.
- Accounts may have multiple separate active keys at the same time; rotation is per key, not account-wide.
- Customer-visible key states are `active`, `expired`, `revoked`, and `disabled`.
- `disabled` is a temporary pause that can be resumed later. `revoked` is permanent removal of access.
- After creation, customer-visible key details should include the nickname, created date, last-used timestamp, redacted suffix, expiration summary, budget summary, and allowlist summary. The raw secret must not be shown again.

### Model access policy
- New keys should start with a curated default model set rather than full catalog access.
- Premium model access is opt-in.
- Model access should support reusable groups or sets. The initial taxonomy should include `default`, `premium`, `oss`, and `closed`.
- Keys can be granted model access through group or set membership, explicit per-alias checklist selections, and manual overrides.
- Hive should also support an explicit `all models` access mode for keys that are intended to inherit the full public catalog.
- Future aliases should auto-attach only when a key has explicit `all models` access, or when the alias is added to a model group that the key already allows.
- Requests for disallowed model aliases must fail immediately with an OpenAI-style error. Hive must not silently remap to a different allowed alias.

### Per-key budget semantics
- Per-key budgets are layered guardrails on top of the shared workspace wallet; they are not sub-wallets and do not reserve dedicated balance.
- The workspace wallet may run out before a key-specific budget is exhausted.
- Per-key budgets are expressed and enforced in Hive Credits only.
- Keys should support both lifetime budgets and recurring budgets.
- Once a key is over budget, requests must hard-fail immediately with an OpenAI-style error rather than allowing temporary overage or requiring a separate manual review flow.

### Rate-limit posture and anti-fraud windows
- Phase 5 hot-path enforcement stays at the account and key levels. Per-user enforcement is out of scope for this phase.
- Rate limiting should be model-specific, with RPM and TPM policies enforced per account and per key.
- Longer-horizon anti-fraud guardrails should exist at the account and key levels using a rolling 5-hour limit and a weekly trust-tier limit.
- The long-horizon trust-tier meter should be hybrid: Hive Credits consumed plus token usage.
- Token usage for the long-horizon anti-fraud meter should count billable tokens plus `0.1x` free-token usage.
- Rate-limit and anti-fraud failures on the public API should return OpenAI-style `429` responses.
- Include standard retry headers only when it is safe and meaningful to do so. Do not add Hive-specific public diagnostics to the hot path by default.

### Claude's Discretion
- Exact secret length, encoding, redacted-suffix length, and storage schema, as long as the public secret prefix and one-time reveal rule are preserved.
- Exact data model for key groups, presets, overrides, and how `all models` is represented internally.
- Exact short-window counter algorithm for RPM and TPM, and exact weekly reset mechanics, as long as the customer-facing policy remains model-specific RPM/TPM plus rolling 5-hour and weekly trust-tier enforcement.
- Exact thresholds and progression rules for trust tiers, as long as the long-horizon anti-fraud meter uses Hive Credits consumed plus billable and weighted free-token usage.
- Exact OpenAI-style error copy and retry-header conditions, as long as failures stay compatibility-oriented and provider-blind.

</decisions>

<specifics>
## Specific Ideas

- Secret prefix should be short and recognizable: `hk_...`.
- Model governance should feel set-based first, with reusable buckets such as `default`, `premium`, `oss`, and `closed`, but still allow explicit per-alias overrides.
- The `all models` access mode is distinct from the curated default set and is the only blanket mode that should auto-inherit newly published aliases.
- Long-horizon fraud protection should not look only at request counts; it should combine Hive Credits consumed with token usage, including a discounted contribution from free tokens.

</specifics>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope and product requirements
- `.planning/ROADMAP.md` § "Phase 5: API Keys & Hot-Path Enforcement" — Defines the phase goal, success criteria, and 05-01 through 05-03 plan breakdown.
- `.planning/REQUIREMENTS.md` § "API Keys & Limits" — Defines `KEY-01` through `KEY-05`.
- `.planning/PROJECT.md` § "Requirements" — Locks account and API-key controls for budgets, expiration, allowed models, per-key usage tracking, and account-tier rate limiting into the active product scope.
- `.planning/PROJECT.md` § "Constraints" — Locks provider abstraction, prepaid credits, privacy posture, hosted Supabase as the primary relational store, and Redis-backed hot-path expectations.
- `.planning/STATE.md` § "Accumulated Context" — Carries forward the shared-wallet model, provider-blind posture, verified-owner gate behavior, and current Phase 5 focus.

### Carry-forward decisions from prior phases
- `.planning/phases/02-identity-account-foundation/02-CONTEXT.md` — Establishes the workspace-first account model and verified-owner gating for sensitive actions.
- `.planning/phases/03-credits-ledger-usage-accounting/03-CONTEXT.md` — Establishes the shared workspace wallet, layered budget model, strict blocking default, and durable per-key attribution dependency.
- `.planning/phases/04-model-catalog-provider-routing/04-CONTEXT.md` — Establishes stable provider-blind model aliases and alias allowlist expectations that Phase 5 key policy must reuse.

### Research and architecture guidance
- `.planning/research/ARCHITECTURE.md` § "Component Responsibilities" — Places auth, model policy, pricing, credits, and key controls in the control plane, with the public compatibility surface at the edge.
- `.planning/research/SUMMARY.md` § "Architecture Approach" — Recommends Redis-backed hot checks and a split between edge compatibility, control-plane governance, and provider adapters.
- `.planning/research/STACK.md` — Recommends Redis for hot-path counters and LiteLLM as provider infrastructure rather than the public business API.
- `.planning/research/PITFALLS.md` § "Pitfall 4: Slow Billing in the Hot Path" — Requires cheap Redis-backed authorization checks and minimal synchronous Postgres work.
- `.planning/research/FEATURES.md` § "API keys / budgets / rate limits" — Connects API-key governance to ledger-backed usage attribution and launch-critical controls.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `apps/control-plane/internal/accounts/service.go` — Already computes `CanManageAPIKeys` using the verified-owner gate, which should stay the authority for who can issue, rotate, disable, or revoke keys.
- `apps/control-plane/internal/accounts/service.go` — Already includes `generateToken` and `HashToken`, showing an existing one-time-secret plus SHA-256 hashing pattern that can inform API key storage.
- `apps/control-plane/internal/accounts/http.go` — Already resolves the current account from authenticated context plus `X-Hive-Account-ID`, so key-management APIs should remain account-scoped rather than trusting client-supplied ownership hints.
- `apps/control-plane/internal/usage/types.go` and `apps/control-plane/internal/usage/repository.go` — Already carry `api_key_id` on request attempts and usage events, so per-key attribution does not need a new accounting channel.
- `apps/control-plane/internal/accounting/types.go` and `apps/control-plane/internal/accounting/service.go` — Already implement the shared-wallet reservation model and strict-vs-overage policy posture that key budgets must layer on top of.
- `apps/control-plane/internal/routing/service.go` and `apps/control-plane/internal/routing/types.go` — Already support alias allowlists, which is the direct seam for enforcing key-level model access.
- `apps/control-plane/internal/platform/redis/client.go` and `apps/control-plane/internal/platform/config/config.go` — Redis is already configured as the hot-path state store for counters and short-lived policy data.
- `apps/edge-api/internal/errors/openai.go` and `apps/edge-api/internal/errors/openai_test.go` — OpenAI-style error envelopes and `rate_limit_exceeded` behavior already exist and should be reused for public failures.

### Established Patterns
- Control-plane modules use repository/service/http layering with direct Postgres repositories and account-scoped handlers.
- Sensitive customer operations already resolve the authenticated viewer first and only then derive account context; Phase 5 should preserve that pattern for all key-management mutations.
- The product already treats the workspace wallet as the only real balance, with layered policy controls above it.
- Public API behavior is already compatibility-first and provider-blind, so key, budget, and rate-limit failures should preserve that external posture.

### Integration Points
- Key issuance and lifecycle APIs belong in the control plane beside the existing account and billing modules.
- Hot-path authorization will need to combine key validity, model allowlist resolution, budget state, and rate-limit counters before Phase 6 inference endpoints expand.
- Key-level model access should plug directly into the routing selection input rather than creating a separate model-policy system.
- Per-key budgets and long-horizon anti-fraud checks should use the existing ledger and usage primitives for durable attribution while keeping request-serving enforcement cheap.
- Phase 5 decisions become direct dependencies for Phase 6 inference routing and Phase 9 developer-console key-management UX.

</code_context>

<deferred>
## Deferred Ideas

- Per-user rate limits or per-user model quotas — explicitly deferred to a later phase; Phase 5 scope is account and key enforcement only.
- Spend alerts, notification workflows, and broader trust-and-safety support tooling — later console and operations work.
- Full developer-console UX for key presets, grouped-model administration, and rich audit history — later console work, even though the underlying policy primitives land here.

</deferred>

---

*Phase: 05-api-keys-hot-path-enforcement*
*Context gathered: 2026-03-31*
