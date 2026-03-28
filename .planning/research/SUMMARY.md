# Project Research Summary

**Project:** Hive API Platform
**Domain:** OpenAI-compatible multi-provider AI gateway with prepaid billing and developer control plane
**Researched:** 2026-03-28
**Confidence:** HIGH

## Executive Summary

Hive is not a normal "AI wrapper" project. As of March 28, 2026, the current OpenAI API exposes a very large public surface, and the product promise here is not "similar to OpenAI" but "drop-in compatible for official SDKs while hiding the real providers." Research strongly points to a contract-first architecture: import the OpenAI public API contract, generate the public layer, and make compatibility a regression-tested product capability instead of a hand-maintained coding style.

The most efficient implementation path is a hybrid architecture, not a fully custom Rust or Go rewrite of every provider and every endpoint. Use Go for the public edge and business control plane, and use LiteLLM as an internal provider adapter where it already covers routing, normalization, budgets, and provider breadth. Layer strict Hive-owned compatibility, billing, aliasing, and no-prompt-retention logic on top. This minimizes custom code while keeping the hot path efficient and the business-critical parts under Hive control.

The main risks are predictable: billing drift on streams/retries, provider leakage, partial compatibility dressed up as full compatibility, and payment/FX/tax inconsistency across Stripe, bKash, and SSLCommerz. The research suggests explicit prevention patterns for each: reserve-then-finalize ledgers, public model aliases, endpoint/provider capability matrices, canonical payment intents with FX snapshots, and structured metadata-only observability.

## Key Findings

### Recommended Stack

The recommended stack is Go 1.26.1 for the public edge/control plane, hosted Supabase for auth plus the primary managed Postgres database, Redis 8.4 for hot-path counters and short-lived state, and Next.js 16.1 + React 19.2 for the developer console. The local developer workflow should be Docker-only, including hot reload, code generation, builds, and tests. The key cost-saving move is to avoid re-implementing provider translation from scratch: LiteLLM 1.81.3-stable already supports OpenAI-shaped endpoints across many providers, including OpenRouter, and brings routing, budgets, rate limiting, and spend hooks.

Use `ogen` 1.15.1 or `oapi-codegen` 2.5.1 against the OpenAI OpenAPI spec (currently version 2.3.0 in the fetched reference) to keep the public contract aligned with the source of truth. Keep Hive's internal prepaid ledger authoritative even if Stripe Billing Credits or OpenMeter are used for supporting flows, because both ecosystems are still evolving and should not own the entire business invariant set on day one.

**Core technologies:**
- **Go 1.26.1:** edge API, workers, control plane — best delivery/performance balance
- **LiteLLM 1.81.3-stable:** provider adapter and routing — minimizes custom provider code
- **Supabase hosted Postgres:** ledger, payments, catalog, keys, invoices — strongest low-ops transactional base for v1
- **Redis 8.4:** limits, idempotency, reservations — best fit for hot-path enforcement
- **Supabase hosted project (`yimgflllgdsbcibnaxqe`)**: auth/session layer and primary relational store — fastest low-ops identity and transactional foundation
- **Next.js 16.1 / React 19.2:** developer console — current stable frontend foundation
- **Docker Compose with containerized watchers:** local dev, hot reload, codegen, builds, and tests — no host Go/Node installs required

### Expected Features

Research confirms that the true table stakes are broader than inference itself. A credible developer AI gateway in 2026 needs official SDK compatibility, streaming fidelity, model cataloging, prepaid wallets, usage/itemization, per-key governance, rate limiting, and a self-serve console. Because Hive is also targeting business buyers and Bangladesh-local payment methods, billing, invoices, spend alerts, tax/business profile capture, and localized payment support move from "nice to have" into launch scope.

**Must have (table stakes):**
- OpenAI-compatible public surface with SDK and SSE regression coverage
- Model catalog and stable Hive aliases
- API keys with budgets, expirations, and model allowlists
- Prepaid credit wallet, top-ups, invoices, usage analytics, and rate limiting
- Developer console and privacy-safe observability

**Should have (competitive):**
- Hidden provider abstraction with cost-aware/fallback routing
- BDT pricing and local Bangladesh payment rails
- Reasoning compatibility and detailed usage transparency
- Spend alerts and account/business profile tooling

**Defer (v2+):**
- End-user chat product
- RAG projects/workspaces
- Hosted code execution environments
- Subscription packaging beyond prepaid-credit primitives

### Architecture Approach

The recommended architecture is a modular platform with three clear scaling boundaries from day one: a Go edge API, an internal provider adapter plane, and background/control-plane workers. Do not explode into many tiny services immediately. Instead, split only where the scaling economics are obvious: request-serving traffic, payment/billing control plane, and web/admin traffic. Start with Supabase managed Postgres + Redis and defer ClickHouse until the reporting workload justifies it.

**Major components:**
1. **Edge API** — owns public OpenAI compatibility, auth, rate limiting, streaming normalization
2. **Control plane** — owns catalog, credits, payments, tax prep, key governance, and usage policy
3. **Provider adapter layer** — owns upstream translation/routing via LiteLLM plus Hive shims
4. **Worker plane** — owns reconciliation, alerts, invoicing side effects, and analytics fan-out
5. **Web console** — owns customer-facing billing and developer administration UX

### Critical Pitfalls

1. **Compatibility by approximation** — avoid with spec import, generated types, and official SDK regression suites
2. **Billing drift on streams/retries** — avoid with reserve/finalize accounting and immutable ledger entries
3. **Provider leakage** — avoid with a strict public alias catalog and sanitized errors/headers
4. **Slow billing in the hot path** — avoid with Redis-backed hot checks and async reporting
5. **Payment/FX reconciliation drift** — avoid with canonical payment intents and persisted FX snapshots

## Implications for Roadmap

Based on research, suggested phase structure:

### Phase 1: Contract and Compatibility Harness
**Rationale:** If the public contract is wrong, every later phase compounds the error.
**Delivers:** Imported OpenAI spec, generated contract layer, endpoint inventory, SDK/SSE golden tests
**Addresses:** OpenAI compatibility
**Avoids:** Compatibility-by-approximation risk

### Phase 2: Auth, Tenancy, and Ledger Foundation
**Rationale:** Billing and key governance depend on identity and durable money-like state.
**Delivers:** hosted Supabase auth, account model, immutable Hive ledger in Supabase Postgres, usage event model, canonical payment intent model
**Addresses:** Credits, organizations, prepaid flows
**Avoids:** Billing drift and reconciliation chaos

### Phase 3: Model Catalog and Routing Policy
**Rationale:** Public aliases and provider abstraction must exist before public inference routing is stable.
**Delivers:** Hive model catalog, alias mapping, provider capability matrix, routing policies
**Uses:** LiteLLM, OpenRouter, Groq
**Implements:** Provider abstraction boundary

### Phase 4: Edge Gateway and Hot-Path Enforcement
**Rationale:** The low-latency serving path should be correct before broad endpoint rollout.
**Delivers:** Go edge server, auth/key checks, rate limiting, credit reservation, idempotency, streaming scaffolding
**Uses:** Go, Redis, Supabase Postgres
**Implements:** Public API hot path

### Phase 5: Core Inference Surfaces
**Rationale:** `models`, `responses`, `chat/completions`, `completions`, `embeddings`, `moderations` are the most critical drop-in endpoints.
**Delivers:** Core inference endpoints with usage accounting and reasoning-compatible behavior where supported
**Addresses:** Most common agent and SDK use cases
**Avoids:** Premature long-tail surface work

### Phase 6: Files, Uploads, Images, Audio, and Async Expansion
**Rationale:** File and media workflows are required for a serious OpenAI-compatible platform and introduce different storage/streaming semantics.
**Delivers:** `files`, `uploads`, `images`, `audio`, `batches`, and related capability handling
**Uses:** Object storage and provider capability shims
**Implements:** Media/file surface coverage

### Phase 7: Long-Tail Public Surface and Capability Gaps
**Rationale:** The promise is a full public mirror except org/admin endpoints, so remaining public surfaces need explicit classification and rollout.
**Delivers:** Remaining public endpoints that fit Hive's provider strategy, plus OpenAI-style unsupported responses where a provider gap still exists during rollout
**Addresses:** Full public mirror promise
**Avoids:** Silent inconsistency across endpoint families

### Phase 8: Payments, Tax, and Credit Top-Ups
**Rationale:** Monetization must be production-safe before external customers rely on it.
**Delivers:** Stripe, bKash, SSLCommerz, FX snapshots, fee math, invoice data, tax monitoring hooks
**Uses:** Canonical payment abstraction
**Implements:** Prepaid commercialization

### Phase 9: Developer Console and Operational Hardening
**Rationale:** Customers need self-serve administration and the platform needs production-grade supportability.
**Delivers:** Web console, spend alerts, usage drill-down, invoices, business profile, metrics, incident tooling
**Addresses:** Developer UX and operator readiness
**Avoids:** Launching an opaque black-box billing system

### Phase Ordering Rationale

- Contract and ledger come before breadth because compatibility and money errors are harder to fix than missing endpoints.
- Model aliasing must precede public routing so provider abstraction remains stable.
- Core inference surfaces come before long-tail public endpoints because they are the highest-value path for internal use and early adopters.
- Payments and console can proceed once the underlying ledger, auth, and usage models are trustworthy.

### Research Flags

Phases likely needing deeper research during planning:
- **Phase 3:** provider capability matrix, especially for reasoning/media surface mismatches
- **Phase 6:** file/media endpoint semantics, storage strategy, and streaming partial image behavior
- **Phase 7:** which public endpoints are truly supportable with current providers vs need staged unsupported handling
- **Phase 8:** exact tax handling per payment rail and jurisdiction evidence requirements

Phases with standard patterns (skip research-phase):
- **Phase 2:** auth/account/ledger foundation is standard, though correctness is critical
- **Phase 4:** Go edge + Redis/Supabase Postgres hot-path enforcement follows established patterns

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | Verified against official tool/docs sources and current releases |
| Features | HIGH | Strong market convergence across OpenAI-compatible developer platforms |
| Architecture | HIGH | The split between hot path, control plane, and workers is well-supported by the problem shape |
| Pitfalls | HIGH | Risks are direct consequences of compatibility, billing, and multi-provider routing complexity |

**Overall confidence:** HIGH

### Gaps to Address

- Full public-surface coverage is a product commitment, but provider capability gaps must be explicitly planned endpoint by endpoint.
- Stripe billing-credit and token-billing features are evolving; Hive should not depend on preview Stripe features as the only prepaid-credit system.
- Tax treatment for non-Stripe local rails will need explicit design beyond what Stripe Tax covers automatically.

## Sources

### Primary (HIGH confidence)
- OpenAI API reference and OpenAPI spec — public contract, streaming, uploads, batches, images
- OpenAI cookbook verification guide — compatibility test strategy
- LiteLLM docs and releases — provider abstraction capability, budgets, rate limits, spend tracking
- Groq OpenAI compatibility docs — compatibility caveats
- OpenRouter API and provider-routing docs — schema differences, routing, generation stats
- Stripe billing/tax docs — billing credits, usage alerts, tax monitoring
- bKash developer docs — payment flow and BDT constraints
- SSLCommerz official site/docs — Bangladesh payment aggregation and payment channel breadth
- XE Currency Data API help docs — FX data availability and caching guidance

### Secondary (MEDIUM confidence)
- OpenMeter docs — useful optional metering/entitlements layer, but still beta for self-hosting decisions
- ClickHouse release notes — strong future analytics fit once Postgres reporting becomes expensive

### Tertiary (LOW confidence)
- None used for architectural recommendations

---
*Research completed: 2026-03-28*
*Ready for roadmap: yes*
