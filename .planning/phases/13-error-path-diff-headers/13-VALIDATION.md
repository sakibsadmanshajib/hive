---
phase: 13
slug: error-path-diff-headers
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-22
---

# Phase 13 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest |
| **Config file** | `apps/api/package.json` → `pnpm --filter @hive/api test` |
| **Quick run command** | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-error-diff-headers.test.ts apps/api/test/routes/v1-stubs.test.ts` |
| **Full suite command** | `cd /home/sakib/hive && pnpm --filter @hive/api test` |
| **Estimated runtime** | ~25 seconds |

---

## Sampling Rate

- **After every task commit:** Run the task-specific command from the map below.
- **After every plan wave:** Run `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-error-diff-headers.test.ts apps/api/test/routes/v1-stubs.test.ts`
- **Before `$gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 25 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| T1 | 01 | 1 | DIFF-01 | route/unit regression guard | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/chat-completions-route.test.ts apps/api/test/routes/images-generations-route.test.ts apps/api/test/routes/responses-route.test.ts` | ✅ | ⬜ pending |
| T2 | 01 | 2 | DIFF-01 | live route + stub regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-error-diff-headers.test.ts apps/api/test/routes/v1-stubs.test.ts` | ✅ | ⬜ pending |
| T3 | 01 | 3 | DIFF-01 | full suite + Docker build | `cd /home/sakib/hive && pnpm --filter @hive/api test && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Existing infrastructure covers all phase requirements.

No Wave 0 setup needed — vitest and the v1 test-app harness already exist.

---

## Manual-Only Verifications

All phase behaviors should be automatable with route/stub regression tests and the final Docker build.

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 25s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
