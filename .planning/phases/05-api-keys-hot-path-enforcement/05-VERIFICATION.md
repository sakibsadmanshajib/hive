---
phase: 05-api-keys-hot-path-enforcement
verified: 2026-04-01T21:45:12Z
status: gaps_found
score: 3/4 must-haves verified
gaps:
  - truth: "Requests are rejected quickly when keys are revoked, expired, over budget, or over rate limit."
    status: failed
    reason: "Revoked and expired keys are denied, but over-budget and full KEY-05 hot-path rate and quota enforcement are not wired through the auth snapshot and edge limiter."
    artifacts:
      - path: "apps/control-plane/internal/apikeys/service.go"
        issue: "ResolveSnapshot still hard-codes BudgetConsumedCredits and BudgetReservedCredits to 0, so budget denials cannot reflect durable usage windows."
      - path: "apps/control-plane/internal/accounting/service.go"
        issue: "Reservation and finalize paths always write API-key budget deltas to the lifetime window, so monthly budget controls are never projected."
      - path: "apps/edge-api/internal/authz/client.go"
        issue: "The edge limiter falls back to hard-coded 60 RPM and 120000 TPM defaults and reads snapshot.Policy fields that the control-plane snapshot never emits."
      - path: "apps/edge-api/internal/authz/ratelimit.go"
        issue: "The planned limiter artifact is missing, along with the long-window Lua scripts for quota enforcement."
    missing:
      - "Load current API-key budget consumption and reservation totals into AuthSnapshot during ResolveSnapshot."
      - "Apply reservation and finalize deltas to the key's configured budget window kind instead of hard-coding lifetime."
      - "Project rate-policy data from control-plane storage into the edge hot path."
      - "Implement the planned account-tier and long-window quota checks, not just default per-key RPM and TPM."
---

# Phase 05: API Keys & Hot-Path Enforcement Verification Report

**Phase Goal:** Give customers safe multi-key management while keeping authorization, budgets, and rate limits cheap on the hot path.
**Verified:** 2026-04-01T21:45:12Z
**Status:** gaps_found
**Re-verification:** No, initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Account owner can create multiple keys and only sees each secret once when it is issued. | ✓ VERIFIED | `CreateKey` generates and returns the raw `hk_...` secret once while list responses only serialize metadata and `redacted_suffix` ([service.go](apps/control-plane/internal/apikeys/service.go) lines 72-109, [http.go](apps/control-plane/internal/apikeys/http.go) lines 93-109 and 395-410). |
| 2 | Keys support nickname, expiration, model allowlist, and per-key budget controls. | ✓ VERIFIED | Create and rotate accept nickname and expiration, and `/policy` accepts model and budget fields backed by `api_key_policies` ([http.go](apps/control-plane/internal/apikeys/http.go) lines 112-140 and 260-323, [20260331_03_api_key_policies.sql](supabase/migrations/20260331_03_api_key_policies.sql)). |
| 3 | Requests are rejected quickly when keys are revoked, expired, over budget, or over rate limit. | ✗ FAILED | Revocation and rotation invalidation are wired, but `ResolveSnapshot` still returns zero budget usage and the edge limiter does not consume durable rate-policy data or implement the planned quota artifacts ([service.go](apps/control-plane/internal/apikeys/service.go) lines 262-346, [client.go](apps/edge-api/internal/authz/client.go) lines 146-185, missing `apps/edge-api/internal/authz/ratelimit.go`). |
| 4 | Spend and usage are attributable per key and per model. | ✓ VERIFIED | Public reservation creation now accepts `api_key_id`, finalize records completed usage events with `api_key_id`, `last_used_at` is updated, usage responses expose `api_key_id`, and durable rollups exist per key and model ([http.go](apps/control-plane/internal/accounting/http.go) lines 61-91, [service.go](apps/control-plane/internal/accounting/service.go) lines 269-297, [usage/http.go](apps/control-plane/internal/usage/http.go) lines 75-99, [20260331_04_api_key_usage_and_limits.sql](supabase/migrations/20260331_04_api_key_usage_and_limits.sql)). |

**Score:** 3/4 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `supabase/migrations/20260331_02_api_keys.sql` | Durable one-time-secret lifecycle schema | ✓ VERIFIED | Contains `public.api_keys`, `public.api_key_events`, and raw-secret-at-rest prohibitions. |
| `apps/control-plane/internal/apikeys/service.go` | Lifecycle logic plus auth snapshot construction | ⚠️ PARTIAL | Lifecycle and invalidation paths exist, but snapshot budget totals are still hard-coded to zero. |
| `apps/control-plane/internal/apikeys/http.go` | Authenticated current-account key and policy endpoints | ✓ VERIFIED | Create/list/rotate/disable/enable/revoke/policy/internal resolve routes are present and gated. |
| `supabase/migrations/20260331_03_api_key_policies.sql` | Durable policy storage for allowlists and budgets | ✓ VERIFIED | `api_key_policies`, model groups, and memberships exist. |
| `supabase/migrations/20260331_04_api_key_usage_and_limits.sql` | Per-key usage, budget windows, and rate-policy tables | ✓ VERIFIED | `usage_events.api_key_id`, `api_key_usage_rollups`, `api_key_budget_windows`, and `api_key_rate_policies` exist. |
| `apps/edge-api/internal/authz/client.go` | Redis-backed snapshot resolution and edge limiter entrypoint | ⚠️ PARTIAL | Redis snapshot caching is wired, but limiter logic uses fallback defaults and no control-plane-fed rate policy. |
| `apps/edge-api/internal/authz/ratelimit.go` | Redis Lua-backed RPM/TPM and quota limiter | ✗ MISSING | File is absent. |
| `apps/edge-api/internal/authz/scripts/rpm_tpm.lua` | Atomic hot-path RPM/TPM script | ✗ MISSING | File is absent. |
| `apps/edge-api/internal/authz/scripts/window_score.lua` | Long-window quota script | ✗ MISSING | File is absent. |

### Key Link Verification

| From | To | Via | Status | Details |
| --- | --- | --- | --- | --- |
| `apps/control-plane/internal/accounts/service.go` | `apps/control-plane/internal/apikeys/http.go` | `CanManageAPIKeys` owner gate | ✓ WIRED | Viewer-context gate is defined in accounts service and enforced by API-key handlers. |
| `apps/control-plane/cmd/server/main.go` | `apps/control-plane/internal/platform/http/router.go` | `APIKeysHandler` registration | ✓ WIRED | Main constructs `apikeys.NewService(...)` and router registers both public and internal API-key routes. |
| `apps/control-plane/internal/apikeys/service.go` | `apps/control-plane/internal/routing/service.go` | Alias-space allowlists | ✓ WIRED | Snapshot allowlists stay in alias IDs, matching routing's alias-based allowlist checks. |
| `apps/edge-api/internal/authz/client.go` | `apps/control-plane/internal/apikeys/http.go` | `/internal/apikeys/resolve` | ✓ WIRED | Edge falls back to the internal resolver instead of querying Postgres directly. |
| `apps/control-plane/internal/accounting/http.go` | `apps/control-plane/internal/accounting/service.go` | `CreateReservationInput.APIKeyID` | ✓ WIRED | HTTP parsing now propagates `api_key_id` into reservation creation. |
| `apps/control-plane/internal/accounting/service.go` | `apps/control-plane/internal/apikeys/service.go` | `RecordUsageFinalization` | ✓ WIRED | Finalize calls API-key budget, usage-rollup, and `last_used_at` hooks on attributed attempts. |
| `apps/edge-api/internal/authz/ratelimit.go` | `apps/edge-api/internal/errors/openai.go` | 429 limiter response path | ✗ NOT WIRED | The planned limiter artifact is missing, so the intended quota path does not exist. |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| --- | --- | --- | --- | --- |
| KEY-01 | 05-01, 05-04 | Account owner can create multiple API keys and sees each raw secret only once. | ✓ SATISFIED | Create returns `secret`, list omits it, and live smoke confirmed one-time reveal plus multi-key listing. |
| KEY-02 | 05-02 | Account owner can set per-key nickname, expiration, allowed models, and Hive Credit budget. | ✓ SATISFIED | Create/rotate accept nickname and expiration; `/policy` stores allowlists and budget fields in `api_key_policies`. |
| KEY-03 | 05-01 | Account owner can revoke or rotate one key without affecting siblings. | ✓ SATISFIED | Rotation revokes only the source key, revoke is terminal, and live smoke confirmed rotated-away and revoked secrets return 401 immediately. |
| KEY-04 | 05-03, 05-04 | Hive tracks usage and spend per API key and per model. | ✓ SATISFIED | Attempts, usage events, and rollups carry `api_key_id` and `model_alias`; live smoke confirmed `api_key_id`, `completed`, and `last_used_at`. |
| KEY-05 | 05-02, 05-03, 05-04 | Hive enforces account-tier and per-key rate limits and quotas on the hot path. | ✗ BLOCKED | `api_key_rate_policies` is never projected into `AuthSnapshot`, the edge limiter falls back to defaults, and the planned quota artifacts are missing. |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| `apps/control-plane/internal/apikeys/service.go` | 342 | Hard-coded `BudgetConsumedCredits: 0` | 🛑 Blocker | Over-budget keys cannot be denied from live budget projections. |
| `apps/control-plane/internal/apikeys/service.go` | 343 | Hard-coded `BudgetReservedCredits: 0` | 🛑 Blocker | Reserved spend never contributes to hot-path budget enforcement. |
| `apps/edge-api/internal/authz/client.go` | 156 | Hard-coded fallback rate limits | 🛑 Blocker | KEY-05 policy and quota enforcement cannot reflect durable per-key or account-tier settings. |
| `apps/edge-api/internal/authz/client.go` | 111 | `TODO` logger hookup | ℹ️ Info | Redis cache read failures remain silent during edge authorization fallback. |

### Gaps Summary

Phase 05 is close but not complete. Multi-key lifecycle safety, immediate revoke and rotate invalidation, and end-to-end API-key attribution are present. The remaining miss is the hot path itself: the control plane does not project live budget window totals into `AuthSnapshot`, and the edge does not consume durable rate-policy data or implement the planned quota limiter artifacts. That leaves `KEY-05` incomplete and means the roadmap promise for quick over-budget and full hot-path rate-limit rejection is not yet achieved.

### Verification Notes

- No previous `05-VERIFICATION.md` existed, so this was an initial verification.
- Package tests were re-run in Docker for the touched control-plane and edge-api packages and exited successfully.
- The provided live smoke results were used as supporting evidence for revoke/rotate invalidation and end-to-end `api_key_id` attribution, but they do not close the code-level hot-path gaps above.

---

_Verified: 2026-04-01T21:45:12Z_
_Verifier: Claude (gsd-verifier)_
