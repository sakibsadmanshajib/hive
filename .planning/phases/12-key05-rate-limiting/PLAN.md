---
phase: 12-key05-rate-limiting
plan: 01
type: execute
wave: 1
depends_on: [11]
branch: a/phase-12-key05-rate-limiting
milestone: v1.1
track: A
files_modified:
  - supabase/migrations/20260425_01_api_key_rate_limits.sql
  - apps/control-plane/internal/apikeys/types.go
  - apps/control-plane/internal/apikeys/repository.go
  - apps/control-plane/internal/apikeys/service.go
  - apps/control-plane/internal/apikeys/http.go
  - apps/control-plane/internal/apikeys/repository_test.go
  - apps/control-plane/internal/apikeys/http_test.go
  - apps/edge-api/internal/authz/ratelimit.go
  - apps/edge-api/internal/authz/ratelimit_test.go
  - apps/edge-api/internal/authz/tier.go
  - apps/edge-api/internal/authz/tier_test.go
  - apps/edge-api/internal/authz/scripts/rpm_tpm.lua
  - apps/edge-api/internal/middleware/ratelimit_headers.go
  - apps/edge-api/internal/middleware/ratelimit_headers_test.go
  - apps/edge-api/internal/authz/authorizer.go
  - apps/edge-api/internal/proxy/handler.go
  - apps/edge-api/internal/proxy/handler_test.go
  - apps/web-console/app/console/api-keys/[id]/limits/page.tsx
  - apps/web-console/components/api-keys/rate-limit-form.tsx
  - apps/web-console/components/api-keys/rate-limit-form.test.tsx
  - apps/web-console/lib/api-keys.ts
  - deploy/prometheus/alerts/rate-limit.yml
  - deploy/grafana/dashboards/rate-limit.json
  - tests/integration/ratelimit_burst_test.go
  - tests/load/ratelimit_p99_test.go
  - .planning/phases/12-key05-rate-limiting/12-VERIFICATION.md
  - .planning/REQUIREMENTS.md
autonomous: true
requirements:
  - KEY-05-01
  - KEY-05-02
  - KEY-05-03
  - KEY-05-04
  - KEY-05-05
  - KEY-05-06
  - KEY-05-07
must_haves:
  truths:
    - "Per-API-key RPM and TPM limits are persisted on api_keys (rate_limit_rpm, rate_limit_tpm, tier_overrides JSONB) with non-null defaults applied via migration to all existing rows."
    - "Owner-gated control-plane endpoints (GET/PUT /v1/admin/api-keys/{id}/limits) read and write per-key + tier-override limits; non-owner callers receive 403."
    - "edge-api authz middleware consults Redis token-bucket BEFORE LiteLLM dispatch; on overflow returns 429 with Retry-After header and never enters proxy.Forward path."
    - "Tier resolution (guest|unverified|verified|credited) is computed from request auth context using a env-driven default map (HIVE_TIER_DEFAULTS_*) and a JWT-claim override (hive_tier) — Phase 20 wiring point left as a single resolveTier() function."
    - "Every successful and rejected hot-path request emits X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset response headers reflecting the more-restrictive of (key, tier) buckets."
    - "Hot-path latency overhead from the limiter is <2ms p99 measured by tests/load/ratelimit_p99_test.go against a warm Redis with 1000 concurrent virtual users."
    - "Prometheus counter rate_limit_exceeded_total{tier,key_id,limit_type} increments on every 429 and a Grafana dashboard surfaces 5m/1h rate plus tier breakdown."
    - "Web-console owner UI at /console/api-keys/[id]/limits reads and writes per-key RPM/TPM and tier overrides; non-owner workspace member sees the page in read-only mode."
    - "12-VERIFICATION.md records p99 measurement, burst test result, tier enforcement matrix per the four tiers, and links to .planning/REQUIREMENTS.md row updates closing KEY-05."
  artifacts:
    - path: "supabase/migrations/20260425_01_api_key_rate_limits.sql"
      provides: "api_keys schema columns rate_limit_rpm, rate_limit_tpm, tier_overrides JSONB with NOT NULL defaults backfilled."
      contains: "rate_limit_rpm"
    - path: "apps/edge-api/internal/authz/tier.go"
      provides: "resolveTier(ctx) function + env-driven default-limits map per tier."
      contains: "resolveTier"
    - path: "apps/edge-api/internal/authz/ratelimit.go"
      provides: "Token-bucket Redis check extended to take min(keyLimit, tierLimit) and return rate-limit headers."
      contains: "RateLimitDecision"
    - path: "apps/edge-api/internal/middleware/ratelimit_headers.go"
      provides: "Middleware that writes X-RateLimit-* headers + 429 Retry-After on overflow before LiteLLM dispatch."
      contains: "X-RateLimit-Remaining"
    - path: "apps/control-plane/internal/apikeys/http.go"
      provides: "Owner-gated CRUD endpoints for per-key + tier-override limits."
      contains: "/limits"
    - path: "apps/web-console/app/console/api-keys/[id]/limits/page.tsx"
      provides: "Owner UI to view and edit per-key + tier-override limits."
      contains: "rate-limit-form"
    - path: "tests/load/ratelimit_p99_test.go"
      provides: "Load test asserting <2ms p99 limiter overhead at 1000 concurrent VUs."
      contains: "p99"
    - path: "deploy/prometheus/alerts/rate-limit.yml"
      provides: "Alert rule firing when rate_limit_exceeded_total{tier=\"verified\"} 5m rate exceeds expected baseline."
      contains: "rate_limit_exceeded_total"
    - path: ".planning/phases/12-key05-rate-limiting/12-VERIFICATION.md"
      provides: "Phase 12 verification log feeding v1.1.0 ship-gate."
      contains: "KEY-05"
  key_links:
    - from: "apps/edge-api/internal/proxy/handler.go"
      to: "apps/edge-api/internal/authz/ratelimit.go"
      via: "Authorizer.Decide() invoked before any LiteLLM client call"
      pattern: "ratelimit\\.(Check|Decide)"
    - from: "apps/edge-api/internal/authz/ratelimit.go"
      to: "apps/edge-api/internal/authz/scripts/rpm_tpm.lua"
      via: "redis EVALSHA token-bucket script returning {allowed, limit, remaining, reset_at}"
      pattern: "rpm_tpm\\.lua"
    - from: "apps/edge-api/internal/authz/ratelimit.go"
      to: "apps/edge-api/internal/authz/tier.go"
      via: "resolveTier(ctx) -> tier limits merged with key limits"
      pattern: "resolveTier"
    - from: "apps/control-plane/internal/apikeys/http.go"
      to: "supabase/migrations/20260425_01_api_key_rate_limits.sql"
      via: "repository.UpdateLimits() writes to rate_limit_rpm/tpm/tier_overrides columns"
      pattern: "rate_limit_rpm"
    - from: "apps/web-console/app/console/api-keys/[id]/limits/page.tsx"
      to: "apps/control-plane/internal/apikeys/http.go"
      via: "fetch /v1/admin/api-keys/{id}/limits with owner JWT"
      pattern: "/admin/api-keys/.*/limits"
    - from: "deploy/grafana/dashboards/rate-limit.json"
      to: "apps/edge-api/internal/authz/ratelimit.go"
      via: "scrapes rate_limit_exceeded_total{tier,key_id,limit_type} from /metrics"
      pattern: "rate_limit_exceeded_total"
---

<shipped_deviations>
The `must_haves.truths` and link-pattern hints above were drafted before the
final implementation landed. Honor the source code as the canonical reference;
the items below differ from what shipped:

- **Storage location.** Truths and `key_links` say per-key limits live on
  `public.api_keys` with columns `rate_limit_rpm`, `rate_limit_tpm`,
  `tier_overrides`. The shipped migration
  (`supabase/migrations/20260425_01_api_key_rate_limits.sql`) instead extends
  the existing `public.api_key_rate_policies` table — rate-policy data already
  lives there and the edge-api hot path reads it via
  `Repository.GetKeyRatePolicy`. Only `tier_overrides jsonb` is added; the
  `requests_per_minute` / `tokens_per_minute` columns predate Phase 12.
- **Console route.** Truths and `key_links` cite
  `/v1/admin/api-keys/{id}/limits`. The shipped owner-gated routes are
  `/api/v1/accounts/current/api-keys/{id}/limits` (GET and PUT) under the
  account-scoped API key tree.
- **p99 measurement.** The "<2 ms p99 limiter overhead" truth is **deferred to
  Phase 24 (load-test gate)**; `tests/load/ratelimit_p99_test.go` ships as a
  scaffold-only skip-by-default benchmark in this phase.
- **Prometheus counter emission.** The
  `rate_limit_exceeded_total{tier,key_id,limit_type}` truth and the
  Grafana/alert rule are wired in shape only — the alert rules pass
  `promtool check rules`, but counter emission from
  `apps/edge-api/internal/authz/ratelimit.go` is **deferred to a follow-up
  commit before Phase 13**. KEY-05-06 is therefore Partial in
  `.planning/REQUIREMENTS.md`, not Satisfied.
- **Cardinality note (post-deferral).** When the counter lands, only `tier`
  and `limit_type` should be Prometheus labels — `key_id` belongs in
  structured logs / traces to keep series cardinality bounded.
</shipped_deviations>

<objective>
Wire per-API-key + per-tier rate limiting in the edge-api hot path with <2ms p99 added latency. Phase 12 closes ship-gate item KEY-05 and lays the tier-enforcement primitive that Phases 18 (RBAC), 20 (chat-app SSO + tier resolution), and 21 (chat-app tier limits) all consume.

Existing infra reused: `apps/edge-api/internal/authz/ratelimit.go` already implements Redis token-bucket via embedded Lua (`scripts/rpm_tpm.lua`) keyed by account+alias and key+alias. This phase extends — not rewrites — that surface to (a) accept per-key configurable RPM/TPM from DB, (b) layer tier-level limits on top using a `min(key, tier)` decision, (c) emit standard X-RateLimit-* headers + 429 Retry-After before LiteLLM dispatch, (d) expose owner-gated CRUD in control-plane + web-console, (e) instrument Prometheus + Grafana.

Tier resolution in this phase is a stub seam: `resolveTier(ctx)` reads JWT claim `hive_tier` if present, else falls back to env-driven defaults (`HIVE_TIER_DEFAULTS_GUEST_RPM=...`, `_VERIFIED_RPM=...`, etc). Phase 20 will replace the stub by wiring Supabase email/phone-verified state. Contract is locked here so Phase 20 swaps implementation only.

Output: schema migration, control-plane CRUD + owner gate, edge-api hot-path enforcement, web-console owner UI, Prometheus + Grafana wiring, unit + integration + load tests, and 12-VERIFICATION.md closing KEY-05 in `.planning/REQUIREMENTS.md`.
</objective>

<execution_context>
@/home/sakib/.claude/get-shit-done/workflows/execute-plan.md
@/home/sakib/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/STATE.md
@.planning/REQUIREMENTS.md
@.planning/v1.1-chatapp/V1.1-MASTER-PLAN.md
@.planning/phases/11-verification-cleanup/PLAN.md
@apps/edge-api/internal/authz/ratelimit.go
@apps/edge-api/internal/authz/ratelimit_test.go
@apps/edge-api/internal/authz/scripts/rpm_tpm.lua
@apps/edge-api/internal/authz/authorizer.go
@apps/edge-api/internal/proxy/handler.go
@apps/control-plane/internal/apikeys
@apps/web-console/components/api-keys/api-key-list.tsx
@apps/web-console/app/console/api-keys
@supabase/migrations/20260424_01_api_key_policies_embedding_alias.sql
@deploy/docker/docker-compose.yml

<interfaces>
<!-- Existing edge-api authz surface — reuse, do not rewrite. -->

apps/edge-api/internal/authz/ratelimit.go (existing):
  - package authz
  - Embeds scripts/rpm_tpm.lua + scripts/window_score.lua
  - Existing key patterns: rl:{acct:<account_id>:<alias_id>}:rpm:current/previous, rl:{key:<key_id>:<alias_id>}:rpm:current/previous, equivalent for tpm
  - go-redis/v9 client injected
  - Does NOT currently emit X-RateLimit-* response headers (this phase adds)
  - Does NOT currently consult per-key DB limits (this phase adds)
  - Does NOT currently apply tier limits (this phase adds)

apps/edge-api/internal/authz/authorizer.go:
  - Authorizer.Authorize(ctx, request) is the single entry point invoked by proxy.handler before forwarding
  - Returns AuthorizeResult — extend with RateLimitDecision{Limit, Remaining, ResetAt, Allowed, RetryAfter, LimitType}

apps/control-plane/internal/apikeys/ (existing):
  - types.go — APIKey struct (extend with RateLimitRPM, RateLimitTPM *int, TierOverrides map[string]TierLimit)
  - repository.go — pgx-based; add UpdateLimits, GetLimits methods
  - service.go — owner-check helper exists, reuse for /limits handlers
  - http.go — chi router; add GET/PUT /v1/admin/api-keys/{id}/limits

apps/web-console/components/api-keys/ (existing):
  - api-key-list.tsx — link "Manage limits" per row to /console/api-keys/[id]/limits
  - lib/api-keys.ts — extend with getLimits, updateLimits client functions

supabase/migrations/ — naming convention: 20260425_01_*.sql (next slot after 20260424_04_*).

deploy/docker/docker-compose.yml:
  - redis service available under `local` profile; staging uses Upstash via REDIS_URL (per project memory).
  - Load test runs against `local` profile redis only.
</interfaces>

<tier_defaults>
<!-- Placeholder defaults for Phase 12. Phase 20 may override. Sourced from V1.1-MASTER-PLAN.md §Tier Model — finalized numbers in Phase 21 PLAN.md. -->

guest:        rpm=10,  tpm=2000   (10 msg/day → tight)
unverified:   rpm=30,  tpm=4000   (50 msg/day medium)
verified:     rpm=120, tpm=8000   (200 msg/day normal free)
credited:     rpm=600, tpm=20000  (per-credit consumption — generous)
</tier_defaults>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Migration + control-plane per-key + tier-override CRUD with owner gate</name>
  <files>
    supabase/migrations/20260425_01_api_key_rate_limits.sql,
    apps/control-plane/internal/apikeys/types.go,
    apps/control-plane/internal/apikeys/repository.go,
    apps/control-plane/internal/apikeys/repository_test.go,
    apps/control-plane/internal/apikeys/service.go,
    apps/control-plane/internal/apikeys/http.go,
    apps/control-plane/internal/apikeys/http_test.go
  </files>
  <behavior>
    - Migration adds rate_limit_rpm INT NOT NULL DEFAULT 60, rate_limit_tpm INT NOT NULL DEFAULT 4000, tier_overrides JSONB NOT NULL DEFAULT '{}'::jsonb to api_keys; backfills existing rows; adds CHECK constraints rate_limit_rpm >= 0 AND <= 100000, same for tpm.
    - Repository GetLimits(keyID) returns (rpm, tpm, tierOverrides, error); UpdateLimits validates ranges and writes atomic UPDATE.
    - Repository test asserts: round-trip GetLimits → UpdateLimits → GetLimits returns set values; bad ranges (-1, 999999) return validation error; tier_overrides JSONB shape `{"guest":{"rpm":int,"tpm":int}, "verified":{...}}` accepted.
    - HTTP GET /v1/admin/api-keys/{id}/limits returns 200 {rpm, tpm, tier_overrides} for owner, 403 for non-owner workspace member, 401 for unauthenticated.
    - HTTP PUT /v1/admin/api-keys/{id}/limits accepts {rpm, tpm, tier_overrides} body; returns 200 on owner, 403 non-owner, 422 on out-of-range, 404 on unknown key id.
    - http_test asserts: owner JWT → 200; non-owner JWT in same workspace → 403; foreign-workspace JWT → 404.
  </behavior>
  <action>
    1. Create migration `supabase/migrations/20260425_01_api_key_rate_limits.sql`:
       ```sql
       ALTER TABLE api_keys
         ADD COLUMN rate_limit_rpm INT NOT NULL DEFAULT 60 CHECK (rate_limit_rpm >= 0 AND rate_limit_rpm <= 100000),
         ADD COLUMN rate_limit_tpm INT NOT NULL DEFAULT 4000 CHECK (rate_limit_tpm >= 0 AND rate_limit_tpm <= 10000000),
         ADD COLUMN tier_overrides JSONB NOT NULL DEFAULT '{}'::jsonb;
       CREATE INDEX IF NOT EXISTS api_keys_rate_limit_idx ON api_keys (rate_limit_rpm, rate_limit_tpm);
       COMMENT ON COLUMN api_keys.tier_overrides IS 'Per-tier override map. Shape: {tier: {rpm: int, tpm: int}}. Empty = use env defaults.';
       ```
    2. Extend `types.go`: add RateLimitRPM int, RateLimitTPM int, TierOverrides map[string]TierLimit to APIKey; add TierLimit struct {RPM int, TPM int}.
    3. Extend `repository.go`: add GetLimits + UpdateLimits using pgx. Use existing transaction helper. Validate ranges in repo before SQL — fail fast.
    4. Extend `service.go`: add GetLimits / UpdateLimits methods; reuse existing owner-check helper (look for existing `requireOwner` or workspace-role helper — service.go already gates other admin actions).
    5. Extend `http.go`: register GET + PUT /v1/admin/api-keys/{id}/limits under existing admin router group; reuse existing JWT middleware + owner gate.
    6. Tests follow RED→GREEN: write repository_test + http_test asserting behaviors above first; implement to make them pass. Use math/big for any numeric assertions per project convention (financial-style discipline).
    7. NO changes to edge-api in this task — pure control-plane + DB.

    Constraint: do NOT add any USD/FX fields. Limits are unitless integer counts. Per project regulatory rules, customer-facing surfaces stay BDT-only.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose --profile tools run --rm toolchain bash -c "cd /workspace &amp;&amp; go test ./apps/control-plane/internal/apikeys/... -count=1 -short -run 'TestLimits|TestRepository|TestHTTP'"</automated>
  </verify>
  <done>
    Migration applied locally without errors; api_keys table has the three new columns with defaults backfilled. Repository round-trip + range validation tests green. HTTP owner-gate test matrix green (owner 200, non-owner 403, foreign 404). Zero changes under apps/edge-api/.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: edge-api hot-path tier resolver + token-bucket extension + X-RateLimit headers + 429 Retry-After</name>
  <files>
    apps/edge-api/internal/authz/tier.go,
    apps/edge-api/internal/authz/tier_test.go,
    apps/edge-api/internal/authz/ratelimit.go,
    apps/edge-api/internal/authz/ratelimit_test.go,
    apps/edge-api/internal/authz/scripts/rpm_tpm.lua,
    apps/edge-api/internal/authz/authorizer.go,
    apps/edge-api/internal/middleware/ratelimit_headers.go,
    apps/edge-api/internal/middleware/ratelimit_headers_test.go,
    apps/edge-api/internal/proxy/handler.go,
    apps/edge-api/internal/proxy/handler_test.go,
    tests/integration/ratelimit_burst_test.go,
    tests/load/ratelimit_p99_test.go
  </files>
  <behavior>
    - resolveTier(ctx) returns Tier ∈ {guest, unverified, verified, credited}; reads JWT claim "hive_tier" first, falls back to env HIVE_TIER_DEFAULT (default "unverified").
    - Tier defaults loaded once at startup from env: HIVE_TIER_LIMITS_GUEST_RPM, _GUEST_TPM, _UNVERIFIED_*, _VERIFIED_*, _CREDITED_*. Defaults match §tier_defaults table above.
    - RateLimitDecision struct returned with Limit, Remaining, ResetAt time.Time, Allowed bool, RetryAfterSeconds int, LimitType ∈ {"rpm","tpm","rpm_tier","tpm_tier"}.
    - Decision = effective limit is min(keyLimit, tierLimit) per dimension; LimitType records which side bound it.
    - Lua script extended to return remaining + reset_at alongside allowed; existing acct/key keys preserved; new tier-keyed bucket added: rl:{tier:<tier>:<account_id>}:rpm:current.
    - On Allowed=false: middleware writes 429 with Retry-After: <seconds>, X-RateLimit-Limit, X-RateLimit-Remaining=0, X-RateLimit-Reset, body `{"error":{"type":"rate_limit_exceeded","message":"<provider-blind sanitized message>"}}`.
    - On Allowed=true: middleware injects X-RateLimit-* headers via response writer wrapper; proxy.Forward proceeds to LiteLLM.
    - Prometheus counter rate_limit_exceeded_total{tier,key_id,limit_type} increments only on 429.
    - Concurrent-burst integration test: 1000 requests in 5s against rpm=60 key → ≥940 receive 429.
    - Load test asserts limiter overhead p99 <2ms across 1000 VUs at 100 rps with allowed=true (warm Redis, not first-call cold path).

    Test cases (RED first):
    - tier_test.go: claim "hive_tier"="verified" → Verified; missing claim + env default "guest" → Guest; invalid claim → falls back to env default and logs warn.
    - ratelimit_test.go: existing tests still pass; new test: keyLimit=60, tierLimit=10 → effective=10, LimitType="rpm_tier"; reverse → "rpm".
    - middleware test: Allowed=true sets headers and calls next; Allowed=false writes 429 + Retry-After + JSON error and does NOT call next.
    - proxy/handler_test: 429 path never invokes LiteLLM client (mock asserts zero calls).
    - burst test: dockerized Redis, 1000 parallel requests, asserts ~940 rejections.
    - load test: time.Now() before/after Authorize() call across 1000 VUs, asserts percentile.NewSlice(samples).P99() < 2*time.Millisecond.
  </behavior>
  <action>
    1. Create `tier.go`:
       ```go
       package authz

       type Tier string

       const (
           TierGuest      Tier = "guest"
           TierUnverified Tier = "unverified"
           TierVerified   Tier = "verified"
           TierCredited   Tier = "credited"
       )

       type TierLimits struct{ RPM, TPM int }

       type TierResolver struct {
           defaults map[Tier]TierLimits
           fallback Tier
       }

       func NewTierResolverFromEnv() *TierResolver { /* read HIVE_TIER_LIMITS_*_RPM/TPM */ }
       func (r *TierResolver) Resolve(ctx context.Context) Tier { /* JWT claim "hive_tier" else r.fallback */ }
       func (r *TierResolver) Limits(t Tier) TierLimits { return r.defaults[t] }
       ```
       Phase 20 will replace Resolve() body with Supabase verification-state lookup. Keep contract stable.

    2. Extend `ratelimit.go`:
       - Add RateLimitDecision struct.
       - New method DecideHotPath(ctx, accountID, keyID, aliasID, keyLimits, tierLimits) RateLimitDecision.
       - Effective limit = min per dimension; LimitType reflects binding side.
       - Reuse existing rpm_tpm.lua via EVALSHA; extend script to return tuple {allowed, limit_used, remaining, reset_unix, limit_type}.
       - Compute RetryAfterSeconds = max(1, resetUnix - now).

    3. Extend `scripts/rpm_tpm.lua`:
       - Existing logic preserved.
       - Add return fields beyond Allowed: remaining + reset_at unix seconds + limit_type (passed as KEYS or ARGV input).
       - Add tier-bucket keys: rl:{tier:<tier>:<account_id>}:rpm:current/previous + tpm equivalents.
       - Single-EVAL call performs key-bucket + tier-bucket atomic check; returns the binding bucket's diagnostics.

    4. Extend `authorizer.go`:
       - Authorize() loads keyLimits via control-plane RPC or local cache (cache TTL 60s — per-key limits don't change on hot path).
       - Calls TierResolver.Resolve(ctx) once; merges; calls ratelimit.DecideHotPath.
       - Returns AuthorizeResult.RateLimit field.

    5. Create `middleware/ratelimit_headers.go`:
       - Wraps http.ResponseWriter, sets X-RateLimit-Limit/Remaining/Reset on every response (allowed and rejected).
       - On rejection: writes 429 + Retry-After + provider-blind JSON error; returns without calling next.
       - On allowance: calls next.

    6. Update `proxy/handler.go`:
       - Insert middleware ratelimit_headers BEFORE LiteLLM dispatch.
       - On rejection from authorizer, never enter forwardToLiteLLM.

    7. Create `tests/integration/ratelimit_burst_test.go`:
       - Spins up Redis via testcontainers OR uses docker-compose `--profile local` Redis (prefer existing compose to match staging).
       - Burst 1000 parallel requests against keyLimit=60; assert ≥940 rejections.
       - Run via `go test -tags=integration ./tests/integration/...`.

    8. Create `tests/load/ratelimit_p99_test.go`:
       - Build-tag `load`; warm Redis; 1000 VUs at 100rps total; measure Authorize() wall-clock; assert P99 < 2ms.
       - Use github.com/montanaflynn/stats or hand-rolled percentile (avoid new deps if existing stats lib present).

    9. Add Prometheus counter via existing edge-api metrics package; register in main.go startup; emit on 429 path only.

    Constraint: provider-blind error sanitization per CLAUDE.md — limiter rejection messages MUST NOT leak provider names. Sanitize at middleware boundary.

    Constraint: math/big NOT required here (counts, not money). Counts are int64.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose --profile tools run --rm toolchain bash -c "cd /workspace &amp;&amp; go test ./apps/edge-api/internal/authz/... ./apps/edge-api/internal/middleware/... ./apps/edge-api/internal/proxy/... -count=1 -short -run 'Tier|RateLimit|Headers|Burst'"</automated>
  </verify>
  <done>
    Unit tests green for tier resolver, ratelimit decision, middleware headers, and proxy 429 short-circuit. Burst integration test rejects ≥940/1000. Load test asserts <2ms p99 limiter overhead. 429 responses include Retry-After + X-RateLimit-* headers + provider-blind error body. Prometheus counter rate_limit_exceeded_total{tier,key_id,limit_type} visible at /metrics. Existing tests unbroken.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Web-console owner UI for per-key + tier-override limits + Prometheus alerts + Grafana dashboard + 12-VERIFICATION.md + REQUIREMENTS.md update</name>
  <files>
    apps/web-console/app/console/api-keys/[id]/limits/page.tsx,
    apps/web-console/components/api-keys/rate-limit-form.tsx,
    apps/web-console/components/api-keys/rate-limit-form.test.tsx,
    apps/web-console/lib/api-keys.ts,
    deploy/prometheus/alerts/rate-limit.yml,
    deploy/grafana/dashboards/rate-limit.json,
    .planning/phases/12-key05-rate-limiting/12-VERIFICATION.md,
    .planning/REQUIREMENTS.md
  </files>
  <behavior>
    - Page renders form with two integer inputs (RPM, TPM) for per-key limits, plus a tier-overrides section (4 rows: guest/unverified/verified/credited × {RPM, TPM}); each row optional.
    - Owner viewer: form is editable, Save button calls PUT endpoint, success toast, error toast on 422/403.
    - Non-owner viewer: same form rendered read-only (existing viewer-gates pattern from web-console).
    - Form test asserts: owner sees enabled inputs; non-owner sees disabled; out-of-range value triggers inline validation; PUT call uses correct path.
    - Prometheus alert rule fires when rate_limit_exceeded_total{tier="verified"} 5m rate > X (X is a placeholder for ops to tune; rule loads cleanly via promtool).
    - Grafana dashboard has 3 panels: rate_limit_exceeded_total time-series by tier, by key_id top-10, by limit_type breakdown.
    - 12-VERIFICATION.md records: must_have truth pass/fail with command + output snippet for each truth in this PLAN's frontmatter; load-test p99 number; burst-test rejection count; tier matrix with all 4 tiers exercised against a test key.
    - REQUIREMENTS.md updated: KEY-05 row(s) Status flips Pending→Satisfied with Evidence column linking to 12-VERIFICATION.md.
  </behavior>
  <action>
    1. Create `apps/web-console/lib/api-keys.ts` extension:
       ```ts
       export async function getKeyLimits(keyId: string): Promise<{rpm: number; tpm: number; tier_overrides: Record<string, {rpm: number; tpm: number}>}> { /* fetch /v1/admin/api-keys/{id}/limits */ }
       export async function updateKeyLimits(keyId: string, body: ...): Promise<void> { /* PUT */ }
       ```
       Strict types per project rules — no `as`, no `any`, no `unknown` casts. If structurally complex, define interfaces explicitly.

    2. Build form component `rate-limit-form.tsx` using existing shadcn/ui primitives (Form, Input, Button) — match patterns in `components/api-keys/api-key-create-form.tsx`. Disable inputs based on viewer role from existing viewer-gates lib.

    3. Page route `app/console/api-keys/[id]/limits/page.tsx`: server component fetches key + limits; passes to client form.

    4. Form test `rate-limit-form.test.tsx` with vitest + react-testing-library: owner enabled, non-owner disabled, range validation, submit calls mocked client.

    5. Prometheus alert `deploy/prometheus/alerts/rate-limit.yml`:
       ```yaml
       groups:
         - name: rate_limit
           rules:
             - alert: HighRateLimitRejections
               expr: sum by (tier) (rate(rate_limit_exceeded_total[5m])) > 0.5
               for: 5m
               labels: {severity: warning}
               annotations:
                 summary: "Tier {{ $labels.tier }} rate-limit rejections elevated"
       ```
       Validate via `promtool check rules` in toolchain container.

    6. Grafana dashboard JSON: 3 panels per behavior. Provision via existing `deploy/grafana/dashboards/` provisioning loader.

    7. Run full validation suite to populate `12-VERIFICATION.md`:
       - `bash scripts/verify-requirements-matrix.sh` (Phase 11 validator) — exits 0.
       - Burst integration test — capture rejection count.
       - Load test — capture p99 number.
       - Manual tier matrix: 4 curl invocations with synthetic JWTs for each tier; capture Limit/Remaining headers.
       - promtool check rules.
       - Web-console build green.

    8. Update `.planning/REQUIREMENTS.md`:
       - If KEY-05 row(s) exist: flip Status from Pending → Satisfied; Evidence column → `[12-VERIFICATION.md](phases/12-key05-rate-limiting/12-VERIFICATION.md)`.
       - If KEY-05 rows don't yet exist (Phase 11 may have left them as TBD), add rows KEY-05-01..KEY-05-07 mapping to the seven sub-requirements (RPM bucket, TPM bucket, headers, 429 + Retry-After, tier resolver, Prometheus counter, owner UI).

    9. Commit `12-VERIFICATION.md` with structure mirroring 11-VERIFICATION.md from Phase 11.

    Constraint: zero USD/FX visible on web-console limits page (counts only — no money). Page must pass project lint blocking USD/FX keys.

    Constraint: per project memory `feedback_no_human_verification.md` — verification commands MUST run autonomously via Docker/Playwright/curl. NO "user verifies" steps.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose run --rm web-console npm run test:unit -- rate-limit-form &amp;&amp; docker compose --profile tools run --rm toolchain bash -c "cd /workspace &amp;&amp; promtool check rules deploy/prometheus/alerts/rate-limit.yml" &amp;&amp; bash /home/sakib/hive/scripts/verify-requirements-matrix.sh &amp;&amp; test -f /home/sakib/hive/.planning/phases/12-key05-rate-limiting/12-VERIFICATION.md &amp;&amp; grep -q "KEY-05" /home/sakib/hive/.planning/REQUIREMENTS.md &amp;&amp; grep -q "p99" /home/sakib/hive/.planning/phases/12-key05-rate-limiting/12-VERIFICATION.md</automated>
  </verify>
  <done>
    Owner UI renders editable form on /console/api-keys/[id]/limits, non-owner read-only. Form test green. Prometheus alert validates with promtool. Grafana dashboard JSON loads cleanly. 12-VERIFICATION.md records p99 number, burst rejection count, tier matrix per truth. REQUIREMENTS.md flips KEY-05-01..07 to Satisfied with evidence link. scripts/verify-requirements-matrix.sh exits 0.
  </done>
</task>

</tasks>

<verification>
Phase-level checks (run after all tasks complete):

1. Migration applied: `docker compose --profile tools run --rm toolchain psql $SUPABASE_DB_URL -c "\d api_keys"` shows rate_limit_rpm, rate_limit_tpm, tier_overrides columns.
2. Owner gate: curl with non-owner JWT to PUT /v1/admin/api-keys/{id}/limits returns 403.
3. Hot-path 429: curl past keyLimit returns 429 with Retry-After + X-RateLimit-* headers; does not contain provider names.
4. Tier matrix: 4 curl calls (one per tier, synthetic JWT) return distinct Limit values matching env defaults.
5. Load: `go test -tags=load ./tests/load/...` reports p99 <2ms.
6. Burst: `go test -tags=integration ./tests/integration/ratelimit_burst_test.go` reports ≥940/1000 rejections.
7. Metrics: curl edge-api /metrics shows rate_limit_exceeded_total{tier,key_id,limit_type} counter.
8. Prometheus rules: `promtool check rules deploy/prometheus/alerts/rate-limit.yml` exits 0.
9. Web-console build: `docker compose run web-console npm run build` exits 0; visit /console/api-keys/[id]/limits as owner JWT, see editable form; as non-owner, see disabled.
10. REQUIREMENTS validator: `bash scripts/verify-requirements-matrix.sh` exits 0.
11. FX/USD audit: `git diff --name-only | xargs grep -l 'amount_usd\|usd_\|fx_'` returns empty for customer-facing files.
12. 12-VERIFICATION.md records all must_have truths with pass/fail.
</verification>

<success_criteria>
Definition of Done — also serves as v1.1.0 ship-gate input for KEY-05:

- [ ] supabase/migrations/20260425_01_api_key_rate_limits.sql applied; api_keys has rate_limit_rpm, rate_limit_tpm, tier_overrides with NOT NULL defaults backfilled.
- [ ] control-plane GET/PUT /v1/admin/api-keys/{id}/limits owner-gated (403 non-owner, 404 foreign-workspace) with range validation (422 out-of-range).
- [ ] edge-api hot-path consults Redis token-bucket BEFORE LiteLLM dispatch with effective limit = min(keyLimit, tierLimit) per dimension; emits X-RateLimit-Limit/Remaining/Reset on every response; emits 429 + Retry-After on overflow with provider-blind error body.
- [ ] resolveTier(ctx) reads JWT claim "hive_tier" with env-driven defaults fallback; Phase 20 contract seam preserved (single function).
- [ ] tests/integration/ratelimit_burst_test.go rejects ≥940/1000 burst; tests/load/ratelimit_p99_test.go asserts p99 <2ms limiter overhead at 1000 VUs warm.
- [ ] rate_limit_exceeded_total{tier,key_id,limit_type} Prometheus counter wired; deploy/prometheus/alerts/rate-limit.yml validates with promtool; deploy/grafana/dashboards/rate-limit.json loads cleanly.
- [ ] Web-console /console/api-keys/[id]/limits page renders editable form for owner, read-only for non-owner; vitest test green; web-console build green.
- [ ] .planning/REQUIREMENTS.md KEY-05-01..07 rows Satisfied with Evidence link to 12-VERIFICATION.md; scripts/verify-requirements-matrix.sh exits 0.
- [ ] .planning/phases/12-key05-rate-limiting/12-VERIFICATION.md records p99 measurement, burst result, full tier matrix, validator output; status=closed iff Blockers section empty.
- [ ] Branch a/phase-12-key05-rate-limiting created; single PR opened against main per V1.1-MASTER-PLAN.md branching strategy.
- [ ] Zero USD/FX added to any customer-visible surface (regulatory).

Ship-gate mapping: closes KEY-05 master-plan ship-gate item. Establishes the tier-enforcement primitive consumed by Phases 18, 20, 21. Does NOT close FX audit (Phase 17), RBAC matrix (Phase 18), or chat-app tier limits (Phase 21).
</success_criteria>

<blockers>
Discovered during planning (2026-04-25):

1. **Existing limiter surface is account+alias scoped, not global per-key.** `apps/edge-api/internal/authz/ratelimit.go` already implements Redis token-bucket with key patterns `rl:{acct:<account_id>:<alias_id>}` and `rl:{key:<key_id>:<alias_id>}` — both are alias-scoped (per-model). Phase 12 must add tier-keyed buckets `rl:{tier:<tier>:<account_id>}` and ensure the new per-key DB-driven limit is enforced alongside the existing alias-scoped buckets without double-counting. Implementation merges all three into one EVAL call returning the binding bucket. Documented in Task 2.

2. **Tier resolution depends on Phase 20 (Supabase email/phone-verified state).** Phase 12 ships with a stub `resolveTier()` that reads JWT claim `hive_tier` + env defaults. Phase 20 replaces the body. Contract is locked here — Phase 20 changes implementation only. Risk: if Phase 12 stub is wrong shape, Phase 20 churns. Mitigation: stub returns the same Tier enum + TierLimits struct that Phase 20 will return.

3. **Load-test environment.** Project uses Docker for all tests per CLAUDE.md. Load test requires warm Redis under realistic conditions. Plan uses `--profile local` docker-compose Redis (not Upstash) for the load test — staging Upstash Redis behaviorally equivalent but rate-limited per Upstash plan. p99 <2ms budget verified against local Redis only; staging budget verification deferred to Phase 24 staging deploy gate.

4. **Existing rpm_tpm.lua extension risk.** Modifying the embedded Lua script changes EVALSHA — must invalidate Redis script cache on rollout (Redis SCRIPT FLUSH or version-suffix new script as rpm_tpm_v2.lua). Plan keeps both versions during deploy: v2 active in code, v1 archived for rollback. Documented in Task 2 action.

5. **Phase 11 dependency.** `.planning/REQUIREMENTS.md` must exist (Phase 11 creates it). Task 3 updates KEY-05 rows in that file. If KEY-05 rows aren't defined yet (Phase 11 may have only carried v1.0 reqs), Task 3 ADDS rows KEY-05-01..07. Planner has accounted for both cases.

6. **No interactive verification.** Per project memory `feedback_no_human_verification.md` — all checks autonomous via Docker/curl/Playwright. Owner-UI verification uses Playwright E2E or vitest; no "user opens browser to verify" step.
</blockers>

<output>
After completion, create `.planning/phases/12-key05-rate-limiting/12-01-SUMMARY.md` per the GSD summary template, recording:
- Files created
- Files modified (production: edge-api, control-plane, web-console; infra: prometheus, grafana, migration; planning: REQUIREMENTS.md, 12-VERIFICATION.md)
- Burst rejection count
- Load-test p99 number
- Tier matrix results (all 4 tiers)
- KEY-05 row status update in REQUIREMENTS.md
- Any Blockers carried forward to Phase 18 / 20
- Ship-gate status update for v1.1.0 KEY-05 closure
</output>
