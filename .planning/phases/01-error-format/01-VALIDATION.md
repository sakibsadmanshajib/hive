---
phase: 1
slug: error-format
status: complete
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-17
audited: 2026-03-21
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
| 01-01-01 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ✅ | ✅ green |
| 01-01-02 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ✅ | ✅ green |
| 01-01-03 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ✅ | ✅ green |
| 01-01-04 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ✅ | ✅ green |
| 01-01-05 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ✅ | ✅ green |
| 01-01-06 | 01 | 1 | FOUND-01 | unit | `pnpm --filter @hive/api test -- api-error-format` | ✅ | ✅ green |
| 01-02-01 | 02 | 1 | FOUND-01 | integration | `pnpm test` | ✅ | ✅ green |
| 01-02-02 | 02 | 1 | FOUND-01 | integration | `pnpm test` | ✅ | ✅ green |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [x] `apps/api/test/routes/api-error-format.test.ts` — 12 tests covering FOUND-01 (all status codes, scoping, malformed JSON)
- [x] Test helpers for creating Fastify instances with v1 plugin registered — extended FakeApp with `register`/`setErrorHandler`/`setNotFoundHandler` stubs

*Wave 0 complete — test files exist and all 222 tests pass.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| OpenAI SDK parses errors without crashing | FOUND-01 | SDK integration test requires live SDK client | 1. Install `openai` SDK as dev dep 2. Send malformed request via SDK 3. Verify `error.message`, `error.type`, `error.param`, `error.code` are all defined |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 5s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** 2026-03-21 (Nyquist audit — all 222 tests green, 8/8 tasks covered)

---

## Validation Audit 2026-03-21
| Metric | Count |
|--------|-------|
| Gaps found | 0 |
| Resolved | 0 |
| Escalated | 0 |
| Stale entries updated | 8 |
| Notes | VALIDATION.md was pre-execution draft; all tests existed and passed. Added Plan 02 tasks (01-02-01, 01-02-02) to map. |
