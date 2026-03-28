# Pitfalls Research

**Domain:** OpenAI-compatible AI gateway with prepaid billing and multi-provider routing
**Researched:** 2026-03-28
**Confidence:** HIGH

## Critical Pitfalls

### Pitfall 1: Compatibility by Approximation

**What goes wrong:**
The product "mostly works" for curl demos, but official OpenAI SDKs break on edge cases such as streaming chunk order, error object shape, unsupported parameter handling, reasoning fields, or file upload semantics.

**Why it happens:**
Teams implement a subset of endpoints manually and validate with ad hoc examples instead of treating OpenAI compatibility as a spec + regression-testing problem.

**How to avoid:**
Import the OpenAI OpenAPI contract, generate public types, and maintain SDK regression tests plus an API-shape verification suite from the start.

**Warning signs:**
- Different output between `stream=false` and `stream=true`
- SDKs require request rewrites beyond base URL and key
- Unsupported fields are silently ignored instead of failing in an OpenAI-style way

**Phase to address:**
Phase 1: Contract import and compatibility harness

---

### Pitfall 2: Billing Drift on Streaming and Retries

**What goes wrong:**
Customers get charged twice, undercharged, or blocked incorrectly because retries, streaming disconnects, or webhook races mutate balances inconsistently.

**Why it happens:**
Usage authorization, upstream execution, and ledger finalization are tightly coupled or non-idempotent.

**How to avoid:**
Use reserve-then-finalize accounting, immutable ledger entries, idempotency keys on every billable request, and reconciliation workers for late or partial upstream outcomes.

**Warning signs:**
- Balance changes are updated in-place instead of append-only
- Usage totals differ between invoice lines and internal ledger
- Replaying a webhook or request changes a settled balance

**Phase to address:**
Phase 2: Ledger foundation and payment canonical model

---

### Pitfall 3: Provider Leakage Through Public Surface

**What goes wrong:**
Customers see Groq/OpenRouter/provider-specific model IDs, error strings, or headers, making Hive aliases meaningless and routing changes breaking.

**Why it happens:**
Teams pass upstream values through for convenience instead of enforcing a strict public alias catalog and sanitized response layer.

**How to avoid:**
Keep a Hive-controlled model catalog, sanitize provider-specific metadata, and translate provider failures into Hive/OpenAI-compatible errors.

**Warning signs:**
- Public model list includes upstream slugs
- Response headers or error messages mention upstream vendors
- Support needs to tell customers which provider was used to explain behavior

**Phase to address:**
Phase 3: Model catalog and routing policy

---

### Pitfall 4: Slow Billing in the Hot Path

**What goes wrong:**
Every request waits on expensive database queries, payment checks, or analytics writes, inflating latency and defeating the point of a fast AI gateway.

**Why it happens:**
Billing correctness is implemented as synchronous back-office logic instead of separating hot authorization data from cold reporting data.

**How to avoid:**
Keep hot-path checks in Redis + cached account/key policy, use a small reservation query set on Postgres, and defer reporting/alerts to workers.

**Warning signs:**
- p95 latency spikes when invoice/reporting jobs run
- Request-serving code writes directly to many reporting tables
- Rate limit and balance checks require multiple DB joins per request

**Phase to address:**
Phase 4: Edge API and hot-path authorization

---

### Pitfall 5: No Prompt Storage, No Debug Plan

**What goes wrong:**
The team honors the no-body-retention rule but cannot explain failures, disputes, or strange usage without prompt logs.

**Why it happens:**
Metadata design is too thin; teams assume body retention is the only usable observability strategy.

**How to avoid:**
Design rich structured metadata from day one: request IDs, account/key/model alias, provider mapping, status, timings, usage counts, pricing inputs, error codes, and hashable fingerprints where appropriate.

**Warning signs:**
- Support requests require asking customers for raw request payloads every time
- Errors are logged only as opaque strings
- Billing disputes cannot be traced to a deterministic request lifecycle

**Phase to address:**
Phase 5: Observability and support tooling

---

### Pitfall 6: Payment and FX Reconciliation Drift

**What goes wrong:**
BDT top-ups, fees, and credited Hive balances cannot be reconstructed later because the system failed to snapshot the exchange rate, payment-method fee, or verified settlement details.

**Why it happens:**
Teams treat FX and payment callbacks as display concerns rather than ledger inputs.

**How to avoid:**
Persist canonical payment intents with FX rate, fee basis, payment method, tax inputs, gateway IDs, and verified settlement states before minting credits.

**Warning signs:**
- Credit minting is based only on the final paid amount
- BDT purchases cannot explain how the USD peg was derived
- Gateway callback replays create duplicate top-ups

**Phase to address:**
Phase 2: Payments and FX snapshots

---

### Pitfall 7: Chasing the Entire Surface Without a Capability Matrix

**What goes wrong:**
Hive promises the full OpenAI public surface, but some providers cannot support a given endpoint or parameter combination, leading to silent failures and confusing inconsistencies.

**Why it happens:**
The team treats "endpoint implemented" as binary and ignores provider capability gaps.

**How to avoid:**
Maintain a public endpoint matrix and an internal provider capability matrix that explicitly marks pass-through, translated, emulated, and unsupported behavior.

**Warning signs:**
- New endpoints are marked complete without provider-level test coverage
- Unsupported behavior differs by provider with no documented rule
- Internal routing chooses providers that cannot honor requested parameters

**Phase to address:**
Phase 6: Surface expansion and capability coverage

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Store balances as mutable counters only | Easy to query | No audit trail, hard reconciliation, support pain | Never |
| Use provider-native model IDs as public IDs | Fast initial setup | Breaks routing freedom and pricing abstraction | Never |
| Keep analytics in Postgres only | Simpler ops | Reporting queries may eventually hit the billing database too hard | Acceptable until real usage data proves otherwise |
| Implement only curl-smoke compatibility tests | Faster early demo | Misses SDK, SSE, and schema regressions | Never |
| Start with Stripe-only billing and no payment abstraction | Faster global card launch | Harder to add bKash/SSLCommerz cleanly later | Acceptable only if the Bangladesh rail integration is intentionally phased, which is not this launch |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| OpenRouter | Assuming it is a perfect OpenAI clone | Treat it as "very similar with small differences" and normalize through Hive compatibility rules. |
| Groq | Passing unsupported OpenAI fields straight through | Filter/translate unsupported fields and return OpenAI-style errors when necessary. |
| Stripe | Using preview billing-credit features as the only prepaid ledger | Keep Hive's internal ledger authoritative and let Stripe support checkout/invoicing/tax. |
| bKash | Treating payment create/execute as one step | Follow the create → redirect/callback → execute/query verification flow and persist gateway references. |
| SSLCommerz | Letting gateway-specific business logic leak into product code | Wrap SSLCommerz behind the same canonical payment intent abstraction as Stripe and bKash. |
| XE | Fetching live FX during reconciliation | Snapshot the exact FX rate used at quote/top-up time and store it with the transaction. |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Heavy billing joins on every request | p95/p99 latency climbs with active customers | Cache key/account policy in Redis and keep the authorization query path minimal | Often visible well before 1k active users |
| Writing analytics synchronously in the request path | Slow streaming start and backpressure | Use an outbox and workers for reporting/event fan-out | Breaks once traffic is bursty |
| Single database for OLTP + high-cardinality analytics forever | Lock contention and slow dashboards | Move usage/event analytics to ClickHouse or equivalent when evidence justifies it | Common around sustained multi-million event volumes |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Logging prompt/response bodies "temporarily" | Violates privacy posture and expands breach impact | Enforce structured metadata-only logging and explicit redaction tests. |
| Weak separation between public API keys and internal provider secrets | Full provider compromise if customer keys are confused with upstream credentials | Keep provider credentials server-only and segregated by service/account. |
| Missing webhook signature verification and replay protection | Fraudulent credit minting or duplicated settlements | Verify signatures, persist idempotency/replay windows, and reconcile asynchronously. |
| Returning raw upstream errors | Provider leakage and inconsistent customer behavior | Normalize upstream failures into Hive/OpenAI-compatible error objects. |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| Credits feel opaque | Customers do not trust pricing or invoices | Show fiat reference price, Hive Credit conversion, FX rate snapshot, fee/tax line items, and per-model cost detail. |
| Budgets stop traffic without warning | Teams get broken automations | Add configurable alerts and clear dashboard states before hard cutoffs. |
| API key settings are too coarse | Teams cannot safely separate projects or customers | Support per-key names, model allowlists, budgets, expirations, and usage drill-down. |

## "Looks Done But Isn't" Checklist

- [ ] **Chat / Responses compatibility:** Often missing SDK and SSE regression coverage — verify with official OpenAI SDKs and streamed golden cases.
- [ ] **Prepaid billing:** Often missing idempotent reservation/finalization — verify duplicate request and reconnect scenarios.
- [ ] **Payments:** Often missing webhook replay handling and FX snapshots — verify credits are minted exactly once and can be reconstructed later.
- [ ] **Model catalog:** Often missing provider capability metadata — verify routing never picks a provider that cannot satisfy the request.
- [ ] **No-body observability:** Often missing support-grade metadata — verify incidents can be debugged without prompt storage.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Compatibility regressions | MEDIUM | Freeze public contract, diff against OpenAI spec, rerun SDK suites, patch translation layer. |
| Billing drift | HIGH | Halt automated credits where needed, rebuild from immutable ledger + provider usage, issue adjustment entries, publish incident notes. |
| Payment duplication | HIGH | Stop minting path, reconcile against gateway settlement IDs, void duplicate grants, harden idempotency logic. |
| Provider leakage | LOW | Rotate public alias metadata, sanitize responses, add regression tests on headers/errors/model lists. |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| Compatibility by approximation | Phase 1 | Official SDK and streamed contract suites pass |
| Billing drift on streaming and retries | Phase 2 | Duplicate/replay tests preserve exact balances |
| Provider leakage | Phase 3 | Public models/errors/headers never reveal upstream providers |
| Slow billing in the hot path | Phase 4 | Edge latency stays stable under billing/reporting load |
| No prompt storage, no debug plan | Phase 5 | Support investigations succeed using metadata only |
| Surface expansion without capability matrix | Phase 6 | Every endpoint/provider combo is explicitly classified and tested |

## Sources

- OpenAI API reference and cookbook verification guide
- Groq OpenAI compatibility docs
- OpenRouter API reference and provider routing docs
- LiteLLM docs
- Stripe billing/tax docs
- bKash developer docs
- SSLCommerz merchant/developer docs
- XE Currency Data API help docs

---
*Pitfalls research for: OpenAI-compatible AI gateway and developer billing platform*
*Researched: 2026-03-28*
