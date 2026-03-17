---
phase: 2
slug: type-infrastructure
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-17
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
| 02-01-01 | 01 | 0 | FOUND-07 | build | `cd apps/api && npx tsc --noEmit` | ❌ W0 | ⬜ pending |
| 02-01-02 | 01 | 1 | FOUND-06 | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | ❌ W0 | ⬜ pending |
| 02-01-03 | 01 | 1 | FOUND-06 | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | ❌ W0 | ⬜ pending |
| 02-02-01 | 02 | 2 | FOUND-06 | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | ❌ W0 | ⬜ pending |
| 02-02-02 | 02 | 2 | FOUND-06 | integration | `cd apps/api && npx vitest run test/routes/typebox-validation.test.ts -x` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `apps/api/test/routes/typebox-validation.test.ts` — failing tests for FOUND-06: extra fields rejected (400), missing required fields rejected (400), valid requests pass, error format matches Phase 1 `{ error: { message, type, param, code } }` — for all 4 routes (chat-completions, models, images-generations, responses)
- [ ] `apps/api/src/types/openai.d.ts` — generated from `docs/reference/openai-openapi.yml` via `openapi-typescript` (covers FOUND-07)
- [ ] Verify `typebox/type` subpath exports resolve with current tsconfig (CommonJS + Node moduleResolution) — if not, fallback to `@sinclair/typebox@0.34`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| TypeScript compilation fails for wrong response shape | FOUND-07 | Requires intentionally wrong type annotation to verify tsc catches it | Add a temporary `as WrongType` cast to a response builder, run `npx tsc --noEmit`, confirm error, revert |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
