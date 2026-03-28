---
phase: 9
slug: operational-hardening
status: complete
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-19
audited: 2026-03-21
---

# Phase 9 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | vitest |
| **Config file** | none (vitest defaults) |
| **Quick run command** | `pnpm --filter api test -- "v1-stubs"` |
| **Full suite command** | `pnpm --filter api test` |
| **Estimated runtime** | ~2 seconds (v1-stubs only) |

---

## Sampling Rate

- **After every task commit:** Run `pnpm --filter api test -- "v1-stubs"`
- **After every plan wave:** Run `pnpm --filter api test`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 9-01-01 | 01 | 1 | OPS-01 | integration | `pnpm --filter api test -- "v1-stubs"` | ✅ | ✅ green |
| 9-01-02 | 01 | 1 | OPS-01 | integration | `pnpm --filter api test -- "v1-stubs"` | ✅ | ✅ green |
| 9-02-01 | 02 | 2 | OPS-02 | manual | see Manual-Only below | n/a | ✅ complete |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [x] `apps/api/test/routes/v1-stubs.test.ts` — 10 integration tests covering all 7 stub endpoint groups (audio, files, uploads, batches, completions, fine_tuning, moderations)

*Note: File was initially created at `apps/api/src/routes/__tests__/v1-stubs.test.ts` (commit a3c534f) then moved to `apps/api/test/routes/v1-stubs.test.ts` (commit 4477ddf) as part of type-safety fixes.*

*All stubs share identical response shape — a single parametric test file covers all groups.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Status | Test Instructions |
|----------|-------------|------------|--------|-------------------|
| GitHub issues exist for each deferred endpoint group | OPS-02 | Requires GitHub API/browser verification | ✅ complete (issues #81–#87) | Open GitHub repo issues list; confirm 7 issues exist with titles matching `feat: implement /v1/{group}`, each with acceptance criteria |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 15s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** 2026-03-21

---

## Validation Audit 2026-03-21

| Metric | Count |
|--------|-------|
| Gaps found | 0 |
| Resolved | 0 |
| Escalated | 0 |
| Tests verified green | 10 |
| Manual-only | 1 |

**Notes:** VALIDATION.md was in draft state with incorrect test file path and pending statuses. Audit confirmed `test/routes/v1-stubs.test.ts` exists (moved from `src/routes/__tests__/` in commit 4477ddf) with all 10 tests passing. OPS-02 verified complete via SUMMARY (GitHub issues #81–#87 created). No new tests needed — coverage was already complete.
