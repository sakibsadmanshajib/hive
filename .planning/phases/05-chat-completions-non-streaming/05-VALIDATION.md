---
phase: 5
slug: chat-completions-non-streaming
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-18
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
| 5-01-01 | 01 | 1 | CHAT-02 | unit | `pnpm --filter api test --run src/domain/ai-service` | ❌ W0 | ⬜ pending |
| 5-01-02 | 01 | 1 | CHAT-01, CHAT-03 | unit | `pnpm --filter api test --run src/domain/ai-service` | ❌ W0 | ⬜ pending |
| 5-01-03 | 01 | 1 | CHAT-02 | unit | `pnpm --filter api test --run src/domain` | ❌ W0 | ⬜ pending |
| 5-02-01 | 02 | 2 | CHAT-01 | integration | `pnpm --filter api test --run src/routes/chat-completions` | ❌ W0 | ⬜ pending |
| 5-02-02 | 02 | 2 | CHAT-02 | integration | `pnpm --filter api test --run src/routes/chat-completions` | ❌ W0 | ⬜ pending |
| 5-02-03 | 02 | 2 | CHAT-03 | integration | `pnpm --filter api test --run src/routes/chat-completions` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `apps/api/src/domain/__tests__/ai-service.chat.test.ts` — unit tests for updated `chatCompletions()` params forwarding and response shaping (CHAT-01, CHAT-02, CHAT-03)
- [ ] `apps/api/src/routes/__tests__/chat-completions.test.ts` — integration tests for route handler (stream guard, full params pass-through, response fields)

*Existing vitest infrastructure covers the framework — only test files need creation.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| `openai` SDK `client.chat.completions.create()` returns fully typed response | CHAT-01 | Requires live OpenRouter API key in test environment | Run test script: `OPENAI_BASE_URL=http://localhost:3000 OPENAI_API_KEY=sk-test npx ts-node scripts/sdk-smoke.ts` |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 20s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
