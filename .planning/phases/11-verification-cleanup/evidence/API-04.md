---
requirement_id: API-04
status: satisfied
verified_at: 2026-04-25
verified_by: gsd-execute-phase agent (Phase 11 Task 1)
phase_satisfied: 06-core-text-embeddings-api
evidence:
  code_paths:
    - apps/edge-api/internal/inference/chat_completions.go
    - apps/edge-api/internal/inference/orchestrator.go
    - apps/edge-api/internal/inference/litellm_client.go
    - packages/openai-contract/
    - packages/sdk-tests/js/
    - packages/sdk-tests/python/
  integration_tests:
    - cd deploy/docker && docker compose --env-file ../../.env --profile test up --build
    - cd deploy/docker && docker compose --profile tools run toolchain bash -c "cd /workspace && go test ./apps/edge-api/internal/inference/... -count=1 -short"
  summary_refs:
    - .planning/phases/06-core-text-embeddings-api/06-02-SUMMARY.md
    - .planning/phases/06-core-text-embeddings-api/06-VERIFICATION.md
---

# API-04 Evidence — reasoning / thinking parameters + translated outputs

## Behavior

A developer can pass reasoning- or thinking-related request parameters
(`reasoning_effort`, `reasoning`, `thinking`, etc.) to supported text-generation
endpoints. Hive translates upstream-provider reasoning outputs + usage details
into OpenAI-compatible response fields when upstream support exists, and
preserves provider-blind error handling when not.

## Code paths

- `apps/edge-api/internal/inference/chat_completions.go` — accepts reasoning
  params + propagates translated outputs.
- `apps/edge-api/internal/inference/orchestrator.go` — provider-blind
  reasoning translation.
- `apps/edge-api/internal/inference/litellm_client.go` — upstream reasoning
  surface bridging.
- `packages/openai-contract/` — reasoning fields documented in the contract.
- `packages/sdk-tests/{js,python}/` — SDK reasoning round-trip tests.

## Reproduce

```bash
# SDK harness reasoning tests
cd deploy/docker && docker compose --env-file ../../.env --profile test up --build

# Go unit tests for reasoning translation
cd deploy/docker && docker compose --profile tools run toolchain bash -c \
  "cd /workspace && go test ./apps/edge-api/internal/inference/... -count=1 -short"
```

## Phase 06 summary references

- `06-02-SUMMARY.md` — reasoning parameter parity.
- `06-VERIFICATION.md` — phase-level verification log.

## Known Caveats

Reasoning output coverage tracks upstream provider capability — not all aliased
models surface reasoning. Provider-blind errors are emitted when an upstream
declines a reasoning param; this is part of the API-04 contract, not a defect.
