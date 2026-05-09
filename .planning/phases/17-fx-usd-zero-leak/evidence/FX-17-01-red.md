# FX-17-01 RED Evidence — Task 1

Phase 17 — FX/USD Zero-Leak. Task 1 of 10 — RED phase.

This evidence file proves the customer-USD wire-shape contract is locked
by failing tests on `b87fa24`. Tasks 2–4 turn each subtest GREEN.

## Test command

```
cd /home/sakib/hive/deploy/docker && \
  docker compose --env-file ../../.env --profile tools --profile local run \
    --rm --no-deps -T toolchain \
    'cd /workspace && go test -buildvcs=false -count=1 -v -run FXZeroLeak \
        ./apps/control-plane/internal/payments/... \
        ./apps/control-plane/internal/ledger/...'
```

Note on Dockerfile.toolchain entrypoint: `ENTRYPOINT ["/bin/sh", "-c"]` —
the command must be passed as a SINGLE quoted string argument, not as
multi-token `sh -c "..."`. Go EXIT recorded as `GO_EXIT=$?` inside the
container.

## Run metadata

- cwd (host): `/home/sakib/hive/deploy/docker`
- HEAD: `b87fa24 docs(17): PLAN.md — 10-task graph for FX/USD zero-leak`
- Branch: `a/phase-17-fx-zero-leak`
- Date: 2026-05-08
- `GO_EXIT=1` — tests failed as expected (RED).

## RED subtests (FAILS as required)

| # | Subtest | File | Locked by Task |
| - | - | - | - |
| 1 | `TestInitiateResponseWireShape_FXZeroLeak/no_amount_usd` | `apps/control-plane/internal/payments/http_fx_zero_leak_test.go` | Task 2 (FX-17-01) |
| 2 | `TestPaymentIntentWireShape_FXZeroLeak/no_amount_usd` | `apps/control-plane/internal/payments/service_fx_zero_leak_test.go` | Task 2 (FX-17-01) |
| 3 | `TestCheckoutOptionsWireShape_FXZeroLeak/has_per_country_primitive` (asserts `"currency"` AND `"price_per_credit_minor"`) | `apps/control-plane/internal/payments/service_fx_zero_leak_test.go` | Task 4 (FX-17-03) |
| 4 | `TestInvoiceWireShape_FXZeroLeak/no_amount_usd` | `apps/control-plane/internal/ledger/types_fx_zero_leak_test.go` | Task 3 (FX-17-02) |

All four subtests carry `--- FAIL` lines in the run log.

## Failure tail (last 30 lines)

```
=== RUN   TestInvoiceWireShape_FXZeroLeak
=== RUN   TestInvoiceWireShape_FXZeroLeak/no_amount_usd
    types_fx_zero_leak_test.go:55: InvoiceRow JSON wire shape contains banned key "amount_usd"
        payload: {"id":"...","account_id":"...","payment_intent_id":"...","invoice_number":"INV-2026-05-0001","status":"paid","credits":100000,"amount_usd":100,"amount_local":1250000,"local_currency":"BDT","tax_treatment":"vat_inclusive","rail":"bkash","line_items":[{"amount_local":1250000,"description":"100,000 Hive Credits"}],"created_at":"2026-05-08T00:00:00Z"}
=== RUN   TestInvoiceWireShape_FXZeroLeak/no_usd_
=== RUN   TestInvoiceWireShape_FXZeroLeak/no_fx_
=== RUN   TestInvoiceWireShape_FXZeroLeak/no_price_per_credit_usd
=== RUN   TestInvoiceWireShape_FXZeroLeak/no_exchange_rate
--- FAIL: TestInvoiceWireShape_FXZeroLeak (0.00s)
    --- FAIL: TestInvoiceWireShape_FXZeroLeak/no_amount_usd (0.00s)
    --- PASS: TestInvoiceWireShape_FXZeroLeak/no_usd_ (0.00s)
    --- PASS: TestInvoiceWireShape_FXZeroLeak/no_fx_ (0.00s)
    --- PASS: TestInvoiceWireShape_FXZeroLeak/no_price_per_credit_usd (0.00s)
    --- PASS: TestInvoiceWireShape_FXZeroLeak/no_exchange_rate (0.00s)
FAIL
FAIL	github.com/hivegpt/hive/apps/control-plane/internal/ledger	0.004s
FAIL
GO_EXIT=1
```

## Banned-key surface confirmed

Each test asserts `bytes.Contains` against the JSON marshal output for these
banned customer-surface keys:

```
amount_usd
usd_
fx_
price_per_credit_usd
exchange_rate
```

Subtests `no_usd_`, `no_fx_`, `no_price_per_credit_usd`, `no_exchange_rate`
all PASS today — those keys are not in the live structs. The single live
leak per struct is `amount_usd`. CheckoutOptions also lacks the per-country
primitive (`currency` + `price_per_credit_minor`), which Task 4 introduces.
