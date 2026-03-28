---
phase: 13
slug: error-path-diff-headers
status: audited
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-22
audited: 2026-03-22
---

# Phase 13 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest |
| **Config file** | `apps/api/package.json` → `pnpm --filter @hive/api test` |
| **Quick run command** | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/typebox-validation.test.ts test/routes/v1-error-diff-headers.test.ts test/routes/v1-stubs.test.ts` |
| **Full suite command** | `cd /home/sakib/hive && pnpm --filter @hive/api test` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run the task-specific command from the map below.
- **After every plan wave:** Run `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/typebox-validation.test.ts test/routes/v1-error-diff-headers.test.ts test/routes/v1-stubs.test.ts`
- **Before closing Plan 13-01:** Run `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/v1-error-diff-headers.test.ts test/routes/v1-stubs.test.ts`
- **Before closing Plan 13-02 / Phase 13:** Run `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/typebox-validation.test.ts test/routes/v1-error-diff-headers.test.ts test/routes/v1-stubs.test.ts`, `cd /home/sakib/hive && pnpm --filter @hive/api test`, and `cd /home/sakib/hive && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`
- **Max feedback latency:** ~30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 13-01-T1 | 01 | 1 | DIFF-01 | route regression guard | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/chat-completions-route.test.ts test/routes/images-generations-route.test.ts test/routes/responses-route.test.ts` | ✅ | ✅ green |
| 13-01-T2 | 01 | 2 | DIFF-01 | live route + stub regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/v1-error-diff-headers.test.ts test/routes/v1-stubs.test.ts` | ✅ | ✅ green |
| 13-01-T3 | 01 | 3 | DIFF-01 | full suite + Docker build | `cd /home/sakib/hive && pnpm --filter @hive/api test && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` | ✅ | ✅ green |
| 13-02-T1 | 02 | 1 | DIFF-01 | plugin validation regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/typebox-validation.test.ts` | ✅ | ✅ green |
| 13-02-T2 | 02 | 2 | DIFF-01 | plugin + route regression pack | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/typebox-validation.test.ts test/routes/v1-error-diff-headers.test.ts` | ✅ | ✅ green |
| 13-02-T3 | 02 | 3 | DIFF-01 | focused pack + full suite + Docker build | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/typebox-validation.test.ts test/routes/v1-error-diff-headers.test.ts test/routes/v1-stubs.test.ts && pnpm --filter @hive/api test && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` | ✅ | ✅ green |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Existing infrastructure covers all phase requirements.

No Wave 0 setup needed — vitest, the shared `v1Plugin` harness, and the route/stub regression suites already exist.

---

## Manual-Only Verifications

None. The requirement surface for `DIFF-01` is covered by automated route, stub, and plugin validation regressions plus the full API suite and Docker-only API build. The direct plugin not-found probe in `13-VERIFICATION.md` is retained as supplemental verifier evidence, not as a remaining Nyquist gap.

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** audited 2026-03-22 — route guard, route/stub DIFF-header, plugin validation, full API, and Docker build commands green

## Validation Audit 2026-03-22

| Metric | Count |
|--------|-------|
| Gaps found | 0 |
| Resolved | 0 |
| Escalated | 0 |
| Commands run | 4 |

All requirements are COVERED. The prior validation artifact was stale, not incomplete: this audit corrected the task map to include both Phase 13 execution plans, fixed package-local `vitest` paths, and confirmed the current DIFF-01 coverage with fresh focused regressions.
