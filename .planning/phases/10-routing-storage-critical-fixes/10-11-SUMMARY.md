---
phase: 10-routing-storage-critical-fixes
plan: 11
subsystem: api
tags: [go, docker, runtime, batches, accounting, smoke]

# Dependency graph
requires:
  - phase: 10-routing-storage-critical-fixes
    provides: Strict media and batch reservation contracts from 10-09 plus durable batch attribution from 10-10
provides:
  - Terminal batch reservation settlement through accounting finalize and release paths
  - Honest smoke checks for media and batch accounting-contract regressions
  - Runtime Dockerfiles that build the go.work storage module in live compose images
affects: [batchstore, accounting, filestore, edge-api, control-plane, docker, phase-10-gap-closure]

# Tech tracking
tech-stack:
  added: []
  patterns: [terminal reservation settlement, explicit zero-work release reasons, workspace-aware runtime docker builds, honest smoke failure surfacing]

key-files:
  created:
    - .planning/phases/10-routing-storage-critical-fixes/10-11-SUMMARY.md
  modified:
    - apps/control-plane/internal/batchstore/worker.go
    - apps/control-plane/internal/batchstore/worker_test.go
    - apps/control-plane/cmd/server/main.go
    - scripts/phase10-smoke.sh
    - deploy/docker/Dockerfile.edge-api
    - deploy/docker/Dockerfile.control-plane
    - .planning/REQUIREMENTS.md

key-decisions:
  - "Terminal batch settlement reads the persisted batch record and uses its attribution fields as the accounting source of truth."
  - "Zero-work terminal batches release reservations with explicit reasons instead of forcing zero-credit finalization."
  - "The smoke script preserves request failures before positional-argument unpacking so runtime errors stay readable under set -u."
  - "Runtime Dockerfiles must copy packages/storage because go.work now declares it as a first-class workspace module."

patterns-established:
  - "Batch worker terminal states settle reservations before batch status persistence so spend and release outcomes are written atomically with terminal metadata."
  - "Live smoke evidence is recorded even when the environment blocks full success; external quota and config issues are separated from code regressions."

requirements-completed: [API-05, API-06, API-07, KEY-04]

# Metrics
duration: 18 min
completed: 2026-04-21
---

# Phase 10 Plan 11: Terminal Batch Settlement And Honest Smoke Summary

**Terminal batch reservations now settle through accounting, KEY-04 is complete, and the final smoke/runtime checks surface real environment blockers instead of masking them.**

## Performance

- **Duration:** 18 min
- **Started:** 2026-04-20T21:22:21-04:00
- **Completed:** 2026-04-20T21:39:49-04:00
- **Tasks:** 2
- **Files modified:** 8

## Accomplishments

- Added terminal batch settlement to the control-plane worker, including finalization for partial and completed work plus explicit release reasons for zero-work failure, cancel, and expiry cases.
- Wired the control-plane batch worker to the real accounting service at startup so live worker executions can settle reservations instead of leaving them stranded.
- Tightened `scripts/phase10-smoke.sh` so media and batch probes fail on the specific accounting-contract regressions fixed in 10-09 and so request failures do not degrade into `parameter not set`.
- Fixed the runtime Dockerfiles to include the `packages/storage` workspace module declared in `go.work`, allowing live compose images to build the current phase-10 storage code.
- Marked `KEY-04` complete after the terminal settlement and accounting rollup tests passed.

## Task Commits

Each task was committed atomically:

1. **Task 1: Settle terminal batch reservations through accounting** - `cac9789`, `2db0304` (test, feat)
2. **Task 2: Tighten smoke/runtime contracts and close KEY-04** - `67115e4` (fix)

**Plan metadata:** final docs commit created after state updates.

## Files Created/Modified

- `apps/control-plane/internal/batchstore/worker.go` - Finalizes or releases reservations for terminal batches and records `actual_credits`.
- `apps/control-plane/internal/batchstore/worker_test.go` - Covers completed, partial-cancelled, zero-work release, and missing-reservation terminal cases.
- `apps/control-plane/cmd/server/main.go` - Injects the accounting service into the batch worker.
- `scripts/phase10-smoke.sh` - Adds forbidden-text checks for media and batch responses and preserves curl failures under `set -u`.
- `deploy/docker/Dockerfile.edge-api` - Copies `packages/storage` manifest and source so the workspace build resolves in the live image.
- `deploy/docker/Dockerfile.control-plane` - Copies `packages/storage` manifest and source so the workspace build resolves in the live image.
- `.planning/REQUIREMENTS.md` - Marks `KEY-04` complete.

## Test Evidence

- Task 1 red and green target:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/batchstore ./apps/control-plane/cmd/server ./apps/control-plane/internal/accounting -run "TestBatchWorkerStoresCompletedOutputFiles|TestBatchWorkerFinalizesCancelledPartialBatches|TestBatchWorkerReleasesFailedCancelledAndExpiredReservations|TestBatchWorkerRequiresReservationForTerminalSettlement|TestFinalizeReservationUpdatesBudgetWindowAndUsageRollup" -count=1'`
  - Red result: exited 1 before implementation because the worker did not settle terminal reservations and the new tests could not find finalize/release calls.
  - Green result: exited 0 after settlement wiring landed.

- Task 2 package and script checks:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/batchstore ./apps/control-plane/cmd/server ./apps/control-plane/internal/accounting -run "TestBatchWorkerStoresCompletedOutputFiles|TestBatchWorkerFinalizesCancelledPartialBatches|TestBatchWorkerReleasesFailedCancelledAndExpiredReservations|TestBatchWorkerRequiresReservationForTerminalSettlement|TestFinalizeReservationUpdatesBudgetWindowAndUsageRollup" -count=1'`
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/... ./apps/edge-api/... ./packages/storage/... -count=1'`
  `sh -n scripts/phase10-smoke.sh`
  `sh scripts/phase10-scrub-legacy-storage.sh --check`
  - Result: all four commands exited 0 after the smoke-script and Dockerfile changes.

- Runtime build regression and fix:
  `docker compose -f deploy/docker/docker-compose.yml build control-plane edge-api`
  - Red result: exited 1 before the Dockerfile patch because `go.work` referenced `./packages/storage` but the runtime images did not copy that module manifest.
  - Green result: exited 0 after both Dockerfiles copied `packages/storage/go.mod`, `packages/storage/go.sum`, and the package source tree.

- Database/runtime repair for live smoke:
  `docker run --rm -e SUPABASE_DB_URL="$SUPABASE_DB_URL" -v "$PWD:/workspace" postgres:16-alpine sh -lc 'psql "$SUPABASE_DB_URL" -v ON_ERROR_STOP=1 -f /workspace/supabase/migrations/20260414_01_provider_capabilities_media_columns.sql -f /workspace/supabase/migrations/20260414_02_filestore_tables.sql -f /workspace/supabase/migrations/20260420_01_batch_accounting_attribution.sql'`
  - Result: provider-capability columns were added, existing filestore tables were confirmed with notices, and batch attribution columns/indexes were applied.

- Live runtime probe after rebuild and migration:
  `curl -sS -X POST http://localhost:8081/internal/routing/select -H 'Content-Type: application/json' -d '{"alias_id":"hive-auto","need_image_generation":true}'`
  - Result: returned HTTP 200 with route `route-openrouter-auto`.

- Live media probe after rebuild and migration:
  `docker compose -f deploy/docker/docker-compose.yml run --rm -e HIVE_API_KEY="$HIVE_API_KEY" --entrypoint /bin/sh toolchain -lc 'out=/tmp/image.json; code=$(curl -sS -o "$out" -w "%{http_code}" -X POST http://edge-api:8080/v1/images/generations -H "Authorization: Bearer $HIVE_API_KEY" -H "Content-Type: application/json" -d "{\"model\":\"hive-auto\",\"prompt\":\"test image\",\"n\":1,\"size\":\"1024x1024\"}"); echo "$code"; cat "$out"'`
  - Result: returned HTTP 402 with an OpenAI-style `insufficient_quota` error and did not contain `policy_mode must be strict or temporary_overage`, `model_alias is required`, or `Failed to reserve credits for batch`.

- Live smoke execution:
  `docker compose -f deploy/docker/docker-compose.yml run --rm -e HIVE_API_KEY="$HIVE_API_KEY" -e HIVE_BASE_URL=http://edge-api:8080 -e HIVE_CONTROL_PLANE_URL=http://control-plane:8081 --entrypoint /bin/sh toolchain -lc 'cd /workspace && sh scripts/phase10-smoke.sh'`
  - Result: executed against the rebuilt compose network, passed the control-plane route probes, and then stopped at chat with HTTP 403 from the configured upstream provider because the current OpenRouter key had exceeded its total limit.

## Decisions Made

- Capped terminal actual credits to the estimated reservation amount so settlement cannot overcharge beyond the reserved budget.
- Treated completed batches with missing request counts as estimated-credit finalizations, but treated failed/cancelled/expired zero-work batches as release paths.
- Fixed runtime Docker build inputs instead of weakening `go.work`, because the workspace module layout is already the correct source-of-truth for local and CI builds.
- Recorded the live smoke failure as an environment/provider issue rather than changing the phase-10 chat success contract to hide exhausted upstream quota.

## Deviations from Plan

- The live smoke ran inside the compose network with `HIVE_BASE_URL=http://edge-api:8080` and `HIVE_CONTROL_PLANE_URL=http://control-plane:8081` because host port `8080` was already allocated in the workspace.
- Before the live smoke could run meaningfully, I had to rebuild stale compose images, apply the missing Supabase migrations to the configured database, and correct `S3_ENDPOINT` in the launching shell to include `https://`.

## Issues Encountered

- The initial live runtime used stale `control-plane` and `edge-api` images because `docker compose up -d` reused previously built images.
- The configured database was missing the `provider_capabilities` media columns until the phase-10 migrations were applied.
- `.env` provided `S3_ENDPOINT` without a URL scheme, which prevented the rebuilt services from constructing the phase-10 storage client until the shell export corrected it.
- The configured upstream OpenRouter key was out of quota, so chat smoke could not complete successfully even after routing, storage, and media/batch contract issues were fixed.
- Unrelated local changes in `.env.example`, `.gitignore`, and `.claude/` were present before execution and were intentionally left untouched.

## User Setup Required

- Update `.env` so `S3_ENDPOINT` is a full URL with scheme, for example `https://.../storage/v1/s3`, before starting the compose stack normally.
- Use a provider key with available quota if you want the chat portion of `scripts/phase10-smoke.sh` to complete with HTTP 200.

## Next Phase Readiness

Plan 10-11 is implemented and committed. Phase 10 execution is complete, `KEY-04` is closed, and the phase is ready for verification/state transition with one known external runtime blocker: the current upstream provider key does not allow the chat smoke to finish successfully.

## Self-Check: PASSED

- Found `10-11-SUMMARY.md` on disk with the required `requirements-completed` frontmatter and `## Test Evidence` heading.
- Found task commits `cac9789`, `2db0304`, and `67115e4` in git history.
- Confirmed the focused settlement tests, full Go suite, script checks, and runtime Docker builds all exited 0 after implementation.
- Confirmed live routing and media probes reached the rebuilt runtime without the fixed media/batch accounting-contract errors.

---
*Phase: 10-routing-storage-critical-fixes*
*Completed: 2026-04-21*
