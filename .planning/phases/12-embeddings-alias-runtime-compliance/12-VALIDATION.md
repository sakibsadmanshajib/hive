---
phase: 12
slug: embeddings-alias-runtime-compliance
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-22
---

# Phase 12 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest |
| **Config file** | `apps/api/package.json` → `pnpm --filter @hive/api test` |
| **Quick run command** | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run src/config/__tests__/model-aliases.test.ts test/domain/model-service.test.ts test/providers/provider-registry.test.ts src/routes/__tests__/embeddings-compliance.test.ts src/routes/__tests__/differentiators-headers.test.ts test/openai-sdk-regression.test.ts` |
| **Full suite command** | `cd /home/sakib/hive && pnpm --filter @hive/api test` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run the task-specific command from the map below.
- **After every plan wave:** Run `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run src/config/__tests__/model-aliases.test.ts test/domain/model-service.test.ts test/providers/provider-registry.test.ts src/routes/__tests__/embeddings-compliance.test.ts src/routes/__tests__/differentiators-headers.test.ts test/openai-sdk-regression.test.ts`
- **Before closing Plan 12-01:** Run `cd /home/sakib/hive && pnpm --filter @hive/api test` and `cd /home/sakib/hive && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`
- **Before `$gsd-verify-work` / closing Phase 12:** Run `cd /home/sakib/hive && pnpm --filter @hive/api test` and `cd /home/sakib/hive && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`
- **Max feedback latency:** ~30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 12-01-T1 | 01 | 1 | DIFF-03 | alias + catalog regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run src/config/__tests__/model-aliases.test.ts test/domain/model-service.test.ts src/routes/__tests__/embeddings-compliance.test.ts src/routes/__tests__/differentiators-headers.test.ts` | ✅ | ⬜ pending |
| 12-01-T2 | 01 | 2 | DIFF-03 | provider-registry embeddings boundary regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/providers/provider-registry.test.ts` | ✅ | ⬜ pending |
| 12-01-T3 | 01 | 3 | DIFF-03 | focused alias/runtime regression pack | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run src/config/__tests__/model-aliases.test.ts test/domain/model-service.test.ts test/providers/provider-registry.test.ts src/routes/__tests__/embeddings-compliance.test.ts src/routes/__tests__/differentiators-headers.test.ts` | ✅ | ⬜ pending |
| 12-02-T1 | 02 | 1 | DIFF-03 | helper/runtime surface regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/routes/models-route.test.ts test/routes/v1-auth-compliance.test.ts test/routes/v1-stubs.test.ts` | ✅ | ⬜ pending |
| 12-02-T2 | 02 | 2 | DIFF-03 | real-runtime SDK embeddings regression | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts -t "real runtime catalog path"` | ✅ | ⬜ pending |
| 12-02-T3 | 02 | 3 | DIFF-03 | focused SDK regression pack | `cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run test/openai-sdk-regression.test.ts` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Existing infrastructure covers all phase requirements.

No Wave 0 setup needed — vitest, the route/provider regression files, and the SDK regression harness already exist.

---

## Manual-Only Verifications

All phase behaviors are automatable. The Docker-only API build remains an explicit plan/phase completion gate rather than a task-level quick check.

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
