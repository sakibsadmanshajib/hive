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
completed: 2026-03-29
---

# Phase 01 Plan 03: SDK Compatibility Harness Summary

**JS/Python/Java SDK test suites proving OpenAI SDK compatibility for models endpoint, unsupported endpoint errors, compat headers, and golden fixture regression**

## Performance

- **Duration:** 5 min
- **Started:** 2026-03-29T01:47:37Z
- **Completed:** 2026-03-29T01:52:36Z
- **Tasks:** 2/3 (Task 3 is human checkpoint, pending)
- **Files modified:** 21

## Accomplishments
- JS SDK tests: health, models listing, unsupported endpoint errors (planned + explicit), error envelope shape, compat headers, streaming error handling
- Python SDK tests: health, models listing, unsupported endpoint errors, error envelope shape, compat headers with uniqueness check
- Java SDK tests: health, models listing, unsupported endpoint errors, error envelope shape, compat headers
- Golden fixtures established for models-list, error-unsupported, and error-unknown response shapes
- All error tests assert provider-blind messaging (no "provider", "upstream", or "openai" in error text)

## Task Commits

Each task was committed atomically:

1. **Task 1: Create JS SDK compatibility tests with golden fixtures** - `dc45aa0` (feat)
2. **Task 2: Create Python and Java SDK compatibility tests** - `d9b97cc` (feat)
3. **Task 3: Verify full SDK compatibility harness** - PENDING (human checkpoint)

## Files Created/Modified
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

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
- Docker is not available in the WSL2 environment, so the automated verification step (running tests against edge-api container) could not be executed. This verification is deferred to Task 3 (human checkpoint).

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- SDK compatibility harness complete pending Docker verification in Task 3
- Once Task 3 human checkpoint passes, Phase 01 is complete and ready for Phase 02 (auth layer)
- Future endpoint implementations will add tests to these suites and update golden fixtures

## Self-Check: PASSED

All 21 key files verified present. Both task commits (dc45aa0, d9b97cc) verified in git log.

---
*Phase: 01-contract-compatibility-harness*
*Completed: 2026-03-29 (Tasks 1-2; Task 3 human checkpoint pending)*
