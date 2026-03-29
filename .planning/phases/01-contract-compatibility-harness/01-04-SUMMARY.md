---
phase: 01-contract-compatibility-harness
plan: 04
subsystem: api
tags: [openapi, swagger, compatibility, docs, support-matrix, contract-generation]

# Dependency graph
requires:
  - phase: 01-contract-compatibility-harness (plan 02)
    provides: "Support matrix classification, Swagger docs wiring, and public contract inventory"
  - phase: 01-contract-compatibility-harness (plan 03)
    provides: "Verified runtime compatibility surface and the COMP-03 gap diagnosis"
provides:
  - "Generated Hive-specific OpenAPI contract derived from support-matrix.json"
  - "Generated support-matrix markdown derived from the same source data as the published contract"
  - "Edge API docs route serving the generated Hive contract by default"
  - "Regression tests covering generated contract serving and default docs wiring"
affects: [02-auth, 06-inference-surface]

# Tech tracking
tech-stack:
  added: [py3-yaml]
  patterns: [matrix-derived-contract-artifacts, generated-docs-source-of-truth, docs-route-regression-tests]

key-files:
  created:
    - packages/openai-contract/scripts/sync_hive_contract.py
    - packages/openai-contract/generated/hive-openapi.yaml
    - apps/edge-api/cmd/server/main_test.go
    - apps/edge-api/docs/swagger_test.go
  modified:
    - packages/openai-contract/scripts/generate-matrix.sh
    - docs/support-matrix.md
    - apps/edge-api/cmd/server/main.go
    - deploy/docker/Dockerfile.edge-api
    - deploy/docker/Dockerfile.toolchain
    - deploy/docker/docker-compose.override.yml

key-decisions:
  - "Published docs are generated from support-matrix.json plus the upstream spec so runtime support classification stays the single source of truth"
  - "The generated contract drops top-level upstream x-oaiMeta so out-of-scope organization/admin docs metadata cannot leak into Hive's published contract"
  - "The generator entrypoint is POSIX-sh compatible and the toolchain image includes py3-yaml so Docker verification runs the same generation path as local development"

patterns-established:
  - "Contract-docs generation pattern: sync_hive_contract.py rewrites the published spec and markdown from support-matrix.json"
  - "Docs serving pattern: OPENAPI_SPEC_PATH defaults to the committed generated contract artifact"
  - "Regression pattern: docs route tests assert Hive-specific spec contents and absence of the upstream OpenAI base URL"

requirements-completed: [COMP-03]

# Metrics
duration: 21min
completed: 2026-03-28
---

# Phase 01 Plan 04: Docs Contract Summary

**Generated Hive-specific OpenAPI and support-matrix artifacts derived from the runtime support matrix, with `/docs` now serving the generated contract instead of the raw upstream spec**

## Performance

- **Duration:** 21 min
- **Started:** 2026-03-28T23:18:17-04:00
- **Completed:** 2026-03-28T23:39:40-04:00
- **Tasks:** 2/2 complete
- **Files modified:** 12

## Accomplishments
- Added a real contract-sync generator that derives both `packages/openai-contract/generated/hive-openapi.yaml` and `docs/support-matrix.md` from `support-matrix.json`
- Injected `x-hive-status`, `x-hive-phase`, and `x-hive-notes` into every published public operation while excluding out-of-scope organization/admin endpoints from the generated contract
- Switched the edge API and runtime image defaults to serve the generated Hive contract at `/docs/openapi.yaml`
- Added runtime regression tests for the docs route and spec-path defaults, plus Docker wiring so regenerated contract artifacts flow into local development without a manual rebuild

## Task Commits

Each task was committed atomically:

1. **Task 1: Replace the placeholder docs sync with a real generator that derives published contract artifacts from support-matrix.json** - `6a83d88` (feat)
2. **Task 2: Serve the generated Hive contract from edge-api and add regression coverage for the docs route** - `3729296` (feat)

**TDD red commit:** `8d4ae14` (`test(01-04): add failing tests for Hive contract generator`)

## Files Created/Modified
- `packages/openai-contract/scripts/sync_hive_contract.py` - Generates the published Hive OpenAPI contract and support-matrix markdown from `support-matrix.json`
- `packages/openai-contract/scripts/generate-matrix.sh` - POSIX-sh entrypoint that validates inputs and generated outputs before succeeding
- `packages/openai-contract/generated/hive-openapi.yaml` - Committed generated OpenAPI artifact served by `/docs/openapi.yaml`
- `docs/support-matrix.md` - Generated human-readable contract surface derived from the same source data
- `apps/edge-api/cmd/server/main.go` - Defaults `OPENAPI_SPEC_PATH` to the generated Hive contract
- `apps/edge-api/cmd/server/main_test.go` - Verifies the generated-spec default path and env override behavior
- `apps/edge-api/docs/swagger_test.go` - Verifies `/docs/` and `/docs/openapi.yaml` serve the expected Hive contract behavior
- `deploy/docker/Dockerfile.edge-api` - Copies the generated contract artifact into the runtime image
- `deploy/docker/docker-compose.override.yml` - Syncs `packages/openai-contract` into the dev container so regenerated artifacts appear without a rebuild
- `deploy/docker/Dockerfile.toolchain` - Adds `py3-yaml` so Docker verification can run the Python generator

## Decisions Made
- Kept the published contract generator in Python because the repository already had Python-based TDD coverage and the toolchain image already carries Python for developer workflows
- Scrubbed the upstream base URL from all generated spec strings, not just the `servers` block, so examples and metadata cannot drift back toward OpenAI production endpoints
- Removed the top-level upstream `x-oaiMeta` block from the published artifact because it reintroduced organization/admin documentation references that are outside Hive's public API surface

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Toolchain image lacked the YAML dependency required by the new generator**
- **Found during:** Task 1 verification
- **Issue:** `docker compose ... run --rm toolchain 'cd /workspace && packages/openai-contract/scripts/generate-matrix.sh'` failed with `ModuleNotFoundError: No module named 'yaml'`
- **Fix:** Added `py3-yaml` to `deploy/docker/Dockerfile.toolchain`
- **Files modified:** `deploy/docker/Dockerfile.toolchain`
- **Verification:** Rebuilt the toolchain image and reran the generator command successfully
- **Committed in:** `6a83d88`

**2. [Rule 3 - Blocking] The generator entrypoint assumed bash in an Alpine `/bin/sh` container**
- **Found during:** Task 1 verification
- **Issue:** `generate-matrix.sh` used `#!/usr/bin/env bash` and `${BASH_SOURCE[0]}`, which failed in the toolchain container with `env: can't execute 'bash'`
- **Fix:** Converted the entrypoint to POSIX `sh` and replaced the script-dir resolution logic
- **Files modified:** `packages/openai-contract/scripts/generate-matrix.sh`
- **Verification:** The toolchain container now runs the generator command successfully
- **Committed in:** `6a83d88`

**3. [Rule 1 - Missing Critical] Upstream docs metadata reintroduced `/organization/` references after path filtering**
- **Found during:** Task 1 acceptance review
- **Issue:** The generated spec removed out-of-scope API paths but the top-level upstream `x-oaiMeta` block still contained organization/admin documentation references, violating the published-contract acceptance criteria
- **Fix:** Removed the top-level `x-oaiMeta` block from the generated artifact
- **Files modified:** `packages/openai-contract/scripts/sync_hive_contract.py`, `packages/openai-contract/generated/hive-openapi.yaml`
- **Verification:** `rg -n "https://api.openai.com/v1|/organization/" packages/openai-contract/generated/hive-openapi.yaml` now returns only the expected Hive annotations and no upstream/org references
- **Committed in:** `6a83d88`

**Total deviations:** 3 auto-fixed (2 blocking, 1 missing critical)
**Impact on plan:** All deviations were required to make the generated contract actually verifiable in Docker and to keep the published spec aligned with Hive's scoped public surface.

## Issues Encountered
None beyond the auto-fixed deviations above.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Phase 01 now has a generated, committed, and served Hive-specific OpenAPI contract that matches the support-matrix classification
- The remaining workflow step is phase-level verification so `COMP-03` can be rechecked against the refreshed docs path and the phase can be marked complete
- If verification passes, Phase 02 can proceed without further Phase 01 docs work

## Self-Check: PASSED

Verified with:
- `python3 -m unittest packages.openai-contract.scripts.test_sync_hive_contract`
- `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && packages/openai-contract/scripts/generate-matrix.sh"`
- `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/edge-api && go test ./docs/... ./cmd/server/... -v -count=1"`

---
*Phase: 01-contract-compatibility-harness*
*Completed: 2026-03-28*
