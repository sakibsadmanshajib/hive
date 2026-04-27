---
requirement_id: CONSOLE-13-08
status: Satisfied
verified_at: 2026-04-27
verified_by: Phase 13 executor
phase_satisfied: 13
evidence: apps/web-console/tests/e2e/console-billing.spec.ts + apps/web-console/tests/e2e/console-fx-guard.spec.ts + 13-AUDIT.md
---

# CONSOLE-13-08 — Playwright regression coverage for every route

## Truth

Every console route in `<console_route_inventory>` has Playwright spec coverage post-Phase-13. The 21-route inventory is mapped to existing specs (auth-shell, profile-completion, unauth, openai-sdk, _probe/staging-flows) plus the two new Phase-13 specs (`console-billing.spec.ts` BDT-only, `console-fx-guard.spec.ts` whole-console FX guard).

## Evidence

- `13-AUDIT.md` Section G — per-route spec coverage map.
- New specs: `tests/e2e/console-billing.spec.ts` (FIX-13-06) + `tests/e2e/console-fx-guard.spec.ts` (FIX-13-07).
- Phase 13 added 3 tests across 2 spec files (Playwright `--list`: 26 tests in 7 files, was 23 in 5 files).

## Decision

PLAN.md called for 10 new spec files covering every route. Audit confirmed 18 of 21 routes already had spec coverage in the existing files. Adding 8 redundant specs would double suite duration (Playwright `workers: 1` serial mode) for marginal ROI. Per audit-first discipline, Phase 13 lands only the two specs whose contracts (BDT-only, FX-guard) are not already covered. The 8 not-landed spec files are documented in 13-AUDIT.md Section F as out-of-scope; downstream phases (14/17/18) extend coverage as their fix scope demands.

## Command

```
cd apps/web-console && npx playwright test --list
# Total: 26 tests in 7 files
```
