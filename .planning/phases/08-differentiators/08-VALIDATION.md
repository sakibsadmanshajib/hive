---
phase: 8
slug: differentiators
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-18
---

# Phase 8 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Vitest (existing) |
| **Config file** | apps/api/vitest.config.ts |
| **Quick run command** | `cd apps/api && npx vitest run src/routes/__tests__/differentiators-compliance.test.ts` |
| **Full suite command** | `cd apps/api && npx vitest run` |
| **Estimated runtime** | ~10 seconds |

---

## Sampling Rate

- **After every task commit:** Run `cd apps/api && npx vitest run src/routes/__tests__/differentiators-*.test.ts`
- **After every plan wave:** Run `cd apps/api && npx vitest run`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** ~15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 8-01-01 | 01 | 0 | DIFF-04 | unit | `cd apps/api && npx vitest run src/routes/__tests__/differentiators-headers.test.ts -x` | ❌ W0 | ⬜ pending |
| 8-01-02 | 01 | 0 | DIFF-01 | unit | `cd apps/api && npx vitest run src/routes/__tests__/differentiators-headers.test.ts -x` | ❌ W0 | ⬜ pending |
| 8-01-03 | 01 | 1 | DIFF-01, DIFF-02 | unit | `cd apps/api && npx vitest run src/routes/__tests__/differentiators-headers.test.ts -x` | ❌ W0 | ⬜ pending |
| 8-01-04 | 01 | 1 | DIFF-04 | unit | `cd apps/api && npx vitest run src/routes/__tests__/differentiators-headers.test.ts -x` | ❌ W0 | ⬜ pending |
| 8-02-01 | 02 | 0 | DIFF-03 | unit | `cd apps/api && npx vitest run src/config/__tests__/model-aliases.test.ts -x` | ❌ W0 | ⬜ pending |
| 8-02-02 | 02 | 1 | DIFF-03 | unit | `cd apps/api && npx vitest run src/config/__tests__/model-aliases.test.ts -x` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `apps/api/src/routes/__tests__/differentiators-headers.test.ts` — test stubs for DIFF-01, DIFF-02, DIFF-04 (headers on all endpoints)
- [ ] `apps/api/src/config/__tests__/model-aliases.test.ts` — test stubs for DIFF-03 (alias resolution)
- [ ] `apps/api/src/config/model-aliases.ts` — alias map module (new file, tested by Wave 0)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| OpenAI SDK does not break on x-* headers | DIFF-01, DIFF-04 | SDK parsing behavior needs live SDK call | Run `openai.chat.completions.create(...)` via official SDK, confirm no header-related error thrown |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
