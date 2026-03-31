---
phase: 5
slug: api-keys-hot-path-enforcement
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-31
---

# Phase 5 - Validation Strategy

> Draft Nyquist validation contract derived from `05-RESEARCH.md` before plan execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` for `apps/control-plane` and `apps/edge-api`, plus Redis-backed integration checks where hot-path policy behavior crosses the edge/control-plane boundary |
| **Config file** | `deploy/docker/docker-compose.yml` |
| **Quick run command** | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/accounts ./apps/control-plane/internal/accounting ./apps/control-plane/internal/usage ./apps/control-plane/internal/routing ./apps/edge-api/internal/errors ./apps/edge-api/cmd/server -count=1"` |
| **Full suite command** | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./... -count=1" && docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/... -count=1"` |
| **Estimated runtime** | ~150 seconds once the Phase 5 key, limiter, and edge-auth packages exist |

---

## Sampling Rate

- **After every task commit:** Run the most specific touched Go package test, with the quick run command as the minimum cross-package smoke check for key policy changes.
- **After every plan wave:** Run the full control-plane suite plus the edge Go suite.
- **Before `$gsd-verify-work`:** Full suite must be green.
- **Max feedback latency:** 150 seconds.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 05-01-01 | 01 | 1 | KEY-01, KEY-03 | repository + service | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/apikeys/... -run 'Test(CreateKeyStoresHashOnly|RotateKeyRevokesPreviousSnapshot|DisableAndReEnableKey)' -count=1"` | ❌ W0 | ⬜ pending |
| 05-01-02 | 01 | 1 | KEY-01, KEY-03 | HTTP + authz | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/apikeys/... ./internal/accounts/... -run 'Test(CreateKeyRequiresVerifiedOwner|ListKeysRedactsSecrets|RevokeKeyDoesNotAffectSiblings)' -count=1"` | ❌ W0 | ⬜ pending |
| 05-02-01 | 02 | 2 | KEY-02, KEY-05 | service + Redis snapshot | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/apikeys/... ./internal/routing/... -run 'TestResolveKeyModelPolicy|TestBuildAuthSnapshotIncludesBudgetAndAllowlist' -count=1"` | ❌ W0 | ⬜ pending |
| 05-02-02 | 02 | 2 | KEY-02, KEY-05 | edge hot-path integration | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/... -run 'TestAuthorizeRequestRejectsExpiredOrDisallowedKey|TestOverBudgetAndRateLimitedResponsesStayOpenAICompatible' -count=1"` | ❌ W0 | ⬜ pending |
| 05-03-01 | 03 | 3 | KEY-04, KEY-05 | accounting + usage reconciliation | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/usage/... ./internal/accounting/... ./internal/apikeys/... -run 'TestFinalizeUsageAttributesAPIKeyAndModel|TestBudgetProjectionCountsReservations' -count=1"` | ❌ W0 | ⬜ pending |
| 05-03-02 | 03 | 3 | KEY-04, KEY-05 | Redis limiter + public metadata | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/... ./apps/edge-api/... -run 'TestRateLimitLuaReturnsRemainingAndReset|TestPermanentFailuresOmitRetryHeaders' -count=1"` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `supabase/migrations/*api_keys*` - add durable API key lifecycle, policy, and budget tables.
- [ ] `apps/control-plane/internal/apikeys/` - create repository, service, HTTP handlers, and tests for key issuance and policy management.
- [ ] edge auth middleware or request gate - add the Redis-backed auth snapshot consumer that enforces key state, expiry, allowlists, budgets, and rate limits before routing.
- [ ] Redis Lua script loader and limiter wrapper package - add atomic RPM, TPM, 5-hour, and weekly counter execution plus tests.
- [ ] projection/reconciliation tests - add coverage for snapshot invalidation, budget reservation math, `last_used_at` debounce, and per-key usage rollups.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Customer-visible key list is useful without leaking secrets | KEY-01, KEY-03 | Requires reviewing redaction, suffix display, and state summaries rather than just field presence | Create multiple keys, rotate one, disable one, and revoke one; confirm list/read payloads show nickname, suffix, timestamps, status, expiration summary, budget summary, and allowlist summary without ever returning the raw secret again. |
| Public denial posture stays OpenAI-style and provider-blind across all hot-path failures | KEY-02, KEY-05 | Requires judgment about wording, compatibility posture, and whether retry metadata is meaningful | Trigger invalid-key, expired-key, disallowed-model, over-budget, and rate-limit denials; inspect HTTP status, error body, and headers to confirm permanent failures omit retry headers while true rate limits include only meaningful reset metadata. |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies.
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify.
- [ ] Wave 0 covers all missing validation references.
- [ ] No watch-mode flags.
- [ ] Feedback latency < 150s.
- [ ] `nyquist_compliant: true` set in frontmatter.

**Approval:** pending
