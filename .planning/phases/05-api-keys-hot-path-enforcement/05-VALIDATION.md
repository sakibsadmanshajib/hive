---
phase: 5
slug: api-keys-hot-path-enforcement
status: revised
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-31
updated: 2026-04-01
---

# Phase 5 - Validation Strategy

> Revised Nyquist validation contract after the Phase 5 plan split and verification feedback.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` for focused `apps/control-plane` and `apps/edge-api` packages, plus targeted `rg` smoke checks for route/contract wiring |
| **Config file** | `deploy/docker/docker-compose.yml` |
| **Quick run command** | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/apikeys ./apps/control-plane/internal/accounting ./apps/control-plane/internal/usage ./apps/edge-api/internal/authz ./apps/edge-api/internal/errors ./apps/edge-api/cmd/server -count=1"` |
| **Full suite command** | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/apikeys ./internal/accounting ./internal/usage -count=1" && docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz ./apps/edge-api/internal/errors ./apps/edge-api/cmd/server -count=1"` |
| **Estimated runtime** | ~75 seconds once the full Phase 5 package set exists |

---

## Sampling Rate

- **After every task commit:** Run the most specific touched package tests from the table below.
- **After every wave:** Run the quick run command.
- **Before `$gsd-verify-work`:** Run the full suite command and resolve all failures.
- **Max feedback latency:** 75 seconds.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | Status |
|---------|------|------|-------------|-----------|-------------------|--------|
| 05-01-01 | 01 | 1 | KEY-01, KEY-03 | schema + service | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/apikeys/... -run 'Test(CreateKeyStoresHashAndRedactedSuffixOnly|RotateKeyCreatesReplacementAndRevokesSource|RevokeKeyIsTerminal)' -count=1"` | ⬜ pending |
| 05-01-02 | 01 | 1 | KEY-01, KEY-03 | HTTP + summaries | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/apikeys/... -run 'Test(CreateKeyReturnsSecretOnlyOnCreate|ListKeysReturnsCustomerVisibleSummaries|GetKeyReturnsSummariesWithoutSecret)' -count=1"` | ⬜ pending |
| 05-02-01 | 02 | 2 | KEY-02, KEY-05 | policy storage + snapshot | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/apikeys/... -run 'Test(CreateKeyCreatesDefaultPolicyRow|ResolveSnapshotReturnsAllModelsWhenAllowAllModelsIsSet|ListKeyViewsExposeDefaultSummaries)' -count=1"` | ⬜ pending |
| 05-02-02 | 02 | 2 | KEY-02, KEY-05 | HTTP + resolver | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/apikeys/... -run 'Test(PolicyRouteUpdatesSummaries|InternalResolveRouteReturnsSnapshotJSON)' -count=1"` | ⬜ pending |
| 05-03-01 | 03 | 3 | KEY-02, KEY-05 | edge snapshot resolution | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz -run 'Test(ResolveHydratesRedisFromControlPlane|CheckAccessRejectsProjectedBudgetOverrun|CheckAccessRejectsDisallowedAliasWithoutRemap)' -count=1"` | ⬜ pending |
| 05-03-02 | 03 | 3 | KEY-02, KEY-05 | edge authorizer + server | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz ./apps/edge-api/cmd/server -run 'Test(AuthorizeReturnsInsufficientQuotaOnProjectedBudgetOverrun|ModelsRouteRequiresValidAPIKey)' -count=1"` | ⬜ pending |
| 05-04-01 | 04 | 4 | KEY-01, KEY-05 | cache invalidation | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/apikeys/... -run 'Test(RevokeInvalidatesCachedSnapshot|RotateInvalidatesBothSnapshots|UpdatePolicyInvalidatesCachedSnapshot)' -count=1"` | ⬜ pending |
| 05-04-02 | 04 | 4 | KEY-04 | attribution surfaces | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/accounting/... ./apps/control-plane/internal/usage/... -run 'Test(CreateReservationPropagatesAPIKeyID|FinalizeWritesCompletedUsageEvent|UsageEventsIncludeAPIKeyID)' -count=1"` | ⬜ pending |
| 05-05-01 | 05 | 5 | KEY-04, KEY-05 | accounting + budget windows | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/accounting/... ./internal/usage/... -run 'Test(FinalizeReservationUpdatesBudgetWindowAndUsageRollup|BudgetProjectionCountsOpenReservations|FinalizeReservationUsesConfiguredBudgetWindowKind)' -count=1"` | ⬜ pending |
| 05-05-02 | 05 | 5 | KEY-05 | snapshot projection | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace/apps/control-plane && go test ./internal/apikeys/... -run 'Test(ResolveSnapshotIncludesLiveBudgetWindow|ResolveSnapshotReturnsSeparateAccountAndKeyRatePolicies|InternalResolveRouteIncludesSeparateRatePolicyFields)' -count=1"` | ⬜ pending |
| 05-06-01 | 06 | 6 | KEY-05 | Redis limiter | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz -run 'Test(LimiterUsesSeparateAccountAndKeyThresholds|WindowScoreUsesWeightedFreeTokens|LimiterRejectsMissingAccountPolicy)' -count=1"` | ⬜ pending |
| 05-06-02 | 06 | 6 | KEY-05 | 429 header handling | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz ./apps/edge-api/internal/errors ./apps/edge-api/cmd/server -run 'Test(AuthorizeReturnsRateLimitHeadersOnlyFor429|PermanentFailuresOmitRetryHeaders|ModelsRouteUsesLimiter)' -count=1"` | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

None. Every plan task now includes a concrete runnable `<automated>` command, and the new files introduced by the plans are created inside the task scopes themselves.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Customer-visible key list/detail responses stay useful without leaking secrets | KEY-01, KEY-02, KEY-03 | Requires reviewing summary wording and one-time-secret behavior, not just field presence | Create multiple keys, rotate one, disable one, and revoke one. Confirm list/detail payloads show nickname, suffix, timestamps, expiration summary, budget summary, and allowlist summary without ever returning the raw secret again after create/rotate. |
| Public denial posture stays OpenAI-style and provider-blind across all hot-path failures | KEY-02, KEY-05 | Requires judgment about wording and header posture | Trigger invalid-key, expired-key, disallowed-model, projected-budget, and rate-limit denials. Confirm permanent failures omit retry metadata while actual rate limits include only meaningful reset headers. |

---

## Validation Sign-Off

- [x] All tasks have runnable `<automated>` verification commands.
- [x] Sampling continuity: no three consecutive tasks without an automated verify.
- [x] No Wave 0 gaps remain.
- [x] No watch-mode flags are used.
- [x] Feedback latency target is under 90 seconds.
- [x] `nyquist_compliant: true` is set in frontmatter.

**Approval:** ready
