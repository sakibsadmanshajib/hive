---
phase: 10-routing-storage-critical-fixes
plan: 07
subsystem: storage
tags: [docs, supabase-storage, s3, cleanup, planning]

# Dependency graph
requires:
  - phase: 10-routing-storage-critical-fixes
    provides: Edge and control-plane storage wiring from plans 10-05 and 10-06
provides:
  - Supabase Storage S3 environment contract in .env.example and CLAUDE.md
  - Phase 10 roadmap ownership for plans 10-07 and 10-08
  - Generated-candidate purge script for legacy storage references
  - Repository-wide planning/doc cleanup proven by the purge check
affects: [storage, routing, docs, phase-10-verification]

# Tech tracking
tech-stack:
  added: []
  patterns: [generated-candidate cleanup script, docs-owned storage env contract]

key-files:
  created:
    - scripts/phase10-scrub-legacy-storage.sh
    - .planning/phases/10-routing-storage-critical-fixes/10-07-SUMMARY.md
  modified:
    - .env.example
    - CLAUDE.md
    - .planning/ROADMAP.md
    - .planning/STATE.md
    - .planning/UAT-REPORT.md
    - .planning/v1.0-MILESTONE-AUDIT.md
    - .planning/phases/10-routing-storage-critical-fixes/10-CONTEXT.md
    - .planning/phases/10-routing-storage-critical-fixes/10-RESEARCH.md
    - .planning/phases/10-routing-storage-critical-fixes/10-VALIDATION.md

key-decisions:
  - "Supabase Storage is documented as the only object storage backend, and both edge-api and control-plane require S3 env vars at startup."
  - "Phase 10 roadmap progress kept the current 6/8 execution state instead of reverting to the stale 0/8 baseline from the original plan text."
  - "Historical planning references are scrubbed mechanically with a generated rg candidate list instead of hand-selected file paths."

patterns-established:
  - "Repository-wide legacy storage cleanup uses scripts/phase10-scrub-legacy-storage.sh --check as the repeatable gate."
  - "Storage setup docs require pre-created hive-files and hive-images buckets before service startup."

requirements-completed: [ROUT-02, API-05, API-06, API-07]

# Metrics
duration: 6min
completed: 2026-04-20
---

# Phase 10 Plan 07: Env Docs and Legacy Storage Purge Summary

**Supabase Storage startup documentation plus repeatable generated-candidate purge of historical legacy storage references.**

## Performance

- **Duration:** 6 min
- **Started:** 2026-04-20T19:26:30Z
- **Completed:** 2026-04-20T19:31:55Z
- **Tasks:** 2
- **Files modified:** 21

## Accomplishments

- Documented the required Supabase Storage S3 env vars, including `S3_REGION=us-east-1`, `S3_BUCKET_FILES=hive-files`, and `S3_BUCKET_IMAGES=hive-images`.
- Updated project guidance so Supabase Storage is the only object storage backend and required buckets must be pre-created before `edge-api` or `control-plane` startup.
- Made Phase 10 eight-plan ownership explicit in the roadmap, with 10-07 owning env docs and purge and 10-08 owning final verification.
- Added `scripts/phase10-scrub-legacy-storage.sh` with generated candidate discovery, `--apply`, and `--check`.
- Applied the purge across historical planning and audit files and verified the generated candidate scan is clean outside `.git`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Document required Supabase Storage env and update roadmap ownership** - `741e19b` (docs)
2. **Task 2: Create and run generated-candidate purge script** - `ca676c3` (chore)

**Plan metadata:** final docs commit created after state updates.

## Files Created/Modified

- `scripts/phase10-scrub-legacy-storage.sh` - Generated-candidate purge script with apply and check modes.
- `.env.example` - Required Supabase Storage S3 endpoint, credentials, region, and bucket env contract.
- `CLAUDE.md` - Current storage convention and startup/bucket setup requirement.
- `.planning/ROADMAP.md` - Phase 10 eight-plan ownership and purge/final verification split.
- `.planning/STATE.md` - Historical decision text scrubbed by the generated candidate purge.
- `.planning/UAT-REPORT.md` - Historical storage failure wording scrubbed while preserving audit meaning.
- `.planning/v1.0-MILESTONE-AUDIT.md` - Integration gap wording scrubbed while preserving audit meaning.
- `.planning/phases/07-media-file-and-async-api-surface/*` - Historical Phase 7 storage planning references scrubbed.
- `.planning/phases/09-developer-console-operational-hardening/09-RESEARCH.md` - Historical storage reference scrubbed.
- `.planning/phases/10-routing-storage-critical-fixes/10-CONTEXT.md` - Phase 10 context storage wording scrubbed.
- `.planning/phases/10-routing-storage-critical-fixes/10-RESEARCH.md` - Phase 10 research storage wording scrubbed.
- `.planning/phases/10-routing-storage-critical-fixes/10-VALIDATION.md` - Purge verification wording scrubbed.

## Verification

- `docker compose --env-file .env -f deploy/docker/docker-compose.yml config --services`
  - Result: exited 0 and listed `control-plane` and `edge-api`.
  - Note: Compose warned that local `.env` lacks `S3_REGION`; `.env.example` now documents the required value.
- `sh -n scripts/phase10-scrub-legacy-storage.sh`
  - Result: exited 0.
- `sh scripts/phase10-scrub-legacy-storage.sh --check`
  - Result: exited 0.

## Decisions Made

- Kept the roadmap's actual Phase 10 progress at `6/8` during Task 1 because summaries 10-01 through 10-06 already exist; only the plan count ownership needed correction.
- The purge script assembles the forbidden token from pieces and uses `rg -uu -il` at runtime so the script does not become a candidate and future checks are not tied to a hardcoded file list.
- The purge rewrites historical planning text with neutral phrases such as `legacy local object-store emulator`, `legacy S3-compatible client`, `old storage client`, and `old object-storage dependency`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Preserved current roadmap progress instead of stale baseline**
- **Found during:** Task 1 (Document required Supabase Storage env and update roadmap ownership)
- **Issue:** The plan text expected an old `0/7` to `0/8` roadmap update, but the current roadmap already had six completed Phase 10 summaries.
- **Fix:** Updated the Phase 10 plan-count line to `8 plans (6/8 executed)` instead of regressing the roadmap to `0/8`.
- **Files modified:** `.planning/ROADMAP.md`
- **Verification:** Task 1 acceptance probes confirmed `**Plans:** 8 plans` and `10-08-PLAN.md`; final roadmap update is owned by GSD tooling after this summary.
- **Committed in:** `741e19b`

**2. [Rule 3 - Blocking] Fixed POSIX loop syntax before applying purge**
- **Found during:** Task 2 (Create and run generated-candidate purge script)
- **Issue:** Initial `sh -n` failed because an environment assignment was placed directly before a `while` loop.
- **Fix:** Moved the environment assignment to the Perl invocation inside the loop.
- **Files modified:** `scripts/phase10-scrub-legacy-storage.sh`
- **Verification:** `sh -n scripts/phase10-scrub-legacy-storage.sh` exited 0.
- **Committed in:** `ca676c3`

**3. [Rule 1 - Bug] Added uppercase env-var token cleanup**
- **Found during:** Task 2 (Create and run generated-candidate purge script)
- **Issue:** The first check run found one remaining uppercase historical env-var token in Phase 7 research.
- **Fix:** Added a generic uppercase prefix replacement before reapplying the generated purge.
- **Files modified:** `scripts/phase10-scrub-legacy-storage.sh`, `.planning/phases/07-media-file-and-async-api-surface/07-RESEARCH.md`
- **Verification:** `sh scripts/phase10-scrub-legacy-storage.sh --check` exited 0.
- **Committed in:** `ca676c3`

---

**Total deviations:** 3 auto-fixed (2 bugs, 1 blocking issue)
**Impact on plan:** The output scope stayed the same. Fixes preserved current GSD progress, made the script portable, and made the purge check actually pass.

## Issues Encountered

- The local `.env` file does not set `S3_REGION`, so Docker Compose prints a warning during config rendering. The command exits 0, and `.env.example` now documents the required value.
- The worktree still has unrelated `.gitignore`, `go.work.sum`, and `.claude/` entries. They were not staged or committed by this plan.

## User Setup Required

Runtime startup requires Supabase Storage S3 access enabled, valid S3 access keys, and pre-created `hive-files` and `hive-images` buckets.

## Next Phase Readiness

Plan 10-08 can run final route/media checks, full Go suite verification, live smoke tests, and the purge verification gate using `sh scripts/phase10-scrub-legacy-storage.sh --check`.

## Self-Check: PASSED

- Found `scripts/phase10-scrub-legacy-storage.sh`, this summary, `.env.example`, `CLAUDE.md`, and `.planning/ROADMAP.md` on disk.
- Found task commits `741e19b` and `ca676c3` in git history.
- `sh scripts/phase10-scrub-legacy-storage.sh --check` exited 0.

---
*Phase: 10-routing-storage-critical-fixes*
*Completed: 2026-04-20*
