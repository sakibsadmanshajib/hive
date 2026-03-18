---
phase: 6
slug: chat-completions-streaming
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-18
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
| 6-01-01 | 01 | 0 | CHAT-04, CHAT-05 | unit | `npx vitest run apps/api/src/routes/__tests__/chat-completions-streaming-compliance.test.ts -x` | ❌ W0 | ⬜ pending |
| 6-01-02 | 01 | 1 | CHAT-04 | unit | `npx vitest run apps/api/src/routes/__tests__/chat-completions-streaming-compliance.test.ts -x` | ❌ W0 | ⬜ pending |
| 6-01-03 | 01 | 1 | CHAT-04 | unit | Same | ❌ W0 | ⬜ pending |
| 6-01-04 | 01 | 1 | CHAT-05 | unit | Same | ❌ W0 | ⬜ pending |
| 6-02-01 | 02 | 2 | CHAT-04, CHAT-05 | unit | `npx vitest run` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `apps/api/src/routes/__tests__/chat-completions-streaming-compliance.test.ts` — SSE chunk shape validation (CHAT-04, CHAT-05)
- [ ] Test helper to create mock SSE ReadableStream for unit tests (simulates OpenRouter response)

*Wave 0 must be complete before Wave 1 execution begins.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| `openai` SDK `for await` iteration works end-to-end | CHAT-04 | Requires live OpenRouter call with real SSE stream | Run `npx ts-node` script calling `client.chat.completions.create({ stream: true })` against local server; confirm all chunks received |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
