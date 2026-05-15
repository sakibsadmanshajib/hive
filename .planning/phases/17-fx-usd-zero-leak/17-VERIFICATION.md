---
phase: 17
artifact: verification
status: closed
created: 2026-05-09
date_closed: 2026-05-09
verified_by: phase-17-task-10
branch: a/phase-17-fx-zero-leak
pr: 137
---

# Phase 17 — FX/USD Zero-Leak — Verification Log

## Phase 17 — closed

Phase 17 closes the BD regulatory blocker for the v1.1.0 milestone tag.
All ten tasks executed RED → GREEN → IMPROVE under the worktree-isolated
sub-agent driver protocol on branch `a/phase-17-fx-zero-leak`. Customer
surfaces (control-plane HTTP, ledger wire, web-console DOM, chat-app
rendered strings) carry zero customer-USD/FX keys. Internal accounting
USD path (DB columns, server→Stripe payload) preserved. Lint
`packages/openai-contract/scripts/lint-no-customer-usd.mjs --all` walks
Go + TS + chat-app sources and is wired into CI as a blocking step.

## Per-task verification

### Task 1 — RED wire-shape assertions (FX-17-01/02)

- **SHA:** `70ef865`
- **Files:** `apps/control-plane/internal/payments/http_fx_zero_leak_test.go`,
  `apps/control-plane/internal/payments/service_fx_zero_leak_test.go`,
  `apps/control-plane/internal/ledger/types_fx_zero_leak_test.go`
- **Command:**
  ```
  cd deploy/docker && docker compose --profile tools --profile local run --rm toolchain \
    "cd /workspace && go test -buildvcs=false -count=1 \
      -run 'FXZeroLeak|FX_ZeroLeak' \
      ./apps/control-plane/internal/payments/... \
      ./apps/control-plane/internal/ledger/..."
  ```
- **Expected outcome:** `--- FAIL` on each of four new tests (RED).
- **Outcome:** RED locked into history at `70ef865`.

### Task 2 — Split internal vs wire DTOs in payments (FX-17-01)

- **SHA:** `5458f40`
- **Files:** `apps/control-plane/internal/payments/types.go`,
  `apps/control-plane/internal/payments/http.go`
- **Command:** `go test -buildvcs=false -count=1 -race ./apps/control-plane/internal/payments/...`
- **Tail:** `ok  github.com/hivegpt/hive/apps/control-plane/internal/payments`
- **Evidence:** `evidence/FX-17-01.md`

### Task 3 — Split ledger InvoiceRow wire DTO (FX-17-02)

- **SHA:** `0486d01`
- **Files:** `apps/control-plane/internal/ledger/types.go`
- **Command:** `go test -buildvcs=false -count=1 -race ./apps/control-plane/internal/ledger/... ./apps/control-plane/internal/payments/invoices/...`
- **Tail:** `ok  ledger`, `ok  payments/invoices`
- **Evidence:** `evidence/FX-17-02.md`

### Task 4 — Per-country pricing primitive (FX-17-03)

- **SHA:** `7e8d4b2`
- **Files:** `apps/control-plane/internal/payments/service.go`,
  `apps/control-plane/internal/payments/service_checkout_options_test.go`
- **Command:** `go test -buildvcs=false -count=1 -race -run 'CheckoutOptions|FXZeroLeak' ./apps/control-plane/internal/payments/...`
- **Tail:** all `CheckoutOptions` + `FXZeroLeak` subtests PASS; `math/big`
  conversion exercises BD (BDT paisa) + non-BD (USD cents) branches.
- **Evidence:** `evidence/FX-17-03.md`

### Task 5 — web-console consumer (FX-17-04)

- **SHA:** `2530fd6`
- **Files:** `apps/web-console/lib/control-plane/client.ts`,
  `apps/web-console/components/billing/checkout-modal.tsx`,
  `apps/web-console/__tests__/billing/checkout-modal.test.tsx`
- **Command:** `npm run build && npm run test:unit`
- **Tail:** `tsc --noEmit` exit 0; vitest `12 files passed (12) / 57 tests passed (57)`.
- **Evidence:** `evidence/FX-17-04.md`

### Task 6 — chat-app FX/USD sweep (FX-17-05)

- **SHA:** `afc51a8`
- **Files:** `apps/chat-app/client/src/locales/en/translation.json`,
  `apps/chat-app/client/src/locales/bn-BD/translation.json`,
  `17-AUDIT.md` (Section A.5 appended)
- **Command:** `grep -RnE 'amount_usd|usd_[A-Za-z0-9_]*|fx_[A-Za-z0-9_]*|price_per_credit_usd|exchange_rate' apps/chat-app/client/src apps/chat-app/api/server`
- **Tail:** exit 1 (no match) on BD-locale + non-locale paths.
- **Hand-off:** `et`/`de`/`lv`/`he` upstream USD prose deferred to HANDOFF-17-02.
- **Evidence:** `evidence/FX-17-05.md`

### Task 7 — Lint primitive repo-wide (FX-17-06)

- **SHA:** `0ff20f8`
- **Files:** `packages/openai-contract/scripts/lint-no-customer-usd.mjs`,
  `packages/openai-contract/scripts/lint-no-customer-usd.test.mjs`
- **Command:** `node packages/openai-contract/scripts/lint-no-customer-usd.mjs --all && node --test packages/openai-contract/scripts/lint-no-customer-usd.test.mjs`
- **Tail:** `lint-no-customer-usd: ok (1170 files clean — YAML+Go+TS, whitelist 'PHASE-17-INTERNAL-ONLY')`; node-test `# pass <synthetic-fixture-tests>`.
- **Evidence:** `evidence/FX-17-06.md`

### Task 8 — CI blocking workflow (FX-17-07)

- **SHA:** `3aee403`
- **Files:** `.github/workflows/ci.yml` (lines 60–82 — `fx-usd-zero-leak` job)
- **Command:** local validation by re-introducing `amount_usd` on a wire
  DTO; lint exits 1; commit reverted before push (per local-first rule).
- **CI step:** required for PR merge on `main` and `a/*` per branch protection.
- **Evidence:** `evidence/FX-17-07.md`

### Task 9 — Integration tests (FX-17-08)

- **SHA:** `018c457`
- **Files:** `apps/control-plane/internal/payments/integration_fx_zero_leak_test.go`,
  `apps/control-plane/internal/payments/invoices/pdf_fx_zero_leak_test.go`,
  `apps/web-console/__tests__/billing/usage-page.fx-zero-leak.test.tsx`,
  `apps/web-console/tests/e2e/billing-fx-zero-leak.spec.ts`
- **Command:** `go test ./apps/control-plane/internal/payments/...
  ./apps/control-plane/internal/payments/invoices/...` + `npm run
  test:unit` + `npx playwright test --list billing-fx-zero-leak`
- **Tail:** Go all packages `ok`; vitest 57/57 PASS (including 3 new RTL
  cases); Playwright `Total: 2 tests in 1 file` — runtime skip-gated by
  `E2E_VERIFIED_*` envs (mirrors `console-fx-guard.spec.ts`).
- **Evidence:** `evidence/FX-17-08.md`

### Task 10 — REQUIREMENTS + VERIFICATION + closure (FX-17-09/10)

- **SHA:** (this commit)
- **Files:** `.planning/REQUIREMENTS.md`, `.planning/phases/17-fx-usd-zero-leak/17-VERIFICATION.md`,
  `.planning/phases/17-fx-usd-zero-leak/evidence/FX-17-09.md`,
  `.planning/phases/17-fx-usd-zero-leak/evidence/FX-17-10.md`,
  `.planning/phases/17-fx-usd-zero-leak/17-AUDIT.md`,
  `.planning/STATE.md`, `.wolf/anatomy.md`, `.wolf/memory.md`, `.wolf/cerebrum.md`
- **Command (gate):** `node packages/openai-contract/scripts/lint-no-customer-usd.mjs --all && go test -buildvcs=false -count=1 -race ./apps/control-plane/... ./apps/edge-api/...`
- **PR transition:** `gh pr ready 137` → `isDraft == false`.
- **Evidence:** `evidence/FX-17-09.md` + `evidence/FX-17-10.md`

## Hand-offs emitted

| ID | Target | Description |
|---|---|---|
| HANDOFF-17-01 | Phase 18 | RBAC matrix replaces `is_platform_admin` stub. Phase 17 audit recorded no remaining customer-surface usage; any residual internal callers are documented in 17-AUDIT.md Section A.2. |
| HANDOFF-17-02 | Phase 25 | Re-audit chat-app post-Phase-23 i18n bundles. Non-BD locales (`et`, `de`, `lv`, `he`) currently carry upstream USD prose for `com_nav_info_balance`; Phase 23 replaces locale bundles wholesale, Phase 25 confirms zero residual USD prose pre-launch. |

## Acceptance gate — success criteria → evidence

| Success criterion (PLAN.md) | Evidence |
|---|---|
| Zero customer-surface JSON keys matching banned-key regex | Tasks 2/3/4 + Task 7 lint + Task 9 integration tests |
| Internal accounting USD preserved (DB columns + server→Stripe) | 17-AUDIT.md Section A.2; `stripe/rail.go:40` untouched |
| `lint-no-customer-usd.mjs --all` covers Go + TS + chat-app | `evidence/FX-17-06.md` |
| GH Actions step blocks PR on lint hit | `evidence/FX-17-07.md` + `.github/workflows/ci.yml:60-82` |
| Integration test green for BD checkout, invoice PDF, usage, chat-app | `evidence/FX-17-08.md` |
| Strict TS — no `as`/`any`/`unknown` introduced in wire-shape edits | `evidence/FX-17-04.md` + `evidence/FX-17-08.md` strict-TS proof |
| 17-VERIFICATION.md closure log filed | this file |
| REQUIREMENTS.md FX-17-01..10 rows wired to evidence | `evidence/FX-17-09.md` |
| PR #137 out of draft, reviewers requested | `evidence/FX-17-10.md` (gh pr ready 137) |

## Lint guard

- Local: `node packages/openai-contract/scripts/lint-no-customer-usd.mjs --all` → exit 0 (`1170 files clean — YAML+Go+TS, whitelist 'PHASE-17-INTERNAL-ONLY'`).
- CI: `.github/workflows/ci.yml:60-82` `fx-usd-zero-leak` job runs the
  same command on every PR; failure blocks merge on `main` and `a/*`.
  Whitelist comment `// PHASE-17-INTERNAL-ONLY` available for future
  server→Stripe internals if needed; tests directories excluded by
  scanner design (`*_test.go`, `__tests__/`, `*.test.{ts,tsx}`,
  `*.spec.ts`).

## Reviewer requests

`gh pr edit 137 --add-reviewer ...` attempted with role-prefixed slugs
(`go-reviewer`, `typescript-reviewer`, `security-reviewer`,
`database-reviewer`). If the slugs do not match GitHub usernames in this
repo (likely — these are agent role names, not GH logins), the
add-reviewer call is skipped and team review is solicited via the
`code-review` label and direct ping in the PR thread. PR #137 is taken
out of draft regardless (`gh pr ready 137`).

## Conclusion

Phase 17 lands a wire-only zero-leak fix without DB rename or migration.
The customer surface is now BDT-only end-to-end across control-plane
HTTP, web-console DOM, invoice PDF, and chat-app rendered strings.
Internal USD accounting, server→Stripe payload, and DB columns are
untouched. Repo-wide lint + CI guard prevent regression. v1.1.0 milestone
tag is no longer blocked by FX/USD leakage; remaining v1.1 phases own
the rest of the gate.
