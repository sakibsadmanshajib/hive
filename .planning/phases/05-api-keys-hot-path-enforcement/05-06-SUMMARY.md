---
phase: 05-api-keys-hot-path-enforcement
plan: 06
subsystem: edge-api
tags: [edge-api, authz, redis, rate-limits, lua, go]
provides:
  - Separate account-scope and key-scope hot-path limiter enforcement on the edge
  - Redis Lua artifacts for sliding-window RPM/TPM and long-window quota checks
  - Retry metadata only on true rate-limit denials
affects: [05-verification, future-v1-routes]
tech-stack:
  added:
    - embedded Redis Lua scripts for edge hot-path limiter checks
  patterns:
    - "Limiter logic is separate from snapshot resolution; the client only resolves auth state"
    - "Only 429 rate-limit responses emit retry metadata headers"
key-files:
  created:
    - .planning/phases/05-api-keys-hot-path-enforcement/05-06-SUMMARY.md
    - apps/edge-api/internal/authz/ratelimit.go
    - apps/edge-api/internal/authz/ratelimit_test.go
    - apps/edge-api/internal/authz/authorizer_test.go
    - apps/edge-api/internal/authz/scripts/rpm_tpm.lua
    - apps/edge-api/internal/authz/scripts/window_score.lua
  modified:
    - apps/edge-api/internal/authz/authz.go
    - apps/edge-api/internal/authz/client.go
    - apps/edge-api/internal/authz/authorizer.go
    - apps/edge-api/internal/errors/openai.go
    - apps/edge-api/internal/errors/openai_test.go
    - apps/edge-api/cmd/server/main.go
    - apps/edge-api/cmd/server/main_test.go
key-decisions:
  - "The limiter consumes separate account and key policy objects; it never substitutes one scope for the other"
  - "Permanent failures stay on `WriteError`; only retryable `rate_limit_exceeded` paths go through `WriteRateLimitError`"
patterns-established:
  - "`/v1/models` reuses the same authorizer/limiter path as future billable routes, but passes zero-cost inputs explicitly"
duration: 48min
completed: 2026-04-02
---

# Phase 05 Plan 06: Edge Limiter And Retry Metadata Summary

**The edge now enforces separate account and key thresholds with Redis-backed limiter artifacts, and only genuine 429 denials emit rate-limit headers**

## Performance

- **Duration:** 48 min
- **Tasks:** 2
- **Files modified:** 12

## Accomplishments

- Added `Limiter.Check`, embedded Lua scripts, and tests that prove separate account/key thresholds, weighted free-token scoring, and contract errors for missing account policy.
- Removed inline rate-limit logic from the auth client so snapshot resolution and limiter enforcement are cleanly separated.
- Changed the authorizer contract to return header metadata for retryable denials only, then wired server/error handling to emit `x-ratelimit-*` and `retry-after` only for `rate_limit_exceeded`.
- Added `/v1/models` regression coverage to verify the limiter is invoked with zero estimated cost, zero billable tokens, and zero free tokens for non-billable model listing.

## Task Commits

1. **Task 1: Add Redis Lua limiter artifacts that enforce separate account and key thresholds** - `10cd871`
2. **Task 2: Remove inline client-side limit logic and wire real 429 header handling through the authorizer** - `10cd871`

## Files Created/Modified

- `apps/edge-api/internal/authz/ratelimit.go` - limiter orchestration, key construction, weighted long-window scoring, and Redis script execution.
- `apps/edge-api/internal/authz/ratelimit_test.go` - separate-scope threshold, weighted free-token, and missing-account-policy coverage.
- `apps/edge-api/internal/authz/scripts/rpm_tpm.lua` - 60-second sliding-window RPM/TPM script.
- `apps/edge-api/internal/authz/scripts/window_score.lua` - 5-hour and 7-day integer-only quota script.
- `apps/edge-api/internal/authz/authz.go` - separate `account_rate_policy` and `key_rate_policy` snapshot fields.
- `apps/edge-api/internal/authz/client.go` - snapshot-only responsibility, with no inline limiter behavior.
- `apps/edge-api/internal/authz/authorizer.go` - limiter invocation, reason-specific 429 messages, and header propagation.
- `apps/edge-api/internal/errors/openai.go` - `WriteRateLimitError`.
- `apps/edge-api/internal/errors/openai_test.go` - permanent-failure header omission coverage.
- `apps/edge-api/cmd/server/main.go` - limiter construction and 429-only retry-header writing.
- `apps/edge-api/cmd/server/main_test.go` - `/v1/models` limiter wiring coverage.

## Decisions & Deviations

- Redis limiter failures still fail open inside the authorizer so transient Redis problems do not become hard edge outages.
- The long-window limiter returns retry timing through the same header map used for request/token denials, keeping server/error wiring simple while staying header-clean on permanent failures.

## Next Phase Readiness

- Phase 5 verification can now assert true hot-path enforcement for separate account and key thresholds, plus correct retry metadata behavior.

## Self-Check

- [x] `apps/edge-api/internal/authz/client.go` no longer contains `CheckRateLimit`
- [x] `apps/edge-api/internal/errors/openai.go` contains `WriteRateLimitError`
- [x] `apps/edge-api/internal/authz/authorizer_test.go` contains `TestAuthorizeReturnsRateLimitHeadersOnlyFor429`
- [x] `apps/edge-api/internal/errors/openai_test.go` contains `TestPermanentFailuresOmitRetryHeaders`
- [x] `apps/edge-api/cmd/server/main_test.go` contains `TestModelsRouteUsesLimiter`
- [x] `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz ./apps/edge-api/internal/errors ./apps/edge-api/cmd/server -count=1"` passed
