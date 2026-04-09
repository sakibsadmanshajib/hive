---
phase: 06-core-text-embeddings-api
plan: 04
subsystem: api
tags: [go, inference, embeddings, openai, sdk-tests, typescript, python, integration-tests]

requires:
  - phase: 06-02
    provides: inference orchestrator, executeSync lifecycle, litellm dispatch
  - phase: 06-03
    provides: streaming, responses handler foundation

provides:
  - POST /v1/embeddings with OpenAI-compatible list response and capability gating
  - EmbeddingsRequest, EmbeddingsResponse, EmbeddingObject, EmbeddingsUsage types
  - JS SDK integration tests for chat/completions, completions, responses, embeddings, streaming
  - Python SDK integration tests for chat/completions and embeddings

affects: [SDK test execution, embeddings endpoint consumers]

tech-stack:
  added: []
  patterns:
    - "Pragmatic dimensions capability gating via model name heuristic (embedding-3 substring)"
    - "json.RawMessage for Embedding field to support both float arrays and base64 strings"
    - "normalizeEmbeddings reuses executeSync lifecycle with NeedEmbeddings flag"
    - "SDK tests use HIVE_BASE_URL env var with localhost:8080 default for local dev"
    - "OpenAI SDK ResponseOutputItem union type narrowing for content access"

key-files:
  created:
    - apps/edge-api/internal/inference/embeddings.go
    - apps/edge-api/internal/inference/embeddings_test.go
    - packages/sdk-tests/js/tests/chat-completions/chat-completions.test.ts
    - packages/sdk-tests/js/tests/completions/completions.test.ts
    - packages/sdk-tests/js/tests/responses/responses.test.ts
    - packages/sdk-tests/js/tests/embeddings/embeddings.test.ts
    - packages/sdk-tests/js/tests/streaming/streaming-chat.test.ts
    - packages/sdk-tests/python/tests/test_chat_completions.py
    - packages/sdk-tests/python/tests/test_embeddings.py
  modified:
    - apps/edge-api/internal/inference/types.go
    - apps/edge-api/internal/inference/handler.go
    - apps/edge-api/internal/inference/handler_test.go
    - packages/sdk-tests/js/package.json
    - packages/sdk-tests/js/tsconfig.json
    - packages/sdk-tests/js/tests/errors/unsupported-endpoint.test.ts
    - .gitignore

key-decisions:
  - "dimensions gating uses model name heuristic (contains 'embedding-3') rather than capability flag — pragmatic Phase 6 approach; future phase can add SupportsDimensions to routing types"
  - "EmbeddingObject.Embedding stays json.RawMessage to handle both float arrays and base64 encoding_format without type assertions"
  - "SDK tests are integration tests requiring live services; they use HIVE_BASE_URL env var with sensible defaults"
  - "Added @types/node to sdk-tests/js to fix pre-existing tsc type errors affecting all test files"

duration: 12min
completed: 2026-04-09
---

# Phase 06 Plan 04: Embeddings Endpoint and SDK Integration Tests Summary

**POST /v1/embeddings with capability gating plus 7 SDK integration test suites covering all four Phase 6 endpoints in JS and Python**

## Performance

- **Duration:** ~12 min
- **Tasks:** 2
- **Files modified:** 17 (9 created, 8 modified)

## Accomplishments

- `handleEmbeddings` implements the full `executeSync` lifecycle with `NeedEmbeddings: true`
- Validation: model required, input required and non-empty
- Capability gating: `dimensions` parameter rejected for non-embedding-3 models with `unsupported_parameter` code
- `normalizeEmbeddings`: overwrites model with Hive alias, enforces `object: "list"` and `data[].object: "embedding"`, converts `EmbeddingsUsage` to `UsageResponse`
- `/v1/embeddings` route wired in `handler.go` (501 placeholder replaced)
- Unit tests: missing model/input (400), dimensions gating on non-embedding-3 (400), dimensions allowed on embedding-3 models, normalize correctness, model alias replacement
- JS SDK test suites: 5 test files covering chat/completions, completions, responses, embeddings, and streaming (including `include_usage` terminal chunk)
- Python SDK test suites: 2 test files covering chat/completions (sync + streaming) and embeddings (single + batch)
- Fixed pre-existing `@types/node` gap in sdk-tests/js — all test files (including existing ones) now pass `tsc --noEmit`
- Fixed pre-existing `OpenAI.NotFoundError` type-as-value error in `unsupported-endpoint.test.ts`

## Task Commits

1. **Task 1: Embeddings endpoint handler** - `0953ea9` (feat)
2. **Task 2: SDK integration tests** - `79a0243` (feat)

## Files Created

- `apps/edge-api/internal/inference/embeddings.go` — POST /v1/embeddings handler + normalizer
- `apps/edge-api/internal/inference/embeddings_test.go` — Unit tests (7 test functions)
- `packages/sdk-tests/js/tests/chat-completions/chat-completions.test.ts` — JS chat/completions integration tests
- `packages/sdk-tests/js/tests/completions/completions.test.ts` — JS completions integration tests
- `packages/sdk-tests/js/tests/responses/responses.test.ts` — JS Responses API integration tests
- `packages/sdk-tests/js/tests/embeddings/embeddings.test.ts` — JS embeddings integration tests
- `packages/sdk-tests/js/tests/streaming/streaming-chat.test.ts` — JS streaming integration tests
- `packages/sdk-tests/python/tests/test_chat_completions.py` — Python chat/completions tests
- `packages/sdk-tests/python/tests/test_embeddings.py` — Python embeddings tests

## Files Modified

- `apps/edge-api/internal/inference/types.go` — Added EmbeddingsRequest, EmbeddingsResponse, EmbeddingObject, EmbeddingsUsage structs
- `apps/edge-api/internal/inference/handler.go` — Wired /v1/embeddings to handleEmbeddings
- `apps/edge-api/internal/inference/handler_test.go` — Updated embeddings placeholder test + responses handler test
- `packages/sdk-tests/js/package.json` — Added @types/node devDependency
- `packages/sdk-tests/js/tsconfig.json` — Added types: ["node"]
- `packages/sdk-tests/js/tests/errors/unsupported-endpoint.test.ts` — Fixed TS2749 type error
- `.gitignore` — Added node_modules/ and package-lock.json

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical Functionality] Added @types/node to resolve tsc type errors**
- **Found during:** Task 2 verification
- **Issue:** `process` global and `node:fs`/`node:path` modules unresolvable under strict TypeScript — affected ALL test files (pre-existing gap)
- **Fix:** Added `@types/node: ^22.0.0` to devDependencies and `"types": ["node"]` to tsconfig.json
- **Files modified:** packages/sdk-tests/js/package.json, packages/sdk-tests/js/tsconfig.json
- **Commit:** 79a0243

**2. [Rule 1 - Bug] Fixed OpenAI.NotFoundError used as type (TS2749)**
- **Found during:** Task 2 verification (tsc --noEmit)
- **Issue:** `err as OpenAI.NotFoundError` is invalid since NotFoundError is a value, not a type, in newer OpenAI SDK
- **Fix:** Changed to `err as InstanceType<typeof OpenAI.NotFoundError>`
- **Files modified:** packages/sdk-tests/js/tests/errors/unsupported-endpoint.test.ts
- **Commit:** 79a0243

**3. [Rule 3 - Blocking] Replaced handler_test TestHandler_EmbeddingsPlaceholder (expected 501) with TestHandler_Embeddings_MissingModel (expected 400)**
- **Found during:** Task 1 — the old test expected 501 (placeholder) but embeddings is now live
- **Fix:** Updated existing test to verify live endpoint behavior (missing model returns 400)
- **Files modified:** apps/edge-api/internal/inference/handler_test.go
- **Commit:** 0953ea9

## Verification Results

- `go build ./...` passes for edge-api — exit 0
- `go test ./internal/inference/ -count=1` passes — exit 0
- `npx tsc --noEmit --project packages/sdk-tests/js/tsconfig.json` passes — exit 0, no errors
- `python3 -m py_compile packages/sdk-tests/python/tests/test_chat_completions.py` passes
- `python3 -m py_compile packages/sdk-tests/python/tests/test_embeddings.py` passes

## Self-Check: PASSED
