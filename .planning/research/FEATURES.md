# Feature Research

**Domain:** OpenAI-compatible AI gateway, billing, and developer control plane
**Researched:** 2026-03-28
**Confidence:** HIGH

## Feature Landscape

### Table Stakes (Users Expect These)

Features users assume exist. Missing these = product feels incomplete.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Official SDK compatibility | The product pitch is "change base URL + API key" | HIGH | Includes request/response shapes, error objects, and endpoint semantics. |
| Streaming fidelity | AI SDK users rely on streaming for UX and agent orchestration | HIGH | Must preserve SSE/chunk ordering and finish semantics. |
| Model catalog and aliasing | Developers need to know what models they can call | MEDIUM | Public names should be Hive-owned aliases, not raw upstream identifiers. |
| API keys with per-key controls | Multi-project teams expect scoped keys, budgets, and expirations | MEDIUM | Also needed for internal margin protection and support workflows. |
| Prepaid credit wallet and top-ups | Pay-as-you-go API buyers expect balance visibility and reliable recharge paths | HIGH | Ledger correctness is core, not a back-office afterthought. |
| Usage analytics and itemized billing | Customers need to reconcile spend by key, model, and time window | HIGH | Hourly/daily aggregation and invoice-quality detail are expected. |
| Rate limits and quotas | Providers and platforms need margin protection and abuse control | MEDIUM | Must support account-tier and per-key enforcement. |
| Developer console | Billing-only API businesses still need self-serve admin UX | MEDIUM | Auth, invoices, top-ups, keys, spend alerts, tax/business profile. |
| Privacy-safe observability | API businesses need metrics without storing customer prompts | MEDIUM | Retain structured usage/error metadata only. |

### Differentiators (Competitive Advantage)

Features that set the product apart. Not required, but valuable.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Hidden provider abstraction | Customers buy "Hive models", not provider contracts | HIGH | Lets Hive swap providers and pricing behind stable public aliases. |
| Bangladesh-friendly payment rails | Global AI gateways often ignore bKash/SSLCommerz and BDT realities | HIGH | Strong regional moat for local buyers and mixed-currency operations. |
| Hive Credits pegged to USD with FX-aware BDT pricing | Simple customer-facing economy with localized payment support | HIGH | Requires FX snapshots, fee math, and reconciliation discipline. |
| Per-key budgets + model allowlists + validity windows | Strong guardrails for teams, agencies, and friend/customer reselling | MEDIUM | Useful for both internal agent startups and external customers. |
| Reasoning/"thinking" compatibility | Serious agent users increasingly care about reasoning fields and token accounting | HIGH | Requires provider capability matrix and faithful translation. |
| Spend alerts and business/tax profile UX | Makes the product feel production-grade instead of hobby-grade | MEDIUM | Especially important for business buyers and invoicing workflows. |

### Anti-Features (Commonly Requested, Often Problematic)

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| Launching end-user chat, RAG workspaces, and code runner alongside the API | Feels like "more value" | Splits focus away from API compatibility and billing correctness | Launch the developer API first, then expand into products later. |
| Storing prompts/responses by default for "debugging" | Easier support and analytics | Directly conflicts with privacy goal and increases operational risk | Store rich metadata, hashes, status, and usage only; add opt-in debug tooling later if ever needed. |
| Exposing raw upstream model/provider names | Faster to wire up | Makes routing changes breaking and reveals sourcing strategy | Maintain a Hive model catalog with internal provider mappings. |
| Building taxes from static hardcoded country rules only | Seems cheaper | Tax rules, thresholds, and evidence requirements change frequently | Use Stripe Tax where possible and a tax abstraction for non-Stripe rails. |

## Feature Dependencies

```text
OpenAI compatibility harness
    └──requires──> OpenAI spec import + generated contract
                          └──requires──> endpoint capability matrix

API keys / budgets / rate limits
    └──requires──> auth + tenancy
                          └──requires──> ledger + usage event model

Prepaid billing / top-ups / invoices
    └──requires──> ledger + FX snapshot + payment intent abstraction
                          └──requires──> provider-agnostic usage accounting

Spend alerts / analytics
    └──requires──> normalized usage events
                          └──requires──> per-key / per-model attribution

Reasoning compatibility
    └──requires──> model capability matrix
                          └──requires──> response translation layer
```

### Dependency Notes

- **Compatibility harness requires generated contract:** without a spec-driven public surface, "drop-in" becomes a vague promise instead of a testable contract.
- **Keys/budgets require ledger-backed usage attribution:** budgets without durable usage accounting drift quickly and become support nightmares.
- **Payments require FX snapshots:** BDT top-ups anchored to USD cannot be reconstructed later unless Hive stores the FX rate and fee inputs used at purchase time.
- **Alerts require normalized usage events:** emailing customers or cutting off keys is only safe when the usage event stream is authoritative and idempotent.
- **Reasoning compatibility requires a capability matrix:** providers differ; Hive must know what can be passed through, translated, emulated, or rejected.

## MVP Definition

### Launch With (v1)

- [ ] OpenAI-compatible public API surface with phased internal delivery but launch-time coverage of the promised public endpoints
- [ ] Official SDK compatibility verification for JavaScript/TypeScript, Python, and Java
- [ ] Hive model catalog with internal provider aliasing and pricing metadata
- [ ] Supabase auth, tenancy, API key lifecycle, budgets, expirations, and model allowlists
- [ ] Prepaid Hive Credits ledger, Stripe + bKash + SSLCommerz top-ups, invoices, FX snapshots, and spend limits
- [ ] Rate limits, usage tracking, itemized billing, and privacy-safe health/error/usage metrics
- [ ] Developer web console for billing, tax/business info, spend alerts, usage exploration, and key management

### Add After Validation (v1.x)

- [ ] First-party branded SDK wrappers beyond official OpenAI SDK drop-in support — add after the API contract is stable
- [ ] ClickHouse-backed deep analytics — add when Postgres reporting stops being economical
- [ ] Advanced enterprise controls such as org hierarchies, approval flows, and custom procurement workflows — add once business demand appears

### Future Consideration (v2+)

- [ ] Credit-based subscriptions — depends on a stable prepaid ledger and packaging model
- [ ] End-user chat application — separate product surface with different UX and data needs
- [ ] RAG projects/workspaces — requires file, retrieval, workspace, and sharing semantics beyond the API gateway
- [ ] Hosted code execution / dev environments — separate risk, isolation, and cost profile

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| OpenAI compatibility + SDK regression harness | HIGH | HIGH | P1 |
| Prepaid ledger + top-ups + invoices | HIGH | HIGH | P1 |
| API keys, budgets, rate limits | HIGH | MEDIUM | P1 |
| Model catalog + alias routing | HIGH | MEDIUM | P1 |
| Developer console core flows | HIGH | MEDIUM | P1 |
| Reasoning compatibility | HIGH | HIGH | P1 |
| Spend alerts + tax/business profile UX | MEDIUM | MEDIUM | P1 |
| Branded SDK packages | MEDIUM | MEDIUM | P2 |
| Advanced analytics warehouse | MEDIUM | HIGH | P2 |
| Subscription bundles | MEDIUM | MEDIUM | P3 |
| Chat/RAG/code runner products | LOW for launch | HIGH | P3 |

**Priority key:**
- P1: Must have for launch
- P2: Should have, add when possible
- P3: Nice to have, future consideration

## Competitor Feature Analysis

| Feature | Competitor A | Competitor B | Our Approach |
|---------|--------------|--------------|--------------|
| OpenAI-compatible API | OpenAI is the source of truth | OpenRouter is close but documents schema differences | Match OpenAI's public contract, not merely "similar" behavior. |
| Provider routing | OpenRouter load-balances providers and exposes routing controls | Groq offers mostly OpenAI-compatible access to Groq-hosted models | Keep provider routing internal and hidden behind Hive aliases. |
| Usage spend visibility | OpenRouter exposes generation stats and native token-cost detail | Groq exposes spend/rate-limit oriented console features | Provide per-key/per-model/hourly spend detail in the Hive console. |
| Regional payments | Global gateways usually optimize for cards | Bangladesh local methods are rarely first-class in AI gateways | Make Stripe + bKash + SSLCommerz part of the core monetization design. |

## Sources

- OpenAI API reference and OpenAPI spec
- OpenAI cookbook guidance for verifying API shapes
- OpenRouter docs for schema differences, routing, and generation stats
- Groq docs for OpenAI compatibility and unsupported fields
- LiteLLM docs for budgets, virtual keys, and provider translation
- Stripe docs for billing credits, alerts, and tax monitoring
- bKash developer docs and SSLCommerz merchant/developer docs

---
*Feature research for: OpenAI-compatible AI gateway and developer billing platform*
*Researched: 2026-03-28*
