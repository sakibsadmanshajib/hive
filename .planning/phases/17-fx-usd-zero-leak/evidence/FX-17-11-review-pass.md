# FX-17-11 — PR #137 Review-Pass Closure

| Field | Value |
|---|---|
| Phase | 17 — FX/USD Zero-Leak Audit & Hardening |
| Task | Review-pass (post-PR-#137 commits) |
| Date | 2026-05-14 |
| Branch | `a/phase-17-fx-zero-leak` |
| Parent | FX-17-01..10 (closed 2026-05-09) |

## Findings addressed

Sources: codex P1 + CodeRabbit majors/minors on PR #137 + adversarial review.

| ID | Severity | Source | Surface | Fix |
|---|---|---|---|---|
| R-1 | P1 | codex | `checkout-modal.tsx` | `price_per_credit_minor` was per-block, not per-credit — `creditAmount * price` inflated non-BD totals by 100,000× ($0.05 → $5,000). Wire field renamed to `price_per_block_minor`; added `credit_block_size` to expose the divisor. Front-end now computes `floor(credits * price / blockSize)` using integer math. |
| R-2 | Major | CodeRabbit | `payments/types.go` | `FXSnapshotID` was tagged `json:"fx_snapshot_id,omitempty"`. Whitelist comment is not a runtime guarantee — flipped to `json:"-"` so a stray `json.Marshal(PaymentIntent)` cannot leak an `fx_*` key. |
| R-3 | Major | CodeRabbit | `.planning/STATE.md` | Frontmatter still reported `milestone_shipped_awaiting_next` with a 2026-04-21 timestamp. Synced to `phase_closed` + 2026-05-14 to match the body and v1.1 ship-gate state. |
| R-4 | Major | CodeRabbit | `billing-fx-zero-leak.spec.ts` | Forbidden-pattern sweep would green on auth-error HTML. Added `response.ok`, status 200, `content-type: application/json`, and positive presence asserts (`currency` + `price_per_block_minor` + `credit_block_size`) before the negative loop. |
| R-5 | Major | CodeRabbit | `lint-no-customer-usd.mjs` | TS field regex missed `readonly amount_usd?: number`. Added `(?:readonly\s+)?` prefix to `TS_FIELD_PATTERN`; new lint tests pin `readonly amount_usd`, `readonly fx_*`, and the whitelist-still-exempts case. |
| R-6 | Minor | CodeRabbit | `integration_fx_zero_leak_test.go` | Non-BD `keyPosBanned` used exact-match strings, missed `"usd_balance"` / `"fx_rate_basis"`. Added `"usd_` + `"fx_` prefix entries. Service-layer `bannedKeys` aligned with `fx_` + `usd_` prefixes. |
| **R-7** | **P0 (adversarial)** | **this review** | `payments/service.go:339` → `ledger/types.go` | `PostPurchaseGrant` wrote `fx_snapshot_id` into ledger entry metadata. `LedgerEntry.Metadata` is tagged `json:"metadata"` and serialized to customers via `GET /api/v1/accounts/current/ledger/entries`, so an `fx_*` key would land on a BD customer surface. Stripped from grant metadata; audit linkage preserved through `payment_intent_id`. Added regression test `TestPostPurchaseGrant_NoFXKeyInLedgerMetadata`. |

## Wire-shape change

`CheckoutOptions` wire (BD + non-BD):

```text
- price_per_credit_minor: int      // misleading: per-block, not per-credit
+ price_per_block_minor:  int      // honest: per-block (= per credit_block_size credits)
+ credit_block_size:      int      // = CreditsPerUSD = 100,000
  currency:               string
```

Frontend display math:

```ts
total_minor = Math.floor(
  (creditAmount * options.price_per_block_minor) / options.credit_block_size,
);
```

Worst-case magnitude: `500_000_000 credits × ~15_000 paisa ≈ 7.5e12`, inside `Number.MAX_SAFE_INTEGER` (9.0e15). No BigInt needed.

## Acceptance gates

| Gate | Command | Result |
|---|---|---|
| Go build | `go build -buildvcs=false ./apps/control-plane/...` | `BUILD_OK` |
| Go test | `go test -buildvcs=false -count=1 -short ./apps/control-plane/... ./apps/edge-api/...` | all packages `ok` |
| FX lint | `node packages/openai-contract/scripts/lint-no-customer-usd.mjs --all` | `1170 files clean` |
| FX lint suite | `node --test packages/openai-contract/scripts/lint-no-customer-usd.test.mjs` | `21 pass / 0 fail` |
| Web-console tsc | `tsc --noEmit` | clean |
| Web-console vitest | `vitest run` | `67 pass / 0 fail / 12 files` |

## Files changed

```text
apps/control-plane/internal/payments/service.go
apps/control-plane/internal/payments/types.go
apps/control-plane/internal/payments/service_test.go
apps/control-plane/internal/payments/service_checkout_options_test.go
apps/control-plane/internal/payments/service_fx_zero_leak_test.go
apps/control-plane/internal/payments/integration_fx_zero_leak_test.go
apps/control-plane/internal/payments/service_post_purchase_grant_metadata_test.go   (new)
apps/web-console/lib/control-plane/client.ts
apps/web-console/components/billing/checkout-modal.tsx
apps/web-console/components/billing/checkout-modal.test.tsx
apps/web-console/tests/e2e/billing-fx-zero-leak.spec.ts
packages/openai-contract/scripts/lint-no-customer-usd.mjs
packages/openai-contract/scripts/lint-no-customer-usd.test.mjs
.planning/STATE.md
.planning/phases/17-fx-usd-zero-leak/evidence/FX-17-11-review-pass.md           (this file)
```

## Adversarial review — residual checks (no further fixes)

The following surfaces were re-audited end-to-end for FX/USD key leakage; all clean post-R-7:

- `GET /api/v1/accounts/current/checkout/rails` — exact bytes via in-process Handler test
- `POST /api/v1/accounts/current/checkout/initiate` — exact bytes
- `GET /api/v1/accounts/current/ledger/entries` — `Metadata` map sanitized at the grant boundary (R-7)
- `GET /api/v1/accounts/current/invoices` + invoice PDF — no FX/USD keys
- `GET /api/v1/accounts/current/budgets` / `spend-alerts` / `grants` — covered by existing fx-zero-leak http tests
- OpenAPI spec YAML — covered by default lint scan + `--all` repo-wide scan
- Web-console rendered DOM — covered by Playwright `billing-fx-zero-leak.spec.ts` (post-hardening, R-4)

No additional leaks found.
