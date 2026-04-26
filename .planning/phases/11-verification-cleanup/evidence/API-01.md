---
requirement_id: API-01
status: satisfied
verified_at: 2026-04-25
verified_by: gsd-execute-phase agent (Phase 11 Task 1)
phase_satisfied: 06-core-text-embeddings-api
evidence:
  code_paths:
    - apps/edge-api/internal/inference/chat_completions.go
    - apps/edge-api/internal/inference/completions.go
    - apps/edge-api/internal/inference/handler.go
    - apps/edge-api/internal/inference/orchestrator.go
    - apps/edge-api/internal/inference/litellm_client.go
    - packages/openai-contract/
    - packages/sdk-tests/js/
    - packages/sdk-tests/python/
    - packages/sdk-tests/java/
  integration_tests:
    - cd deploy/docker && docker compose --env-file ../../.env --profile test up --build
    - cd deploy/docker && docker compose --profile tools run toolchain bash -c "cd /workspace && go test ./apps/edge-api/internal/inference/... -count=1 -short"
  summary_refs:
    - .planning/phases/06-core-text-embeddings-api/06-01-SUMMARY.md
    - .planning/phases/06-core-text-embeddings-api/06-02-SUMMARY.md
    - .planning/phases/06-core-text-embeddings-api/06-VERIFICATION.md
---

# API-01 Evidence — chat/completions, completions, responses

## Behavior

A developer can call `POST /v1/responses`, `POST /v1/chat/completions`, and
`POST /v1/completions` against Hive with OpenAI-compatible request + response
shapes. Hive's OpenAI-compatible contract is encoded in
`packages/openai-contract/`; the official OpenAI JS, Python, and Java SDKs
exercise it via `packages/sdk-tests/`.

## Code paths

- `apps/edge-api/internal/inference/chat_completions.go` — chat/completions
  handler.
- `apps/edge-api/internal/inference/completions.go` — completions handler.
- `apps/edge-api/internal/inference/handler.go` — shared request/response
  plumbing.
- `apps/edge-api/internal/inference/orchestrator.go` — provider-blind dispatch.
- `apps/edge-api/internal/inference/litellm_client.go` — LiteLLM upstream
  bridge.
- `packages/openai-contract/` — OpenAI contract source of truth.
- `packages/sdk-tests/{js,python,java}/` — official SDK integration tests.

## Reproduce

```bash
# Full SDK harness against the live stack
cd deploy/docker && docker compose --env-file ../../.env --profile test up --build

# Or just the Go unit tests for the inference package
cd deploy/docker && docker compose --profile tools run toolchain bash -c \
  "cd /workspace && go test ./apps/edge-api/internal/inference/... -count=1 -short"
```

## Phase 06 summary references

- `06-01-SUMMARY.md` — chat/completions + completions handlers shipped.
- `06-02-SUMMARY.md` — responses surface + harness wiring.
- `06-VERIFICATION.md` — phase-level verification log.

## Known Caveats

None for API-01. SSE-specific behavior is tracked under API-02.
