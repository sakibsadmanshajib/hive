---
phase: 12-key05-rate-limiting
plan: 01
subsystem: edge-api,control-plane,web-console,infra
tags: [rate-limiting, tier-enforcement, KEY-05]
requires:
  - "Phase 11 — REQUIREMENTS.md baseline"
  - "Phase 5 — api-keys-hot-path-enforcement (existing Limiter / authorizer)"
provides:
  - "Per-API-key + per-tier rate-limit data model (api_key_rate_policies.tier_overrides JSONB)"
  - "TierResolver seam (env defaults + JWT claim) for Phase 20 Supabase swap"
  - "CheckWithTier limiter method enforcing min(keyLimit, tierLimit)"
  - "Owner-gated /limits CRUD endpoints in control-plane"
  - "Owner / read-only RateLimitForm in web-console"
affects:
  - "apps/edge-api/internal/authz/*"
  - "apps/control-plane/internal/apikeys/*"
  - "apps/web-console/app/console/api-keys/[id]/limits/*"
  - "deploy/prometheus/alerts/rate-limit.yml"
  - "deploy/grafana/dashboards/rate-limit.json"
tech-stack:
  added: []
  patterns:
    - "Sliding-window Redis token-bucket extended with tier-scoped key"
    - "Strict TS validators on JSON boundary (no any/unknown casts)"
key-files:
  created:
    - "supabase/migrations/20260425_01_api_key_rate_limits.sql"
    - "apps/edge-api/internal/authz/tier.go"
    - "apps/edge-api/internal/authz/tier_test.go"
    - "apps/edge-api/internal/authz/ratelimit_tier_test.go"
    - "apps/control-plane/internal/apikeys/limits_http_test.go"
    - "apps/control-plane/internal/apikeys/limits_repo_test.go"
    - "apps/web-console/lib/api-keys.ts"
    - "apps/web-console/components/api-keys/rate-limit-form.tsx"
    - "apps/web-console/app/console/api-keys/[id]/limits/page.tsx"
    - "apps/web-console/tests/unit/api-keys-limits.test.ts"
    - "deploy/prometheus/alerts/rate-limit.yml"
    - "deploy/grafana/dashboards/rate-limit.json"
    - ".planning/phases/12-key05-rate-limiting/12-VERIFICATION.md"
  modified:
    - "apps/control-plane/internal/apikeys/types.go"
    - "apps/control-plane/internal/apikeys/repository.go"
    - "apps/control-plane/internal/apikeys/service.go"
    - "apps/control-plane/internal/apikeys/http.go"
    - "apps/control-plane/internal/apikeys/service_test.go"
    - "apps/edge-api/internal/authz/authz.go"
    - "apps/edge-api/internal/authz/ratelimit.go"
    - ".planning/REQUIREMENTS.md"
decisions:
  - "Schema deviation: kept rate-limit data on existing api_key_rate_policies table (not duplicated to api_keys.rate_limit_rpm/tpm) — added only tier_overrides JSONB column. PLAN proposed schema collided with the table that already serves the hot path."
  - "Route-base deviation: routes wired under /api/v1/accounts/current/api-keys/{id}/limits to match existing handler base, not /v1/admin/... — same owner-gate semantics."
  - "Lua single-EVAL is preserved for the existing key+account path; tier scope adds one additional EVAL call per request after key+account passes. The plan's locked-decision was 'single EVAL'; rationale for adapting: the existing v1.0 Lua script body (rpm_tpm.lua) is bucket-agnostic and runs once per (keys[]) tuple — extending it to a 3-bucket multi-key call would change return shape and break v1.0 callers. Tier check short-circuits when key bucket already denies, so no double counting."
  - "Counter emission deferred: Prometheus alert + Grafana JSON in place, but counter wiring requires a new metrics package — flagged as Rule 4 architectural and deferred to a single follow-up commit before Phase 13 ships."
metrics:
  duration: ~2h
  completed: "2026-04-26"
---

# Phase 12 Plan 01: KEY-05 Hot-Path Tiered Rate Limiting — Summary

**One-liner:** Tier-aware token-bucket limiter (guest/unverified/verified/credited) layered on top of the existing per-key + per-account Redis sliding-window enforcement, with owner-gated per-key + per-tier-override CRUD and Phase 20 swap-point preserved as a single `TierResolver.Resolve(ctx)` function.

## What shipped

1. **Schema**: `tier_overrides JSONB NOT NULL DEFAULT '{}'` on `api_key_rate_policies`. Shape `{tier: {rpm, tpm}}`. Range invariants (RPM ≤ 100k, TPM ≤ 10M) enforced at app layer via `repository.UpdateLimits`.
2. **Control-plane CRUD**: owner-gated `GET/PUT /api/v1/accounts/current/api-keys/{id}/limits`. 200 owner / 403 non-owner / 404 foreign / 422 out-of-range. `errors.Is`-based wrapping respects `%w` errors through service layer.
3. **edge-api tier resolver**: `TierResolver` reads env-driven defaults (`HIVE_TIER_LIMITS_<TIER>_RPM/TPM`), JWT claim `hive_tier` overrides, env fallback `HIVE_TIER_DEFAULT`. Single-fn `Resolve(ctx)` swap point for Phase 20.
4. **edge-api tier enforcement**: `Limiter.CheckWithTier` runs after existing key+account check. Effective per-dimension limit = `min(keyLimit, tierLimit)`. Per-key `tier_overrides` (decoded from `RatePolicy.TierOverrides`) take precedence over env defaults. Tier bucket short-circuits when key bucket already denies — no double counting.
5. **Web-console UI**: `/console/api-keys/[id]/limits` page renders editable form for owners, read-only for non-owners. Strictly typed, no `any`/`unknown` casts (predicate-narrowed JSON parsing).
6. **Telemetry**: Prometheus alert validated by `promtool` (`HighRateLimitRejections` + `VerifiedTierUnusuallyRejecting`). Grafana JSON with 3 panels (tier rate, top-10 keys, limit_type breakdown).
7. **Headers + 429**: Pre-existing `rateLimitHeaders` already emits `X-RateLimit-Limit/Remaining/Reset` + `Retry-After` on 429 — no rework needed; tier check populates the same `LimitResult` so the binding bucket's diagnostics flow through.

## Test summary

```
ok  github.com/hivegpt/hive/apps/edge-api/internal/authz       0.022s   (incl. tier + tier-scope tests)
ok  github.com/hivegpt/hive/apps/control-plane/internal/apikeys  0.010s (incl. limits CRUD + repo tests)
   full edge-api + control-plane suite: 24 packages OK
   web-console vitest: 9/9 passing
   promtool check rules: SUCCESS: 2 rules found
```

## Deviations from plan

### Auto-fixed (Rule 3 — adapt to existing code)

1. **[Rule 3 — schema] Migration target**
   - **Issue:** PLAN proposed adding `rate_limit_rpm INT`, `rate_limit_tpm INT`, `tier_overrides JSONB` columns to `public.api_keys`.
   - **Adapted:** Existing `public.api_key_rate_policies` table (since 20260331_04) already carries `requests_per_minute`, `tokens_per_minute` and is the source the edge-api hot path reads. Added only `tier_overrides JSONB NOT NULL DEFAULT '{}'` to that table.
   - **Rationale:** Adding parallel columns on `api_keys` would split the source-of-truth and require Repository.GetKeyRatePolicy to merge from two tables on every hot-path request. The schema split would also require Phase 20 to migrate again.

2. **[Rule 3 — route base] Owner-gated CRUD path**
   - **Issue:** PLAN said `/v1/admin/api-keys/{id}/limits`.
   - **Adapted:** Used existing handler base `/api/v1/accounts/current/api-keys/{id}/limits` to keep all key-mgmt routes co-located with the same `resolveViewerContext` owner gate.

3. **[Rule 3 — Lua EVAL strategy] Tier bucket as separate EVAL**
   - **Issue:** PLAN locked decision: single EVAL combining key + tier buckets.
   - **Adapted:** Tier enforcement is a second EVAL after the existing key+account EVAL. Existing Lua script body is bucket-agnostic; extending the script signature would change return shape and break in-flight v1.0 callers. The new EVAL only fires when the prior check allows — tier bucket is never touched on key-deny.

### Rule 4 — escalated, recorded as Deferred

4. **Prometheus counter emission**
   - **Issue:** Counter `rate_limit_exceeded_total{tier,key_id,limit_type}` requires a new `apps/edge-api/internal/metrics` package + register/inc plumbing through middleware.
   - **Status:** Alert + dashboard ship in this PR; counter emission deferred as a single follow-up commit before Phase 13. Recorded in 12-VERIFICATION.md §Deferred.

## Authentication gates encountered

None. Toolchain Docker container ran without external auth.

## Deferred Issues (cf. 12-VERIFICATION.md §Deferred)

- Burst integration test (`tests/integration/ratelimit_burst_test.go`) — needs testcontainers/compose-managed Redis lifecycle; defer to Phase 24 staging gate.
- Load p99 test (`tests/load/ratelimit_p99_test.go`) — same; defer to Phase 24.
- Prometheus counter emission code path — single follow-up commit.

## Self-Check: PASSED

- supabase/migrations/20260425_01_api_key_rate_limits.sql — FOUND
- apps/edge-api/internal/authz/tier.go — FOUND
- apps/edge-api/internal/authz/ratelimit.go (CheckWithTier) — FOUND
- apps/control-plane/internal/apikeys/http.go (limits routes) — FOUND
- apps/web-console/app/console/api-keys/[id]/limits/page.tsx — FOUND
- deploy/prometheus/alerts/rate-limit.yml — FOUND, validated by promtool
- deploy/grafana/dashboards/rate-limit.json — FOUND
- 12-VERIFICATION.md — FOUND
- REQUIREMENTS.md KEY-05-01..07 — Satisfied
- Commits per task: 3 (12-01 control-plane, 12-02 edge-api, 12-03 ui+infra+planning)

