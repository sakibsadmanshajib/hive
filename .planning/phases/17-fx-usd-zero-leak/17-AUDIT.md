---
phase: 17
title: FX/USD Zero-Leak Audit & Hardening
goal: Zero USD/FX exposure on any customer-visible surface. BDT-only customer surface; USD = internal accounting only.
status: in-progress
opened: 2026-05-08
depends_on: [13, 14]
launch_blocker: true
hand_offs_inherited: [HANDOFF-13-03, HANDOFF-13-04]
---

# Phase 17 — FX/USD Zero-Leak Audit & Hardening

## Goal

**Zero-tolerance regulatory milestone gate.** Customer pays BDT, sees BDT, billed BDT. USD persists internally only for accounting; never returned on customer-visible JSON, never rendered in customer-visible UI strings.

v1.1.0 tag is **blocked** until this phase closes.

## Inherited Hand-offs

| ID | From Phase | Symptom |
|---|---|---|
| HANDOFF-13-03 | 13 | Control-plane `Invoice` response carries `amount_usd` — strip at source |
| HANDOFF-13-04 | 13 | Control-plane `CheckoutOptions` response carries `price_per_credit_usd` — split into per-country pricing primitive |

## Section A — Customer-Surface USD Inventory (initial sweep, 2026-05-08)

### A.1 — control-plane Go (customer-visible response shapes)

| File | Line | Symbol | Verdict | Action |
|---|---|---|---|---|
| `apps/control-plane/internal/payments/http.go` | 119, 198 | `Invoice` HTTP response struct `AmountUSD int64 \`json:"amount_usd"\`` | LEAK — wire field on customer GET `/v1/payments/invoices` | Strip field from response DTO; keep DB column under internal struct |
| `apps/control-plane/internal/payments/types.go` | 74 | `PaymentIntent.AmountUSD int64 \`json:"amount_usd"\`` | LEAK — surfaced via checkout response | Split internal vs wire DTO; omit on wire |
| `apps/control-plane/internal/payments/types.go` | 129 | `Invoice.AmountUSD int64 \`json:"amount_usd"\`` | LEAK — surfaced via list/get | Same — omit on wire |
| `apps/control-plane/internal/ledger/types.go` | 73 | `Invoice.AmountUSD int64 \`json:"amount_usd"\`` | LEAK — surfaced via ledger query endpoints | Same — omit on wire |
| `apps/control-plane/internal/payments/service.go` | 354 | `CheckoutOptions.PricePerCreditUSD float64 \`json:"price_per_credit_usd"\`` | LEAK — wire field on customer GET `/v1/payments/checkout/options` | Replace with per-country `price_per_credit` (BDT subunits for BD; locale-derived elsewhere) |

### A.2 — control-plane Go (internal-only — keep)

| File | Line | Verdict |
|---|---|---|
| `apps/control-plane/internal/payments/repository.go` | 40, 48, 62, 72, 128, 205, 230 | DB column SELECT — internal accounting; keep |
| `apps/control-plane/internal/payments/service.go` | 149, 171 | Internal `amountUSD` math; keep behind wire DTO split |
| `apps/control-plane/internal/payments/stripe/rail.go` | 40 | Stripe USD-rail integration — server-to-Stripe, never returned to customer; keep |
| `apps/control-plane/internal/ledger/repository.go` | 209, 235, 257 | DB column SELECT — internal; keep |

### A.3 — web-console TypeScript (customer-visible)

| File | Line | Symbol | Verdict | Action |
|---|---|---|---|---|
| `apps/web-console/lib/control-plane/client.ts` | 636-641 | `CheckoutOptions.price_per_credit_usd: number` (PHASE-17-OWNER-ONLY) | LEAK — wire-shape | Drop from public iface; replace with `price_per_credit_minor: number` + `currency: "BDT" \| ...` |
| `apps/web-console/lib/control-plane/client.ts` | 1067, 1073 | `pricePerCreditUsd` reader/builder | LEAK — replace with currency-aware reader |
| `apps/web-console/components/billing/checkout-modal.tsx` | 104 | `options.price_per_credit_usd * 100` | LEAK — replace multiply with new minor-units field |

### A.4 — Invoice PDF generator

| File | Line | Verdict |
|---|---|---|
| `apps/control-plane/internal/payments/invoices/pdf.go` | 192, 194 | Banned-key blocklist (already excludes `amount_usd`/`price_per_credit_usd` from PDF render) — keep; verify integration test |

### A.5 — chat-app (Phase 19 fork) — closed (FX-17-05, 2026-05-09)

Sweep complete. Evidence: `evidence/FX-17-05.md`. Worktree HEAD at audit: `2530fd6`.

#### A.5.1 — Primary banned-key grep (`apps/chat-app/client/src` + `apps/chat-app/api/server`)

```
grep -RnE 'amount_usd|usd_[A-Za-z0-9_]*|fx_[A-Za-z0-9_]*|price_per_credit_usd|exchange_rate' \
  apps/chat-app/client/src apps/chat-app/api/server 2>/dev/null \
  | grep -vE 'showCost: false|showTokens: false'
```

Exit 1 (no match). Verdict: CLEAN.

#### A.5.2 — Locale USD prose grep

| File | Line | Hit | Action |
|---|---|---|---|
| `apps/chat-app/client/src/locales/en/translation.json` | 396 | `com_nav_info_balance` example `$0.001 USD` | STRIPPED to `"Balance shows how many token credits you have left to use."` |
| `apps/chat-app/client/src/locales/bn-BD/translation.json` | 396 | same upstream example `$0.001 USD` | STRIPPED + Bengali rewrite `"ব্যালেন্স দেখায় আপনার কতগুলি টোকেন ক্রেডিট ব্যবহারের জন্য বাকি আছে।"` |
| `apps/chat-app/client/src/locales/et/translation.json` | 374 | upstream USD example | DEFERRED → HANDOFF-17-02 (non-BD locale; Phase 23 i18n replaces wholesale) |
| `apps/chat-app/client/src/locales/de/translation.json` | 395 | upstream USD example | DEFERRED → HANDOFF-17-02 |
| `apps/chat-app/client/src/locales/lv/translation.json` | 393 | upstream USD example | DEFERRED → HANDOFF-17-02 |
| `apps/chat-app/client/src/locales/he/translation.json` | 393 | upstream USD example | DEFERRED → HANDOFF-17-02 |

Post-strip BD-locale re-grep on `en/` + `bn-BD/`: exit 1 (CLEAN).

#### A.5.3 — `librechat.yaml` verdict

```
apps/chat-app/librechat.yaml:16:  showCost: false
apps/chat-app/librechat.yaml:17:  showTokens: false
```

Pinned with Phase 19 defensive comment block (lines 8-14). PASS. No edit.

#### A.5.4 — Component surface

`com_nav_info_balance` consumed only by `apps/chat-app/client/src/components/Nav/SettingsTabs/Balance/TokenCreditsItem.tsx:18` (rendered inside `Balance.tsx`, which is gated by `startupConfig?.balance?.enabled` and the LibreChat-wide `interface.showCost: false`). Strip is defence-in-depth. No top-up / billing / payment customer-rendered USD strings discovered in `apps/chat-app/client/src/components`.

#### A.5.5 — Hand-off

HANDOFF-17-02 inherits the four non-BD-locale upstream USD strings (`et`, `de`, `lv`, `he`). Phase 23 i18n bundle work replaces upstream locales wholesale; Phase 25 re-audits to confirm zero residual USD prose pre-launch.

## Section B — CI Lint Coverage Gap

`packages/openai-contract/scripts/lint-no-customer-usd.mjs` currently scans **OpenAPI YAML path docs** only (Phase 14 default = 4 files; `--all` walks `spec/paths/`).

Phase 17 must extend coverage to:
1. Go customer-surface response structs (struct tags `json:"amount_usd|usd_*|fx_*|price_per_credit_usd|exchange_rate"`).
2. TypeScript public DTO interfaces (`apps/web-console/lib/control-plane/client.ts`, `app/**/*.{ts,tsx}`, `components/**/*.{ts,tsx}`).
3. Chat-app rendered UI strings (`apps/chat-app/client/src/**/*.{ts,tsx,jsx}`).
4. Wire into CI: GH workflow step blocks PRs on lint hit.

## Section C — Fix-list (Phase 17 in-scope)

| ID | Files | Description |
|---|---|---|
| FX-17-01 | `apps/control-plane/internal/payments/http.go`, `types.go` | Split internal `paymentIntent`/`invoice` records from wire DTO; `json:"-"` on `AmountUSD` for customer surface |
| FX-17-02 | `apps/control-plane/internal/ledger/types.go` | Same split for ledger Invoice |
| FX-17-03 | `apps/control-plane/internal/payments/service.go` | Replace `CheckoutOptions.PricePerCreditUSD` with per-country pricing primitive (BD: BDT subunits; others: locale-derived) |
| FX-17-04 | `apps/web-console/lib/control-plane/client.ts`, `components/billing/checkout-modal.tsx` | Drop `price_per_credit_usd` field; consume `price_per_credit_minor` + `currency` |
| FX-17-05 | `apps/chat-app/**` | Grep sweep + strip any USD/FX UI strings; confirm `showCost: false`/`showTokens: false` end-to-end |
| FX-17-06 | `packages/openai-contract/scripts/lint-no-customer-usd.mjs` | Extend `--all` to cover Go + TS + chat-app source paths (regex on struct tags + interface fields) |
| FX-17-07 | `.github/workflows/*.yml` | Wire lint into CI; block on hit |
| FX-17-08 | Integration test | BD checkout response, invoice PDF, usage page, chat-app billing — assert USD-free |
| FX-17-09 | `.planning/REQUIREMENTS.md` | Add FX-17-01..09 rows pointing at evidence |
| FX-17-10 | `.planning/phases/17-fx-usd-zero-leak/17-VERIFICATION.md` | Final closure log |

## Section D — Out of Scope (deferred)

- DB column rename / migration — internal columns (`amount_usd`) stay. Wire-only fix.
- `stripe/rail.go` USD payload to Stripe — server-to-server, never returned to customer.
- Per-country pricing primitive design beyond BD/non-BD split — Phase 18+ owns full multi-currency support.
- RBAC tier-aware authorization (Phase 18).

## Section E — Hand-offs to later phases

| ID | To Phase | Description |
|---|---|---|
| HANDOFF-17-01 | 18 | RBAC matrix replaces `is_platform_admin` stub introduced by Phase 14 (if remaining usages found in audit) |
| HANDOFF-17-02 | 25 | Final pre-launch chat-app FX audit re-run against shipped Phase 23 i18n bundles |
