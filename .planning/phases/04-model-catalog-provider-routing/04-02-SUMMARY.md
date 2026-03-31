---
phase: 04-model-catalog-provider-routing
plan: 02
subsystem: routing
tags: [routing, litellm, control-plane, postgres, go]

# Dependency graph
requires:
  - phase: 04-model-catalog-provider-routing (plan 01)
    provides: "Seeded public aliases, the control-plane snapshot layer, and the provider-blind public catalog boundary"
provides:
  - "Provider-route, capability, and alias-policy schema for internal route selection"
  - "Deterministic capability-filtered route selection with allowlists and fallback-order enforcement"
  - "Internal `/internal/routing/select` contract and LiteLLM route-group config keyed by private route handles"
affects: [04-03, 05-api-keys-hot-path-enforcement, 06-core-text-embeddings-api, 09-developer-console-operational-hardening]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Hive applies alias policy, capability filtering, and allowlists before any LiteLLM routing decision"
    - "LiteLLM model groups are keyed by internal route handles, never by public alias IDs"

key-files:
  created:
    - supabase/migrations/20260331_02_routing_policy.sql
    - apps/control-plane/internal/routing/types.go
    - apps/control-plane/internal/routing/repository.go
    - apps/control-plane/internal/routing/service.go
    - apps/control-plane/internal/routing/http.go
    - deploy/litellm/config.yaml
  modified:
    - apps/control-plane/internal/catalog/types.go
    - apps/control-plane/internal/catalog/repository.go
    - apps/control-plane/internal/platform/http/router.go
    - apps/control-plane/cmd/server/main.go
    - .env.example
    - deploy/docker/docker-compose.yml

key-decisions:
  - "Alias policy and capability checks happen inside Hive before LiteLLM receives a route handle"
  - "Internal route handles such as `route-openrouter-default` remain private and become the only LiteLLM-facing identifiers"

patterns-established:
  - "Routing selection pattern: allowlists and capability flags filter candidates before health and fallback ordering are applied"
  - "Private-route config pattern: Compose and LiteLLM configuration use internal route handles, keeping public aliases decoupled from provider adapter naming"

requirements-completed:
  - ROUT-02

# Metrics
duration: 16min
completed: 2026-03-31
---

# Phase 04 Plan 02: Internal Routing Contract Summary

**Provider-route schema, deterministic alias policy selection, and a LiteLLM-backed internal routing contract keyed by private route handles**

## Performance

- **Duration:** 16 min
- **Started:** 2026-03-31T03:40:27-04:00
- **Completed:** 2026-03-31T03:56:25-04:00
- **Tasks:** 2/2 complete
- **Files modified:** 14

## Accomplishments

- Added durable provider-route, capability, and alias-policy tables so routing policy can be stored and seeded in the control-plane instead of being implied by provider strings.
- Built a new `internal/routing` package that filters route candidates by alias, capability flags, provider allowlists, health state, and price-class widening rules before returning a route and fallback chain.
- Exposed `POST /internal/routing/select` for later edge execution phases and added LiteLLM config/model groups keyed by private route handles rather than public aliases.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add routing-policy schema, capability records, and deterministic route selection logic** - `7df3736` (test), `76bd52f` (feat)
2. **Task 2: Add LiteLLM route-group config and expose internal route selection over HTTP** - `793a331` (test), `82b9d9c` (feat)

## Files Created/Modified

- `supabase/migrations/20260331_02_routing_policy.sql` - Defines internal provider routes, capability records, and alias-level fallback policy state.
- `apps/control-plane/internal/routing/service.go` - Implements the ordered route selector and fallback eligibility logic.
- `apps/control-plane/internal/routing/http.go` - Exposes the internal route-selection contract used by later execution phases.
- `apps/control-plane/internal/catalog/repository.go` - Extends the internal catalog snapshot with route and alias-policy metadata for internal consumers.
- `deploy/litellm/config.yaml` - Maps private route handles onto LiteLLM model groups sourced from environment variables.
- `deploy/docker/docker-compose.yml` - Adds the LiteLLM service and its config mount to the development stack.

## Decisions Made

- Kept route eligibility inside Hive so capability flags, alias allowlists, and widening rules are evaluated before LiteLLM fallback behavior begins.
- Used seeded fallback order arrays plus priority as the deterministic route ordering mechanism, which keeps latency/cost/stability policy behavior explicit and testable.
- Registered `/internal/routing/select` without auth as an internal control-plane contract, matching the earlier snapshot-route pattern used for model catalog data.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- The `toolchain` image already defines `ENTRYPOINT ["/bin/sh","-c"]`, so Docker verification in this repo must pass the command string directly rather than wrapping it in another `sh -lc`.

## User Setup Required

None - the new LiteLLM-related variables were added to `.env.example`, but no additional dashboard setup was required for this plan.

## Next Phase Readiness

- Phase 04-03 can now normalize cache-aware usage and provider-blind errors against a real internal route selection contract.
- Phase 06 can later ask the control-plane for an eligible internal route without exposing provider model IDs on the public API.

## Self-Check

- [x] `supabase/migrations/20260331_02_routing_policy.sql` contains `create table public.provider_routes`
- [x] `supabase/migrations/20260331_02_routing_policy.sql` contains `create table public.provider_capabilities`
- [x] `supabase/migrations/20260331_02_routing_policy.sql` contains `route-openrouter-default`
- [x] `apps/control-plane/internal/routing/types.go` contains `type SelectionInput`
- [x] `apps/control-plane/internal/routing/service.go` contains `func (s *Service) SelectRoute`
- [x] `apps/control-plane/internal/routing/service.go` contains `AllowedAliases`
- [x] `apps/control-plane/internal/routing/http.go` contains `/internal/routing/select`
- [x] `apps/control-plane/internal/platform/http/router.go` contains `/internal/routing/select`
- [x] `.env.example` contains `LITELLM_MASTER_KEY=litellm-dev-key`
- [x] `.env.example` contains `OPENROUTER_FAST_FALLBACK_MODEL=`
- [x] `deploy/docker/docker-compose.yml` contains `4000:4000`
- [x] `deploy/docker/docker-compose.yml` contains `../../deploy/litellm/config.yaml:/app/config.yaml`
- [x] `deploy/litellm/config.yaml` contains `route-openrouter-default`
- [x] `deploy/litellm/config.yaml` contains `route-groq-fast`
- [x] `docker compose --env-file .env -f deploy/docker/docker-compose.yml config --services` lists `litellm`
- [x] `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/routing/... -count=1"` passed
