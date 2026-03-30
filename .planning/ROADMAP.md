# Roadmap: Hive API Platform

## Overview

Hive launches as a developer-first AI gateway whose value depends on three things staying correct at the same time: OpenAI contract fidelity, prepaid billing correctness, and provider abstraction. The roadmap therefore starts by making the public contract testable, then builds identity and a money-safe ledger foundation, then adds routing, hot-path controls, and endpoint coverage before commercial checkout and console hardening. This sequencing reduces the risk of expensive rewrites in compatibility, billing, and metering once external users start depending on the platform.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: Contract & Compatibility Harness** - Import the OpenAI contract, define launch coverage, and make compatibility regression-tested. (completed 2026-03-29)
- [x] **Phase 2: Identity & Account Foundation** - Stand up hosted Supabase auth, tenancy, sessions, customer account profile data, and the primary relational model. (completed 2026-03-29)
- [ ] **Phase 3: Credits Ledger & Usage Accounting** - Build immutable prepaid billing, reservations, and privacy-safe usage events.
- [ ] **Phase 4: Model Catalog & Provider Routing** - Create Hive aliases, pricing catalog, routing policy, and provider capability matrix.
- [ ] **Phase 5: API Keys & Hot-Path Enforcement** - Add key lifecycle, per-key controls, budgets, rate limits, and hot-path authorization.
- [ ] **Phase 6: Core Text & Embeddings API** - Deliver the most-used OpenAI-compatible inference endpoints with streaming and reasoning support.
- [ ] **Phase 7: Media, File, and Async API Surface** - Expand to files, uploads, batches, images, and audio workflows.
- [ ] **Phase 8: Payments, FX, and Compliance Checkout** - Add Stripe, bKash, SSLCommerz, FX snapshots, and tax-aware checkout.
- [ ] **Phase 9: Developer Console & Operational Hardening** - Finish user-facing billing and usage UX plus production observability and alerts.

## Phase Details

### Phase 1: Contract & Compatibility Harness
**Goal**: Make Hive's public API a verified compatibility product instead of an approximation, on top of a Docker-only developer workflow.
**Depends on**: Nothing (first phase)
**Requirements**: [COMP-01, COMP-02, COMP-03, API-08]
**Success Criteria** (what must be TRUE):
  1. Hive has an imported, versioned OpenAI-facing contract with an explicit launch support matrix for public non-org/admin endpoints.
  2. Official OpenAI JavaScript/TypeScript, Python, and Java SDK smoke tests run against Hive for the implemented launch subset.
  3. Unsupported public endpoints return consistent OpenAI-style errors instead of ad hoc failures.
  4. Swagger/OpenAPI documentation is generated and published for the implemented Hive surface.
  5. Contributors can run hot reload, code generation, builds, and tests from Docker containers without host-installed Go or Node.
**Plans**: 4/4 plans complete

Plans:
- [ ] 01-01-PLAN.md — Docker-only developer stack with Go edge-api, toolchain, and SDK test containers (Wave 1)
- [ ] 01-02-PLAN.md — Import OpenAI contract, build support matrix, error envelope, unsupported middleware, compat headers, and Swagger docs (Wave 2)
- [ ] 01-03-PLAN.md — SDK compatibility harness: JS, Python, and Java tests with golden fixtures (Wave 3)
- [ ] 01-04-PLAN.md — Close the `COMP-03` docs gap by generating a Hive-specific OpenAPI contract from the support matrix and serving it at `/docs` (Wave 4)

### Phase 2: Identity & Account Foundation
**Goal**: Establish authenticated accounts, tenant identity, and customer profile data required by billing and console flows.
**Depends on**: Phase 1
**Requirements**: [AUTH-01, AUTH-02, AUTH-03, AUTH-04]
**Success Criteria** (what must be TRUE):
  1. Developer can sign up, sign in, verify email, and reset password through hosted Supabase-backed flows.
  2. Developer console sessions survive refresh and normal browser revisits.
  3. Each account stores billing contact, legal entity, country, and VAT or business data in a durable profile.
**Plans**: 7/7 plans executed

Plans:
- [x] 02-01: Create the control-plane module, Docker wiring, shared env contract, and initial identity schema.
- [x] 02-02: Implement viewer bootstrap, invitation APIs, invitation acceptance, and explicit current-account selection semantics.
- [x] 02-03: Create the web-console app, hosted Supabase auth routes, and SSR session middleware.
- [x] 02-04: Build the verification-aware console shell, members roster, invitation acceptance UX, and workspace switcher persistence.
- [x] 02-05: Add the current-account core profile API for minimal pre-billing identity data.
- [x] 02-06: Build the short setup flow plus profile settings UI for the core profile.
- [x] 02-07: Add optional durable billing-profile storage and billing settings without making billing completeness a Phase 2 gate.

### Phase 3: Credits Ledger & Usage Accounting
**Goal**: Make prepaid credits and request metering financially correct without storing prompts or responses at rest.
**Depends on**: Phase 2
**Requirements**: [BILL-01, BILL-02, PRIV-01]
**Success Criteria** (what must be TRUE):
  1. Every credit purchase, reservation, charge, refund, and adjustment is represented in an immutable ledger stored in Supabase Postgres.
  2. Request execution reserves credits before dispatch and finalizes or refunds usage correctly for success, failure, retry, cancellation, and interrupted streams.
  3. Usage and billing events can be reconstructed without persisting prompt or response bodies.
**Plans**: 2/3 plans complete

Plans:
- [x] 03-01: Implement the Hive Credit ledger, idempotency model, and balance calculations.
- [x] 03-02: Add privacy-safe usage events and request accounting primitives.
- [ ] 03-03: Build reservation, finalization, and refund paths for streaming and retry scenarios.

### Phase 4: Model Catalog & Provider Routing
**Goal**: Expose Hive-owned model aliases while keeping provider selection internal, policy-driven, and cost-aware.
**Depends on**: Phase 3
**Requirements**: [ROUT-01, ROUT-02, ROUT-03]
**Success Criteria** (what must be TRUE):
  1. Public model catalog lists Hive aliases, pricing, and capability metadata without provider leakage.
  2. Routing uses an internal capability matrix, fallback policy, and allowlist checks before selecting an upstream provider.
  3. Cache-related token categories are captured in usage accounting when an upstream provider supports them.
**Plans**: 3 plans

Plans:
- [ ] 04-01: Create the Hive model catalog, alias schema, and pricing metadata.
- [ ] 04-02: Build provider capability matrices and routing policies over LiteLLM-backed adapters.
- [ ] 04-03: Add cache-aware usage attribution and sanitized provider error translation.

### Phase 5: API Keys & Hot-Path Enforcement
**Goal**: Give customers safe multi-key management while keeping authorization, budgets, and rate limits cheap on the hot path.
**Depends on**: Phase 4
**Requirements**: [KEY-01, KEY-02, KEY-03, KEY-04, KEY-05]
**Success Criteria** (what must be TRUE):
  1. Account owner can create multiple keys and only sees each secret once when it is issued.
  2. Keys support nickname, expiration, model allowlist, and per-key budget controls.
  3. Requests are rejected quickly when keys are revoked, expired, over budget, or over rate limit.
  4. Spend and usage are attributable per key and per model.
**Plans**: 3 plans

Plans:
- [ ] 05-01: Implement API key issuance, hashing, rotation, and revocation flows.
- [ ] 05-02: Add per-key budgets, expirations, allowlists, and hot-path policy checks.
- [ ] 05-03: Implement per-key usage attribution and Redis-backed rate limiting and quotas.

### Phase 6: Core Text & Embeddings API
**Goal**: Deliver the main OpenAI-compatible inference endpoints used by agents and developer workflows.
**Depends on**: Phase 5
**Requirements**: [API-01, API-02, API-03, API-04]
**Success Criteria** (what must be TRUE):
  1. Developers can call `responses`, `chat/completions`, and `completions` against Hive aliases with compatible request and response behavior.
  2. Streaming responses follow expected OpenAI SSE chunk structure and completion behavior.
  3. `embeddings` works with compatible request and response objects.
  4. Reasoning or thinking-related parameters and outputs are translated consistently when supported upstream.
**Plans**: 3 plans

Plans:
- [ ] 06-01: Implement `responses`, `chat/completions`, and `completions` on the public edge.
- [ ] 06-02: Normalize SSE streaming, reasoning fields, and usage accounting across providers.
- [ ] 06-03: Implement `embeddings` and verify compatibility with official SDK flows.

### Phase 7: Media, File, and Async API Surface
**Goal**: Extend compatibility to the file and media workflows needed by real OpenAI-integrated applications.
**Depends on**: Phase 6
**Requirements**: [API-05, API-06, API-07]
**Success Criteria** (what must be TRUE):
  1. Developers can use `files`, `uploads`, and `batches` end to end for the supported launch workflows.
  2. Image-generation and image-processing endpoints work with the supported OpenAI-compatible operations.
  3. Speech, transcription, and translation endpoints work with contract-consistent responses and error handling.
  4. Unsupported media or file cases return explicit OpenAI-style errors.
**Plans**: 3 plans

Plans:
- [ ] 07-01: Implement object-storage-backed `files`, `uploads`, and `batches` flows.
- [ ] 07-02: Implement image-generation and image-processing adapter routes.
- [ ] 07-03: Implement speech, transcription, and translation adapter routes.

### Phase 8: Payments, FX, and Compliance Checkout
**Goal**: Let customers buy credits safely across global and Bangladesh-local rails with reproducible FX and tax math.
**Depends on**: Phases 2-3
**Requirements**: [BILL-03, BILL-04, BILL-07]
**Success Criteria** (what must be TRUE):
  1. Customers can buy credits in 1,000-credit increments through Stripe, bKash, and SSLCommerz.
  2. Every BDT transaction stores the exact USD/BDT FX snapshot and 3% conversion fee used to compute the checkout amount.
  3. Checkout and invoicing flows apply country, business, tax, and surcharge data consistently per payment rail.
  4. Payment intents, webhooks, and ledger posting are idempotent and reconcilable.
**Plans**: 3 plans

Plans:
- [ ] 08-01: Build a canonical payment-intent and payment-rail abstraction.
- [ ] 08-02: Integrate Stripe, bKash, and SSLCommerz with idempotent webhook reconciliation.
- [ ] 08-03: Implement FX snapshots, surcharge logic, and tax evidence capture.

### Phase 9: Developer Console & Operational Hardening
**Goal**: Ship the customer-facing control plane and the operator-facing telemetry needed for launch.
**Depends on**: Phases 5-8
**Requirements**: [BILL-05, BILL-06, CONS-01, CONS-02, CONS-03, OPS-01]
**Success Criteria** (what must be TRUE):
  1. Customers can manage balance, top-ups, ledger history, invoices, tax profile, and API keys from the web console.
  2. Customers can browse model catalog and pricing and inspect privacy-safe usage, spend, and error trends by account, key, model, and time window.
  3. Customers can configure spend thresholds and receive budget-related notifications.
  4. Operators can monitor health, latency, upstream, billing, payment, and rate-limit signals needed to run the platform.
**Plans**: 3 plans

Plans:
- [ ] 09-01: Build console flows for billing, invoices, profile, and API key management.
- [ ] 09-02: Build model catalog, usage analytics, error inspection, and spend-alert UX.
- [ ] 09-03: Add operational dashboards, alerting, and launch-readiness hardening.

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Contract & Compatibility Harness | 4/4 | Complete | 2026-03-29 |
| 2. Identity & Account Foundation | 7/7 | Complete | 2026-03-29 |
| 3. Credits Ledger & Usage Accounting | 2/3 | In Progress | - |
| 4. Model Catalog & Provider Routing | 0/3 | Not started | - |
| 5. API Keys & Hot-Path Enforcement | 0/3 | Not started | - |
| 6. Core Text & Embeddings API | 0/3 | Not started | - |
| 7. Media, File, and Async API Surface | 0/3 | Not started | - |
| 8. Payments, FX, and Compliance Checkout | 0/3 | Not started | - |
| 9. Developer Console & Operational Hardening | 0/3 | Not started | - |
