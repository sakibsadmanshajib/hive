---
phase: 04-model-catalog-provider-routing
plan: 03
subsystem: routing
tags: [routing, usage, errors, control-plane, edge-api, go]

# Dependency graph
requires:
  - phase: 04-model-catalog-provider-routing (plan 02)
    provides: "Internal route selection, LiteLLM route handles, and provider-blind public catalog surfaces"
provides:
  - "Cache-aware provider usage normalized into Hive's existing cache token fields"
  - "Customer-visible usage responses that omit meaningless zero cache fields"
  - "Provider-blind upstream error sanitization in both control-plane routing helpers and edge OpenAI error responses"
affects: [05-api-keys-hot-path-enforcement, 06-core-text-embeddings-api, 09-developer-console-operational-hardening]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Cache-aware provider accounting feeds existing `cache_read_tokens` and `cache_write_tokens` instead of creating a second public usage shape"
    - "Provider-blind error translation logic is mirrored at the edge boundary so public errors never depend on control-plane internals"

key-files:
  created:
    - apps/control-plane/internal/routing/normalizer.go
    - apps/control-plane/internal/routing/normalizer_test.go
    - apps/control-plane/internal/routing/sanitize.go
    - apps/control-plane/internal/routing/sanitize_test.go
    - apps/edge-api/internal/errors/provider_blind.go
    - apps/edge-api/internal/errors/provider_blind_test.go
  modified:
    - apps/control-plane/internal/usage/http.go
    - apps/control-plane/internal/usage/http_test.go

key-decisions:
  - "Cache-read and cache-write attribution stays on the existing usage-event schema so future endpoints do not need a second customer-facing accounting contract"
  - "Edge provider-blind error writing duplicates the sanitization rules locally rather than importing control-plane routing code across service boundaries"

patterns-established:
  - "Usage shaping pattern: optional cache fields are emitted only when providers actually reported cache-aware token semantics"
  - "Provider-blind translation pattern: route handles, provider names, and provider model groups are rewritten before customer-visible errors are serialized"

requirements-completed:
  - ROUT-03

# Metrics
duration: 5min
completed: 2026-03-31
---

# Phase 04 Plan 03: Cache-Aware Usage And Provider-Blind Error Translation Summary

**Cache-aware usage normalization, conditional customer cache fields, and provider-blind upstream error translation across control-plane and edge APIs**

## Performance

- **Duration:** 5 min
- **Started:** 2026-03-31T04:01:55-04:00
- **Completed:** 2026-03-31T04:06:21-04:00
- **Tasks:** 2/2 complete
- **Files modified:** 8

## Accomplishments

- Added `NormalizeCacheUsage` so cache-aware provider billing can flow into Hive's existing `cache_read_tokens` and `cache_write_tokens` fields.
- Updated current-account usage responses to omit zero cache fields while preserving the existing non-cache response contract.
- Added provider-blind sanitization and upstream error writers so route handles and provider names do not leak through customer-visible failures.

## Task Commits

Each task was committed atomically:

1. **Task 1: Normalize provider cache usage into Hive cache token categories and omit unsupported cache fields from customer responses** - `5ecb1c3` (test), `918960c` (feat)
2. **Task 2: Add provider-blind sanitization helpers and edge-side upstream error translation** - `ae57ecc` (test), `5dfae15` (feat)

## Files Created/Modified

- `apps/control-plane/internal/routing/normalizer.go` - Maps provider cache-aware usage fields onto Hive's existing cache token categories.
- `apps/control-plane/internal/usage/http.go` - Emits cache fields only when they have meaningful non-zero values.
- `apps/control-plane/internal/routing/sanitize.go` - Rewrites provider names and internal route handles before messages cross a customer-visible boundary.
- `apps/edge-api/internal/errors/provider_blind.go` - Serializes sanitized OpenAI-style upstream errors with status-to-code mapping.
- `apps/edge-api/internal/errors/provider_blind_test.go` - Verifies provider-blind output and rate-limit/unavailable error mapping.

## Decisions Made

- Reused the current usage-event schema instead of inventing new cache-accounting keys, which keeps future endpoint usage payloads compatible with Phase 3 consumers.
- Kept the edge error translation logic self-contained in `apps/edge-api/internal/errors` so the public API boundary does not import control-plane routing internals.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- The host shell does not provide `gofmt`, so formatting for this plan was run through the existing Docker toolchain container.

## User Setup Required

None - this plan only changed internal normalization and public error-shaping behavior.

## Next Phase Readiness

- Phase 05 can now rely on provider-blind routing and usage-accounting surfaces without exposing upstream route handles through API-key enforcement paths.
- Phase 06 can build endpoint adapters on top of normalized cache usage and reusable provider-blind upstream error serialization.

## Self-Check

- [x] `apps/control-plane/internal/routing/normalizer.go` contains `type ProviderUsageInput`
- [x] `apps/control-plane/internal/routing/normalizer.go` contains `CacheCreationInputTokens`
- [x] `apps/control-plane/internal/routing/normalizer.go` contains `PromptCachedTokens`
- [x] `apps/control-plane/internal/usage/http_test.go` contains `TestListEventsOmitsCacheFieldsWhenZero`
- [x] `apps/control-plane/internal/usage/http_test.go` contains `TestListEventsIncludesCacheFieldsWhenPresent`
- [x] `apps/control-plane/internal/routing/sanitize.go` contains `func SanitizeProviderMessage`
- [x] `apps/control-plane/internal/routing/sanitize.go` contains `route-openrouter-default`
- [x] `apps/edge-api/internal/errors/provider_blind.go` contains `func WriteProviderBlindUpstreamError`
- [x] `apps/edge-api/internal/errors/provider_blind.go` contains `upstream_unavailable`
- [x] `apps/edge-api/internal/errors/provider_blind.go` contains `upstream_rate_limited`
- [x] `apps/edge-api/internal/errors/provider_blind_test.go` contains `TestWriteProviderBlindUpstreamErrorStripsProviderStrings`
- [x] `apps/control-plane/internal/routing/sanitize_test.go` contains `TestSanitizeProviderMessageUsesAliasWhenAvailable`
- [x] `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/usage/... ./internal/routing/... -count=1"` passed
- [x] `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/errors/... -count=1"` passed
