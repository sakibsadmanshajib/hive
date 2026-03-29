---
phase: 01-contract-compatibility-harness
verified: 2026-03-29T03:45:34Z
status: passed
score: 7/7 must-haves verified
gaps: []
---

# Phase 01: Contract Compatibility Harness Verification Report

**Phase Goal:** Make Hive's public API a verified compatibility product instead of an approximation, on top of a Docker-only developer workflow.
**Verified:** 2026-03-29T03:45:34Z
**Status:** passed
**Re-verification:** Yes - targeted gap-closure verification after plan `01-04`

## Goal Achievement

Phase 01 previously had one open gap: `COMP-03`, where `/docs` still served the raw upstream OpenAI spec. Plan `01-04` replaced that placeholder docs path with a generated Hive contract derived from `support-matrix.json`, added regression coverage, and rewired the runtime image to serve the generated artifact by default.

This report re-verifies the former gap with fresh evidence and confirms the rest of the phase remains satisfied. Where a truth relies on code that `01-04` did not modify, that is called out explicitly as an inference from unchanged artifacts plus the prior successful verification.

## Fresh Session Evidence

- `python3 -m unittest packages.openai-contract.scripts.test_sync_hive_contract` passed: 3 tests
- `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && packages/openai-contract/scripts/generate-matrix.sh"` passed and regenerated 97 public operations
- `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/edge-api && go test ./docs/... ./cmd/server/... -v -count=1"` passed
- `docker compose -f deploy/docker/docker-compose.yml up -d --build edge-api` rebuilt and started the runtime image with the generated contract artifact copied into it
- `docker compose -f deploy/docker/docker-compose.yml exec -T edge-api sh -lc 'body="$(wget -qO- http://localhost:8080/docs/)"; ...'` returned `PASS`, confirming Swagger UI still loads `./openapi.yaml`
- `docker compose -f deploy/docker/docker-compose.yml exec -T edge-api sh -lc 'spec="$(wget -qO- http://localhost:8080/docs/openapi.yaml)"; ...'` returned `PASS`, confirming the served spec contains `url: /v1`, contains `x-hive-status:`, and does not contain `https://api.openai.com/v1`
- `docker compose -f deploy/docker/docker-compose.yml exec -T edge-api sh -lc 'wget -S -O- http://localhost:8080/v1/models ...'` returned `200 OK` with `X-Request-Id`, `Openai-Version: 2020-10-01`, and `Openai-Processing-Ms`

## Observable Truths

| # | Truth | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Docker-only development and verification workflows exist for the edge API, toolchain, and SDK harnesses. | ✓ VERIFIED | Fresh toolchain and edge-api Docker runs succeeded. `deploy/docker/docker-compose.yml` still defines `edge-api`, `toolchain`, and the SDK test services. |
| 2 | The running edge API exposes `/v1/models` and returns OpenAI compatibility headers. | ✓ VERIFIED | Fresh runtime probe returned `200 OK` plus `X-Request-Id`, `Openai-Version`, and `Openai-Processing-Ms`. |
| 3 | Error responses use an OpenAI-style envelope and classify unsupported endpoints explicitly. | ✓ VERIFIED | Inference from unchanged `apps/edge-api/internal/errors/openai.go`, `apps/edge-api/internal/middleware/unsupported.go`, and their existing verified test coverage; plan `01-04` did not modify these paths. |
| 4 | Public endpoints are fully classified and runtime enforcement is matrix-driven. | ✓ VERIFIED | Fresh generator run succeeded from `support-matrix.json`, and runtime still loads `SUPPORT_MATRIX_PATH` through `matrix.LoadMatrix(...)`; plan `01-04` preserved the matrix-driven enforcement path. |
| 5 | Official OpenAI JS, Python, and Java SDKs work against Hive for the supported launch endpoint by changing only base URL and API key. | ✓ VERIFIED | Inference from unchanged plan `01-03` harness code and the prior passing SDK verification; plan `01-04` did not touch the SDK suites or `/v1/models` handler behavior. |
| 6 | The compatibility harness captures regressions for supported responses, unsupported responses, and headers. | ✓ VERIFIED | Inference from unchanged JS/Python/Java harness files and golden fixtures, plus new docs-route regression tests added in `apps/edge-api/docs/swagger_test.go` and `apps/edge-api/cmd/server/main_test.go`. |
| 7 | Swagger/OpenAPI docs at `/docs` match Hive's contract surface, not just the upstream OpenAI spec. | ✓ VERIFIED | Fresh container probe of `/docs/openapi.yaml` passed. The served artifact now comes from `packages/openai-contract/generated/hive-openapi.yaml`, advertises `url: /v1`, includes `x-hive-status`/`x-hive-phase`, and excludes the upstream base URL and out-of-scope organization/admin paths. |

**Score:** 7/7 truths verified

## Required Artifacts

| Artifact | Status | Details |
| --- | --- | --- |
| `packages/openai-contract/scripts/sync_hive_contract.py` | ✓ VERIFIED | Generates the published Hive OpenAPI contract and support-matrix markdown from `support-matrix.json`. |
| `packages/openai-contract/scripts/generate-matrix.sh` | ✓ VERIFIED | Real POSIX-sh entrypoint; no placeholder text remains; Docker toolchain verification passed. |
| `packages/openai-contract/generated/hive-openapi.yaml` | ✓ VERIFIED | Generated artifact contains `url: /v1`, `x-hive-status`, and no `https://api.openai.com/v1` or `/organization/` strings. |
| `docs/support-matrix.md` | ✓ VERIFIED | Generated from `support-matrix.json` and clearly marked with provenance. |
| `apps/edge-api/cmd/server/main.go` | ✓ VERIFIED | `OPENAPI_SPEC_PATH` now defaults to `/app/packages/openai-contract/generated/hive-openapi.yaml`. |
| `apps/edge-api/docs/swagger_test.go` | ✓ VERIFIED | Covers `/docs/`, `/docs/openapi.yaml`, and missing-spec behavior. |
| `apps/edge-api/cmd/server/main_test.go` | ✓ VERIFIED | Guards the default generated-spec path and env override behavior. |
| `deploy/docker/Dockerfile.edge-api` | ✓ VERIFIED | Copies `packages/openai-contract/generated/hive-openapi.yaml` into the runtime image instead of the raw upstream spec. |
| `deploy/docker/docker-compose.override.yml` | ✓ VERIFIED | Syncs `../../packages/openai-contract` into `/app/packages/openai-contract` for local Docker development. |
| `deploy/docker/Dockerfile.toolchain` | ✓ VERIFIED | Includes `py3-yaml`, allowing Docker verification to run the Python generator. |

## Requirements Coverage

| Requirement | Description | Status | Evidence |
| --- | --- | --- | --- |
| `COMP-01` | Official OpenAI JS/Python/Java SDKs work against Hive by changing base URL and API key | ✓ SATISFIED | Unchanged from the previously verified plan `01-03` harness and not regressed by `01-04`. |
| `COMP-02` | OpenAI-style status codes, error objects, and compatibility headers | ✓ SATISFIED | Fresh `/v1/models` probe still returns compatibility headers; error/middleware paths were unchanged in `01-04`. |
| `COMP-03` | Swagger/OpenAPI docs match the Hive public API contract and supported launch surface | ✓ SATISFIED | Fresh `/docs/openapi.yaml` runtime probe and generator/toolchain verification confirm the served contract is Hive-specific and matrix-derived. |
| `API-08` | Unsupported public non-org/admin endpoints are classified and return explicit unsupported responses | ✓ SATISFIED | Matrix-driven classification remains intact and the generated contract now mirrors that public classification in docs as well. |

## Issues Encountered

None during final phase verification. The only prior gap (`COMP-03`) is resolved.

## Conclusion

Phase 01 now satisfies its original phase goal. The compatibility harness, runtime contract enforcement, and generated documentation surface are aligned: the runtime enforces `support-matrix.json`, the published docs are derived from the same source, and the built container serves the generated Hive contract by default.

---

_Verified: 2026-03-29T03:45:34Z_  
_Verifier: Codex (manual phase re-verification after agent fallback)_
