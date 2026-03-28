---
phase: 5
slug: chat-completions-non-streaming
status: complete
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-18
audited: 2026-03-21
---

# Phase 5 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest |
| **Config file** | apps/api/vitest.config.ts |
| **Quick run command** | `pnpm --filter api test --run` |
| **Full suite command** | `pnpm --filter api test --run` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `pnpm --filter api test --run`
- **After every plan wave:** Run `pnpm --filter api test --run`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 20 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 5-01-01 | 01 | 1 | CHAT-02 | unit | `pnpm --filter api test --run src/domain/ai-service` | ✅ | ✅ green |
| 5-01-02 | 01 | 1 | CHAT-01, CHAT-03 | unit | `pnpm --filter api test --run src/domain/ai-service` | ✅ | ✅ green |
| 5-01-03 | 01 | 1 | CHAT-02 | unit | `pnpm --filter api test --run src/domain` | ✅ | ✅ green |
| 5-02-01 | 02 | 2 | CHAT-01 | integration | `pnpm --filter api test --run src/routes/chat-completions` | ✅ | ✅ green |
| 5-02-02 | 02 | 2 | CHAT-02 | integration | `pnpm --filter api test --run src/routes/chat-completions` | ✅ | ✅ green |
| 5-02-03 | 02 | 2 | CHAT-03 | integration | `pnpm --filter api test --run src/routes/chat-completions` | ✅ | ✅ green |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [x] `apps/api/src/domain/__tests__/ai-service.chat.test.ts` — unit tests for updated `chatCompletions()` params forwarding and response shaping (CHAT-01, CHAT-02, CHAT-03)
- [x] `apps/api/src/routes/__tests__/chat-completions-compliance.test.ts` — compliance tests for OpenAI response schema shape
- [x] `apps/api/test/routes/chat-completions-route.test.ts` — route tests for stream guard and full body forwarding

*Existing vitest infrastructure covers the framework — only test files need creation.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| `openai` SDK `client.chat.completions.create()` returns fully typed response | CHAT-01 | Requires live OpenRouter API key in test environment | Run test script: `OPENAI_BASE_URL=http://localhost:3000 OPENAI_API_KEY=sk-test npx ts-node scripts/sdk-smoke.ts` |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 20s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** 2026-03-21

---

## Validation Audit 2026-03-21

| Metric | Count |
|--------|-------|
| Gaps found | 6 |
| Resolved | 6 |
| Escalated | 0 |

All 6 task requirements were in MISSING/pending state (Wave 0 not updated post-execution). Tests were already written during Plan 02 execution. Verified 18/18 tests passing across 3 files: `ai-service.chat.test.ts` (6), `chat-completions-compliance.test.ts` (6), `chat-completions-route.test.ts` (6).
