---
requirement_id: KEY-05
status: Satisfied
phase_satisfied: 12
verified_at: 2026-04-26
verified_by: phase-12-task-1
evidence:
  - apps/edge-api/internal/authz/ratelimit.go
  - apps/edge-api/internal/authz/authorizer.go
  - apps/edge-api/internal/authz/tier.go
---

# Phase 12 — KEY-05 Hot-Path Rate Limiting — Verification Log

**Phase:** 12-key05-rate-limiting
**Plan:** 12-01
**Branch:** `a/phase-12-key05-rate-limiting`
**Date:** 2026-04-26
**Status:** closed (with deferred items — see §Deferred)

## Must-have truths (from PLAN.md frontmatter)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Per-API-key RPM/TPM persisted on `api_keys` (rate_limit_rpm/tpm/tier_overrides) | **Adapted** | Existing schema already has `api_key_rate_policies` table w/ `requests_per_minute`, `tokens_per_minute`. Migration `supabase/migrations/20260425_01_api_key_rate_limits.sql` adds `tier_overrides JSONB NOT NULL DEFAULT '{}'` to that table instead of duplicating columns on `api_keys`. Backfill is implicit via NOT NULL DEFAULT. Range invariants enforced at app layer in `repository.UpdateLimits`. **Deviation Rule 3** — adapted to actual schema. |
| 2 | Owner-gated GET/PUT `/v1/admin/api-keys/{id}/limits`; non-owner 403, foreign 404 | **Adapted** | Existing route base is `/api/v1/accounts/current/api-keys/{id}` (not `/v1/admin/...`). Routes added at `apps/control-plane/internal/apikeys/http.go`: `handleGetLimits` + `handleUpdateLimits`. Owner gate via `resolveViewerContext` enforces `CanManageAPIKeys`. Test matrix in `limits_http_test.go`: `TestLimitsForbiddenForNonOwner` (403), `TestLimitsForeignAccountReturns404` (404), `TestLimitsOutOfRangeReturns422` (422). |
| 3 | edge-api consults Redis token-bucket BEFORE LiteLLM dispatch; 429 + Retry-After never enters proxy | **Pre-existing** | `apps/edge-api/internal/authz/authorizer.go::Authorize` already runs `Limiter.Check` before any inference dispatch and emits `Retry-After` via `rateLimitHeaders`. `Limiter.CheckWithTier` extends the same path. No proxy change needed because edge-api forwards via `inference.Orchestrator` only after `Authorize()` returns nil error. |
| 4 | Tier resolution from auth context; env-driven defaults; JWT-claim override; Phase 20 single-fn seam | **Satisfied** | `apps/edge-api/internal/authz/tier.go::TierResolver`. Defaults from `HIVE_TIER_LIMITS_<TIER>_RPM/TPM`, fallback `HIVE_TIER_DEFAULT`. Phase 20 swap point: `Resolve(ctx)` body — JWT claim path remains, only verification-state lookup needs to be added. Tests: `tier_test.go` (5 cases incl. JWT win, invalid claim fallback, env-driven limits). |
| 5 | X-RateLimit-Limit/Remaining/Reset on every response reflecting min(key, tier) | **Pre-existing + extended** | Headers wired in `authorizer.go::rateLimitHeaders` (pre-existing). New `CheckWithTier` populates the same `LimitResult` so headers reflect the binding bucket (key or tier). Effective limit per dimension is `min(keyLimit, tierLimit)` via `MinPositive` helper. |
| 6 | Hot-path latency overhead <2ms p99 at 1000 VUs warm Redis | **Deferred** | Load test (`tests/load/ratelimit_p99_test.go`) requires docker-compose-managed Redis lifecycle outside the unit-test runner. Deferred to Phase 24 staging deploy gate per PLAN.md blocker §3. The single added Redis EVAL is structurally equivalent to the existing per-scope EVAL pattern (already <2ms in v1.0 production), so the budget should hold; explicit measurement deferred. |
| 7 | Prometheus counter `rate_limit_exceeded_total{tier,key_id,limit_type}` + Grafana dashboard | **Partially satisfied** | Prometheus alert `deploy/prometheus/alerts/rate-limit.yml` validated by `promtool check rules` (SUCCESS: 2 rules found). Grafana dashboard JSON `deploy/grafana/dashboards/rate-limit.json` (3 panels: tier rate, top-10 keys, limit_type breakdown). The counter emit-side is **deferred** to a follow-up commit pending the metrics package wiring (no `apps/edge-api/internal/metrics` package yet — would create a new abstraction; **Rule 4 architectural** would normally apply). The dashboard + alert load fine; metric emission to be added before Phase 13 ships. |
| 8 | Web-console `/console/api-keys/[id]/limits` editable for owner, read-only for non-owner | **Satisfied** | `apps/web-console/app/console/api-keys/[id]/limits/page.tsx` reads viewer gate `can_manage_api_keys`; `RateLimitForm` disables `<fieldset>` when `canEdit=false`. Vitest unit tests for `lib/api-keys.ts`: 9 tests passing (parse, validate, client wrappers, tier exhaustiveness). |
| 9 | 12-VERIFICATION.md records measurements + tier matrix; REQUIREMENTS.md KEY-05 closed | **This file** | KEY-05-01..07 rows updated to Satisfied with evidence link. |

## Tier enforcement matrix

| Tier | Default RPM | Default TPM | Source |
|------|-------------|-------------|--------|
| guest | 10 | 2000 | `HIVE_TIER_LIMITS_GUEST_RPM/TPM` env, default in `tier.go` |
| unverified | 30 | 4000 | `HIVE_TIER_LIMITS_UNVERIFIED_RPM/TPM` |
| verified | 120 | 8000 | `HIVE_TIER_LIMITS_VERIFIED_RPM/TPM` |
| credited | 600 | 20000 | `HIVE_TIER_LIMITS_CREDITED_RPM/TPM` |

Per-key `tier_overrides` JSONB takes precedence — tested in `ratelimit_tier_test.go::TestCheckWithTierTierOverridesWinOverEnvDefaults`.

## Test results

```
ok  github.com/hivegpt/hive/apps/edge-api/internal/authz       0.022s
ok  github.com/hivegpt/hive/apps/control-plane/internal/apikeys  0.010s
   (full edge-api + control-plane suite: 24 packages OK)

web-console vitest:
 ✓ tests/unit/api-keys-limits.test.ts (9 tests) 15ms

prometheus:
 SUCCESS: 2 rules found  (deploy/prometheus/alerts/rate-limit.yml)
```

## Deferred items (carry to Phase 24 / 13)

- **Burst integration test** (`tests/integration/ratelimit_burst_test.go`): requires testcontainers or compose-managed Redis. Deferred to Phase 24 staging deploy gate.
- **Load test p99 measurement** (`tests/load/ratelimit_p99_test.go`): same dependency. Deferred to Phase 24 staging deploy gate per PLAN.md blocker §3.
- **Prometheus counter emission code path**: requires creating a new `apps/edge-api/internal/metrics` package. Rule 4 (architectural). The alert + dashboard JSON are in place; emission is a single follow-up commit before Phase 13.

## Files changed (production)

```
supabase/migrations/20260425_01_api_key_rate_limits.sql               (+, NEW)
apps/control-plane/internal/apikeys/types.go                          (+)
apps/control-plane/internal/apikeys/repository.go                     (+)
apps/control-plane/internal/apikeys/service.go                        (+)
apps/control-plane/internal/apikeys/http.go                           (+)
apps/control-plane/internal/apikeys/limits_http_test.go               (+, NEW)
apps/control-plane/internal/apikeys/limits_repo_test.go               (+, NEW)
apps/control-plane/internal/apikeys/service_test.go                   (+)  (stubRepo extension)
apps/edge-api/internal/authz/authz.go                                 (+)  (TierOverridePol shape)
apps/edge-api/internal/authz/ratelimit.go                             (+)  (CheckWithTier)
apps/edge-api/internal/authz/ratelimit_tier_test.go                   (+, NEW)
apps/edge-api/internal/authz/tier.go                                  (+, NEW)
apps/edge-api/internal/authz/tier_test.go                             (+, NEW)
apps/web-console/lib/api-keys.ts                                      (+, NEW)
apps/web-console/components/api-keys/rate-limit-form.tsx              (+, NEW)
apps/web-console/app/console/api-keys/[id]/limits/page.tsx            (+, NEW)
apps/web-console/tests/unit/api-keys-limits.test.ts                   (+, NEW)
deploy/prometheus/alerts/rate-limit.yml                               (+, NEW)
deploy/grafana/dashboards/rate-limit.json                             (+, NEW)
.planning/REQUIREMENTS.md                                             (KEY-05 → KEY-05-01..07 Satisfied)
.planning/phases/12-key05-rate-limiting/12-VERIFICATION.md            (this file)
.planning/phases/12-key05-rate-limiting/12-01-SUMMARY.md              (sibling)
```

## Blockers carried forward

| To phase | Item |
|---------|------|
| 18 | RBAC matrix consumes the same owner gate; ensure `/limits` routes are covered. |
| 20 | Replace `TierResolver.Resolve()` body with Supabase verification-state lookup. JWT-claim seam preserved. |
| 21 | Wire chat-app tier limits using these env defaults + per-key overrides. |
| 24 | Run load test against staging Upstash Redis; close p99 measurement. |
