---
phase: 01-contract-compatibility-harness
verified: 2026-03-29T02:44:53Z
status: gaps_found
score: 6/7 must-haves verified
gaps:
  - truth: "Swagger/OpenAPI docs at /docs match Hive's public API contract and supported launch surface"
    status: failed
    reason: "The docs route is reachable, but it serves the raw upstream OpenAI spec instead of a Hive-specific contract view. The served spec still advertises OpenAI's production server URL and does not include Hive support-status annotations, so it does not reflect Hive's classified launch surface."
    artifacts:
      - path: "apps/edge-api/docs/swagger.go"
        issue: "Serves the raw spec file from disk at /docs/openapi.yaml without applying the Hive overlay or support matrix"
      - path: "packages/openai-contract/upstream/openapi.yaml"
        issue: "Still contains the upstream OpenAI server URL and no x-hive-status/x-hive-phase fields"
      - path: "packages/openai-contract/scripts/generate-matrix.sh"
        issue: "Explicitly marked as a placeholder, so published docs are not generated from the matrix"
    missing:
      - "Serve a Hive-specific OpenAPI document that applies support classification to the browsable docs"
      - "Replace the upstream OpenAI server URL in the served spec with Hive's API base URL"
      - "Add a real generation/sync step so published docs stay derived from support-matrix.json"
---

# Phase 01: Contract Compatibility Harness Verification Report

**Phase Goal:** Make Hive's public API a verified compatibility product instead of an approximation, on top of a Docker-only developer workflow.
**Verified:** 2026-03-29T02:44:53Z
**Status:** gaps_found
**Re-verification:** No - initial verification

## Goal Achievement

### Fresh Session Evidence

- `docker compose -f deploy/docker/docker-compose.yml run --rm sdk-tests-js` passed: 6 files, 11 tests
- `docker compose -f deploy/docker/docker-compose.yml run --rm sdk-tests-py` passed: 9 tests
- `docker compose -f deploy/docker/docker-compose.yml run --rm sdk-tests-java` passed
- `docker compose -f deploy/docker/docker-compose.yml exec -T edge-api sh -lc 'wget -qO- http://localhost:8080/docs/ | grep -q swagger-ui && echo PASS'` returned `PASS`
- `docker compose -f deploy/docker/docker-compose.yml exec -T edge-api sh -lc 'wget -S -O- http://localhost:8080/v1/models ...'` returned `200 OK` with `X-Request-Id`, `Openai-Version: 2020-10-01`, and `Openai-Processing-Ms`
- `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain 'cd /workspace/apps/edge-api && go test ./... -v'` passed after updating `deploy/docker/Dockerfile.toolchain`

### Observable Truths

| # | Truth | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Docker-only development and verification workflows exist for the edge API, toolchain, and SDK harnesses. | ✓ VERIFIED | `deploy/docker/docker-compose.yml` defines `edge-api`, `toolchain`, and all three SDK test services with shared cache volumes and `HIVE_BASE_URL`; `deploy/docker/Dockerfile.toolchain` now installs `ogen` and `oapi-codegen` with `GOTOOLCHAIN=auto`; fresh session Go/SDK Docker runs passed. |
| 2 | The running edge API exposes `/v1/models` and returns OpenAI compatibility headers. | ✓ VERIFIED | `apps/edge-api/cmd/server/main.go` registers `/v1/models`; `apps/edge-api/internal/middleware/compat_headers.go` adds `x-request-id`, `openai-version`, and `openai-processing-ms`; fresh session probe returned `200 OK` with those headers. |
| 3 | Error responses use an OpenAI-style envelope and classify unsupported endpoints explicitly. | ✓ VERIFIED | `apps/edge-api/internal/errors/openai.go` builds `{error:{message,type,param,code}}`; `apps/edge-api/internal/middleware/unsupported.go` maps planned/unsupported/out-of-scope/unknown cases to 404 JSON errors; unit tests in `apps/edge-api/internal/errors/openai_test.go` and `apps/edge-api/internal/middleware/unsupported_test.go` exercise the envelope and codes. |
| 4 | Public endpoints are fully classified and runtime enforcement is matrix-driven. | ✓ VERIFIED | `packages/openai-contract/matrix/support-matrix.json` contains 148 classified endpoints; `apps/edge-api/cmd/server/main.go` loads it at startup; `apps/edge-api/internal/middleware/unsupported.go` calls `m.Lookup(r.Method, r.URL.Path)` before routing `/v1/*`. |
| 5 | Official OpenAI JS, Python, and Java SDKs work against Hive for the supported launch endpoint by changing only base URL and API key. | ✓ VERIFIED | `packages/sdk-tests/js/package.json`, `packages/sdk-tests/python/pyproject.toml`, and `packages/sdk-tests/java/build.gradle` depend on the official OpenAI SDKs; the JS/Python/Java suites all point to `HIVE_BASE_URL`; fresh session runs passed in all three languages. |
| 6 | The compatibility harness captures regressions for supported responses, unsupported responses, and headers. | ✓ VERIFIED | Golden fixtures exist under `packages/sdk-tests/fixtures/golden/`; JS, Python, and Java tests cover models, unsupported endpoints, error shape, and headers; JS also covers streaming error behavior. |
| 7 | Swagger/OpenAPI docs at `/docs` match Hive's contract surface, not just the upstream OpenAI spec. | ✗ FAILED | `/docs/` loads Swagger UI, but `apps/edge-api/docs/swagger.go` serves `packages/openai-contract/upstream/openapi.yaml` directly; that spec still declares `https://api.openai.com/v1` and no `x-hive-status` or `x-hive-phase` fields were found outside the overlay file. |

**Score:** 6/7 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `deploy/docker/docker-compose.yml` | Containerized dev/test orchestration | ✓ VERIFIED | Builds `edge-api`, `toolchain`, `sdk-tests-js`, `sdk-tests-py`, and `sdk-tests-java`; exposes `8080`; mounts named Go caches. |
| `deploy/docker/Dockerfile.edge-api` | Edge API container with hot reload | ✓ VERIFIED | Installs `air`, copies `support-matrix.json` and `openapi.yaml`, and runs `air -c apps/edge-api/.air.toml`. |
| `deploy/docker/Dockerfile.toolchain` | Docker-only Go/codegen toolchain | ✓ VERIFIED | Installs `ogen` and `oapi-codegen` with `GOTOOLCHAIN=auto`, matching the fresh passing toolchain test evidence. |
| `apps/edge-api/.air.toml` | Hot-reload configuration | ✓ VERIFIED | Builds `./apps/edge-api/cmd/server` into `/app/apps/edge-api/tmp/main` and watches `.go`/`.toml` files. |
| `apps/edge-api/cmd/server/main.go` | Runtime wiring for models, docs, matrix, middleware | ✓ VERIFIED | Loads matrix and spec paths, registers `/health`, `/docs/`, and `/v1/models`, then wraps with `UnsupportedEndpointMiddleware` and `CompatHeaders`. |
| `packages/openai-contract/matrix/support-matrix.json` | Classified public API surface | ✓ VERIFIED | 148 endpoints, with counts `supported_now=1`, `planned_for_launch=24`, `explicitly_unsupported_at_launch=72`, `out_of_scope=51`. |
| `apps/edge-api/internal/errors/openai.go` | OpenAI error envelope builder | ✓ VERIFIED | `NewError` and `WriteError` are implemented and unit-tested. |
| `apps/edge-api/internal/middleware/unsupported.go` | Matrix-driven unsupported response handling | ✓ VERIFIED | Uses matrix lookup and emits `unsupported_endpoint` or `invalid_request_error` with explicit codes. |
| `apps/edge-api/internal/middleware/compat_headers.go` | Compatibility header middleware | ✓ VERIFIED | Injects `x-request-id`, `openai-version`, and `openai-processing-ms` on success and error paths. |
| `apps/edge-api/docs/swagger.go` | Hive contract docs handler | ⚠️ PARTIAL | Reachable and browsable, but serves the raw upstream spec rather than a Hive-shaped contract document. |
| `docs/support-matrix.md` | Human-readable support surface reference | ✓ VERIFIED | Counts align with the matrix and list the same support buckets, but it is a committed snapshot rather than a generated view. |
| `packages/sdk-tests/js/tests/errors/unsupported-endpoint.test.ts` | JS SDK compatibility coverage | ✓ VERIFIED | Uses the official SDK against `HIVE_BASE_URL` and asserts `NotFoundError`, `unsupported_endpoint`, and provider-blind messages. |
| `packages/sdk-tests/python/tests/test_unsupported.py` | Python SDK compatibility coverage | ✓ VERIFIED | Uses the official SDK against `HIVE_BASE_URL` and asserts `openai.NotFoundError` and compatibility codes. |
| `packages/sdk-tests/java/src/test/java/com/hive/sdktests/UnsupportedEndpointTest.java` | Java SDK compatibility coverage | ✓ VERIFIED | Uses the official SDK for chat completions and raw HTTP for fine-tuning unsupported cases. |
| `packages/sdk-tests/fixtures/golden/models-list.json` | Supported response regression fixture | ✓ VERIFIED | Consumed by JS models test. |
| `packages/sdk-tests/fixtures/golden/error-unsupported.json` | Unsupported response regression fixture | ✓ VERIFIED | Captures the planned-endpoint error shape. |

### Key Link Verification

| From | To | Via | Status | Details |
| --- | --- | --- | --- | --- |
| `deploy/docker/docker-compose.yml` | `deploy/docker/Dockerfile.edge-api` | build context reference | WIRED | `edge-api.build.dockerfile` points to `deploy/docker/Dockerfile.edge-api`. |
| `deploy/docker/docker-compose.override.yml` | `apps/edge-api/.air.toml` | synced source tree for hot reload | WIRED | The override syncs `../../apps/edge-api` into `/app/apps/edge-api`, which includes `.air.toml`; the image runs `air -c apps/edge-api/.air.toml`. |
| `apps/edge-api/cmd/server/main.go` | `packages/openai-contract/matrix/support-matrix.json` | `matrix.LoadMatrix` at startup | WIRED | Default matrix path is `/app/packages/openai-contract/matrix/support-matrix.json`; the Docker image copies that file in. |
| `apps/edge-api/internal/middleware/unsupported.go` | `apps/edge-api/internal/errors/openai.go` | `apierrors.WriteError(...)` | WIRED | All unsupported/error branches call the shared error writer. |
| `apps/edge-api/cmd/server/main.go` | `apps/edge-api/internal/middleware/unsupported.go` | middleware wrapping | WIRED | `handler = middleware.UnsupportedEndpointMiddleware(m)(handler)`. |
| `apps/edge-api/cmd/server/main.go` | `apps/edge-api/internal/middleware/compat_headers.go` | middleware wrapping | WIRED | `handler = middleware.CompatHeaders()(handler)`. |
| `packages/sdk-tests/*` | `apps/edge-api` | `HIVE_BASE_URL=http://edge-api:8080/v1` | WIRED | Compose injects the base URL into all SDK test services; each language test suite uses that variable. |
| `apps/edge-api/docs/swagger.go` | `packages/openai-contract/upstream/openapi.yaml` | direct file serving | WIRED | `/docs/openapi.yaml` is the raw spec file from disk. |
| `packages/openai-contract/overlays/hive-support-status.yaml` | served Swagger spec | overlay application | NOT_WIRED | No code or script applies the overlay before serving `/docs/openapi.yaml`. |
| `packages/openai-contract/matrix/support-matrix.json` | published docs | generation/synchronization | PARTIAL | `docs/support-matrix.md` aligns with the matrix today, but no generator or runtime path derives it; `generate-matrix.sh` is explicitly a placeholder. |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| --- | --- | --- | --- | --- |
| `COMP-01` | `01-03` | Official OpenAI JS/Python/Java SDKs work against Hive by changing base URL and API key | ✓ SATISFIED | Official SDK dependencies are pinned in the three SDK test projects, all three suites use `HIVE_BASE_URL`, and all three Docker verification runs passed in this session. |
| `COMP-02` | `01-02`, `01-03` | OpenAI-style status codes, error objects, and compatibility headers | ✓ SATISFIED | `openai.go`, `unsupported.go`, `compat_headers.go`, their Go unit tests, the `/v1/models` header probe, and the JS/Python/Java SDK tests all confirm the contract. |
| `COMP-03` | `01-02` | Swagger/OpenAPI docs match the Hive public API contract and supported launch surface | ✗ BLOCKED | `/docs/` is browsable, but the served spec is the raw upstream `openapi.yaml` with OpenAI server metadata and no Hive support annotations; the overlay is not applied. |
| `API-08` | `01-01`, `01-02` | Unsupported public non-org/admin endpoints are classified and return explicit unsupported responses | ✓ SATISFIED | `support-matrix.json` classifies the full surface; `UnsupportedEndpointMiddleware` enforces it; SDK tests validate planned and explicitly unsupported responses. |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| `packages/openai-contract/scripts/generate-matrix.sh` | 8 | `placeholder for future automation` | Warning | Confirms that published contract docs are not actually generated from the matrix, which leaves the docs path vulnerable to drift. |

### Gaps Summary

Phase 01 delivers the Docker-only workflow, matrix-driven runtime enforcement, OpenAI-style error/header behavior, and a real three-language SDK compatibility harness. Those parts are substantiated by the current code and the fresh session verification commands.

The remaining gap is the documentation contract. Swagger UI is reachable, but the document it serves is still the raw upstream OpenAI spec, not a Hive-specific OpenAPI surface. Because the served spec still points at OpenAI and omits Hive support annotations, `COMP-03` is not achieved, and the phase goal is not fully closed.

---

_Verified: 2026-03-29T02:44:53Z_  
_Verifier: Claude (gsd-verifier)_
