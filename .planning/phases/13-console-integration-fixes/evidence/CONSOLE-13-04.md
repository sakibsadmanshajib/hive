---
requirement_id: CONSOLE-13-04
status: Satisfied
verified_at: 2026-04-27
verified_by: Phase 13 executor
phase_satisfied: 13
evidence: 13-VERIFICATION.md + apps/web-console/lib/control-plane/client.ts + apps/web-console/tests/unit/invoice-decode.test.ts
---

# CONSOLE-13-04 — Zero customer-surface FX/USD leak

## Truth

Zero occurrences of customer-visible `amount_usd`, `usd_`, `fx_`, `exchange_rate`, or human-readable USD strings in any `apps/web-console/` TS/TSX customer surface. Internal admin-only USD primitives are annotated `PHASE-17-OWNER-ONLY` with handoff filed to Phase 17.

## Evidence

- FIX-13-01: `Invoice` interface no longer carries `amount_usd` field (commit `2d28fa3`).
- FIX-13-02: `CheckoutOptions.price_per_credit_usd` is annotated `PHASE-17-OWNER-ONLY`; non-BD code path only.
- Unit test `tests/unit/invoice-decode.test.ts` locks the FX-free Invoice shape — Object.keys check rejects future re-introduction.
- E2E specs `tests/e2e/console-billing.spec.ts` + `tests/e2e/console-fx-guard.spec.ts` walk every console route asserting no FX field-name token reaches DOM (FIX-13-06, FIX-13-07).
- HANDOFF-13-03 + HANDOFF-13-04 file the control-plane response-shape work to Phase 17.

## Command

```
grep -RnE 'amount_usd|usd_|fx_|exchange_rate' \
  apps/web-console/app apps/web-console/components apps/web-console/lib \
  | grep -v PHASE-17-OWNER-ONLY
# returns zero matches
```

## Result

Zero matches. PHASE-17-OWNER-ONLY annotations gate `price_per_credit_usd` (one declaration line + one decoder line + one return line), all on the non-BD code path inside `client.ts`.
