---
phase: 6
slug: chat-completions-streaming
status: executed
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-18
audited: 2026-03-21
---

# Phase 6 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest ^2.1.8 |
| **Config file** | vitest config in package.json (existing) |
| **Quick run command** | `npx vitest run apps/api/src/routes/__tests__/chat-completions-streaming-compliance.test.ts -x` |
| **Full suite command** | `npx vitest run` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `npx vitest run apps/api/src/routes/__tests__/chat-completions-streaming-compliance.test.ts -x`
- **After every plan wave:** Run `npx vitest run`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 6-01-01 | 01 | 0 | CHAT-04, CHAT-05 | unit | `npx vitest run apps/api/src/routes/__tests__/chat-completions-streaming-compliance.test.ts -x` | ✅ | ✅ green |
| 6-01-02 | 01 | 1 | CHAT-04 | unit | `npx vitest run apps/api/src/routes/__tests__/chat-completions-streaming-compliance.test.ts -x` | ✅ | ✅ green |
| 6-01-03 | 01 | 1 | CHAT-04 | unit | Same | ✅ | ✅ green |
| 6-01-04 | 01 | 1 | CHAT-05 | unit | Same | ✅ | ✅ green |
| 6-02-01 | 02 | 2 | CHAT-04, CHAT-05 | unit | `npx vitest run` | ✅ | ✅ green |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [x] `apps/api/src/routes/__tests__/chat-completions-streaming-compliance.test.ts` — SSE chunk shape validation (CHAT-04, CHAT-05)
- [x] Test helper to create mock SSE ReadableStream for unit tests (simulates OpenRouter response)

*Wave 0 must be complete before Wave 1 execution begins.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| `openai` SDK `for await` iteration works end-to-end | CHAT-04 | Requires live OpenRouter call with real SSE stream | Run `npx ts-node` script calling `client.chat.completions.create({ stream: true })` against local server; confirm all chunks received |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 15s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** 2026-03-21 — all 14 tests green (14/14 passed, 827ms)

---

## Validation Audit 2026-03-21

| Metric | Count |
|--------|-------|
| Gaps found | 0 |
| Resolved | 0 |
| Escalated | 0 |
| Tests run | 14 |
| Tests passing | 14 |
