---
phase: 12
slug: embeddings-alias-runtime-compliance
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-22
---

# Phase 12 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest |
| **Config file** | `apps/api/package.json` → `"test": "vitest run --passWithNoTests"` |
| **Quick run command** | `pnpm --filter @hive/api exec vitest run apps/api/src/config/__tests__/model-aliases.test.ts apps/api/test/openai-sdk-regression.test.ts apps/api/src/routes/__tests__/embeddings-compliance.test.ts` |
| **Full suite command** | `pnpm --filter @hive/api test` |
| **Estimated runtime** | ~20 seconds |

---

## Sampling Rate

- **After every task commit:** Run `pnpm --filter @hive/api exec vitest run apps/api/src/config/__tests__/model-aliases.test.ts apps/api/test/openai-sdk-regression.test.ts apps/api/src/routes/__tests__/embeddings-compliance.test.ts`
- **After every plan wave:** Run `pnpm --filter @hive/api test`
- **Before `$gsd-verify-work`:** Full suite must be green and `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` must pass
- **Max feedback latency:** ~20 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 12-01-01 | 01 | 1 | DIFF-03 | unit + SDK regression | `pnpm --filter @hive/api exec vitest run apps/api/src/config/__tests__/model-aliases.test.ts apps/api/test/openai-sdk-regression.test.ts apps/api/src/routes/__tests__/embeddings-compliance.test.ts` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

*Note: Update this table after planning so every concrete task ID maps to the exact targeted verification command.*

---

## Wave 0 Requirements

- [ ] `apps/api/src/config/__tests__/model-aliases.test.ts` — update alias assertions so `text-embedding-3-small` is canonical and `text-embedding-ada-002` resolves to that public id
- [ ] `apps/api/test/openai-sdk-regression.test.ts` — add real-runtime SDK regression coverage for `client.embeddings.create({ model: "text-embedding-3-small" })`
- [ ] `apps/api/src/routes/__tests__/embeddings-compliance.test.ts` or a new runtime/provider test file — verify response `model`, `x-model-routed`, and `x-provider-model` stay on the public-vs-provider boundary

*Existing vitest infrastructure covers the framework; Wave 0 is test-file coverage, not tooling install.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Docker API container builds Phase 12 changes in the supported environment | DIFF-03 | Repo policy requires Docker-only API builds; vitest alone will not catch build regressions | Run `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` after the phase implementation commit |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 20s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
