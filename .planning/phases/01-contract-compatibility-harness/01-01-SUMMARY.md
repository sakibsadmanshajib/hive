---
phase: 01-contract-compatibility-harness
plan: 01
subsystem: infra
tags: [docker, go, air, ogen, oapi-codegen, vitest, pytest, junit, openai-sdk]

# Dependency graph
requires: []
provides:
  - Docker Compose dev stack with edge-api, toolchain, and SDK test containers
  - Go workspace with minimal HTTP server (/health, /v1/models)
  - SDK test scaffolds for JS (Vitest), Python (pytest), Java (JUnit/Gradle)
  - Hot-reload via air inside containerized Go development
affects: [01-02, 01-03, 02, 03, 04]

# Tech tracking
tech-stack:
  added: [go-1.24, air-1.64.5, ogen-1.20.2, oapi-codegen-2.6.0, openai-js-6.33, openai-py-2.30, openai-java-4.30, vitest-3, pytest-8, junit-5.11, gradle-8, docker-compose]
  patterns: [containerized-dev-workflow, go-workspace-monorepo, profile-based-compose-services, healthcheck-based-dependency]

key-files:
  created:
    - go.work
    - apps/edge-api/go.mod
    - apps/edge-api/cmd/server/main.go
    - apps/edge-api/.air.toml
    - deploy/docker/docker-compose.yml
    - deploy/docker/docker-compose.override.yml
    - deploy/docker/Dockerfile.edge-api
    - deploy/docker/Dockerfile.toolchain
    - deploy/docker/Dockerfile.sdk-tests-js
    - deploy/docker/Dockerfile.sdk-tests-py
    - deploy/docker/Dockerfile.sdk-tests-java
    - packages/sdk-tests/js/package.json
    - packages/sdk-tests/js/tsconfig.json
    - packages/sdk-tests/js/vitest.config.ts
    - packages/sdk-tests/python/pyproject.toml
    - packages/sdk-tests/java/build.gradle
    - packages/sdk-tests/java/settings.gradle
    - .gitignore
  modified: []

key-decisions:
  - "Used GOTOOLCHAIN=auto to install air v1.64.5 (requires Go 1.25) on Go 1.24 base image"
  - "Air build command uses absolute paths from /app workspace root for go.work compatibility"
  - "SDK test services use Docker Compose profiles (test) so they only run on demand"

patterns-established:
  - "Containerized dev: all builds, codegen, and tests run inside Docker, no host toolchains required"
  - "Go workspace monorepo: go.work at repo root with module members under apps/"
  - "Profile-based services: tools and test profiles keep docker compose up lightweight"
  - "Healthcheck gating: SDK test containers depend on edge-api service_healthy condition"

requirements-completed: [API-08]

# Metrics
duration: 8min
completed: 2026-03-28
---

# Phase 01 Plan 01: Docker Dev Stack Summary

**Containerized Go edge-api with air hot-reload, ogen/oapi-codegen toolchain, and JS/Python/Java SDK test scaffolds via Docker Compose**

## Performance

- **Duration:** 8 min
- **Started:** 2026-03-28T07:22:18Z
- **Completed:** 2026-03-28T07:30:27Z
- **Tasks:** 3
- **Files modified:** 18

## Accomplishments
- Go workspace with minimal HTTP server exposing /health and /v1/models endpoints
- Docker Compose stack with edge-api (Go+air), toolchain (ogen+oapi-codegen), and 3 SDK test containers
- All SDK test projects scaffolded with pinned OpenAI SDK versions (JS 6.33, Python 2.30, Java 4.30)
- Named volumes for Go module and build caches persist across container restarts

## Task Commits

Each task was committed atomically:

1. **Task 1: Create Go workspace, edge-api module, and minimal HTTP server** - `d21d687` (feat)
2. **Task 2: Create all Dockerfiles, SDK test project scaffolds, and Docker Compose orchestration** - `4813ef1` (feat)
3. **Task 3: Verify full Docker stack boots and health endpoint responds** - `210254e` (fix)

## Files Created/Modified
- `go.work` - Go workspace root for multi-module monorepo
- `apps/edge-api/go.mod` - Edge API Go module definition
- `apps/edge-api/cmd/server/main.go` - Minimal HTTP server with /health and /v1/models
- `apps/edge-api/.air.toml` - Air hot-reload config for containerized development
- `deploy/docker/docker-compose.yml` - Full dev stack orchestration
- `deploy/docker/docker-compose.override.yml` - File sync watch config for development
- `deploy/docker/Dockerfile.edge-api` - Go dev image with air hot-reload
- `deploy/docker/Dockerfile.toolchain` - Codegen tools (ogen, oapi-codegen, redocly)
- `deploy/docker/Dockerfile.sdk-tests-js` - Node + OpenAI SDK + Vitest
- `deploy/docker/Dockerfile.sdk-tests-py` - Python + OpenAI SDK + pytest
- `deploy/docker/Dockerfile.sdk-tests-java` - Java + OpenAI SDK + JUnit/Gradle
- `packages/sdk-tests/js/package.json` - JS test project with openai ^6.33.0
- `packages/sdk-tests/js/tsconfig.json` - TypeScript config for test project
- `packages/sdk-tests/js/vitest.config.ts` - Vitest configuration
- `packages/sdk-tests/python/pyproject.toml` - Python test project with openai >=2.30.0
- `packages/sdk-tests/java/build.gradle` - Java test project with openai-java 4.30.0
- `packages/sdk-tests/java/settings.gradle` - Gradle project settings
- `.gitignore` - Repository gitignore for Go, IDE, Docker, and generated files

## Decisions Made
- Used `GOTOOLCHAIN=auto` for air installation since air v1.64.5 requires Go 1.25 but our base image is Go 1.24
- Air config uses absolute paths from `/app` workspace root to ensure go.work is visible during builds
- SDK test services placed behind Docker Compose `test` profile to keep default `docker compose up` lightweight

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Air v1.64.5 requires Go 1.25, incompatible with Go 1.24 base image**
- **Found during:** Task 3 (Docker stack verification)
- **Issue:** `go install github.com/air-verse/air@v1.64.5` failed because air requires Go >= 1.25
- **Fix:** Added `GOTOOLCHAIN=auto` env var to the install command, allowing Go to download the required toolchain
- **Files modified:** deploy/docker/Dockerfile.edge-api
- **Verification:** Docker build succeeds, air starts correctly inside container
- **Committed in:** 210254e (Task 3 commit)

**2. [Rule 1 - Bug] Air build command resolved paths relative to WORKDIR not root setting**
- **Found during:** Task 3 (Docker stack verification)
- **Issue:** Air resolved `./cmd/server` relative to WORKDIR `/app` instead of air root `/app/apps/edge-api`, causing `stat /app/cmd/server: directory not found`
- **Fix:** Changed air.toml to use absolute paths: `cd /app && go build -o /app/apps/edge-api/tmp/main ./apps/edge-api/cmd/server` and `full_bin` instead of deprecated `bin`
- **Files modified:** apps/edge-api/.air.toml
- **Verification:** Container starts, builds successfully, server responds on port 8080
- **Committed in:** 210254e (Task 3 commit)

---

**Total deviations:** 2 auto-fixed (1 bug, 1 blocking)
**Impact on plan:** Both fixes necessary for the Docker stack to function. No scope creep.

## Issues Encountered
None beyond the auto-fixed deviations above.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Docker dev stack fully operational, ready for OpenAPI spec download and code generation (Plan 02)
- Toolchain container has ogen and oapi-codegen installed for contract generation
- SDK test containers ready to receive test files once endpoints are implemented

## Self-Check: PASSED

All 18 key files verified present. All 3 task commits verified in git log.

---
*Phase: 01-contract-compatibility-harness*
*Completed: 2026-03-28*
