---
phase: 9
slug: operational-hardening
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-19
---

# Phase 9 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest |
| **Config file** | `apps/api/vitest.config.ts` |
| **Quick run command** | `pnpm --filter api test run -- --reporter=verbose` |
| **Full suite command** | `pnpm --filter api test run` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `pnpm --filter api test run -- --reporter=verbose`
- **After every plan wave:** Run `pnpm --filter api test run`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 9-01-01 | 01 | 1 | OPS-01 | integration | `pnpm --filter api test run -- stubs` | ❌ W0 | ⬜ pending |
| 9-01-02 | 01 | 1 | OPS-01 | integration | `pnpm --filter api test run -- stubs` | ❌ W0 | ⬜ pending |
| 9-02-01 | 02 | 2 | OPS-02 | manual | see Manual-Only below | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `apps/api/src/routes/__tests__/v1-stubs.test.ts` — integration tests for all 7 stub endpoint groups (audio, files, uploads, batches, completions, fine_tuning, moderations)

*All stubs share identical response shape — a single parametric test file covers all groups.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| GitHub issues exist for each deferred endpoint group | OPS-02 | Requires GitHub API/browser verification | Open GitHub repo issues list; confirm 7 issues exist with titles matching `feat: implement /v1/{group}`, each with acceptance criteria |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
