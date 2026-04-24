# Phase 3: Credits Ledger & Usage Accounting - Context

**Gathered:** 2026-03-28
**Status:** Ready for planning

<domain>
## Phase Boundary

Make prepaid Hive Credits financially correct at the workspace level by introducing an immutable ledger, reservation and finalization rules, and privacy-safe usage events that can support later budgets, reporting, and policy enforcement without storing prompts or responses at rest. This phase defines the accounting model and durable usage facts; it does not implement payment rails, console UX, full RBAC or team-management features, or API key lifecycle features from later phases.

</domain>

<decisions>
## Implementation Decisions

### Workspace wallet and attribution hierarchy
- The only real spendable balance is the workspace-level wallet.
- Users, teams, service accounts, and API keys may have budgets or limits layered on top of the shared workspace balance, but they do not get true sub-wallets in Phase 3.
- The ledger and usage model must preserve attribution so later phases can report or govern spend by user, team, service account, API key, model, and metadata dimensions without changing the core balance model.
- Model-specific budgets are expected later and Phase 3 should keep that future policy layer straightforward.

### Reservation and limit enforcement posture
- Dispatch authorization should reserve against a strong estimate plus safety margin instead of a worst-case ceiling or a fully loose settle-later posture.
- Specific-limit enforcement should be configurable at the workspace level between strict blocking and a small temporary overage mode.
- The default workspace setting should be strict blocking when a more specific budget or limit fails.
- Streaming or long-running requests should start with an initial estimate and expand the reservation during execution when policy still allows.

### Failure, retry, and interrupted-stream settlement
- If Hive has no confirmed upstream usage, the reservation should be fully released or refunded.
- If usage is ambiguous after interruption or failure, default to customer-favoring settlement unless the provider supplies a terminal usage record.
- Retries may become a new billable attempt once upstream execution has actually started.
- Broken or interrupted streams should keep ambiguous cases reconcilable rather than assuming the full reserved amount was consumed.

### Privacy-safe reporting and cost allocation
- Durable usage events should store rich operational metadata rather than minimal balance facts only.
- Required attribution dimensions include workspace, user, team, API key or service account, model, endpoint, and customer-defined metadata tags for cost allocation.
- Customer-defined reporting metadata should be stored with limits and redaction rules rather than allowlisting only a tiny fixed set or dropping it entirely.
- Future customer-facing usage reporting should support a table view where each event can show model name, actor name, input, output, and cache token counts, Hive Credit cost, timestamp, request ID, and room for additional non-transcript fields over time.
- Internal accounting records should also preserve the fulfilling provider, provider-side cost, provider metadata, and error details for reconciliation, margin analysis, and support, while keeping provider identity hidden from customer-facing reporting by default.
- The reporting philosophy should feel closer to AWS cost allocation and cloud financial reporting than a minimal Stripe-style billing history.

### Claude's Discretion
- Downstream agents can choose the exact schema layout, event envelope shape, redaction rules, and policy-evaluation mechanics as long as they preserve the shared-wallet model, configurable strict-vs-overage enforcement, customer-favoring ambiguous settlement, and AWS-style cost-allocation reporting goals.
- Downstream agents can decide the exact naming and hierarchy of attribution fields for users, teams, service accounts, API keys, and metadata tags, provided future cost filtering remains straightforward.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope and requirements
- `.planning/ROADMAP.md` § "Phase 3: Credits Ledger & Usage Accounting" — Defines the phase goal, success criteria, and plan breakdown for immutable ledger, reservations, privacy-safe usage events, and stream/retry accounting.
- `.planning/REQUIREMENTS.md` § "Billing & Payments" — Defines `BILL-01` and `BILL-02`, the core immutable ledger and reservation/finalization requirements this phase must satisfy.
- `.planning/REQUIREMENTS.md` § "Privacy & Operations" — Defines `PRIV-01`, which prohibits prompt and response storage at rest by default.
- `.planning/REQUIREMENTS.md` § "API Keys & Limits" — Defines `KEY-04` and `KEY-05`, which later depend on durable per-key attribution and enforceable budget dimensions from this phase's accounting model.
- `.planning/REQUIREMENTS.md` § "Developer Console" — Defines `CONS-03`, which later depends on privacy-safe usage analytics by account, key, model, and time window.
- `.planning/PROJECT.md` § "Context" — States the prepaid Hive Credit model, detailed spend visibility expectations, provider-blind posture, and no-transcript-storage rule.
- `.planning/PROJECT.md` § "Constraints" — Locks prepaid-only billing, privacy posture, provider abstraction, hosted Supabase as the primary relational store, and Docker-only development.
- `.planning/STATE.md` § "Accumulated Context" — Carries forward the already-accepted product constraints for privacy, prepaid billing, hosted Supabase, and Docker-only workflows.

### Carry-forward identity model
- `.planning/phases/02-identity-account-foundation/02-CONTEXT.md` — Establishes the workspace-first shared-account model and notes that later ledger, API key, payment, and invoice work depend on the Phase 2 account and membership foundation.

### Architecture and research guidance
- `.planning/research/SUMMARY.md` — Recommends the reserve-then-finalize ledger, metadata-only observability, and immutable usage accounting as the main hedge against billing drift.
- `.planning/research/ARCHITECTURE.md` § "Pattern 2: Reserve-Then-Finalize Ledger" — Defines the default reserve, execute, collect usage, and finalize flow for billable requests.
- `.planning/research/ARCHITECTURE.md` § "Data Flow" — Shows inference billing as reserve before dispatch, finalize on terminal usage, then emit itemized usage events.
- `.planning/research/STACK.md` § "Recommended Stack" — Recommends Supabase Postgres as the authoritative ledger store and Redis for reservations, idempotency, and ephemeral hot-path state.
- `.planning/research/STACK.md` § "Version Compatibility" — States that ledger-critical flows should use direct Postgres access patterns rather than auto-generated REST layers.
- `.planning/research/PITFALLS.md` § "Billing drift on streams/retries" — Explains why reservations, immutable ledger entries, idempotency, and reconciliation workers are required for correctness.
- `.planning/research/PITFALLS.md` § "Logging prompt/response bodies 'temporarily'" — Reinforces the metadata-only privacy posture and explicit redaction expectations.
- `.planning/research/FEATURES.md` — Connects ledger-backed usage attribution to later keys, budgets, alerts, and reporting requirements.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- No application code exists yet; the most reusable assets are the planning and research documents that already define the ledger, privacy, and control-plane direction.
- `.planning/phases/02-identity-account-foundation/02-CONTEXT.md`: Establishes the workspace-account model that ledger and usage events should attach to.
- `.planning/research/ARCHITECTURE.md`: Provides the recommended reserve/finalize and outbox-driven accounting patterns.

### Established Patterns
- The repository is still greenfield, so there are no implementation-level data-access or service patterns to preserve yet.
- Hosted Supabase Postgres is already locked as the authoritative transactional store for identity and ledger state.
- Redis is already the expected place for hot-path counters, idempotency windows, reservation caches, and other ephemeral request state, not the source of truth for balances.
- Docker-only local development and the no-prompt-storage privacy posture are already non-negotiable project constraints.

### Integration Points
- Phase 3 must attach the ledger to the workspace, membership, and profile foundations established in Phase 2.
- The accounting model created here becomes a dependency for Phase 5 API key budgets, per-key attribution, and hot-path policy checks.
- Phase 3 will also supply the money-safe foundation for Phase 8 payment credit grants and Phase 9 ledger history, usage analytics, and financial reporting.
- Phase 6 and later inference phases will depend on this phase's reservation, finalization, and interrupted-stream accounting rules for public API execution.

</code_context>

<specifics>
## Specific Ideas

- Workspace balance should behave like a true shared wallet, with layered budgets for users, teams, service accounts, API keys, and model-specific controls.
- API keys should eventually support JSON metadata for dimensions such as project, product, or environment so billing and reporting can filter on them.
- The desired reporting and policy model is inspired by AWS cost and financial reporting plus AWS IAM-style attribution and governance boundaries.
- Only user accounts should need web UI access; service accounts are API-facing identities. Phase 3 should preserve the accounting hooks for that future split without implementing the identity product itself.
- Usage reporting should eventually support a row-level event table keyed by request ID, with user-visible cost and token breakdowns plus internal-only provider and margin details retained for Hive operations.

</specifics>

<deferred>
## Deferred Ideas

- Full RBAC and role design such as `admin`, `team lead`, and similar workspace permission layers.
- Service-account lifecycle and non-email identity management as a product feature rather than just a future accounting dimension.
- Team entity management, team membership UX, and team-specific administration features.
- Web-chat functionality for user accounts.
- Full AWS IAM-like governance surfaces beyond the accounting and attribution hooks needed in this phase.

</deferred>

---

*Phase: 03-credits-ledger-usage-accounting*
*Context gathered: 2026-03-28*
