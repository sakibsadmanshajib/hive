---
phase: 7
slug: surface-expansion
status: executed
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-18
audited: 2026-03-21
---

# Phase 7 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest (existing) |
| **Config file** | `apps/api/vitest.config.ts` |
| **Quick run command** | `cd apps/api && npx vitest run --passWithNoTests` |
| **Full suite command** | `cd apps/api && npx vitest run` |
| **Estimated runtime** | ~10 seconds |

---

## Sampling Rate

- **After every task commit:** Run `cd apps/api && npx vitest run --passWithNoTests`
- **After every plan wave:** Run `cd apps/api && npx vitest run`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 7-01-01 | 01 | 0 | SURF-01 | unit | `cd apps/api && npx vitest run src/routes/__tests__/embeddings-compliance.test.ts` | ✅ | ✅ green |
| 7-01-02 | 01 | 0 | SURF-02 | unit | `cd apps/api && npx vitest run src/routes/__tests__/images-compliance.test.ts` | ✅ | ✅ green |
| 7-01-03 | 01 | 0 | SURF-03 | unit | `cd apps/api && npx vitest run src/routes/__tests__/responses-compliance.test.ts` | ✅ | ✅ green |
| 7-02-01 | 02 | 1 | SURF-01 | unit | `cd apps/api && npx vitest run src/routes/__tests__/embeddings-compliance.test.ts` | ✅ | ✅ green |
| 7-02-02 | 02 | 1 | SURF-02 | unit | `cd apps/api && npx vitest run src/routes/__tests__/images-compliance.test.ts` | ✅ | ✅ green |
| 7-02-03 | 02 | 1 | SURF-03 | unit | `cd apps/api && npx vitest run src/routes/__tests__/responses-compliance.test.ts` | ✅ | ✅ green |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [x] `apps/api/src/routes/__tests__/embeddings-compliance.test.ts` — stubs for SURF-01
- [x] `apps/api/src/routes/__tests__/images-compliance.test.ts` — stubs for SURF-02
- [x] `apps/api/src/routes/__tests__/responses-compliance.test.ts` — stubs for SURF-03

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| `openai` SDK `client.embeddings.create()` round-trip | SURF-01 | Requires live OpenRouter API key | Run `node -e "const OpenAI = require('openai'); const c = new OpenAI({baseURL:'http://localhost:3000/v1',apiKey:'sk-test'}); c.embeddings.create({model:'text-embedding-3-small',input:'hello'}).then(r=>console.log(r))"` |
| `openai` SDK `client.images.generate()` round-trip | SURF-02 | Requires live OpenRouter API key | Run `node -e "const OpenAI = require('openai'); const c = new OpenAI({baseURL:'http://localhost:3000/v1',apiKey:'sk-test'}); c.images.generate({model:'dall-e-3',prompt:'a cat'}).then(r=>console.log(r))"` |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** passed 2026-03-21

---

## Validation Audit 2026-03-21
| Metric | Count |
|--------|-------|
| Gaps found | 0 |
| Resolved | 0 |
| Escalated | 0 |
| Tests verified green | 30 (3 files) |

All Wave 0 test files were created by Plan 3 (07-03) but VALIDATION.md remained as draft scaffold. Audit confirmed full coverage — no new tests needed.
