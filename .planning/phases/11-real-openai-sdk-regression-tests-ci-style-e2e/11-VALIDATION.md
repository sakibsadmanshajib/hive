---
phase: 11
slug: real-openai-sdk-regression-tests-ci-style-e2e
status: draft
nyquist_compliant: false
wave_0_complete: true
created: 2026-03-22
---

# Phase 11 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest |
| **Config file** | `apps/api/package.json` → `pnpm --filter @hive/api test` |
| **Quick run command** | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts` |
| **Full suite command** | `cd /home/sakib/hive && pnpm --filter @hive/api test` |
| **Estimated runtime** | ~5 seconds |

---

## Sampling Rate

- **After every task commit:** Run `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts`
- **After every plan wave:** Run `cd /home/sakib/hive && pnpm --filter @hive/api test`
- **Before `$gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 20 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| T1 | 01 | 1 | CI-04 | type surface + compile | `cd /home/sakib/hive && pnpm --filter @hive/api exec tsc --noEmit` | ✅ | ⚠️ partial |
| T2 | 01 | 1 | CI-01 | SDK success/404 regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts` | ✅ | ✅ green |
| T3 | 01 | 1 | CI-02 | SDK streaming regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts` | ✅ | ✅ green |
| T4 | 01 | 1 | CI-03 | SDK error-path regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts` | ✅ | ✅ green |
| T5 | 01 | 1 | CI-05 | full API suite | `cd /home/sakib/hive && pnpm --filter @hive/api test` | ✅ | ✅ green |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ partial/flaky*

---

## Wave 0 Requirements

Existing infrastructure covers all phase requirements.

No Wave 0 setup needed — Vitest and the API test harness already exist.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Remove the remaining route-surface bridge cast in `apps/api/test/helpers/test-app.ts` | CI-04 | The helper now uses repo-native request/result types and no longer uses `unknown`-based shapes, but one bridge cast remains when registering the `/v1` plugin because `RuntimeServices` is a broader concrete runtime contract than the narrow mock subset needed for this regression harness. Eliminating it safely requires a route-facing service interface or narrowed plugin typing outside this phase's owned surface. | Review `apps/api/test/helpers/test-app.ts` for `mockServices as RuntimeServices` and decide in a future typing-focused phase whether to export a route-facing service contract or narrow `v1Plugin`'s service requirements. |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 20s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
