---
phase: 10-routing-storage-critical-fixes
plan: 03
subsystem: database
tags: [go, routing, filestore, supabase, migrations]

# Dependency graph
requires:
  - phase: 10-routing-storage-critical-fixes
    provides: Wave 0 red schema ownership tests from 10-02
provides:
  - Supabase migration for provider_capabilities media and batch columns
  - Supabase migration for filestore files/uploads/upload_parts/batches tables and indexes
  - Routing and filestore repositories with constructors that assume migration-managed schema
affects: [10-04, 10-06, routing, filestore, storage]

# Tech tracking
tech-stack:
  added: []
  patterns: [migration-owned schema, source-and-migration contract tests]

key-files:
  created:
    - supabase/migrations/20260414_01_provider_capabilities_media_columns.sql
    - supabase/migrations/20260414_02_filestore_tables.sql
    - .planning/phases/10-routing-storage-critical-fixes/deferred-items.md
  modified:
    - apps/control-plane/internal/routing/repository.go
    - apps/control-plane/internal/filestore/repository.go
    - apps/control-plane/internal/filestore/repository_test.go

key-decisions:
  - "Routing and filestore constructors now trust Supabase migrations instead of mutating schema at runtime."
  - "route-openrouter-auto is explicitly backfilled for media and batch capability filters so the existing hive-auto route remains eligible."
  - "Filestore migration contract coverage was split from runtime-DDL source coverage so Task 1 can validate migrations before Task 2 removes constructors."

patterns-established:
  - "Schema changes for routing and filestore live in Supabase migrations, not Go constructors."
  - "Plan-local schema tests distinguish migration content from source-level runtime-DDL assertions."

requirements-completed: [ROUT-02, API-05, API-06, API-07]

# Metrics
duration: 5min
completed: 2026-04-20
---

# Phase 10 Plan 03: Routing and Filestore Schema Ownership Summary

**Supabase-owned routing media columns and filestore tables with Go constructors stripped of runtime DDL**

## Performance

- **Duration:** 5 min
- **Started:** 2026-04-20T03:28:15Z
- **Completed:** 2026-04-20T03:34:14Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- Added `public.provider_capabilities` media/batch capability columns and backfilled `route-openrouter-auto` for image, edit, TTS, STT, and batch eligibility.
- Added migration-managed `public.files`, `public.uploads`, `public.upload_parts`, and `public.batches` schema plus required indexes.
- Removed runtime `ensureCapabilityColumns` and `ensureSchema` helpers from routing and filestore repository constructors.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add routing and filestore Supabase migrations** - `12b74a6` (feat)
2. **Task 2: Delete runtime DDL from routing and filestore repositories** - `406f001` (fix)

## Verification

- Task 1 migration tests passed:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/routing ./apps/control-plane/internal/filestore -run "TestProviderCapabilitiesMigrationAddsMediaColumns|TestProviderCapabilitiesMigrationBackfillsMediaAndBatchRoutes|TestSelectRouteSucceedsForSeededMediaAndBatchCapabilities|TestFilestoreSchemaLivesInSupabaseMigration" -count=1'`
- Task 2 runtime-DDL tests passed:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/routing ./apps/control-plane/internal/filestore -run "TestRoutingRepositoryDoesNotRunCapabilityDDL|TestFilestoreRepositoryDoesNotRunSchemaDDL|TestFilestoreSchemaLivesInSupabaseMigration" -count=1'`
- Final schema-focused routing/filestore tests passed:
  `docker compose -f deploy/docker/docker-compose.yml run --rm --entrypoint /bin/sh toolchain -lc 'export PATH=/usr/local/go/bin:$PATH; cd /workspace && go test ./apps/control-plane/internal/routing ./apps/control-plane/internal/filestore -run "TestRoutingRepositoryDoesNotRunCapabilityDDL|TestProviderCapabilitiesMigrationAddsMediaColumns|TestProviderCapabilitiesMigrationBackfillsMediaAndBatchRoutes|TestListRouteCandidatesSelectsMediaColumns|TestSelectRouteSucceedsForSeededMediaAndBatchCapabilities|TestFilestoreSchemaLivesInSupabaseMigration|TestFilestoreRepositoryDoesNotRunSchemaDDL" -count=1'`
- The broad package command still exits non-zero on known Wave 0 red tests owned by Plan 10-06:
  `TestInternalFileResponseIncludesStoragePath`, `TestInternalUploadResponseIncludesMultipartFields`, `TestInternalBatchResponseIncludesOutputFieldsAndTimestamps`, and `TestUpdateBatchStatusPersistsAllowedFields`.

## Files Created/Modified

- `supabase/migrations/20260414_01_provider_capabilities_media_columns.sql` - Adds media/batch capability columns and backfills `route-openrouter-auto`.
- `supabase/migrations/20260414_02_filestore_tables.sql` - Creates filestore tables, foreign keys, unique upload-part constraint, and indexes.
- `apps/control-plane/internal/routing/repository.go` - Simplifies `NewPgxRepository` and removes runtime capability DDL.
- `apps/control-plane/internal/filestore/repository.go` - Simplifies `NewRepository` and removes runtime schema creation.
- `apps/control-plane/internal/filestore/repository_test.go` - Separates migration contract assertions from runtime-DDL source assertions.
- `.planning/phases/10-routing-storage-critical-fixes/deferred-items.md` - Logs out-of-scope Plan 10-06 red test failures discovered during broad verification.

## Decisions Made

- Constructors now assume the database has been migrated. Missing columns or tables should surface as operational migration drift, not be hidden by best-effort startup DDL.
- The media/batch backfill targets only the existing approved `route-openrouter-auto` route from the routing policy seed.
- Filestore migration tests now assert the plan-required `create table if not exists public.*` SQL shape.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Split filestore migration and runtime-DDL contract tests**
- **Found during:** Task 1 (Add routing and filestore Supabase migrations)
- **Issue:** `TestFilestoreSchemaLivesInSupabaseMigration` coupled migration assertions to Task 2 source assertions and expected `create table public.*`, while the plan requires `create table if not exists public.*`.
- **Fix:** Kept the migration test focused on migration SQL and added `TestFilestoreRepositoryDoesNotRunSchemaDDL` for the constructor/source assertion.
- **Files modified:** `apps/control-plane/internal/filestore/repository_test.go`
- **Verification:** Task 1 and Task 2 narrow Docker test commands both exited 0.
- **Committed in:** `12b74a6`

**2. [Rule 3 - Blocking] Used corrected Docker toolchain entrypoint for verification**
- **Found during:** Task verification
- **Issue:** The documented `toolchain sh -lc` form was already identified in 10-02 as being swallowed by the service entrypoint.
- **Fix:** Ran verification with `--entrypoint /bin/sh` and `PATH=/usr/local/go/bin:$PATH`.
- **Files modified:** None
- **Verification:** All narrow schema/routing/filestore commands executed real `go test` runs and exited 0.
- **Committed in:** Not applicable

---

**Total deviations:** 2 auto-fixed (2 blocking)
**Impact on plan:** No production scope was expanded. The only code behavior changes are the planned removal of runtime DDL and migration-owned schema.

## Issues Encountered

- The plan's broad final package command still fails on Plan 10-06 red tests for internal filestore response fields and batch status update persistence. These failures predate 10-03 and are logged in `deferred-items.md`.
- The worktree contains unrelated parallel changes in `.gitignore`, `go.work.sum`, and `packages/storage/*`; they were not modified or staged by this plan.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Plan 10-03 is ready for downstream storage and filestore work. Plan 10-06 still needs to implement the known red tests for internal filestore responses and `UpdateBatchStatus` persistence.

## Self-Check: PASSED

- Found both new Supabase migration files and `10-03-SUMMARY.md` on disk.
- Found deferred out-of-scope issue log at `.planning/phases/10-routing-storage-critical-fixes/deferred-items.md`.
- Found task commits `12b74a6` and `406f001` in git history.

---
*Phase: 10-routing-storage-critical-fixes*
*Completed: 2026-04-20*
