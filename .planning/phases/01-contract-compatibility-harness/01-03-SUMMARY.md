---
phase: 01-contract-compatibility-harness
plan: 03
subsystem: testing
tags: [openai-sdk, vitest, pytest, junit, compatibility, golden-fixtures, sdk-tests]

# Dependency graph
requires:
  - phase: 01-contract-compatibility-harness (plan 01)
    provides: "Docker dev stack, SDK test scaffolds (JS/Python/Java), edge-api server"
  - phase: 01-contract-compatibility-harness (plan 02)
    provides: "OpenAI error envelope, unsupported endpoint middleware, compat headers, support matrix"
provides:
  - "JS SDK compatibility tests (health, models, unsupported, error shape, headers, streaming error)"
  - "Python SDK compatibility tests (health, models, unsupported, error shape, headers)"
  - "Java SDK compatibility tests (health, models, unsupported, error shape, headers)"
  - "Golden response fixtures for regression testing (models-list, error-unsupported, error-unknown)"
affects: [02-auth, 06-inference-surface]

# Tech tracking
tech-stack:
  added: []
  patterns: [sdk-compatibility-harness, golden-fixture-regression, provider-blind-error-assertions]

key-files:
  created:
    - packages/sdk-tests/js/tests/health/health.test.ts
    - packages/sdk-tests/js/tests/models/list-models.test.ts
    - packages/sdk-tests/js/tests/errors/unsupported-endpoint.test.ts
    - packages/sdk-tests/js/tests/errors/error-shape.test.ts
    - packages/sdk-tests/js/tests/headers/compat-headers.test.ts
    - packages/sdk-tests/js/tests/streaming/streaming-error.test.ts
    - packages/sdk-tests/python/tests/__init__.py
    - packages/sdk-tests/python/tests/conftest.py
    - packages/sdk-tests/python/tests/test_health.py
    - packages/sdk-tests/python/tests/test_models.py
    - packages/sdk-tests/python/tests/test_unsupported.py
    - packages/sdk-tests/python/tests/test_error_shape.py
    - packages/sdk-tests/python/tests/test_headers.py
    - packages/sdk-tests/java/src/test/java/com/hive/sdktests/HealthTest.java
    - packages/sdk-tests/java/src/test/java/com/hive/sdktests/ModelsTest.java
    - packages/sdk-tests/java/src/test/java/com/hive/sdktests/UnsupportedEndpointTest.java
    - packages/sdk-tests/java/src/test/java/com/hive/sdktests/ErrorShapeTest.java
    - packages/sdk-tests/java/src/test/java/com/hive/sdktests/HeadersTest.java
    - packages/sdk-tests/fixtures/golden/models-list.json
    - packages/sdk-tests/fixtures/golden/error-unsupported.json
    - packages/sdk-tests/fixtures/golden/error-unknown.json
  modified: []

key-decisions:
  - "Java fine-tuning test uses raw HTTP instead of SDK to avoid coupling to SDK API surface changes"
  - "Python conftest uses httpx (bundled with openai SDK) for raw HTTP tests"
  - "Golden fixtures capture minimal expected shapes for regression, not full response bodies"

patterns-established:
  - "SDK harness pattern: each language tests the same scenarios (health, models, unsupported, error shape, headers)"
  - "Provider-blind assertions: every error test asserts message does NOT contain provider/upstream/openai"
  - "Golden fixture regression: response shapes compared against committed JSON fixtures"

requirements-completed: [COMP-01, COMP-02]

# Metrics
duration: 5min
completed: 2026-03-28
---

# Phase 01 Plan 03: SDK Compatibility Harness Summary

**JS/Python/Java SDK test suites proving OpenAI SDK compatibility for models endpoint, unsupported endpoint errors, compat headers, golden fixture regression, and full Docker verification**

## Performance

- **Duration:** 5 min
- **Started:** 2026-03-29T01:47:37Z
- **Checkpoint approved:** 2026-03-28T22:38:50-04:00
- **Tasks:** 3/3 complete
- **Files modified:** 22

## Accomplishments
- JS SDK tests: health, models listing, unsupported endpoint errors (planned + explicit), error envelope shape, compat headers, streaming error handling
- Python SDK tests: health, models listing, unsupported endpoint errors, error envelope shape, compat headers with uniqueness check
- Java SDK tests: health, models listing, unsupported endpoint errors, error envelope shape, compat headers
- Golden fixtures established for models-list, error-unsupported, and error-unknown response shapes
- All error tests assert provider-blind messaging (no "provider", "upstream", or "openai" in error text)
- End-to-end Docker verification completed for JS, Python, Java, Go, Swagger UI, support matrix, and compatibility headers
- Toolchain container drift fixed so Docker-only verification remains reproducible

## Task Commits

Each task was committed atomically:

1. **Task 1: Create JS SDK compatibility tests with golden fixtures** - `dc45aa0` (feat)
2. **Task 2: Create Python and Java SDK compatibility tests** - `d9b97cc` (feat)
3. **Task 3: Verify full SDK compatibility harness** - Completed on `2026-03-28T22:38:50-04:00` after human approval

## Files Created/Modified
- `deploy/docker/Dockerfile.toolchain` - Restored Docker toolchain installs with `GOTOOLCHAIN=auto` for Go 1.25-requiring codegen tools
- `packages/sdk-tests/fixtures/golden/models-list.json` - Golden fixture for /v1/models response
- `packages/sdk-tests/fixtures/golden/error-unsupported.json` - Golden fixture for planned endpoint error
- `packages/sdk-tests/fixtures/golden/error-unknown.json` - Golden fixture for unknown endpoint error
- `packages/sdk-tests/js/tests/health/health.test.ts` - JS health endpoint test
- `packages/sdk-tests/js/tests/models/list-models.test.ts` - JS models listing + golden comparison
- `packages/sdk-tests/js/tests/errors/unsupported-endpoint.test.ts` - JS unsupported endpoint error tests
- `packages/sdk-tests/js/tests/errors/error-shape.test.ts` - JS raw error envelope shape test
- `packages/sdk-tests/js/tests/headers/compat-headers.test.ts` - JS compat header tests
- `packages/sdk-tests/js/tests/streaming/streaming-error.test.ts` - JS streaming error handling test
- `packages/sdk-tests/python/tests/conftest.py` - Python fixtures (client, base_url)
- `packages/sdk-tests/python/tests/test_health.py` - Python health endpoint test
- `packages/sdk-tests/python/tests/test_models.py` - Python models listing test
- `packages/sdk-tests/python/tests/test_unsupported.py` - Python unsupported endpoint error tests
- `packages/sdk-tests/python/tests/test_error_shape.py` - Python raw error envelope shape test
- `packages/sdk-tests/python/tests/test_headers.py` - Python compat header tests
- `packages/sdk-tests/java/.../HealthTest.java` - Java health endpoint test
- `packages/sdk-tests/java/.../ModelsTest.java` - Java models listing test
- `packages/sdk-tests/java/.../UnsupportedEndpointTest.java` - Java unsupported endpoint error tests
- `packages/sdk-tests/java/.../ErrorShapeTest.java` - Java raw error envelope shape test
- `packages/sdk-tests/java/.../HeadersTest.java` - Java compat header tests

## Decisions Made
- Java fine-tuning test uses raw java.net.http.HttpClient instead of OpenAI SDK to avoid coupling to SDK fine-tuning API surface that may change across versions
- Python conftest uses httpx (bundled with openai SDK) for raw HTTP tests, avoiding an extra dependency
- Golden fixtures capture minimal expected shapes rather than full response bodies to allow flexibility
- Go verification for the Docker-only workflow runs through the `toolchain` container from `/workspace/apps/edge-api`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Toolchain image drift broke Docker-only verification**
- **Found during:** Task 3 checkpoint verification
- **Issue:** `github.com/ogen-go/ogen/cmd/ogen@v1.20.2` now requires Go 1.25, but `deploy/docker/Dockerfile.toolchain` still installed it on `golang:1.24-alpine` without `GOTOOLCHAIN=auto`
- **Fix:** Added `GOTOOLCHAIN=auto` to the `ogen` and `oapi-codegen` install steps in `deploy/docker/Dockerfile.toolchain`
- **Verification:** Rebuilt the toolchain image and ran `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain 'cd /workspace/apps/edge-api && go test ./... -v'` successfully

**2. [Rule 1 - Verification Path] Go tests were executed through the toolchain container**
- **Found during:** Task 3 checkpoint verification
- **Issue:** The original checkpoint command targeted the runtime `edge-api` container, but the working Go verification environment is the Docker `toolchain` container rooted at `/workspace/apps/edge-api`
- **Fix:** Verified Go tests from the toolchain container while keeping the runtime checks on the running `edge-api` service
- **Verification:** Swagger UI, response headers, and SDK harnesses were all validated against the running `edge-api` service after the toolchain image fix

**Total deviations:** 2 auto-fixed (1 blocking, 1 verification-path correction)
**Impact on plan:** Both fixes were required to close the pending human-verification checkpoint without changing planned scope.

## Issues Encountered
None beyond the auto-fixed deviations above.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- SDK compatibility harness fully verified in Docker across JS, Python, Java, Go, Swagger UI, and support-matrix checks
- Phase 01 remains blocked on the contract-docs verification gap recorded in `.planning/phases/01-contract-compatibility-harness/01-VERIFICATION.md`
- Once the Swagger/OpenAPI docs serve a Hive-classified spec instead of the raw upstream spec, Phase 01 can be marked complete and Phase 02 can begin
- Future endpoint implementations will add tests to these suites and update golden fixtures

## Self-Check: PASSED

All 21 key files verified present. JS, Python, Java, and Go verification commands passed during checkpoint closure.

---
*Phase: 01-contract-compatibility-harness*
*Completed: 2026-03-28 (all tasks complete, including Task 3 human verification)*
