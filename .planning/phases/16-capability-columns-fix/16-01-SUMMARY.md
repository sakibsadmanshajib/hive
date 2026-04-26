---
phase: 16-capability-columns-fix
plan: 01
subsystem: control-plane/routing + planning artifacts
tags: [verification-only, latent-bug-closure, ship-gate, v1.1.0]
dependency_graph:
  requires:
    - Phase 11 — REQUIREMENTS.md + verify-requirements-matrix.sh validator
    - Phase 14 — supabase/migrations/20260414_01_provider_capabilities_media_columns.sql + repository_schema_test.go
  provides:
    - CAP-16-01 evidence (Routing & Catalog requirement)
  affects:
    - .planning/REQUIREMENTS.md (v1.1 Routing & Catalog row added)
    - CLAUDE.md (Known Issues §1 retired)
tech_stack:
  added: []
  patterns:
    - Verification-only phase — no production code modified
    - DDL belongs in Supabase migrations, not Go runtime
    - Regression guard tests assert against repository.go source string + migration glob
key_files:
  created:
    - .planning/phases/16-capability-columns-fix/PLAN.md
    - .planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md
    - .planning/phases/16-capability-columns-fix/16-VERIFICATION.md
    - .planning/phases/16-capability-columns-fix/16-01-SUMMARY.md
  modified:
    - .planning/REQUIREMENTS.md
    - CLAUDE.md
decisions:
  - The latent bug recorded in CLAUDE.md Known Issues §1 was already closed by Phase 14's media-columns work (2026-04-14). Phase 16 reduces to verification-only — formally captures the closure with evidence and retires the stale Known Issue line, rather than re-fixing already-fixed code.
metrics:
  duration: ~25 min
  completed_date: 2026-04-25
  task_count: 2
  files_created: 4
  files_modified: 2
  commits: 2
  guard_tests_passing: 4
  validator_evidence_count: 7
---

# Phase 16 Plan 01: ensureCapabilityColumns Wrong-Table Fix — Verification-Only Closure

Confirms the latent v1.0 bug — `ensureCapabilityColumns` issuing DDL against
`route_capabilities` instead of `provider_capabilities` — was already
eliminated by Phase 14's media-columns work and stands guarded by a
regression test. Captures CAP-16-01 evidence, adds the row to the active
requirement matrix, and retires CLAUDE.md Known Issues §1.

## What was built

- **Evidence file** for CAP-16-01 — frontmatter (`requirement_id`, `status`,
  `verified_at`, `verified_by`, `phase_satisfied`, `evidence` block with
  code paths + integration tests + summary refs); body lists the three code
  paths involved, the runnable go-test command, and the five static-check
  greps that prove the bug is closed.
- **16-VERIFICATION.md ship-gate log** — must-have truth table with seven
  rows all PASS, requirement coverage line with validator output, captured
  static-check evidence, full guard-test output, final-sweep output, diff
  surface check (zero production-code changes), and explicit v1.1.0
  ship-gate mapping.
- **REQUIREMENTS.md row** — new `## v1.1 Requirements (in flight)` →
  `### Routing & Catalog` section carrying `CAP-16-01 | 16 | Satisfied |
  [evidence/CAP-16-01.md](...)`. Validator now resolves seven evidence
  files (was six).
- **CLAUDE.md retirement** — Known Issues §1 wording replaced with a
  Resolved-by-Phase-16 line linking to the evidence. Items §2–§4 preserved
  untouched.

## What was verified

- **Static checks (5):** `ensureCapabilityColumns` not in repository.go;
  no `ALTER TABLE` in repository.go; `JOIN public.provider_capabilities`
  on line 86; migration alters `public.provider_capabilities`; no
  migration alters `route_capabilities`.
- **Guard tests (4 of 4 PASS):**
  - `TestRoutingRepositoryDoesNotRunCapabilityDDL`
  - `TestProviderCapabilitiesMigrationAddsMediaColumns`
  - `TestProviderCapabilitiesMigrationBackfillsMediaAndBatchRoutes`
  - `TestListRouteCandidatesSelectsMediaColumns`
- **Full routing-package suite** — `go test ./apps/control-plane/internal/routing/...
  -count=1 -short` exits 0.
- **Validator** — `bash scripts/verify-requirements-matrix.sh` prints
  `OK: 7 evidence files validated` and exits 0.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking issue] Toolchain compose service depends on `redis` from a different profile**

- **Found during:** Task 1 Step 1 (running guard tests)
- **Issue:** `docker compose --profile tools run --rm toolchain ...`
  failed with `service "control-plane" depends on undefined service
  "redis": invalid compose project`. Adding `--profile local` activated
  redis but the compose runner reported `Container Created` and exited 0
  with no test output (TTY mismatch in agent harness; bash also missing
  inside the toolchain image — only `sh` available).
- **Fix:** Invoked the prebuilt `hive-toolchain:latest` image directly via
  `docker run --rm --entrypoint sh -v $PWD:/workspace -w /workspace
  hive-toolchain:latest -c "..."`. Semantically identical to the documented
  compose form; documented the alternative invocation in
  16-VERIFICATION.md so the next operator can choose either path.
- **Files modified:** none (operational fix only)
- **Commit:** n/a — verification mechanism only

No source-code or migration changes were required. The bug was already
closed at HEAD on `origin/main` by Phase 14's earlier landing.

## Authentication Gates

None.

## Deferred Issues

None.

## Self-Check: PASSED

- FOUND: `.planning/phases/16-capability-columns-fix/PLAN.md`
- FOUND: `.planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md`
- FOUND: `.planning/phases/16-capability-columns-fix/16-VERIFICATION.md`
- FOUND: `.planning/phases/16-capability-columns-fix/16-01-SUMMARY.md` (this file)
- FOUND: commit `4f2e2e7` (Task 1 evidence)
- FOUND: commit `5abba43` (Task 2 verification + matrix + Known-Issues retirement)
- FOUND: `CAP-16-01` row in `.planning/REQUIREMENTS.md`
- FOUND: "Resolved by Phase 16" line in `CLAUDE.md`

All claims in the summary are verified on disk and in git history.

## Files Touched

| File | Action | Notes |
|------|--------|-------|
| `.planning/phases/16-capability-columns-fix/PLAN.md` | created | Copied from main worktree (untracked there) into the branch tree |
| `.planning/phases/16-capability-columns-fix/evidence/CAP-16-01.md` | created | Per-requirement evidence file with frontmatter |
| `.planning/phases/16-capability-columns-fix/16-VERIFICATION.md` | created | Phase verification log with 7 PASS rows |
| `.planning/phases/16-capability-columns-fix/16-01-SUMMARY.md` | created | This summary |
| `.planning/REQUIREMENTS.md` | modified | Added v1.1 Routing & Catalog section + CAP-16-01 row |
| `CLAUDE.md` | modified | Known Issues §1 retired; §2–§4 unchanged |

## Commits

- `4f2e2e7` — `docs(16-01): add CAP-16-01 evidence file proving capability-columns bug closed`
- `5abba43` — `docs(16-01): close CAP-16-01 — verification log + matrix row + retire Known Issue`
