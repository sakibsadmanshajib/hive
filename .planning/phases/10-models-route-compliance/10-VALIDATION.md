---
phase: 10
slug: models-route-compliance
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-21
---

# Phase 10 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest |
| **Config file** | `apps/api/package.json` → `pnpm --filter @hive/api test` |
| **Quick run command** | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/models-route.test.ts` |
| **Full suite command** | `cd /home/sakib/hive && pnpm --filter @hive/api test` |
| **Estimated runtime** | ~20 seconds |

---

## Sampling Rate

- **After every task commit:** Run the task-specific command from the map below.
- **After every plan wave:** Run `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-auth-compliance.test.ts apps/api/test/openai-sdk-regression.test.ts apps/api/test/routes/typebox-validation.test.ts`
- **Before `$gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 20 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| T1 | 01 | 1 | FOUND-02, DIFF-01 | route/unit | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/models-route.test.ts` | ✅ | ⬜ pending |
| T2 | 01 | 2 | FOUND-02, DIFF-01 | auth + SDK + validation regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-auth-compliance.test.ts apps/api/test/openai-sdk-regression.test.ts apps/api/test/routes/typebox-validation.test.ts` | ✅ | ⬜ pending |
| T3 | 01 | 3 | FOUND-02, DIFF-01 | full suite + Docker build | `cd /home/sakib/hive && pnpm --filter @hive/api test && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Existing infrastructure covers all phase requirements.

No Wave 0 setup needed — vitest and test helpers already exist.

---

## Manual-Only Verifications

All phase behaviors should be automatable in route and SDK tests.

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 20s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
