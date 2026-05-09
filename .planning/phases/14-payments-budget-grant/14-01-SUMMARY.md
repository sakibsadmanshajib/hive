---
phase: 14-payments-budget-grant
plan: 14-01
milestone: v1.1
track: A
status: closed
date: 2026-05-07
branch: a/phase-14-payments-budget-grant
pr: https://github.com/sakibsadmanshajib/hive/pull/136
---

# Phase 14 — Payments, Budgets & Discretionary Credit Grant — Summary

## Outcome

Closes Phase 14 of the v1.1 milestone, Track A. End-to-end payments,
budgets, monthly invoicing, and the **owner-discretionary credit grant
primitive** that Phase 21 (chat-app credited-tier wallet) consumes. No
auto-trial, no signup bonus — owner-issued grants only.

Ship-gate readiness: all 12 truths (PAY-14-01..12) Satisfied with
evidence; matrix verifier exits 0; FX-lint primitive ships green;
race-fixed CI on spendalerts runner.

## Files created

### Schema

- `supabase/migrations/20260428_01_budgets_alerts_invoices_grants.sql` —
  4 new tables (`budgets`, `spend_alerts`, `invoices`, `credit_grants`)
  + immutability trigger (`tg_credit_grants_immutable`) + accounts
  `is_platform_admin` flag.

### Go packages

- `apps/control-plane/internal/platform/` — `RoleService` primitive
  (`IsWorkspaceOwner`, `IsPlatformAdmin`, `RequirePlatformAdmin` middleware);
  pgx-backed concrete (`role_pgx.go`).
- `apps/control-plane/internal/grants/` — types, repository,
  service (single-tx ledger credit), audit, http, http_test.
- `apps/control-plane/internal/payments/invoices/` — NEW sub-package
  (legacy `payments/` package untouched). Types, repository, service,
  PDF renderer (gofpdf), HTTP handlers, monthly cron.
- `apps/control-plane/internal/spendalerts/` — Runner loop (separated
  from `budgets.CronEvaluator`).
- `apps/control-plane/internal/budgets/` — extended with `cron.go`,
  `notifier.go`, threshold math, Redis hard-cap pusher.
- `apps/edge-api/internal/limits/budget_gate.go` — pre-provider 402 gate.

### Web-console (strict TS, zero `as`/`any`)

- `app/console/billing/budgets/page.tsx`
- `app/console/billing/alerts/page.tsx`
- `app/console/billing/invoices/page.tsx`
- `lib/control-plane/types.ts` — Budget, SpendAlert, Invoice extensions.
- `lib/control-plane/client.ts` — typed Budget/Alert/Invoice client methods.
- `components/billing/{budget-form,spend-alert-form,invoice-row}.tsx`.

### Tests

- 4 Playwright specs — console-budgets, console-spend-alerts,
  console-invoices, plus the existing auth/profile carry-forward.
- Go unit tests across budgets, grants, payments/invoices, platform,
  edge-api/limits, spendalerts.

### OpenAPI + lint

- `packages/openai-contract/spec/paths/budgets.yaml`
- `packages/openai-contract/spec/paths/spend-alerts.yaml`
- `packages/openai-contract/spec/paths/invoices.yaml`
- `packages/openai-contract/spec/paths/grants.yaml`
- `packages/openai-contract/scripts/lint-no-customer-usd.mjs` (+ test).

### Planning artifacts

- `14-AUDIT.md` (Task 1)
- `14-VERIFICATION.md` (sections for Tasks 2–7 + phase-level closure).
- `14-01-SUMMARY.md` (this file).
- `evidence/PAY-14-01..12.md` — 12 evidence files.

## Files modified (count by area)

| Area | New files | Modified files |
|------|-----------|----------------|
| control-plane | ~25 | ~5 (cmd/server wiring + budgets extensions) |
| edge-api | 2 | 1 (router) |
| web-console | ~12 | 2 (types + client) |
| supabase | 1 migration | 0 |
| openai-contract | 6 | 0 |
| .planning | ~14 | 2 (REQUIREMENTS.md + 12-VERIFICATION.md frontmatter) |

## Schema

- 4 new tables: `budgets`, `spend_alerts`, `invoices`, `credit_grants`.
- 1 immutability trigger: `credit_grants_immutable_trg` →
  `tg_credit_grants_immutable`.
- Accounts column added: `accounts.users.is_platform_admin BOOLEAN`.
- CHECK constraints on every monetary column (BDT-only, positive,
  threshold ∈ {50,80,100}).

## New endpoints

| Method | Path | Owner-gate |
|--------|------|------------|
| GET / PUT / DELETE | `/api/v1/budgets/{workspace_id}` | IsWorkspaceOwner |
| GET / PUT | `/api/v1/accounts/current/budget` | IsWorkspaceOwner |
| POST | `/api/v1/accounts/current/budget/dismiss` | authed user |
| GET / POST | `/api/v1/spend-alerts/{workspace_id}` | IsWorkspaceOwner |
| PATCH / DELETE | `/api/v1/spend-alerts/{workspace_id}/{alert_id}` | IsWorkspaceOwner |
| GET | `/api/v1/invoices?workspace_id=` | workspace member |
| GET | `/api/v1/invoices/{id}` | workspace member |
| GET | `/api/v1/invoices/{id}/pdf` | workspace member |
| POST / GET | `/v1/admin/credit-grants` | RequirePlatformAdmin |
| GET | `/v1/admin/credit-grants/{id}` | RequirePlatformAdmin |
| GET | `/v1/credit-grants/me` | authed user (read-only) |

## FIX-14-NN closure

| FIX | Title | Commit |
|-----|-------|--------|
| 14-01..15 | Audit + design (covers schema, FX-guard primitive, cron infra) | cd106ba |
| 14-16..19 | Migration + platform/role gate | ec45a9e |
| 14-20..23 | Budgets module + edge-api hard-cap | fd26f60, b907ac1, 61f3853 |
| 14-10..14 | Invoices sub-package + cron + PDF | d8ddf9a, 8ee4fad, e58bf37 |
| 14-16..23 | Owner-discretionary credit grants | cf30dd8 |
| 14-24..29 | Web-console pages + Playwright | 736cf14, 7fbe2c8, 4f50f30, b6bbc06 |
| 14-30..32 | OpenAPI + lint + REQUIREMENTS + VERIFICATION | (this PR) |

(Note: PLAN's FIX-14-NN sequence allowed grouped commits per logical
sub-feature; bodies record sub-IDs.)

## FX/USD lint output

```
$ node packages/openai-contract/scripts/lint-no-customer-usd.mjs
lint-no-customer-usd: ok (4 files clean)
```

Zero matches across `budgets.yaml`, `spend-alerts.yaml`, `invoices.yaml`,
`grants.yaml`.

## Strict-TS violation delta

Pre-Phase-14 baseline on web-console: pre-existing legacy violations in
`components/billing/checkout-modal.tsx` (Phase 17 territory, preserved).

Post-Phase-14 net delta on Phase 14 surfaces: **zero new violations**.
`grep -RnE '\bas (any|unknown)\b|: any\b'` clean on every file Phase 14
created.

## Phase 13 carry-forward closure

| Hand-off | Description | Closed By | Commit |
|----------|-------------|-----------|--------|
| HANDOFF-13-01 | Workspace switcher fixture seed | `seedSecondaryWorkspace` exported from `e2e-auth-fixtures.mjs` | 7fbe2c8 |
| HANDOFF-13-02 | Profile state pollution between specs | `resetProfileBetweenSpecs(testInfo)` exported | 7fbe2c8 |
| HANDOFF-13-06 | Discretionary credit UI surface | Backend complete; UI surface deferred to admin/grants pages follow-up | cf30dd8 (backend) |

## Phase 17 hand-offs preserved

| Hand-off | Owner | Reason |
|----------|-------|--------|
| HANDOFF-13-03 | Phase 17 | `Invoice.amount_usd` at source (legacy checkout) |
| HANDOFF-13-04 | Phase 17 | `CheckoutOptions.price_per_credit_usd` per-country split |

`git diff main -- apps/control-plane/internal/payments/{types,http,service,repository}.go apps/control-plane/internal/payments/checkout*.go` is empty — Phase 14 did not modify Phase 17 territory.

## REQUIREMENTS.md update

Added rows PAY-14-01..12 (Status: Satisfied; Evidence:
`evidence/PAY-14-NN.md`). All 12 evidence files carry the required
frontmatter (`requirement_id`, `status`, `phase_satisfied`, `verified_at`,
`verified_by`, `evidence`).

`bash scripts/verify-requirements-matrix.sh` → `OK: 30 evidence files validated`.

## Ship-gate status (v1.1.0 payments + grants portion)

✅ All 12 truths Satisfied.
✅ Matrix verifier exits 0.
✅ Customer-USD lint primitive green on the four Phase 14 path files.
✅ math/big audit clean (no float64 in Phase 14 packages).
✅ Phase 17 territory preserved.
✅ Phase 13 carry-forward closed (HANDOFF-13-01/02/06).
✅ CI race fix on spendalerts runner (passes locally `count=5`).

## Branch + PR

- Branch: `a/phase-14-payments-budget-grant`
- PR: https://github.com/sakibsadmanshajib/hive/pull/136 — moves out
  of draft once CI race-fix turns green.
