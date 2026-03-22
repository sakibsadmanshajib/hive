---
phase: 2
slug: type-infrastructure
status: complete
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-17
audited: 2026-03-21
---

# Phase 2 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest (workspace-level) |
| **Config file** | Root vitest config (no `apps/api/vitest.config.ts`) |
| **Quick run command** | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` |
| **Full suite command** | `cd apps/api && npx vitest run` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x`
- **After every plan wave:** Run `cd apps/api && npx vitest run`
- **Before `/gsd:verify-work`:** Full suite must be green + `npx tsc --noEmit` passes
- **Max feedback latency:** ~15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 02-01-01 | 01 | 0 | FOUND-07 | build | `cd apps/api && npx tsc --noEmit` | ✅ | ✅ green |
| 02-01-02 | 01 | 1 | FOUND-06 | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | ✅ | ✅ green |
| 02-01-03 | 01 | 1 | FOUND-06 | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | ✅ | ✅ green |
| 02-02-01 | 02 | 2 | FOUND-06 | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | ✅ | ✅ green |
| 02-02-02 | 02 | 2 | FOUND-06 | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | ✅ | ✅ green |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [x] `apps/api/test/routes/typebox-validation.test.ts` — 9/9 tests passing (extra fields rejected 400, valid requests pass, error format verified, all 3 POST routes + GET models covered)
- [x] `apps/api/src/types/openai.d.ts` — generated from `docs/reference/openai-openapi.yml` via `openapi-typescript` (covers FOUND-07)
- [x] `@sinclair/typebox@0.34` used (fallback applied — `typebox/type` subpath not available with CJS + Node moduleResolution)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| TypeScript compilation fails for wrong response shape | FOUND-07 | Requires intentionally wrong type annotation to verify tsc catches it | Add a temporary `as WrongType` cast to a response builder, run `npx tsc --noEmit`, confirm error, revert |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 15s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** 2026-03-21 (retroactive audit — all tests passing, TSC clean)

---

## Validation Audit 2026-03-21

| Metric | Count |
|--------|-------|
| Gaps found | 0 |
| Resolved | 0 |
| Escalated | 0 |
| Tests verified | 9/9 passing |
| TSC | clean (exit 0) |
