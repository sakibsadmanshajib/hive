---
phase: 1
slug: error-format
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-17
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest (workspace dependency) |
| **Config file** | Inherited from workspace (pnpm workspace) |
| **Quick run command** | `pnpm --filter @hive/api test` |
| **Full suite command** | `pnpm test` |
| **Estimated runtime** | ~5 seconds |

---

## Sampling Rate

- **After every task commit:** Run `pnpm --filter @hive/api test`
- **After every plan wave:** Run `pnpm test`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 5 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 01-01-01 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ❌ W0 | ⬜ pending |
| 01-01-02 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ❌ W0 | ⬜ pending |
| 01-01-03 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ❌ W0 | ⬜ pending |
| 01-01-04 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ❌ W0 | ⬜ pending |
| 01-01-05 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ❌ W0 | ⬜ pending |
| 01-01-06 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `apps/api/test/routes/api-error-format.test.ts` — stubs for FOUND-01 (all error format assertions)
- [ ] Test helpers for creating Fastify instances with the v1 plugin registered (extend existing patterns or use `fastify.inject()`)

*Wave 0 creates test file stubs before implementation begins.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| OpenAI SDK parses errors without crashing | FOUND-01 | SDK integration test requires live SDK client | 1. Install `openai` SDK as dev dep 2. Send malformed request via SDK 3. Verify `error.message`, `error.type`, `error.param`, `error.code` are all defined |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 5s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
