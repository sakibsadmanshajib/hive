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
- [x] **Phase 3: Credits Ledger & Usage Accounting** - Build immutable prepaid billing, reservations, and privacy-safe usage events. (completed 2026-03-30)
- [x] **Phase 4: Model Catalog & Provider Routing** - Create Hive aliases, pricing catalog, routing policy, and provider capability matrix. (completed 2026-03-31)
- [ ] **Phase 5: API Keys & Hot-Path Enforcement** - Add key lifecycle, per-key controls, budgets, rate limits, and hot-path authorization.
- [x] **Phase 6: Core Text & Embeddings API** - Deliver the most-used OpenAI-compatible inference endpoints with streaming and reasoning support. (completed 2026-04-09)
- [x] **Phase 7: Media, File, and Async API Surface** - Expand to files, uploads, batches, images, and audio workflows. (completed 2026-04-10)
- [x] **Phase 8: Payments, FX, and Compliance Checkout** - Add Stripe, bKash, SSLCommerz, FX snapshots, and tax-aware checkout. (completed 2026-04-11)
- [x] **Phase 9: Developer Console & Operational Hardening** - Finish user-facing billing and usage UX plus production observability and alerts. (completed 2026-04-11)
- [ ] **Phase 10: Routing & Storage Critical Fixes** - Fix capability schema drift, Supabase S3 storage wiring, batch lifecycle, and batch attribution. (verification gaps found 2026-04-20)
- [ ] **Phase 11: Compliance, Verification & Artifact Cleanup** - Remove amount_usd from BD checkout, verify orphaned Phase 2-3 requirements, and live-verify analytics and monitoring.
- [ ] **Phase 12: KEY-05 Hot-Path Rate Limiting** - Enforce account-tier and per-key rate limits on the hot path; fix media/batch auth policy bypass.
- [ ] **Phase 13: Console Integration Fixes** - Add web-console proxy routes for checkout and API key mutations; wire Buy Credits modal and rotate page.
- [ ] **Phase 14: Payments, Invoicing & Budget Integration** - Create invoice rows on payment success; wire budget threshold checks into spend/grant paths.

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
**Plans**: 3/3 plans complete

Plans:
- [x] 03-01: Implement the Hive Credit ledger, idempotency model, and balance calculations.
- [x] 03-02: Add privacy-safe usage events and request accounting primitives.
- [x] 03-03: Build reservation, finalization, and refund paths for streaming and retry scenarios.

### Phase 4: Model Catalog & Provider Routing
**Goal**: Expose Hive-owned model aliases while keeping provider selection internal, policy-driven, and cost-aware.
**Depends on**: Phase 3
**Requirements**: [ROUT-01, ROUT-02, ROUT-03]
**Success Criteria** (what must be TRUE):
  1. Public model catalog lists Hive aliases, pricing, and capability metadata without provider leakage.
  2. Routing uses an internal capability matrix, fallback policy, and allowlist checks before selecting an upstream provider.
  3. Cache-related token categories are captured in usage accounting when an upstream provider supports them.
**Plans**: 3/3 plans complete

Plans:
- [x] 04-01: Create the Hive model catalog, alias schema, and pricing metadata.
- [x] 04-02: Build provider capability matrices and routing policies over LiteLLM-backed adapters.
- [x] 04-03: Add cache-aware usage attribution and sanitized provider error translation.

### Phase 5: API Keys & Hot-Path Enforcement
**Goal**: Give customers safe multi-key management while keeping authorization, budgets, and rate limits cheap on the hot path.
**Depends on**: Phase 4
**Requirements**: [KEY-01, KEY-02, KEY-03, KEY-04, KEY-05]
**Success Criteria** (what must be TRUE):
  1. Account owner can create multiple keys and only sees each secret once when it is issued.
  2. Keys support nickname, expiration, model allowlist, and per-key budget controls.
  3. Requests are rejected quickly when keys are revoked, expired, over budget, or over rate limit.
  4. Spend and usage are attributable per key and per model.
**Plans**: 2/6 plans complete

Plans:
- [x] 05-01: Implement API key issuance, hashing, rotation, revocation, and customer-visible key summaries.
- [ ] 05-02: Add durable key policy storage, policy updates, and control-plane snapshot/detail projection.
- [ ] 05-03: Add edge snapshot resolution, alias enforcement, and projected-cost budget admission.
- [x] 05-04: Close the two diagnosed Phase 05 UAT gaps around snapshot invalidation and end-to-end API-key attribution.
- [ ] 05-05: Add per-key usage rollups, live budget-window projection, and separate account/key rate-policy sources.
- [ ] 05-06: Implement Redis Lua rate limiting and 429 header handling from projected account/key policies.

### Phase 6: Core Text & Embeddings API
**Goal**: Deliver the main OpenAI-compatible inference endpoints used by agents and developer workflows.
**Depends on**: Phase 5
**Requirements**: [API-01, API-02, API-03, API-04]
**Success Criteria** (what must be TRUE):
  1. Developers can call `responses`, `chat/completions`, and `completions` against Hive aliases with compatible request and response behavior.
  2. Streaming responses follow expected OpenAI SSE chunk structure and completion behavior.
  3. `embeddings` works with compatible request and response objects.
  4. Reasoning or thinking-related parameters and outputs are translated consistently when supported upstream.
**Plans**: 4/4 plans complete

Plans:
- [x] 06-01-PLAN.md — Internal control-plane accounting/usage endpoints for edge-to-control-plane service calls (Wave 1)
- [x] 06-02-PLAN.md — Inference types, LiteLLM client, orchestrator, and non-streaming chat/completions + completions handlers (Wave 1)
- [x] 06-03-PLAN.md — SSE streaming relay, Responses API event translation, and reasoning field normalization (Wave 2)
- [x] 06-04-PLAN.md — Embeddings endpoint and SDK integration tests for all Phase 6 endpoints (Wave 2)
### Phase 7: Media, File, and Async API Surface
**Goal**: Extend compatibility to the file and media workflows needed by real OpenAI-integrated applications.
**Depends on**: Phase 6
**Requirements**: [API-05, API-06, API-07]
**Success Criteria** (what must be TRUE):
  1. Developers can use `files`, `uploads`, and `batches` end to end for the supported launch workflows.
  2. Image-generation and image-processing endpoints work with the supported OpenAI-compatible operations.
  3. Speech, transcription, and translation endpoints work with contract-consistent responses and error handling.
  4. Unsupported media or file cases return explicit OpenAI-style errors.
**Plans**: 4 plans

Plans:
- [x] 07-01-PLAN.md — Storage infrastructure (legacy local object-store emulator, S3 client), file/upload/batch Postgres schemas, control-plane filestore service, and routing capability flag extensions (Wave 1)
- [x] 07-02-PLAN.md — Image generation/edits and audio speech/transcription/translation endpoint handlers with LiteLLM dispatch (Wave 2)
- [x] 07-03-PLAN.md — Files API, Uploads API, Batches API edge handlers, and Asynq batch polling worker (Wave 2)
- [ ] 07-04-PLAN.md — Gap closure: Add auth, routing, and accounting to images and audio handlers (Wave 1)

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
- [ ] 08-01-PLAN.md — Payment types, DB migrations, PaymentRail interface, FX service, tax calculation, repository, and intent service (Wave 1)
- [ ] 08-02-PLAN.md — Stripe, bKash, and SSLCommerz rail implementations with webhook signature verification (Wave 2)
- [ ] 08-03-PLAN.md — HTTP handler, router registration, and main.go wiring for checkout and webhook endpoints (Wave 2)

### Phase 9: Developer Console & Operational Hardening
**Goal**: Ship the customer-facing control plane and the operator-facing telemetry needed for launch.
**Depends on**: Phases 5-8
**Requirements**: [BILL-05, BILL-06, CONS-01, CONS-02, CONS-03, OPS-01]
**Success Criteria** (what must be TRUE):
  1. Customers can manage balance, top-ups, ledger history, invoices, tax profile, and API keys from the web console.
  2. Customers can browse model catalog and pricing and inspect privacy-safe usage, spend, and error trends by account, key, model, and time window.
  3. Customers can configure spend thresholds and receive budget-related notifications.
  4. Operators can monitor health, latency, upstream, billing, payment, and rate-limit signals needed to run the platform.
**Plans**: 4 plans (4 waves)

Plans:
- [ ] 09-04-PLAN.md — Prometheus instrumentation, Grafana dashboards, Alertmanager, and Docker Compose monitoring profile (Wave 1)
- [ ] 09-01-PLAN.md — Control-plane backend: analytics aggregation endpoints, invoice/budget migrations with email notification, cursor pagination, public catalog (Wave 2)
- [ ] 09-02-PLAN.md — Console billing, invoices, checkout modal with BDT compliance test, API key management, and model catalog pages (Wave 3)
- [ ] 09-03-PLAN.md — Console analytics tabs with Recharts, time-window filtering, budget alert form and banner (Wave 4)
### Phase 10: Routing & Storage Critical Fixes
**Goal:** Fix the three infrastructure bugs that break all inference and media endpoints, and fully remove the legacy local object-storage implementation from the codebase.
**Depends on**: Phases 4, 7
**Requirements**: [ROUT-02, API-05, API-06, API-07, KEY-04]
**Gap Closure:** Closes integration gaps #1 (ensureCapabilityColumns wrong table), #2 (legacy S3 client incompatibility), #3 (StorageUploader nil). Fixes all 3 broken E2E flows. Purges all legacy object-storage references. Wires batch final settlement and per-key attribution.
**Success Criteria** (what must be TRUE):
  1. `provider_capabilities` table has all 5 media capability columns via proper SQL migration.
  2. File/image/audio/batch endpoints use Supabase Storage REST API — no legacy object-store client dependency.
  3. Batch worker has a wired StorageUploader for output file upload.
  4. Zero references to the legacy local object-storage implementation remain in application code, Docker config, or documentation.
  5. All 3 previously broken flows (image/audio routing, file/batch registration, batch output) pass.
  6. Batch final settlement correctly attributes spend and usage per API key and model.

**Plans:** 9/11 plans executed
**Verification:** gaps_found — see `.planning/phases/10-routing-storage-critical-fixes/10-VERIFICATION.md`

Plans:
- [x] 10-01-PLAN.md — Wave 0 red validation for shared storage, edge storage config, and status-aware live smoke probes
- [x] 10-02-PLAN.md — Wave 0 red validation for routing schema, media/batch route eligibility, filestore internal contracts, and batch output persistence
- [x] 10-03-PLAN.md — Supabase migrations and backfill for provider media columns, plus filestore tables; remove runtime DDL
- [x] 10-04-PLAN.md — Shared path-style S3-over-HTTP storage package using SigV4 signing
- [x] 10-05-PLAN.md — Edge media/file/batch route wiring with required shared storage config
- [x] 10-06-PLAN.md — Control-plane filestore response fields, batch status persistence, and StorageUploader wiring
- [x] 10-07-PLAN.md — Env documentation and repository-wide legacy storage reference purge
- [x] 10-08-PLAN.md — Final route/media checks, full suite, live smoke, and purge verification
- [x] 10-09-PLAN.md — Gap closure: accepted accounting policy modes and batch model alias reservation propagation
- [ ] 10-10-PLAN.md — Gap closure: batch attribution persistence and edge-to-control-plane propagation
- [ ] 10-11-PLAN.md — Gap closure: terminal reservation settlement, KEY-04, full suite, purge, and live smoke gate

### Phase 11: Compliance, Verification & Artifact Cleanup
**Goal:** Close the regulatory gap in BD checkout responses, formally verify orphaned Phase 2-3 requirements, and update stale planning artifacts.
**Depends on**: Phases 2, 3, 5, 8
**Requirements**: [AUTH-01, AUTH-02, AUTH-03, AUTH-04, BILL-01, BILL-02, PRIV-01, BILL-04, CONS-03, OPS-01]
**Gap Closure:** Closes integration gaps #4 (amount_usd exposed) and #5 (ViewerAccount.slug empty). Formally verifies 7 orphaned requirements. Live-verifies analytics and monitoring. Updates stale planning artifacts.
**Success Criteria** (what must be TRUE):
  1. BD checkout responses never include `amount_usd` or any field exposing FX rates.
  2. ViewerAccount.slug is populated from control-plane viewer endpoint.
  3. 02-VERIFICATION.md exists and formally verifies AUTH-01 through AUTH-04.
  4. 03-VERIFICATION.md exists and formally verifies BILL-01, BILL-02, and PRIV-01.
  5. REQUIREMENTS.md checkboxes for KEY-02 and KEY-04 are checked. Phase 5 ROADMAP progress is accurate.
  6. Live analytics charts render correct data; batch completeness is verified end-to-end.
  7. Prometheus, Grafana, and Alertmanager are verified live against the running stack.

Plans: 0 plans

### Phase 12: KEY-05 Hot-Path Rate Limiting
**Goal:** Complete the last unsatisfied requirement — account-tier and per-key rate limits enforced on the hot path.
**Depends on**: Phase 5
**Requirements**: [KEY-05, KEY-02]
**Gap Closure:** Closes KEY-05 (rate limiting) and KEY-02 (media/batch auth policy bypass). Re-verifies current implementation state and fills remaining hot-path gaps.
**Success Criteria** (what must be TRUE):
  1. Edge proxy enforces account-tier rate limits before dispatch.
  2. Edge proxy enforces per-key rate limits before dispatch.
  3. Rate-limited requests receive 429 with Retry-After header.
  4. Rate limit configuration flows from control-plane snapshot to edge enforcement.
  5. Phase 5 VERIFICATION.md marks KEY-05 as SATISFIED.
  6. Image, audio, and batch auth adapters pass a non-empty model and correct estimated credits to the policy engine — allowlist, budget, and quota scoring apply.

Plans: 0 plans

### Phase 13: Console Integration Fixes
**Goal:** Add the missing web-console proxy routes and UI fixes that make checkout, API key management, and billing pages fully functional from the browser.
**Depends on**: Phases 8, 9
**Requirements**: [BILL-03, BILL-07, CONS-01, CONS-02, KEY-01, KEY-03]
**Gap Closure:** Closes integration gaps #5 (console checkout not reachable) and #6 (console API key mutations broken).
**Success Criteria** (what must be TRUE):
  1. Buy Credits CTA opens a rendered checkout modal; modal submits to a working web-console proxy route.
  2. Checkout applies tax/rail data and posts to control-plane payment intent endpoint.
  3. API key create and revoke fetch correct web-console proxy routes and receive control-plane responses.
  4. API key rotate page exists and completes rotation end-to-end.
  5. Billing and key-management console pages pass a full browser E2E walkthrough.

Plans: 0 plans

### Phase 14: Payments, Invoicing & Budget Integration
**Goal:** Wire the two missing backend accounting integrations: invoice row creation on payment success and budget threshold enforcement on spend/grant paths.
**Depends on**: Phases 8, 9, 13
**Requirements**: [BILL-05, BILL-06]
**Gap Closure:** Closes integration gaps #7 (invoice not created after payment) and #8 (budget threshold not enforced).
**Success Criteria** (what must be TRUE):
  1. Payment webhook success handler inserts a `payment_invoices` row; invoice appears in console list and PDF download.
  2. Credit spend paths call budget threshold check; threshold breach triggers notifier.
  3. Credit grant paths call budget threshold check after top-up.
  4. Notifier sends an actual notification (email or webhook) — not log-only.
  5. Budget threshold alert banner appears in console when threshold is breached.

Plans: 0 plans

## Progress

**Execution Order:**
Phases execute in numeric order: 1 -> 2 -> 3 -> 4 -> 5 -> 6 -> 7 -> 8 -> 9

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Contract & Compatibility Harness | 4/4 | Complete | 2026-03-29 |
| 2. Identity & Account Foundation | 7/7 | Complete | 2026-03-29 |
| 3. Credits Ledger & Usage Accounting | 3/3 | Complete | 2026-03-30 |
| 4. Model Catalog & Provider Routing | 3/3 | Complete | 2026-03-31 |
| 5. API Keys & Hot-Path Enforcement | 2/6 | In Progress | - |
| 6. Core Text & Embeddings API | 4/4 | Complete | 2026-04-09 |
| 7. Media, File, and Async API Surface | 4/4 | Complete   | 2026-04-10 |
| 8. Payments, FX, and Compliance Checkout | 3/3 | Complete   | 2026-04-11 |
| 9. Developer Console & Operational Hardening | 4/4 | Complete   | 2026-04-11 |
| 10. Routing & Storage Critical Fixes | 9/11 | In Progress|  |
| 11. Compliance, Verification & Artifact Cleanup | 0/0 | Pending | - |
| 12. KEY-05 Hot-Path Rate Limiting | 0/0 | Pending | - |
| 13. Console Integration Fixes | 0/0 | Pending | - |
| 14. Payments, Invoicing & Budget Integration | 0/0 | Pending | - |
