# Requirements: Hive API Platform

**Defined:** 2026-03-28
**Core Value:** Developers can switch from OpenAI to Hive with only a base URL and API key change, while keeping predictable prepaid billing and provider-agnostic operations.

## v1 Requirements

### Authentication & Accounts

- [ ] **AUTH-01**: Developer can sign up and sign in with email and password using Supabase-backed authentication.
- [ ] **AUTH-02**: Developer receives email verification and can reset password through an email-based recovery flow.
- [ ] **AUTH-03**: Developer session persists across browser refresh in the billing and key-management console.
- [ ] **AUTH-04**: Account owner can maintain billing contact, legal entity, country, and VAT/business information used for invoicing and tax handling.

### Compatibility & Contract

- [x] **COMP-01**: Developer can use the official OpenAI JavaScript/TypeScript, Python, and Java SDKs against Hive by changing only base URL and API key for supported endpoints.
- [x] **COMP-02**: Hive returns OpenAI-style HTTP status codes, error objects, and compatibility headers for both supported requests and explicit unsupported-feature responses.
- [x] **COMP-03**: Developer can browse Swagger/OpenAPI documentation that matches the Hive public API contract and supported launch surface.

### Inference Surface

- [x] **API-01**: Developer can call `responses`, `chat/completions`, and `completions` with OpenAI-compatible request and response shapes.
- [x] **API-02**: Developer can stream supported text-generation endpoints with OpenAI-compatible SSE event ordering, chunk formats, and terminal events.
- [x] **API-03**: Developer can call `embeddings` with OpenAI-compatible request and response behavior.
- [x] **API-04**: Developer can use reasoning or thinking-related request parameters, and Hive returns translated reasoning outputs and usage details when upstream support exists.
- [ ] **API-05**: Developer can call image-generation and image-processing endpoints with OpenAI-compatible behavior for supported operations.
- [ ] **API-06**: Developer can call speech, transcription, and translation endpoints with OpenAI-compatible behavior for supported operations.
- [ ] **API-07**: Developer can use `files`, `uploads`, and `batches` flows required by official SDK integrations.
- [x] **API-08**: Public non-org/admin endpoints outside the initial launch subset are explicitly classified and return OpenAI-style unsupported responses until implemented.

### Model Catalog & Routing

- [x] **ROUT-01**: Developer can list Hive-owned public model aliases, capabilities, and prices without seeing upstream provider identities.
- [ ] **ROUT-02**: Requests route only to internally approved providers and models that satisfy the alias capability matrix, fallback policy, and account or key allowlists.
- [x] **ROUT-03**: When an upstream provider supports cache-aware billing semantics, Hive tracks and itemizes the related token categories without exposing the provider name.

### Billing & Payments

- [ ] **BILL-01**: Customer has a prepaid Hive Credit balance backed by an immutable ledger of purchases, reservations, charges, refunds, and adjustments.
- [ ] **BILL-02**: Hive reserves credits before execution and finalizes or refunds usage accurately after success, failure, cancellation, retry, or interrupted stream completion.
- [x] **BILL-03**: Customer can buy credits in increments of 1,000 Hive Credits through Stripe, bKash, and SSLCommerz.
- [ ] **BILL-04**: Hive prices credits at 100,000 Hive Credits per 1 USD and persists the exact FX snapshot plus 5% conversion fee used for every BDT transaction.
- [x] **BILL-05**: Customer can view invoices, receipts, and itemized spend by model, API key, and time window.
- [x] **BILL-06**: Customer can set account-level budgets and spend-threshold notifications in Hive Credits.
- [x] **BILL-07**: Hive captures and applies country, business, tax, and payment-method surcharge data needed for compliant checkout and invoicing flows.

### API Keys & Limits

- [x] **KEY-01**: Account owner can create multiple API keys under one account and sees each raw secret only once at creation time.
- [ ] **KEY-02**: Account owner can set per-key nickname, expiration date, allowed models, and Hive Credit budget.
- [x] **KEY-03**: Account owner can revoke or rotate one API key without affecting other keys on the account.
- [ ] **KEY-04**: Hive tracks usage and spend per API key and per model.
- [ ] **KEY-05**: Hive enforces account-tier and per-key rate limits and quotas on the hot path.

### Developer Console

- [x] **CONS-01**: Customer can manage balance, top-ups, ledger entries, invoices, and tax profile from a web console.
- [x] **CONS-02**: Customer can manage API keys, model allowlists, model catalog visibility, and pricing visibility from a web console.
- [x] **CONS-03**: Customer can inspect privacy-safe usage analytics, error history, and spend trends by account, key, model, and time window from a web console.

### Privacy & Operations

- [ ] **PRIV-01**: Customer can use the API without Hive storing prompt or response bodies at rest by default.
- [x] **OPS-01**: Hive operators can monitor health, latency, upstream failures, payment workflows, rate-limit events, and billing events without transcript storage.

## v2 Requirements

### SDKs & Packaging

- **SDK-01**: Hive provides first-party branded SDK wrappers for JavaScript/TypeScript, Python, and Java on top of the OpenAI-compatible API.
- **SUBS-01**: Customer can buy subscription-like credit bundles that still resolve to Hive Credits internally.

### Advanced Operations

- **ENT-01**: Business customer can manage advanced organization hierarchies, procurement controls, and approval workflows.
- **ANAL-01**: Hive offers warehouse-backed deep analytics beyond the launch reporting stack.

## Out of Scope

| Feature | Reason |
|---------|--------|
| End-user chat web application | Launch is strictly a developer API and control-plane product. |
| RAG projects or workspaces | Requires separate retrieval, workspace, and content-governance semantics. |
| Hosted code runner or dev environment | Separate isolation and cost model from the API launch. |
| Credit subscriptions at launch | Commercial model is prepaid only for v1. |
| Customer-supplied upstream provider keys | Hive manages provider credentials internally and hides provider identity. |
| OpenAI org/admin management endpoints | Not part of the drop-in developer value proposition for the launch product. |
| Storing prompt or completion bodies by default | Conflicts with the launch privacy requirement. |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| AUTH-01 | Phase 11 | Pending |
| AUTH-02 | Phase 11 | Pending |
| AUTH-03 | Phase 11 | Pending |
| AUTH-04 | Phase 11 | Pending |
| COMP-01 | Phase 1 | Complete |
| COMP-02 | Phase 1 | Complete |
| COMP-03 | Phase 1 | Complete |
| API-01 | Phase 6 | Complete |
| API-02 | Phase 6 | Complete |
| API-03 | Phase 6 | Complete |
| API-04 | Phase 6 | Complete |
| API-05 | Phase 10 | Pending |
| API-06 | Phase 10 | Pending |
| API-07 | Phase 10 | Pending |
| API-08 | Phase 1 | Complete |
| ROUT-01 | Phase 4 | Complete |
| ROUT-02 | Phase 10 | Pending |
| ROUT-03 | Phase 4 | Complete |
| BILL-01 | Phase 11 | Pending |
| BILL-02 | Phase 11 | Pending |
| BILL-03 | Phase 8 | Complete |
| BILL-04 | Phase 11 | Pending |
| BILL-05 | Phase 9 | Complete |
| BILL-06 | Phase 9 | Complete |
| BILL-07 | Phase 8 | Complete |
| KEY-01 | Phase 5 | Complete |
| KEY-02 | Phase 5 | Pending |
| KEY-03 | Phase 5 | Complete |
| KEY-04 | Phase 5 | Pending |
| KEY-05 | Phase 12 | Pending |
| CONS-01 | Phase 9 | Complete |
| CONS-02 | Phase 9 | Complete |
| CONS-03 | Phase 9 | Complete |
| PRIV-01 | Phase 11 | Pending |
| OPS-01 | Phase 9 | Complete |

**Coverage:**
- v1 requirements: 35 total
- Mapped to phases: 35
- Unmapped: 0

---
*Requirements defined: 2026-03-28*
*Last updated: 2026-04-12 after gap closure phases 10-12 created*
