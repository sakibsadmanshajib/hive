# Roadmap: Hive API Platform

## Milestones

- ✅ **v1.0 developer-api-core** — Phases 1–10 (shipped 2026-04-21) — see `.planning/milestones/v1.0-ROADMAP.md`
- 🚧 **v1.1 — deferred scope** — Phases 11–14 + 4 tech-debt items (planned) — see `.planning/v1.1-DEFERRED-SCOPE.md`

## Overview

Hive v1.0 shipped the developer-API core: OpenAI contract fidelity, prepaid billing
correctness, and provider abstraction. v1.1 closes the regulatory, hot-path-rate-limit,
console-integration, and invoice-row gaps surfaced during v1.0 stabilization.

## Phases

<details>
<summary>✅ v1.0 developer-api-core (Phases 1–10) — SHIPPED 2026-04-21</summary>

- [x] Phase 1: Contract & Compatibility Harness (4/4 plans) — 2026-03-29
- [x] Phase 2: Identity & Account Foundation (7/7 plans) — 2026-03-29
- [x] Phase 3: Credits Ledger & Usage Accounting (3/3 plans) — 2026-03-30
- [x] Phase 4: Model Catalog & Provider Routing (3/3 plans) — 2026-03-31
- [x] Phase 5: API Keys & Hot-Path Enforcement (6/6 plans) — 2026-04-05 (lifecycle + KEY-04; rate-limit carried to Phase 12)
- [x] Phase 6: Core Text & Embeddings API (4/4 plans) — 2026-04-09
- [x] Phase 7: Media, File, and Async API Surface (4/4 plans) — 2026-04-10
- [x] Phase 8: Payments, FX, and Compliance Checkout (3/3 plans) — 2026-04-11
- [x] Phase 9: Developer Console & Operational Hardening (4/4 plans) — 2026-04-11
- [x] Phase 10: Routing & Storage Critical Fixes (11/11 plans) — 2026-04-21

Full breakdown: `.planning/milestones/v1.0-ROADMAP.md`

</details>

### 🚧 v1.1 — deferred scope (Planned)

- [ ] **Phase 11: Compliance, Verification & Artifact Cleanup** — Remove `amount_usd` from BD checkout, formal VERIFICATION.md for Phases 2 & 3, live-verify analytics/monitoring, close AUTH-01..04, BILL-01/02/04, CONS-03, PRIV-01, OPS-01.
- [ ] **Phase 12: KEY-05 Hot-Path Rate Limiting** — Edge-enforced account-tier + per-key rate limits; close KEY-02 + KEY-05.
- [x] **Phase 13: Console Integration Fixes** — Audit-first web-console integration sweep; FX/USD leak strip on customer-surface `Invoice` interface, strict-TS cast removal, `lib/control-plane/types.ts` re-export shim, BDT-only billing + whole-console FX-guard Playwright specs. Closes CONSOLE-13-01..10. Six hand-offs filed to Phases 14/17/18 (fixture-seed flake, control-plane FX response strip, tier-aware viewer-gates). Shipped 2026-04-27.
- [ ] **Phase 14: Payments, Invoicing & Budget Integration** — Invoice-row creation on payment success + budget threshold enforcement on spend/grant paths; close BILL-05/06.

Plus four tech-debt items from v1.0 (see `.planning/v1.1-DEFERRED-SCOPE.md`):

- Batch success-path terminal settlement (local batch executor design).
- `ensureCapabilityColumns` wrong-table fix.
- `amount_usd` BD checkout removal.
- Formal VERIFICATION.md artifacts for Phases 2 + 3.

## Phase Details

### Phase 11: Compliance, Verification & Artifact Cleanup

**Goal:** Close the regulatory gap in BD checkout responses, formally verify orphaned Phase 2-3 requirements, and update stale planning artifacts.
**Depends on:** Phases 2, 3, 5, 8
**Requirements:** [AUTH-01, AUTH-02, AUTH-03, AUTH-04, BILL-01, BILL-02, PRIV-01, BILL-04, CONS-03, OPS-01]
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
**Depends on:** Phase 5
**Requirements:** [KEY-05, KEY-02]
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
**Depends on:** Phases 8, 9
**Requirements:** [BILL-03, BILL-07, CONS-01, CONS-02, KEY-01, KEY-03]
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
**Depends on:** Phases 8, 9, 13
**Requirements:** [BILL-05, BILL-06]
**Gap Closure:** Closes integration gaps #7 (invoice not created after payment) and #8 (budget threshold not enforced).
**Success Criteria** (what must be TRUE):
  1. Payment webhook success handler inserts a `payment_invoices` row; invoice appears in console list and PDF download.
  2. Credit spend paths call budget threshold check; threshold breach triggers notifier.
  3. Credit grant paths call budget threshold check after top-up.
  4. Notifier sends an actual notification (email or webhook) — not log-only.
  5. Budget threshold alert banner appears in console when threshold is breached.

Plans: 0 plans

## Progress

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|----------------|--------|-----------|
| 1. Contract & Compatibility Harness | v1.0 | 4/4 | Complete | 2026-03-29 |
| 2. Identity & Account Foundation | v1.0 | 7/7 | Complete | 2026-03-29 |
| 3. Credits Ledger & Usage Accounting | v1.0 | 3/3 | Complete | 2026-03-30 |
| 4. Model Catalog & Provider Routing | v1.0 | 3/3 | Complete | 2026-03-31 |
| 5. API Keys & Hot-Path Enforcement | v1.0 | 6/6 | Complete | 2026-04-05 |
| 6. Core Text & Embeddings API | v1.0 | 4/4 | Complete | 2026-04-09 |
| 7. Media, File, and Async API Surface | v1.0 | 4/4 | Complete | 2026-04-10 |
| 8. Payments, FX, and Compliance Checkout | v1.0 | 3/3 | Complete | 2026-04-11 |
| 9. Developer Console & Operational Hardening | v1.0 | 4/4 | Complete | 2026-04-11 |
| 10. Routing & Storage Critical Fixes | v1.0 | 11/11 | Complete | 2026-04-21 |
| 11. Compliance, Verification & Artifact Cleanup | v1.1 | 0/0 | Planned | - |
| 12. KEY-05 Hot-Path Rate Limiting | v1.1 | 0/0 | Planned | - |
| 13. Console Integration Fixes | v1.1 | 0/0 | Planned | - |
| 14. Payments, Invoicing & Budget Integration | v1.1 | 0/0 | Planned | - |

---

*Last updated: 2026-04-21 after v1.0 milestone completion.*
