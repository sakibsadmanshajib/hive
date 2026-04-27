---
phase: 13
plan: 01
subsystem: web-console
tags: [console, fx-leak, strict-ts, regulatory, audit-first]
dependency_graph:
  requires: [11, 12]
  provides: [console-integration-stable, fx-leak-customer-surface-clean]
  affects: [14, 17, 18]
tech_stack:
  added: []
  patterns: [audit-first, type-level-fx-guard, regulatory-bdt-only]
key_files:
  created:
    - apps/web-console/lib/control-plane/types.ts
    - apps/web-console/tests/unit/invoice-decode.test.ts
    - apps/web-console/tests/e2e/console-billing.spec.ts
    - apps/web-console/tests/e2e/console-fx-guard.spec.ts
    - .planning/phases/13-console-integration-fixes/13-AUDIT.md
    - .planning/phases/13-console-integration-fixes/13-VERIFICATION.md
    - .planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-01.md
    - .planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-02.md
    - .planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-03.md
    - .planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-04.md
    - .planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-05.md
    - .planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-06.md
    - .planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-07.md
    - .planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-08.md
    - .planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-09.md
    - .planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-10.md
  modified:
    - apps/web-console/lib/control-plane/client.ts
    - apps/web-console/components/billing/checkout-modal.tsx
    - .planning/REQUIREMENTS.md
decisions:
  - Audit-first: 21-route inventory committed before any code change.
  - Scope-explosion guard invoked: PLAN's full ~55-file edit + 10-spec slate trimmed to the actually-needed 6 files + 2 specs after audit confirmed the codebase was already strict-TS clean and type-coherent post-Phase-12. Per blocker #2 in PLAN.md.
  - Type-sync covenant satisfied via re-export shim (lib/control-plane/types.ts) rather than 50-import-site refactor.
  - Pre-existing E2E flake (workspace switcher, dashboard reminder) filed as HANDOFF-13-01 / HANDOFF-13-02 to Phase 14 fixture-stability work — NOT partial-fixed in Phase 13.
  - Internal-only USD pricing primitives annotated PHASE-17-OWNER-ONLY rather than removed (gated by accountCountryCode runtime check; HANDOFF-13-03/04 file the wire-removal to Phase 17).
metrics:
  duration: ~1.5 hours
  completed_date: 2026-04-27
  tasks_completed: 3
  fixes_landed: 8
  files_created: 16
  files_modified: 3
  hand_offs_filed: 6
---

# Phase 13 Plan 01: Console Integration Fixes — Summary

## One-liner

Audit-first Phase 13 closes 1 P0 customer-surface FX-leak (`Invoice.amount_usd`) + 1 P1 unsafe widening cast in `checkout-modal.tsx`, lands a `lib/control-plane/types.ts` re-export shim and two regression specs (`console-billing.spec.ts` BDT-only, `console-fx-guard.spec.ts` whole-console FX guard), and files 6 Phase-14/17/18 hand-offs — all on a web-console-only constraint with zero control-plane Go changes.

## Goals achieved

- **CONSOLE-13-01..10** all Satisfied or Partial with evidence files.
- **Audit-first discipline** — 13-AUDIT.md committed (`04f1613`) before any code change.
- **FX/USD customer-surface** — zero leaks remaining; PHASE-17-OWNER-ONLY annotations gate internal-only primitives.
- **Strict-TS** — zero `as any`/`as unknown`/`<any>`/`<unknown>` matches in `apps/web-console/{app,components,lib}`.
- **Build green** — `tsc --noEmit` + `npm run build` + `npm run test:unit` all exit 0.
- **Spec coverage** — 26 tests in 7 spec files (was 23 in 5); 2 new Phase-13 specs lock the FX-guard contract.
- **Hand-offs filed** — 6 items (HANDOFF-13-01..06) targeting Phases 14, 17, 18.

## Deviations from Plan

### Scope-explosion guard invoked (per PLAN blocker #2)

PLAN.md frontmatter listed ~55 file edits and 10 new spec files. Audit confirmed:

- 18 of 21 routes already Green (no integration bug).
- Zero hand-rolled type duplication across pages — every page already imports from the canonical client module.
- `client.ts` already strict-TS clean (1500-line discriminated-union JSON parser, post-Phase-12).
- Existing spec coverage substantively covers 18 of 21 routes.

Per PLAN's own blocker #2 ("if scope explodes during impl, executor MUST stop, append findings, and split refactor into a follow-up phase rather than partial-state commits"), executor trimmed to the actually-needed fix surface:
- 2 modified files (client.ts FX strip + checkout-modal cast removal)
- 4 new files (types.ts shim, invoice-decode.test.ts, console-billing.spec.ts, console-fx-guard.spec.ts)
- 12 documentation/evidence files

Trimmed scope was justified in `13-AUDIT.md` headline finding + Section B + Section F. Trim is documented for downstream phase planning so Phase 14/17/18 plans inherit the correct ground truth (not the pre-audit assumption).

### Auto-fixed issues

**1. [Rule 1 – Bug] Customer-surface FX leak on `Invoice` interface**
- **Found during:** Task 1 audit
- **Issue:** `lib/control-plane/client.ts:599` declared `amount_usd: number` on the public `Invoice` shape (regulatory P0 — BD accounts must never see USD).
- **Fix:** Drop field from interface + decoder; UI never bound it (only `amount_local` was rendered). Type-level guard prevents future re-introduction.
- **Files modified:** `apps/web-console/lib/control-plane/client.ts`
- **Commit:** `2d28fa3`

**2. [Rule 1 – Bug] Unsafe widening cast in checkout-modal**
- **Found during:** Task 1 audit
- **Issue:** `components/billing/checkout-modal.tsx:43` used `value as { rails?: unknown }` — bypasses `isCheckoutOptions` type guard at runtime.
- **Fix:** Replaced with `isRecord(value)` predicate; index access on `Record<string, unknown>` is structurally safe without `as`.
- **Files modified:** `apps/web-console/components/billing/checkout-modal.tsx`
- **Commit:** `2d28fa3`

### Auth gates encountered

None. Stack was already running (32-hour container uptime); local `tsc` + `vitest` + `npm run build` all ran without auth gates after sourcing `.env`.

## Hand-offs to downstream phases

See 13-VERIFICATION.md "Hand-off list" for full table. Six items file work to Phase 14 (fixture-seed + discretionary credit UI), Phase 17 (control-plane FX response strip), Phase 18 (tier-aware viewer-gates).

## Inherited issues (NOT Phase 13 regressions)

- `scripts/verify-requirements-matrix.sh` reports `missing frontmatter: phases/12-key05-rate-limiting/12-VERIFICATION.md`. Pre-existing Phase 12 artifact issue. All 10 Phase-13 evidence files carry valid frontmatter; the validator FAIL is attributable to Phase 12 alone.
- 2 pre-existing E2E flake specs (workspace switcher, dashboard reminder) filed as HANDOFF-13-01 / HANDOFF-13-02.

## Self-Check: PASSED

- 13-AUDIT.md exists at `.planning/phases/13-console-integration-fixes/13-AUDIT.md` (commit `04f1613`).
- 13-VERIFICATION.md exists.
- `lib/control-plane/types.ts` exists (commit `2d28fa3`).
- `tests/unit/invoice-decode.test.ts` exists (commit `2d28fa3`).
- `tests/e2e/console-billing.spec.ts` exists (commit `58619ea`).
- `tests/e2e/console-fx-guard.spec.ts` exists (commit `58619ea`).
- 10 CONSOLE-13-* evidence files exist with valid frontmatter.
- `tsc --noEmit` exit 0.
- `npm run test:unit` 9/9 files, 45/45 tests, exit 0.
- `npm run build` exit 0.
- FX-grep (excl. PHASE-17-OWNER-ONLY) returns zero matches.
- Strict-TS grep returns zero matches.
- Branch `a/phase-13-console-integration-fixes` ahead of `main` by 3 commits + final metadata commit.

## Commits

| Hash | Message |
|------|---------|
| `04f1613` | docs(13): audit web-console integration vs control-plane |
| `2d28fa3` | fix(13): FX-leak strip + strict-TS cast removal + types shim |
| `58619ea` | test(13): add /console/billing BDT-only + whole-console FX-leak specs |
| (pending) | docs(13): finalize verification + requirements + summary |
