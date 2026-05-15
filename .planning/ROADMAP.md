# Roadmap: Hive API Platform

## Milestones

- ✅ **v1.0 developer-api-core** — Phases 1–10 (shipped 2026-04-21) — see `.planning/milestones/v1.0-ROADMAP.md`
- 🚧 **v1.1 — deferred scope** — Phases 11–18 + 4 tech-debt items (planned) — see `.planning/v1.1-DEFERRED-SCOPE.md`

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
- [x] **Phase 15: Batch Local Executor** — Local fan-out batch executor in control-plane to settle success-path terminal state for OpenRouter/Groq (no native batch API). Closes deferred tech-debt item #5. Known caveat #2 (resume sentinel) inherited by Phase 18-batch follow-up.
- [x] **Phase 16: Capability Columns Fix** — Remove dead `ensureCapabilityColumns` DDL path in `routing/repository.go` (targeted wrong table); migration `20260414_01_provider_capabilities_media_columns.sql` is authoritative; regression guard `TestRoutingRepositoryDoesNotRunCapabilityDDL` enforces non-recurrence.
- [x] **Phase 17: FX/USD Zero-Leak** — Strip `amount_usd` / FX rate / provider hints from all customer-bound surfaces (payments DTOs, ledger invoice rows, web-console types, PDF rendering, post-purchase grant metadata). Adversarial walk of every `map[string]any` customer wire. CI-blocking lint `lint-no-customer-usd.mjs`. Closes BD regulatory gap. PR #137 ready-for-review 2026-05-09.
- [ ] **Phase 18: RBAC Matrix** — Reusable verification-aware permission matrix replacing ad hoc `CanInviteMembers` / `CanManageAPIKeys` / `is_platform_admin` booleans. Roles (guest/unverified/member/owner/platform_admin) × scopes (workspace/platform) × named permissions (billing.*, api_keys.*, analytics.*, members.*, workspace.settings.*) enforced in control-plane handlers AND mirrored in web-console route/nav gating. Inherits HANDOFF-17-01 (`is_platform_admin` replacement) and Phase 14 stub `internal/platform/role.go`. Blocks v1.1 ship-gate.

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

### Phase 13: Console Integration Fixes (SHIPPED 2026-04-27)

**Goal:** Audit-first console integration sweep — strip customer-surface FX/USD leak, eliminate unsafe TypeScript widening casts, add canonical control-plane types re-export shim, lock regression with BDT-only billing + whole-console FX-guard Playwright specs.
**Depends on:** Phases 8, 9, 12
**Requirements:** [CONSOLE-13-01..10]
**Gap Closure:** Removes `Invoice.amount_usd` from customer-facing type/decoder (P0 regulatory). Replaces unsafe `as { rails?: unknown }` cast with structural guard. Adds `lib/control-plane/types.ts` re-export shim. Files Phase 14/17/18 hand-offs (HANDOFF-13-01..06) for fixture-seed flake, control-plane FX response strip, tier-aware viewer-gates, discretionary credit-grant UI. The original BILL-03/BILL-07/CONS-01/CONS-02/KEY-01/KEY-03 remain Pending — re-routed to a future phase.
**Outcome:**
  1. `Invoice` interface and decoder strip `amount_usd`; runtime fallback to `"USD"` removed (now treated as decode failure).
  2. Strict-TS cleanliness: zero `as any` / `as unknown` / `<any>` / `<unknown>` matches in `apps/web-console/{app,components,lib}`.
  3. New `tests/e2e/console-billing.spec.ts` and `tests/e2e/console-fx-guard.spec.ts` lock the BDT-only customer surface across 9 console routes.
  4. New `tests/unit/invoice-decode.test.ts` enforces type-level + runtime FX-leak guard, including optional-field reintroduction.
  5. CONSOLE-13-01..10 satisfied with evidence files; six Phase 14/17/18 hand-offs filed.

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

### Phase 18: RBAC Matrix

**Goal:** Replace ad hoc workspace-role + `is_platform_admin` + `CanInviteMembers` / `CanManageAPIKeys` derived booleans with a reusable, verification-aware authorization model (roles × scopes × named permissions) enforced authoritatively in the control-plane and mirrored in the web-console route/nav gating layer.
**Depends on:** Phases 2 (identity), 5 (API keys), 9 (console), 13 (viewer-gates seed), 14 (`platform.IsWorkspaceOwner` / `IsPlatformAdmin` Phase 14 stub), 17 (HANDOFF-17-01).
**Requirements:** [RBAC-18-01..NN — TBD in REQUIREMENTS.md]
**Gap Closure:** Closes v1.1 ship-gate item `rbac_matrix`. Replaces the latent gap surfaced in v1.1-DEFERRED-SCOPE.md item #8 (authorization is limited to workspace membership roles plus feature-specific booleans). Inherits HANDOFF-17-01 — `is_platform_admin` becomes a derived attribute of the new model rather than a free-standing flag.
**Success Criteria** (what must be TRUE):
  1. A single Go authz package defines an explicit `Role` enum (`guest`, `unverified`, `member`, `owner`, `platform_admin`), an explicit `Permission` enum (billing.view, billing.write, api_keys.read, api_keys.manage, analytics.view, members.invite, members.manage, workspace.settings, platform.admin), and a `Policy.Can(actor, scope, permission)` decision function.
  2. Every control-plane handler that today checks `viewer.EmailVerified && chosen.Role == "owner"` or `IsPlatformAdmin` (or equivalent ad hoc) routes through the policy package — no direct role/flag comparison in business code.
  3. `apps/web-console/lib/viewer-gates.ts` exports a single `can(viewer, permission)` helper derived from the same matrix; `canInviteMembers` / `canManageApiKeys` / `allowedUnverifiedRoutes` are reimplemented as thin wrappers or removed; consumers (sidebar nav, route guards) use the new helper.
  4. Regression coverage: Go integration tests assert that an unverified actor cannot access billing, api_keys, analytics, members, or workspace.settings handlers; a verified member can access member-scoped reads but not owner-only writes; an owner can access workspace-scoped surfaces; a platform_admin can access platform-scoped surfaces.
  5. Web-console: vitest unit tests assert `can()` returns the same decisions for every (role, perm) pair as the Go matrix; one Playwright spec covers the unverified flow on `/billing` and `/keys` (must redirect or block).
  6. STATE.md `v1_1_ship_gate.rbac_matrix` flipped to `true`. Pending todo `2026-04-22-design-rbac-authorization-model.md` resolved to `done`.

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
| 13. Console Integration Fixes | v1.1 | n/a | Complete | 2026-04-27 |
| 14. Payments, Invoicing & Budget Integration | v1.1 | 0/0 | Planned | - |
| 15. Batch Local Executor | v1.1 | n/a | Complete | 2026-04-?? |
| 16. Capability Columns Fix | v1.1 | n/a | Complete | 2026-04-25 |
| 17. FX/USD Zero-Leak | v1.1 | n/a | Complete | 2026-05-09 (PR #137) |
| 18. RBAC Matrix | v1.1 | 0/0 | Planned | - |

---

*Last updated: 2026-05-14 — added Phase 18 (RBAC) entry; backfilled Phases 15/16/17 to reflect on-disk state. Phase 17 PR #137 ready-for-review.*
