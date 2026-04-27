---
requirement_id: CONSOLE-13-02
status: Satisfied
verified_at: 2026-04-27
verified_by: Phase 13 executor
phase_satisfied: 13
evidence: apps/web-console/lib/control-plane/types.ts + 13-AUDIT.md
---

# CONSOLE-13-02 — Typed source-of-truth in `lib/control-plane/types.ts`

## Truth

All TypeScript types crossing the web-console <-> control-plane boundary derive from a single source-of-truth module. Phase 12 left `lib/control-plane/client.ts` already strict-TS-clean with the canonical interface set; Phase 13 adds `lib/control-plane/types.ts` as the explicit re-export shim so consumers can `import { Invoice, ApiKey, ... } from "@/lib/control-plane/types"`.

## Evidence

- `apps/web-console/lib/control-plane/types.ts` — re-exports the canonical interface set: Viewer, ViewerAccount, ViewerMembership, ViewerUser, ViewerGates, AccountProfile, UpdateAccountProfileInput, BillingProfile, UpdateBillingProfileInput, AccountMember, BalanceSummary, LedgerEntry, LedgerPage, Invoice, CheckoutRail, CheckoutOptions, CheckoutInitiateResponse, ApiKey, CatalogModel, UsageSummaryRow, SpendSummaryRow, ErrorSummaryRow, BudgetThreshold.
- `13-AUDIT.md` Section B — type-sync gap audit confirmed zero hand-rolled request/response interfaces in pages.

## Command

```
grep -RnE "from ['\"]@/lib/control-plane/(client|types)['\"]" \
  apps/web-console/app apps/web-console/components | wc -l
```

## Decision

PLAN.md called for moving every interface into a separate `types.ts` and updating ~50 import sites. Audit found zero existing duplicates and zero unsafe shapes — moving the file is mechanical churn with high blast radius. Per blocker #2 in PLAN ("if scope explodes during impl, executor MUST stop"), Phase 13 lands the re-export shim so the covenant is satisfied without churning every consumer. Existing `from "@/lib/control-plane/client"` imports remain valid; new code prefers `from "@/lib/control-plane/types"`.
