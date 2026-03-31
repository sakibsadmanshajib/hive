# Phase 5: API Keys & Hot-Path Enforcement - Research

**Researched:** 2026-03-31
**Domain:** Customer API key lifecycle, model-policy enforcement, per-key budget guardrails, Redis-backed rate limiting, and cheap request-time authorization
**Confidence:** HIGH overall, MEDIUM on the exact weekly trust-tier meter shape because the context intentionally leaves weekly reset mechanics to implementation discretion

## Summary

Phase 5 should add customer API keys as a control-plane-managed resource, then project only the minimum hot-path authorization state into Redis for request serving. The repo already has the right foundations:

1. `apps/control-plane/internal/accounts/service.go` already uses a one-time token issuance plus SHA-256 hashing pattern and already gates sensitive actions behind verified owner checks.
2. `apps/control-plane/internal/usage/*` already records `api_key_id`, so per-key attribution is an extension of the existing accounting model, not a new reporting system.
3. `apps/control-plane/internal/accounting/*` already enforces the shared workspace wallet and reservation/finalization semantics that per-key budgets must layer on top of.
4. `apps/control-plane/internal/routing/*` already owns alias allowlists, which is the seam key-level model policy should reuse.
5. Redis and `go-redis/v9` already exist in the repo, so the hot path does not need new infrastructure.

The safest implementation shape is:

1. Keep key metadata, status, model-policy inputs, and budget/rate-limit configuration durable in Supabase Postgres.
2. Never store raw customer secrets after issuance; show them once, store only a hash plus customer-safe metadata such as nickname and redacted suffix.
3. Push an auth snapshot into Redis keyed by token hash so the edge can validate keys, status, expiry, allowlists, and policy versions without a Postgres round-trip.
4. Reuse the existing reservation and usage-accounting flows so per-key budgets are guardrails on top of the shared wallet, not sub-wallets.
5. Use Redis Lua scripts for atomic rate-limit and anti-fraud counters, with short-window and long-window algorithms chosen separately based on memory and accuracy needs.
6. Keep public failures OpenAI-compatible and provider-blind; emit rate-limit headers only when the limiter can compute a meaningful remaining/reset value.

**Primary recommendation:** plan Phase 5 as three linked deliverables matching the roadmap:

- `05-01`: key issuance, listing, disable/re-enable, revoke, rotate, and Redis snapshot invalidation
- `05-02`: key policy model for expirations, model access, per-key budgets, and hot-path Redis limiters
- `05-03`: per-key attribution, async `last_used_at` projection, budget rollups, and durable limit-usage reconciliation

<user_constraints>

## User Constraints (from 05-CONTEXT.md)

### Locked Decisions

- API secrets use an `hk_...` prefix.
- A raw secret is shown exactly once at creation time.
- Rotating a key issues a brand-new key and immediately revokes the old key.
- Accounts may have multiple active keys at once.
- Customer-visible key states are `active`, `expired`, `revoked`, and `disabled`.
- `disabled` is resumable; `revoked` is permanent.
- New keys start with a curated default model set, not full catalog access.
- Key policy supports model groups or sets plus explicit per-alias overrides.
- `all models` is a distinct mode that can auto-inherit future aliases.
- Disallowed aliases must fail immediately; Hive must not silently remap to a different allowed model.
- Per-key budgets are guardrails on top of the shared workspace wallet, not separate wallets.
- Per-key budgets are expressed in Hive Credits only and must hard-fail when exceeded.
- Hot-path enforcement is account-level and key-level only in this phase; per-user controls are out of scope.
- Rate limiting is model-specific and must enforce RPM and TPM at both account and key scopes.
- Longer-horizon anti-fraud checks must exist for 5-hour and weekly windows.
- Public hot-path failures stay OpenAI-style and provider-blind.

### Claude's Discretion

- Exact token length and encoding beyond the public `hk_` prefix
- Exact relational schema for key policy groups, overrides, and `all models`
- Exact short-window limiter algorithm
- Exact 5-hour and weekly counter bucket shape
- Exact OpenAI-style error copy and when to include reset headers

### Deferred Ideas (OUT OF SCOPE)

- Per-user limits or per-user model quotas
- Rich developer-console UX for presets, grouped policy administration, or audit history
- Spend alerts and notification workflows
- Trust-and-safety tooling beyond the required hot-path guardrails

</user_constraints>

<phase_requirements>

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| KEY-01 | Account owner can create multiple API keys and sees each raw secret only once at creation time. | Reuse the repo's existing one-time token pattern, store only `token_hash`, keep raw secrets out of durable storage, and gate mutations behind the existing verified-owner account flow. |
| KEY-02 | Account owner can set per-key nickname, expiration, allowed models, and Hive Credit budget. | Store durable key config in Postgres, project resolved policy snapshots into Redis, and reuse Phase 4 alias allowlists instead of inventing a second model-policy system. |
| KEY-03 | Account owner can revoke or rotate one key without affecting other keys. | Treat each key as its own durable row and Redis snapshot; rotate by minting a new key and immediately invalidating the old snapshot. |
| KEY-04 | Hive tracks usage and spend per API key and per model. | Keep using the existing `api_key_id` attribution path in `usage` and extend finalization/rollup jobs to maintain per-key spend state. |
| KEY-05 | Hive enforces account-tier and per-key rate limits and quotas on the hot path. | Use Redis-backed atomic scripts for RPM/TPM and long-window anti-fraud counters; do not use Postgres joins in the request path. |

</phase_requirements>

## Repo Reality Check

### What already exists

- `apps/control-plane/internal/accounts/service.go`
  - `CanManageAPIKeys` already exists and should remain the control-plane authorization gate for key mutations.
  - `generateToken` and `HashToken` already implement a one-time-secret plus SHA-256 hash pattern.
- `apps/control-plane/internal/usage/types.go`
  - `RequestAttempt` already carries `APIKeyID`, so request and usage attribution do not need a second ledger.
- `apps/control-plane/internal/accounting/types.go`
  - The workspace wallet and reserve/finalize lifecycle already exist; per-key budgets should compose with these flows.
- `apps/control-plane/internal/routing/service.go`
  - Alias allowlists are already enforced centrally before route selection.
- `apps/control-plane/internal/platform/redis/client.go`
  - `go-redis/v9` is already wired and should be reused for auth snapshots and limiter scripts.
- `apps/edge-api/internal/errors/openai.go`
  - OpenAI-style error envelopes already exist and should be reused for invalid-key, over-budget, disallowed-model, and rate-limit failures.

### Important structural constraints

- `apps/edge-api` and `apps/control-plane` are separate Go modules; the edge should not import control-plane internals directly.
- The hot path must stay cheap, which rules out synchronous Postgres reads or customer-facing reporting writes per request.
- The workspace wallet remains authoritative. Any per-key budget implementation that behaves like a separate spendable balance is a design regression.

**Implication:** the edge should consume a narrow Redis or control-plane projection for key auth state, not become the owner of key persistence.

## Standard Stack

### Core

| Technology | Version / Variant | Purpose | Why It Fits Phase 5 |
|------------|-------------------|---------|---------------------|
| Go | `1.24` in the current repo | Control-plane key lifecycle and edge hot-path checks | Matches the codebase and keeps Phase 5 inside existing services. |
| Supabase Postgres | Existing primary DB | Durable key metadata, policy inputs, state transitions, and reporting truth | Key lifecycle and budget configuration are authoritative product state, not cache entries. |
| Redis + `github.com/redis/go-redis/v9` | Existing repo dependency | Auth snapshots, rate-limit counters, anti-fraud windows, and async `last_used_at` debounce | Already present in the repo and appropriate for hot-path counters and short-lived projections. |
| Redis Lua scripts via `EVALSHA` | Official Redis-supported approach | Atomic read-decide-write limit checks | Redis documents Lua scripting as atomic and locality-preserving; that is exactly what hot-path limit enforcement needs. |
| Existing `usage`, `accounting`, and `routing` packages | Current repo business primitives | Per-key attribution, shared-wallet guardrails, and alias allowlist enforcement | Phase 5 should extend these packages, not create parallel policy systems. |

### Supporting

| Library / Tool | Purpose | When to Use |
|----------------|---------|-------------|
| `crypto/rand` + existing SHA-256 hash helper | Secret generation and at-rest token hashing | Use for opaque customer key material; no new crypto dependency is required. |
| Existing control-plane repository/service/http layering | Admin APIs for key management | Reuse the current package pattern instead of introducing another framework or service. |
| Existing OpenAI-style error writer | Public compatibility errors | Use for invalid key, over budget, disallowed alias, and `429` responses. |
| Docker Compose + `go test` | Verification | Keep Phase 5 development and tests inside the existing Docker-only workflow. |

## Architecture Patterns

### Pattern 1: Opaque Secret, Hash at Rest, Reveal Once

**What:** Issue opaque customer secrets with the required `hk_` prefix, show the raw secret once, and store only:

- `token_hash`
- `account_id`
- key state and timestamps
- customer-visible metadata such as nickname, redacted suffix, and last-used projection

**Why:** The repo already has a working one-time token pattern, and Stripe's key-management guidance explicitly treats secret API keys as credentials that must be handled carefully, seen once at creation time, and rotated on exposure.

**Recommendation:**

- Keep the customer-visible raw secret as one opaque value such as `hk_<base64url-random>`.
- Reuse the existing SHA-256 helper for hashing the raw secret before persistence.
- Use the hash itself as the Redis lookup key for hot-path auth snapshots.
- Store a short redacted suffix separately for list and audit views.
- Compare derived hashes in constant time before accepting the key.

**Inference:** a separate public lookup segment such as `kid` is optional, not required. In this repo, hashing the presented secret and using that hash for Redis/Postgres lookup is simpler and aligns with the existing accounts code.

### Pattern 2: Control-Plane-Owned Key Policy, Redis-Projected Auth Snapshot

**What:** Keep durable key policy in Postgres, but project a compact auth snapshot into Redis for request serving.

**Why:** OWASP recommends local access-control decisions at each REST endpoint to reduce latency and coupling, and this repo's edge/control-plane split makes a cached projection the safest hot-path shape.

**Recommended snapshot fields:**

- `key_id`
- `account_id`
- `status`
- `expires_at`
- resolved allowlist mode: `all`, `resolved_aliases`, or curated default set
- budget policy version and current projected spend state
- account-tier and per-key rate policy identifiers
- anti-fraud policy identifiers
- `policy_version` or `updated_at` for invalidation safety

**Recommended flow:**

1. Control-plane writes or mutates the durable key record.
2. Control-plane writes through the Redis snapshot or deletes it to force rehydrate.
3. Edge hashes the presented bearer token and looks up `auth:key:{<token_hash>}`.
4. On cache miss, edge calls a narrow internal control-plane hydration endpoint and repopulates Redis.
5. All revoke, disable, rotate, expiry, and policy changes invalidate the snapshot immediately.

### Pattern 3: Reuse Phase 4 Alias Allowlists for Key Model Governance

**What:** The key-level model policy should resolve to the same alias space already owned by the routing package.

**Why:** Phase 4 already established alias-level allowlist enforcement before route selection. Building a second model-policy engine would create drift.

**Recommendation:**

- Represent key policy as:
  - curated defaults
  - zero or more model-group memberships
  - explicit alias allows
  - explicit alias denies
  - optional `all_models=true`
- Resolve this policy into a final alias allowlist snapshot before the request reaches routing.
- If `all_models=true`, newly published aliases can auto-attach.
- If an alias is not allowed, fail before route selection and do not silently remap.

### Pattern 4: Layered Key Budgets on Top of the Shared Wallet

**What:** Per-key budgets should be policy guardrails that sit on top of the workspace wallet and the existing reservation/finalization flows.

**Why:** The context is explicit that keys do not get sub-wallets. Phase 3 already established the wallet and reservation model that must stay authoritative.

**Recommendation:**

- Keep budget configuration durable in Postgres:
  - `none`
  - `lifetime`
  - `recurring` with period metadata
- Maintain a hot-path budget projection in Redis:
  - consumed credits in the current budget scope
  - open reserved estimate
  - budget ceiling
  - period boundary or reset metadata
- Budget admission check should use:
  - `projected_consumed + projected_reserved + estimated_request_cost <= budget_limit`
- Update budget projections on reservation create, expand, finalize, and release.
- Use durable usage/accounting events as the source of truth for reconciliation and rebuilds.

**Important rule:** never compute per-key budget eligibility by summing usage rows on each request.

### Pattern 5: Separate Short-Window Rate Limits from Long-Window Anti-Fraud Windows

**What:** Use different Redis limiter shapes for different jobs.

**Why:** Redis's own rate-limiting guidance distinguishes simple fixed windows, sliding logs, sliding counters, token buckets, and leaky buckets. Phase 5 needs low-memory general API limits plus longer-horizon anti-fraud guardrails, not one algorithm for everything.

**Recommendation:**

- **Model-specific RPM/TPM at account and key scope:**
  - Use a sliding window counter implemented with two string keys plus Lua.
  - This matches Redis's current recommendation for general-purpose API rate limiting because it balances accuracy, simplicity, and low memory usage.
- **Rolling 5-hour anti-fraud meter:**
  - Use bounded bucketed counters, not per-request sorted-set logs.
  - Recommendation: `5m` buckets over `5h` per account and per key.
  - Store an integer fraud score, not floats.
- **Weekly trust-tier limit:**
  - Use daily buckets in Redis with a rolling 7-day sum or another explicitly documented weekly reset rule.
  - The exact reset semantics are flexible, but they must be stable, testable, and visible to the control plane.

**Why not sliding-window logs everywhere:** Redis documents sorted-set logs as exact but `O(n)` in entries. That is a poor fit for a public API gateway serving high-cardinality keys and models.

### Pattern 6: One Atomic Lua Decision per Enforcement Scope

**What:** Each limiter or projection update should execute as one atomic server-side decision.

**Why:** Redis documents Lua scripting as atomic and latency-friendly because the logic runs where the data lives. The Redis rate-limiter tutorial also shows why separate read and write commands create TOCTOU races under concurrency.

**Recommendation:**

- Keep scripts parameterized and generic.
- Load them with `SCRIPT LOAD` or rely on client helpers, then execute via `EVALSHA`.
- Keep keys explicit and cluster-safe. If Redis Cluster is introduced later, use hash tags so all keys a script touches stay in the same slot.
- Return enough metadata for public responses:
  - allowed / denied
  - remaining request budget
  - remaining token budget
  - reset durations for request and token windows

**Inference:** Phase 5 does not need Redis Functions yet. `EVALSHA` is sufficient and simpler operationally for this repo.

### Pattern 7: Async `last_used_at` and Reporting Projections

**What:** `last_used_at`, spend rollups, and reporting views should update asynchronously from durable request/accounting events.

**Why:** The hot path cannot afford a Postgres write every time a key is used, and the repo already has a durable usage/accounting surface to project from.

**Recommendation:**

- Write durable request and usage events with `api_key_id` as the system of record.
- In Redis, debounce `last_used_at` updates and flush them periodically to Postgres.
- Build key spend and model-usage rollups from finalization/reconciliation events, not from raw request start events.
- Keep the customer-facing list view eventually consistent for `last_used_at`; correctness matters more than sub-second freshness.

## Don't Hand-Roll

- Do not store raw customer secrets in Postgres, logs, or customer-visible audit payloads after issuance.
- Do not invent a second model-policy engine separate from the alias allowlists already enforced by `routing.Service`.
- Do not hit Postgres synchronously for key validity, model allowlists, `last_used_at`, or rate counters on the public request path.
- Do not model per-key budgets as a separate wallet or mutable balance source of truth.
- Do not use sorted-set sliding logs for every key and model unless you truly need exact per-request timestamps for audit; they are the wrong memory shape for a general API gateway.
- Do not build limiter scripts from dynamically generated Lua source; Redis treats that as a script-cache anti-pattern.
- Do not return retry headers for permanent failures such as revoked, expired, disabled, or over-budget keys.
- Do not broaden scope into per-user enforcement, spend alerts, or console-heavy UX in this phase.

## Common Pitfalls

### Pitfall 1: Reusing the one-time hash pattern but still requiring a database read on every request

Hashing the bearer token is cheap. The expensive mistake is failing to project the result into Redis and turning every public request into a Postgres auth query.

### Pitfall 2: Counting only finalized spend for budget checks

If the hot path ignores open reservations, concurrent requests can overshoot a key budget before finalization lands. Budget admission must consider both consumed and reserved projections.

### Pitfall 3: Revoking a key in Postgres but leaving a stale Redis snapshot

Rotation, disable, revoke, and policy changes must invalidate or rewrite Redis synchronously with the control-plane mutation. Otherwise revoked keys can continue to work until TTL expiry.

### Pitfall 4: Updating `last_used_at` in the request path

That turns every successful request into a write-amplified database workload and undercuts the whole point of Phase 5 hot-path optimization.

### Pitfall 5: Using float arithmetic for the 5-hour or weekly fraud meter

The context requires a hybrid meter across credits and tokens. Use integer normalized units so the limiter stays deterministic and reconciliation-safe.

### Pitfall 6: Using `INCR` and `EXPIRE` as separate client calls

Redis documents Lua as the right tool for atomic read-decide-write flows. Separate commands can race and leave permanent keys or over-admit traffic.

### Pitfall 7: Forgetting future cluster key-slot constraints

If the limiter script touches multiple Redis keys and those keys do not share a slot in Redis Cluster, the script will fail. Design key names with hash tags from the start.

### Pitfall 8: Emitting OpenAI-style rate-limit headers for every denial

Headers such as remaining and reset values are meaningful for RPM/TPM denies. They are misleading for revoked, expired, or over-budget states where retry is not meaningful.

## Code Examples

### Example 1: Issue a key once, hash it immediately, never persist the raw secret

```go
func issueAPIKey() (rawSecret string, tokenHash string, suffix string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", err
	}

	raw := base64.RawURLEncoding.EncodeToString(buf)
	rawSecret = "hk_" + raw
	tokenHash = HashToken(rawSecret)

	if len(rawSecret) <= 6 {
		suffix = rawSecret
	} else {
		suffix = rawSecret[len(rawSecret)-6:]
	}

	return rawSecret, tokenHash, suffix, nil
}
```

**Why this shape:** it matches the repo's current one-time token pattern and keeps the public `hk_` prefix requirement without introducing a second crypto format.

### Example 2: Hot-path auth should read a Redis snapshot keyed by token hash

```go
type AuthSnapshot struct {
	KeyID          uuid.UUID `json:"key_id"`
	AccountID      uuid.UUID `json:"account_id"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
	AllowAllModels bool      `json:"allow_all_models"`
	AllowedAliases []string  `json:"allowed_aliases"`
	PolicyVersion  int64     `json:"policy_version"`
}

func lookupSnapshot(ctx context.Context, rdb *redis.Client, rawSecret string) (*AuthSnapshot, error) {
	tokenHash := accounts.HashToken(rawSecret)
	key := "auth:key:{" + tokenHash + "}"

	payload, err := rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}

	var snap AuthSnapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return nil, err
	}

	return &snap, nil
}
```

**Why this shape:** the edge stays thin, the lookup is one Redis read on the hot path, and the durable source of truth remains in the control plane.

### Example 3: Keep the limiter decision atomic and return reset metadata

```lua
-- KEYS[1] = req_curr
-- KEYS[2] = req_prev
-- KEYS[3] = tok_curr
-- KEYS[4] = tok_prev
-- ARGV    = now_ms, window_ms, req_limit, tok_limit, requested_tokens

local now_ms = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local req_limit = tonumber(ARGV[3])
local tok_limit = tonumber(ARGV[4])
local requested_tokens = tonumber(ARGV[5])

-- calculate weighted current counters, deny if request or token limit would be crossed,
-- then update current-window counters and TTLs atomically
-- return: allowed, remaining_requests, remaining_tokens, reset_requests_ms, reset_tokens_ms
```

**Why this shape:** Redis's official guidance is to make the rate-limit decision inside Lua so reads, branching, writes, and TTL management cannot race.

### Example 4: Budget admission should use reserved plus consumed state

```go
func budgetAllows(limit, consumed, reserved, estimated int64) bool {
	if limit <= 0 {
		return true
	}

	projected := consumed + reserved + estimated
	return projected <= limit
}
```

**Why this shape:** checking only finalized spend is how concurrent requests punch through key budgets.

## Open Questions

These are planning decisions, not blockers to begin planning:

1. Should the auth snapshot be hydrated only on mutation, or should the edge be able to rehydrate on cache miss from a narrow control-plane endpoint?
   - Recommendation: support a miss-path hydration endpoint so Redis can be rebuilt safely after eviction or restart.
2. Should the weekly trust-tier meter use rolling 7-day buckets or a fixed calendar week?
   - Recommendation: rolling 7-day daily buckets are harder to game and still cheap to compute.
3. Should the key record store resolved alias lists directly, or keep normalized group/override tables and derive the resolved allowlist into Redis?
   - Recommendation: normalize durable policy inputs, derive resolved allowlists into Redis snapshots and cacheable read models.

## Recommended Plan Split

### Plan 05-01: Key lifecycle and snapshot invalidation

Focus on:

- durable `api_keys` schema and repository
- create/list/disable/enable/revoke/rotate APIs in the control plane
- one-time secret reveal rules
- Redis auth snapshot write-through and invalidation

### Plan 05-02: Model policy, budget policy, and hot-path limiter middleware

Focus on:

- key model-policy schema and resolved allowlist projection
- per-key budget configuration and projection model
- Redis Lua scripts for account/key RPM and TPM
- request middleware that checks key state, alias allowlist, budget, and rate limits before routing

### Plan 05-03: Attribution, rollups, and async `last_used_at`

Focus on:

- making `api_key_id` required where appropriate in the request-serving path
- per-key spend and usage rollups from durable events
- async `last_used_at` projection
- long-window 5-hour and weekly anti-fraud counters plus reconciliation and rebuild support

## Validation Architecture

### Phase Requirements to Test Map

| Req ID | Behavior | Test Type | Notes |
|--------|----------|-----------|-------|
| KEY-01 | Raw secret is shown once, not persisted, and multiple active keys can coexist | service + repository | Verify only `token_hash` and redacted metadata are stored. |
| KEY-02 | Nickname, expiry, model policy, and budget policy resolve into the expected Redis snapshot | service + unit | Verify group + override + `all_models` resolution. |
| KEY-03 | Disable, revoke, and rotate invalidate the old snapshot immediately without affecting sibling keys | unit + integration | Include stale-cache regression tests. |
| KEY-04 | Usage finalization records `api_key_id` and per-model spend can be rolled up without new accounting channels | repository + service | Reuse existing usage and accounting packages. |
| KEY-05 | RPM/TPM, 5-hour, and weekly policies deny correctly and return the right public metadata | unit + integration | Verify allowed, remaining, and reset values; verify no retry headers for permanent failures. |

### Manual-Only Verifications

| Behavior | Why Manual | Test Instructions |
|----------|------------|-------------------|
| Customer-facing key list feels correct without revealing secrets | Requires reviewing list payloads and redaction behavior | Create, rotate, disable, and revoke keys; confirm only nickname, suffix, timestamps, status, and summaries are visible. |
| Public failure posture remains OpenAI-style and provider-blind | Requires judgment about compatibility and wording | Trigger invalid-key, disallowed-model, over-budget, and rate-limit denials and inspect status code, error body, and headers. |

### Wave 0 Gaps to Expect During Planning

- `supabase/migrations/*api_keys*` for durable key lifecycle and policy tables
- `apps/control-plane/internal/apikeys/` or equivalent package for repository/service/http
- edge-auth middleware or request gate that can consume Redis auth snapshots
- Redis Lua script loader and limiter wrapper package
- tests for snapshot invalidation, limiter math, and budget projection correctness

## Sources

### Repo and planning sources

- `/home/sakib/hive/.planning/ROADMAP.md`
- `/home/sakib/hive/.planning/REQUIREMENTS.md`
- `/home/sakib/hive/.planning/STATE.md`
- `/home/sakib/hive/.planning/phases/05-api-keys-hot-path-enforcement/05-CONTEXT.md`
- `/home/sakib/hive/apps/control-plane/internal/accounts/service.go`
- `/home/sakib/hive/apps/control-plane/internal/usage/types.go`
- `/home/sakib/hive/apps/control-plane/internal/accounting/types.go`
- `/home/sakib/hive/apps/control-plane/internal/routing/service.go`
- `/home/sakib/hive/apps/control-plane/internal/platform/redis/client.go`
- `/home/sakib/hive/apps/edge-api/internal/errors/openai.go`

### External primary sources

- Redis rate-limiter tutorial: https://redis.io/tutorials/howtos/ratelimiting/
  - Redis recommends the sliding window counter as the best general trade-off for many APIs and explains why Lua avoids TOCTOU races.
- Redis Lua scripting docs: https://redis.io/docs/latest/develop/programmability/eval-intro/
  - Confirms atomic execution, data locality benefits, explicit key arguments, and the application-side handling model for scripts.
- Redis `EXPIRE` docs: https://redis.io/docs/latest/commands/expire/
  - Confirms `INCR`, `HSET`, and similar in-place mutations do not clear TTLs, which is important for counter design.
- OWASP REST Security Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/REST_Security_Cheat_Sheet.html
  - Confirms endpoint-local access-control decisions and `429` for over-fast API-key usage.
- Stripe secret API key best practices: https://docs.stripe.com/keys-best-practices
  - Confirms one-time reveal handling, safe storage posture, least privilege, and immediate rotation on exposure.
- OpenAI rate-limit guide: https://platform.openai.com/docs/guides/rate-limits
  - Confirms model-specific RPM/TPM thinking and the public `x-ratelimit-*` header family that Hive should mirror only when meaningful.

---
*Phase: 05-api-keys-hot-path-enforcement*
*Research completed: 2026-03-31*
