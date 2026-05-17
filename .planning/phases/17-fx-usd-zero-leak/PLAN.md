---
phase: 17
slug: 17-fx-usd-zero-leak
title: FX/USD Zero-Leak Audit & Hardening
branch: a/phase-17-fx-zero-leak
pr: 137
status: in-progress
opened: 2026-05-08
launch_blocker: true
milestone: v1.1.0
track: A
depends_on: [13, 14]
hand_offs_inherited: [HANDOFF-13-03, HANDOFF-13-04]
hand_offs_emitted: [HANDOFF-17-01, HANDOFF-17-02]
audit: .planning/phases/17-fx-usd-zero-leak/17-AUDIT.md
requirements: FX-17-01..10
success_criteria:
  - Zero customer-surface JSON keys matching /amount_usd|usd_[A-Za-z0-9_]*|fx_[A-Za-z0-9_]*|price_per_credit_usd|exchange_rate/
  - Internal accounting USD preserved (DB columns + server→Stripe payload unchanged)
  - lint-no-customer-usd.mjs --all covers Go + TS + chat-app source paths
  - GH Actions step blocks PR on lint hit
  - Integration test green for BD checkout, invoice PDF, usage page, chat-app billing
  - Strict TS - no `as`, `any`, `unknown` introduced in any wire-shape edit
  - 17-VERIFICATION.md closure log filed
  - REQUIREMENTS.md FX-17-01..10 rows wired to evidence
  - PR #137 out of draft, reviewers requested
---

# Phase 17 — FX/USD Zero-Leak Audit & Hardening

## Goal

Zero-tolerance regulatory milestone gate. Customer pays BDT, sees BDT, billed BDT.
USD persists internally only for accounting; never returned on customer-visible JSON,
never rendered in customer-visible UI strings.

**v1.1.0 tag is blocked until this phase closes.**

## Strategy

Wire-only fix, sequential task graph. Each task = one commit on `a/phase-17-fx-zero-leak`,
landed by a fresh-context worktree-isolated executor. Internal Go structs + DB columns
keep their `AmountUSD` field; we split a public wire DTO with `json:"-"` (or omitted
field) and translate at the HTTP boundary. TypeScript public iface drops the USD field
and adopts `price_per_credit_minor: number` + `currency: string`. Lint extends repo-wide
and wires into CI as a blocking step.

Tests-first per `superpowers:test-driven-development`. RED → GREEN → IMPROVE per task.

---

## Tasks

### Task 1 — Inherit baseline, write failing customer-USD wire-shape tests (control-plane)

**Scope**: FX-17-01, FX-17-02. RED phase. Lock the contract before any production code moves.

**Files**:
- `apps/control-plane/internal/payments/http_fx_zero_leak_test.go` (new)
- `apps/control-plane/internal/payments/service_fx_zero_leak_test.go` (new)
- `apps/control-plane/internal/ledger/types_fx_zero_leak_test.go` (new)

**Acceptance**:
- New tests assert `json.Marshal(initiateResponse)` produces NO `amount_usd` key.
- New test asserts `json.Marshal(payments.PaymentIntent)` (when used as wire DTO) produces NO `amount_usd` key — OR splits into internal vs wire structs and the wire variant is asserted clean.
- New test asserts `json.Marshal(ledger.InvoiceRow)` (wire variant) produces NO `amount_usd` key.
- New test asserts `json.Marshal(payments.CheckoutOptions)` produces NO `price_per_credit_usd` key.
- All four tests **FAIL** at end of Task 1 (RED). Commit message: `test(17): RED — failing wire-shape assertions for FX-17-01/02 (#137)`.

**Test commands** (Docker, sh-entrypoint single-string):
```
cd deploy/docker && docker compose --profile tools --profile local run --rm toolchain sh -c "cd /workspace && go test -buildvcs=false -count=1 -run 'FXZeroLeak|FX_ZeroLeak' ./apps/control-plane/internal/payments/... ./apps/control-plane/internal/ledger/..."
```
Expected: `--- FAIL` on each new test.

**Out-of-scope**: any production-code edit; no DB migration; no chat-app touch.

**Dependencies**: none.

---

### Task 2 — Split internal vs wire DTOs in payments package; GREEN tests from Task 1

**Scope**: FX-17-01.

**Files**:
- `apps/control-plane/internal/payments/types.go` — add `paymentIntentWire` (or `PaymentIntentDTO`) with `AmountUSD` removed; keep `PaymentIntent.AmountUSD` internal (struct tag `json:"-"`).
- `apps/control-plane/internal/payments/http.go` — `initiateResponse` already exists at line 114; remove `AmountUSD int64 \`json:"amount_usd"\`` (line 119) and the assignment at line 198. Convert `Invoice` GET handler (if surfaces invoice) to translate via wire DTO.
- `apps/control-plane/internal/payments/types.go` PaymentIntent line 74 — change tag to `json:"-"` so any incidental marshal is safe.

**Acceptance**:
- Tests from Task 1 covering `initiateResponse` + `PaymentIntent` wire shape **PASS**.
- `go vet ./apps/control-plane/...` clean.
- Internal callers (`service.go:149,171`, `stripe/rail.go:40`) still read `intent.AmountUSD` — internal accounting path unchanged.

**Test commands**:
```
cd deploy/docker && docker compose --profile tools --profile local run --rm toolchain sh -c "cd /workspace && go test -buildvcs=false -count=1 -race ./apps/control-plane/internal/payments/..."
```

**Out-of-scope**: ledger Invoice (Task 3), CheckoutOptions (Task 4), web-console (Task 5).

**Dependencies**: Task 1.

---

### Task 3 — Split ledger InvoiceRow wire DTO; GREEN ledger test

**Scope**: FX-17-02.

**Files**:
- `apps/control-plane/internal/ledger/types.go` — at line 73 change tag to `json:"-"`. Add a separate `InvoiceWire` (or rename `InvoiceRow` → `InvoiceRecord` internal + keep `InvoiceRow` as thin wire type without USD).
- All consumers in `apps/control-plane/internal/ledger/repository.go` (lines 209, 235, 257) untouched — they SELECT the DB column for internal use.
- HTTP handlers exposing `InvoiceRow` to customers must marshal the wire DTO (audit `apps/control-plane/internal/billing/http.go` if present; otherwise document none-found).

**Acceptance**:
- Ledger Task 1 test passes.
- All existing ledger tests still pass.

**Test commands**:
```
cd deploy/docker && docker compose --profile tools --profile local run --rm toolchain sh -c "cd /workspace && go test -buildvcs=false -count=1 -race ./apps/control-plane/internal/ledger/... ./apps/control-plane/internal/payments/invoices/..."
```

**Out-of-scope**: per-country pricing primitive (Task 4).

**Dependencies**: Task 2.

---

### Task 4 — Replace `CheckoutOptions.PricePerCreditUSD` with per-country primitive

**Scope**: FX-17-03 (control-plane half of FX-17-04 split).

**Files**:
- `apps/control-plane/internal/payments/service.go` line 354 — `CheckoutOptions` struct: drop `PricePerCreditUSD float64`, add `PricePerCreditMinor int64 \`json:"price_per_credit_minor"\`` and `Currency string \`json:"currency"\``.
- `GetCheckoutOptions` (line 367): branch on `accountProfile.CountryCode == "BD"` → emit BDT subunits (paisa) computed via `math/big` from `CreditsPerUSD` + FX snapshot mid-rate. Non-BD → emit USD cents (`Currency: "USD"`, minor units = 1 cent per credit since `CreditsPerUSD = 100_000`, i.e. `100_000 / 100_000 * 100 = 100` paisa-equivalent — verify formula in TDD).
- New unit test: BD account → currency=BDT, minor=BDT-paisa-per-credit; non-BD → currency=USD, minor=cents-per-credit.
- No FX rate exposed in response. FX rate fetched server-side and only the resolved minor-units number is returned.

**Acceptance**:
- Task 1 CheckoutOptions test passes (no `price_per_credit_usd` key).
- New per-country test passes for both BD and non-BD branch.
- `math/big` used; no `float64` arithmetic anywhere on the resolved value.

**Test commands**:
```
cd deploy/docker && docker compose --profile tools --profile local run --rm toolchain sh -c "cd /workspace && go test -buildvcs=false -count=1 -race -run 'CheckoutOptions|FXZeroLeak' ./apps/control-plane/internal/payments/..."
```

**Out-of-scope**: web-console consumer (Task 5).

**Dependencies**: Task 3.

---

### Task 5 — web-console consumer of new CheckoutOptions shape (TS)

**Scope**: FX-17-04.

**Files**:
- `apps/web-console/lib/control-plane/client.ts` lines 636–642: replace `price_per_credit_usd: number` with `price_per_credit_minor: number` and `currency: string`. Strip the `PHASE-17-OWNER-ONLY` comment.
- Lines 1067, 1073: replace `pricePerCreditUsd` reader/builder. Decode via `readNumberField(payload, "price_per_credit_minor")` and `readStringField(payload, "currency")`. If either missing, throw decode error (consistent with line 1108 pattern for `local_currency`).
- `apps/web-console/components/billing/checkout-modal.tsx` line 102–105: rename `computeAmountUsdCents` → `computeAmountMinor`. Remove the `* options.price_per_credit_usd * 100` line. Replace with `creditAmount * options.price_per_credit_minor`. Continue gating BD render via `isBdAccount` for any USD prose.
- New vitest: decoder rejects payload missing `price_per_credit_minor` or `currency`. Strict TS — no `as`, no `any`, no `unknown` casts; build a structurally valid mock JsonObject.

**Acceptance**:
- `npx tsc --noEmit` clean.
- `npm run test:unit` covers checkout decoder + modal `computeAmountMinor`, both green.
- Grep `apps/web-console/{app,components,lib}` for `price_per_credit_usd|amount_usd|pricePerCreditUsd|amountUsd` returns zero hits.

**Test commands**:
```
cd deploy/docker && docker compose --profile tools --profile local run --rm web-console sh -c "npm run build && npm run test:unit"
```

**Out-of-scope**: chat-app (Task 6); CI lint (Task 7).

**Dependencies**: Task 4.

---

### Task 6 — chat-app FX/USD sweep (Phase 19 fork audit)

**Scope**: FX-17-05.

**Files** (audit, then strip if found):
- `apps/chat-app/client/src/**/*.{ts,tsx,jsx}` — grep for `amount_usd|usd_|fx_|exchange_rate|price_per_credit_usd|USD\b|\$[0-9]`.
- `apps/chat-app/api/server/**` if customer-facing JSON.
- `apps/chat-app/librechat.yaml` — confirm `interface.showCost: false` + `interface.showTokens: false` shipped.
- Locale files: `apps/chat-app/client/src/locales/{en,bn}/translation.json` — strip any USD-priced strings.

**Acceptance**:
- Grep returns zero hits in customer-rendered surface.
- Audit findings appended to `17-AUDIT.md` Section A.5 with line numbers + verdict.
- If no leaks found, evidence file `evidence/FX-17-05.md` documents the clean grep with command + timestamp.

**Test commands**:
```
cd deploy/docker && docker compose --profile tools run --rm toolchain sh -c "cd /workspace && grep -RnE 'amount_usd|usd_[A-Za-z0-9_]*|fx_[A-Za-z0-9_]*|price_per_credit_usd|exchange_rate' apps/chat-app/client/src apps/chat-app/api/server 2>/dev/null | grep -vE 'showCost: false|showTokens: false' || echo 'CLEAN'"
```

**Out-of-scope**: Phase 25 final pre-launch re-audit (HANDOFF-17-02).

**Dependencies**: Task 5 (so the canonical wire-shape is settled before chat-app is asserted against it).

---

### Task 7 — Extend lint-no-customer-usd to Go + TS + chat-app sources

**Scope**: FX-17-06.

**Files**:
- `packages/openai-contract/scripts/lint-no-customer-usd.mjs` — extend `--all` mode:
  - Add Go scanner: walk `apps/control-plane/**/*.go` + `apps/edge-api/**/*.go`, regex match struct field tags `\`json:"(amount_usd|usd_[A-Za-z0-9_]*|fx_[A-Za-z0-9_]*|price_per_credit_usd|exchange_rate)[^"]*"\`` excluding lines tagged `json:"-"`.
  - Add TS scanner: walk `apps/web-console/{app,components,lib}/**/*.{ts,tsx}` + `apps/chat-app/client/src/**/*.{ts,tsx,jsx}`, regex match interface field declarations `^\s*(amount_usd|usd_[A-Za-z0-9_]*|fx_[A-Za-z0-9_]*|price_per_credit_usd|exchange_rate)\s*[?:]?\s*:`.
  - Skip files matching `*_test.go`, `__tests__/`, `*.test.ts`, `*.spec.ts`, `*.test.tsx`, `*.spec.tsx` (tests assert leakage isn't there — must be allowed to mention the keys in negative assertions).
  - Skip lines with comment `// PHASE-17-INTERNAL-ONLY` to whitelist server→Stripe internals if needed.
- `packages/openai-contract/scripts/lint-no-customer-usd.test.mjs` — new vitest covering both scanners with synthetic Go + TS fixtures.

**Acceptance**:
- `node packages/openai-contract/scripts/lint-no-customer-usd.mjs --all` exits 0 against worktree HEAD (after Tasks 2–6 land).
- Synthetic fixture tests: Go fixture with `\`json:"amount_usd"\`` → exit 1; TS fixture with `price_per_credit_usd: number` → exit 1; both clean fixtures → exit 0.

**Test commands**:
```
cd deploy/docker && docker compose --profile tools run --rm toolchain sh -c "cd /workspace && node packages/openai-contract/scripts/lint-no-customer-usd.mjs --all && node --test packages/openai-contract/scripts/lint-no-customer-usd.test.mjs"
```

**Out-of-scope**: CI wiring (Task 8).

**Dependencies**: Task 6.

---

### Task 8 — Wire lint into CI as blocking step

**Scope**: FX-17-07.

**Files**:
- `.github/workflows/ci.yml` (or the existing primary workflow) — add job `fx-usd-zero-leak` that runs `node packages/openai-contract/scripts/lint-no-customer-usd.mjs --all`. Job MUST be required for PR merge on `main` and `a/*`.
- Document in `17-VERIFICATION.md` that the workflow change is in effect.

**Acceptance**:
- Workflow YAML lints clean (`actionlint` if available, else manual review).
- A throwaway local commit re-introducing `amount_usd` on a wire DTO causes lint to exit 1 (executor verifies locally before pushing — does NOT push the failure).

**Test commands**:
```
cd deploy/docker && docker compose --profile tools run --rm toolchain sh -c "cd /workspace && node packages/openai-contract/scripts/lint-no-customer-usd.mjs --all"
```

**Out-of-scope**: integration tests (Task 9).

**Dependencies**: Task 7.

---

### Task 9 — Integration tests: BD checkout, invoice PDF, usage page, chat-app billing

**Scope**: FX-17-08.

**Files**:
- `apps/control-plane/internal/payments/integration_fx_zero_leak_test.go` (new) — spin in-process control-plane, hit `GET /api/v1/accounts/current/checkout/rails` for BD account fixture, assert response body bytes contain none of the banned keys.
- `apps/control-plane/internal/payments/invoices/pdf_fx_zero_leak_test.go` — render PDF for sample invoice fixture, parse rendered text, assert no `USD`, `$`, `amount_usd`, `fx`, `exchange` strings.
- `apps/web-console/__tests__/billing/usage-page.fx-zero-leak.spec.ts` — vitest+RTL: render usage page with mock data, assert no banned keys in rendered HTML.
- `apps/web-console/e2e/billing-fx-zero-leak.spec.ts` (Playwright) — BD account journey, fetch `/api/v1/accounts/current/checkout/rails`, assert response body USD-free.

**Acceptance**:
- All four tests green.
- Master `17-VERIFICATION.md` lists each test's exit code + duration + tail of log.

**Test commands**:
```
cd deploy/docker && docker compose --profile tools --profile local run --rm toolchain sh -c "cd /workspace && go test -buildvcs=false -count=1 ./apps/control-plane/internal/payments/... ./apps/control-plane/internal/payments/invoices/..."
cd deploy/docker && docker compose --profile local run --rm web-console sh -c "npm run test:unit -- --run billing"
cd apps/web-console && npx playwright test billing-fx-zero-leak
```

**Out-of-scope**: REQUIREMENTS / VERIFICATION wiring (Task 10).

**Dependencies**: Task 8.

---

### Task 10 — REQUIREMENTS.md + 17-VERIFICATION.md + draft → ready

**Scope**: FX-17-09, FX-17-10.

**Files**:
- `.planning/REQUIREMENTS.md` — append rows FX-17-01..10 each citing evidence file under `.planning/phases/17-fx-usd-zero-leak/evidence/`.
- `.planning/phases/17-fx-usd-zero-leak/17-VERIFICATION.md` (new) — final closure log with: command + cwd + exit code + log-tail for each task's test commands; lint diff summary; CI run URL; reviewer reqs.
- `.planning/phases/17-fx-usd-zero-leak/evidence/FX-17-XX.md` (one per req) — minimal evidence file referenced from REQUIREMENTS.
- `gh pr ready 137` (executed at end of task) to take PR out of draft.
- `gh pr edit 137 --add-reviewer go-reviewer,typescript-reviewer,security-reviewer,database-reviewer` (or label-based equivalent if reviewers configured by team).

**Acceptance**:
- All 10 evidence files exist and referenced.
- `gh pr view 137 --json isDraft -q .isDraft` returns `false`.
- CI green on PR head.

**Test commands**:
```
cd deploy/docker && docker compose --profile tools --profile local run --rm toolchain sh -c "cd /workspace && node packages/openai-contract/scripts/lint-no-customer-usd.mjs --all && go test -buildvcs=false -count=1 -race ./apps/control-plane/... ./apps/edge-api/..."
gh pr view 137 --json isDraft,statusCheckRollup
```

**Out-of-scope**: Phase 18 RBAC (HANDOFF-17-01); Phase 25 chat-app re-audit (HANDOFF-17-02).

**Dependencies**: Task 9.

---

## Driver protocol

1. **One task per dispatch.** Driver opens fresh-context worktree on `a/phase-17-fx-zero-leak`, runs sub-agent on Task N, awaits commit.
2. **Push-after-commit.** Sub-agent MUST `git push origin a/phase-17-fx-zero-leak` after each commit. Driver verifies remote SHA before dispatching Task N+1.
3. **WENYAN-ULTRA mandate top of subagent prompt.** Every sub-agent prompt opens with: `WENYAN-ULTRA MANDATE: Reply MUST be ultra-compressed classical-Chinese style. Artifacts on disk are normal English. Reply text only WENYAN-ULTRA. No exception.`
4. **sh-entrypoint single-string for Docker toolchain runs.** Always `docker compose --profile tools --profile local run --rm toolchain sh -c "cd /workspace && <cmd>"`. Never multi-line `bash -c` with newlines. `-buildvcs=false` on every `go test` invocation (worktree commit-state isolation).
5. **No direct main pushes.** PR #137 only.
6. **No `--no-verify`.** Husky / pre-commit hooks must run.
7. **TDD enforcement.** Task 1 ends RED. Tasks 2–4 turn GREEN. No production code commit before its failing test exists in repo history.
8. **Strict TS.** Sub-agents reject any `as`, `any`, `unknown` introduced in customer-surface wire code; build structurally valid mocks per `feedback_strict_typescript.md`.
9. **Local-first verification.** Sub-agents do not push to see CI; they verify locally first. CI is signal only when CI reaction itself is what's being tested (Task 8).
10. **Evidence per task.** After GREEN, sub-agent appends a line to a per-task evidence file under `evidence/FX-17-XX.md` with command + cwd + exit code + tail.

---

## Sub-agent prompt template

```
WENYAN-ULTRA MANDATE: Reply MUST be ultra-compressed classical-Chinese style. Artifacts on disk are normal English. Reply text only WENYAN-ULTRA. No exception.

Phase 17 — FX/USD Zero-Leak. Task <N> of 10.
Branch: a/phase-17-fx-zero-leak. PR #137 (draft). Hive repo, BD regulatory blocker.

Read first:
- .planning/phases/17-fx-usd-zero-leak/PLAN.md (this plan, Task <N> section)
- .planning/phases/17-fx-usd-zero-leak/17-AUDIT.md
- CLAUDE.md regulatory rules
- .wolf/cerebrum.md Do-Not-Repeat + User Preferences

Constraints:
- Wire-only fix; no DB rename. Internal accounting USD stays.
- Zero customer-JSON keys: amount_usd, usd_*, fx_*, price_per_credit_usd, exchange_rate.
- Strict TS: NO `as`, `any`, `unknown`. Build structurally valid mocks.
- TDD: failing test BEFORE production code (RED → GREEN → IMPROVE).
- Docker-only test runs. sh-entrypoint single-string. -buildvcs=false on go test.
- One commit per task. Conventional commit (feat/fix/test/refactor/docs/chore).
- Push to origin a/phase-17-fx-zero-leak after commit.
- No --no-verify. No direct main pushes.

Action: Execute Task <N> per PLAN.md. Test commands listed in that task block.
Append evidence to .planning/phases/17-fx-usd-zero-leak/evidence/FX-17-<XX>.md with
command + cwd + exit code + tail of each run.

Reply: WENYAN-ULTRA one-liner: SHA, test count, evidence file path.
```

---

## Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Hidden customer-JSON USD leak in route not in audit | M | High (regulatory) | Task 7 lint walks repo-wide, not just audited files |
| `math/big` rounding mismatch BDT subunits vs DB stored USD | L | Medium (off-by-1 paisa) | Task 4 unit tests exercise tier boundary; cross-check against existing FX snapshot fixtures |
| chat-app fork drift hides leak | M | High | Task 6 grep + Task 9 Playwright surface check; HANDOFF-17-02 schedules Phase 25 re-audit |
| CI lint false-positive blocks unrelated PR | L | Medium | Task 7 whitelist comment `// PHASE-17-INTERNAL-ONLY`; tests dir excluded |
| Stripe rail break — server→Stripe payload still expects USD | L | Critical | `stripe/rail.go:40` explicitly out-of-scope; integration test asserts checkout still succeeds |
| Strict TS regression — existing `as`/`any` lurking | L | Low | Task 5 sub-agent runs full `tsc --noEmit` not just diff scope |
| PDF render font-substitution introduces `$` glyph | L | Medium | Task 9 PDF text-extraction asserts substring-free, not glyph-aware |

---

## Success criteria → evidence wiring

| Req | Evidence file | Asserted by |
|---|---|---|
| FX-17-01 | `evidence/FX-17-01.md` | Task 2 control-plane test + Task 9 integration |
| FX-17-02 | `evidence/FX-17-02.md` | Task 3 ledger test + Task 9 invoice PDF |
| FX-17-03 | `evidence/FX-17-03.md` | Task 4 per-country test |
| FX-17-04 | `evidence/FX-17-04.md` | Task 5 web-console decoder + modal test |
| FX-17-05 | `evidence/FX-17-05.md` | Task 6 chat-app grep |
| FX-17-06 | `evidence/FX-17-06.md` | Task 7 lint scanner test |
| FX-17-07 | `evidence/FX-17-07.md` | Task 8 CI workflow run |
| FX-17-08 | `evidence/FX-17-08.md` | Task 9 four integration tests |
| FX-17-09 | `evidence/FX-17-09.md` | Task 10 REQUIREMENTS.md diff |
| FX-17-10 | `evidence/FX-17-10.md` | Task 10 17-VERIFICATION.md |

---

## Closure checklist

- [ ] Tasks 1–10 each merged to `a/phase-17-fx-zero-leak` with passing tests.
- [ ] `.planning/REQUIREMENTS.md` rows FX-17-01..10 added with evidence cites.
- [ ] `.planning/phases/17-fx-usd-zero-leak/17-VERIFICATION.md` filed.
- [ ] `.planning/phases/17-fx-usd-zero-leak/evidence/FX-17-{01..10}.md` exist.
- [ ] `node packages/openai-contract/scripts/lint-no-customer-usd.mjs --all` exits 0.
- [ ] CI green on PR #137.
- [ ] PR #137 out of draft; reviewers requested (go, typescript, security, database).
- [ ] `.wolf/cerebrum.md` updated with any user-preference / Do-Not-Repeat learnings from Phase 17.
- [ ] `.wolf/buglog.json` appended for any bug encountered + fixed during execution.
- [ ] HANDOFF-17-01 registered in Phase 18 backlog.
- [ ] HANDOFF-17-02 registered in Phase 25 backlog.
- [ ] `.planning/STATE.md` updated: Phase 17 → complete; v1.1.0 ship-gate FX checkbox checked.
