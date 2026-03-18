---
phase: 3
slug: auth-compliance
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-17
---

# Phase 3 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest ^2.1.8 |
| **Config file** | none — uses vitest defaults |
| **Quick run command** | `cd apps/api && pnpm test` |
| **Full suite command** | `cd apps/api && pnpm test` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `cd apps/api && pnpm test`
- **After every plan wave:** Run `cd apps/api && pnpm test`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 3-01-01 | 01 | 0 | FOUND-02 | integration | `cd apps/api && pnpm vitest run test/routes/v1-auth-compliance.test.ts` | ❌ W0 | ⬜ pending |
| 3-01-02 | 01 | 1 | FOUND-02 | integration | `cd apps/api && pnpm vitest run test/routes/v1-auth-compliance.test.ts -t "valid bearer"` | ❌ W0 | ⬜ pending |
| 3-01-03 | 01 | 1 | FOUND-02 | integration | `cd apps/api && pnpm vitest run test/routes/v1-auth-compliance.test.ts -t "missing"` | ❌ W0 | ⬜ pending |
| 3-01-04 | 01 | 1 | FOUND-02 | integration | `cd apps/api && pnpm vitest run test/routes/v1-auth-compliance.test.ts -t "invalid"` | ❌ W0 | ⬜ pending |
| 3-01-05 | 01 | 1 | FOUND-02 | integration | `cd apps/api && pnpm vitest run test/routes/v1-auth-compliance.test.ts -t "x-api-key ignored"` | ❌ W0 | ⬜ pending |
| 3-02-01 | 02 | 1 | FOUND-05 | integration | `cd apps/api && pnpm vitest run test/routes/v1-auth-compliance.test.ts -t "content-type"` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `apps/api/test/routes/v1-auth-compliance.test.ts` — SDK integration test stubs for FOUND-02 and FOUND-05
- [ ] `openai` npm package as devDependency — `cd apps/api && pnpm add -D openai`
- [ ] Test helper for creating Fastify app with mock services (server bootstrap utility in `apps/api/test/helpers/`)

*Note: Wave 0 must be complete before any Wave 1 implementation tasks.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Go SDK compatibility | FOUND-02 | Deferred to future phase per CONTEXT.md | N/A this phase |
| Python SDK compatibility | FOUND-02 | Deferred to future phase per CONTEXT.md | N/A this phase |
| Streaming `text/event-stream` header | FOUND-05 | Deferred to Phase 6 per CONTEXT.md | N/A this phase |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
