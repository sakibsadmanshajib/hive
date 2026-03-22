---
phase: 11
slug: real-openai-sdk-regression-tests-ci-style-e2e
status: audited
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-22
audited: 2026-03-22
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
| T1 | 01 | 1 | CI-04 | type surface + compile | `cd /home/sakib/hive && pnpm --filter @hive/api exec tsc --noEmit` | ✅ | ✅ green |
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

All phase behaviors are automatable with the compile, regression, and full-suite commands above.

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 20s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** audited 2026-03-22 — compile, regression suite, and full API suite green

## Validation Audit 2026-03-22

| Metric | Count |
|--------|-------|
| Gaps found | 1 |
| Resolved | 1 |
| Escalated | 0 |
| Commands run | 3 |

All requirements COVERED. No remaining manual-only gaps. `nyquist_compliant: true`.
