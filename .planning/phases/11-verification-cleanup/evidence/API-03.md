---
requirement_id: API-03
status: satisfied
verified_at: 2026-04-25
verified_by: gsd-execute-phase agent (Phase 11 Task 1)
phase_satisfied: 06-core-text-embeddings-api
evidence:
  code_paths:
    - apps/edge-api/internal/inference/embeddings.go
    - apps/edge-api/internal/inference/embeddings_test.go
    - apps/edge-api/internal/inference/handler.go
    - apps/edge-api/internal/inference/orchestrator.go
    - apps/edge-api/internal/inference/litellm_client.go
    - packages/openai-contract/
    - packages/sdk-tests/js/
    - packages/sdk-tests/python/
    - supabase/migrations/20260423_01_embedding_alias.sql
    - supabase/migrations/20260424_01_api_key_policies_embedding_alias.sql
    - supabase/migrations/20260424_02_embedding_fallback_route.sql
  integration_tests:
    - cd deploy/docker && docker compose --env-file ../../.env --profile test up --build
    - cd deploy/docker && docker compose --profile tools run toolchain bash -c "cd /workspace && go test ./apps/edge-api/internal/inference/... -count=1 -short"
  summary_refs:
    - .planning/phases/06-core-text-embeddings-api/06-03-SUMMARY.md
    - .planning/phases/06-core-text-embeddings-api/06-04-SUMMARY.md
    - .planning/phases/06-core-text-embeddings-api/06-VERIFICATION.md
---

# API-03 Evidence — embeddings (OpenAI-compatible)

## Behavior

A developer can call `POST /v1/embeddings` against Hive with OpenAI-compatible
request + response behavior. Embeddings route via the same provider-blind
orchestrator + LiteLLM bridge as text generation. Routing supports embedding
aliases + fallback per the migrations listed above.

## Code paths

- `apps/edge-api/internal/inference/embeddings.go` — embeddings handler.
- `apps/edge-api/internal/inference/embeddings_test.go` — Go unit tests for
  contract parity.
- `apps/edge-api/internal/inference/handler.go` — shared response plumbing.
- `apps/edge-api/internal/inference/orchestrator.go` — provider-blind
  dispatch.
- `apps/edge-api/internal/inference/litellm_client.go` — LiteLLM bridge.
- `supabase/migrations/20260423_01_embedding_alias.sql` — embedding model
  aliases.
- `supabase/migrations/20260424_01_api_key_policies_embedding_alias.sql` —
  per-key policy.
- `supabase/migrations/20260424_02_embedding_fallback_route.sql` — fallback
  routing rules.

## Reproduce

```bash
# SDK harness embeddings tests
cd deploy/docker && docker compose --env-file ../../.env --profile test up --build

# Go unit tests
cd deploy/docker && docker compose --profile tools run toolchain bash -c \
  "cd /workspace && go test ./apps/edge-api/internal/inference/... -count=1 -short"
```

## Phase 06 summary references

- `06-03-SUMMARY.md` — embeddings handler + harness wiring.
- `06-04-SUMMARY.md` — embedding alias + fallback route migrations.
- `06-VERIFICATION.md` — phase-level verification log.

## Known Caveats

None for API-03.
