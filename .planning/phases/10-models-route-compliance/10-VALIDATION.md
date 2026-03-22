---
phase: 10
slug: models-route-compliance
status: audited
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-21
audited: 2026-03-22
---

# Phase 10 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest |
| **Config file** | `apps/api/package.json` → `pnpm --filter @hive/api test` |
| **Quick run command** | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/models-route.test.ts` |
| **Full suite command** | `cd /home/sakib/hive && pnpm --filter @hive/api test` |
| **Estimated runtime** | ~20 seconds |

---

## Sampling Rate

- **After every task commit:** Run the task-specific command from the map below.
- **After every plan wave:** Run `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/v1-auth-compliance.test.ts test/openai-sdk-regression.test.ts test/routes/typebox-validation.test.ts`
- **Before `$gsd-verify-work`:** Run `cd /home/sakib/hive && pnpm --filter @hive/api test`
- **Before closing Phase 10:** Run `cd /home/sakib/hive && pnpm --filter @hive/api test` and `cd /home/sakib/hive && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`
- **Max feedback latency:** 20 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| T1 | 01 | 1 | FOUND-02, DIFF-01 | route/unit | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/models-route.test.ts` | ✅ | ✅ green |
| T2 | 01 | 2 | FOUND-02, DIFF-01 | auth + SDK + validation regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/v1-auth-compliance.test.ts test/openai-sdk-regression.test.ts test/routes/typebox-validation.test.ts` | ✅ | ✅ green |
| T3 | 01 | 3 | FOUND-02, DIFF-01 | full suite + Docker build | `cd /home/sakib/hive && pnpm --filter @hive/api test && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` | ✅ | ✅ green |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ partial/flaky*

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

**Approval:** audited 2026-03-22 — focused route, auth/SDK/TypeBox, full API suite, and Docker-only API build commands green

## Validation Audit 2026-03-22

| Metric | Count |
|--------|-------|
| Gaps found | 0 |
| Resolved | 0 |
| Escalated | 0 |
| Commands run | 4 |

All requirements are COVERED. The prior validation artifact was stale, not incomplete: this audit corrected the package-local `vitest` paths, marked the task map green from fresh verification, and confirmed the phase-level Docker build gate.
