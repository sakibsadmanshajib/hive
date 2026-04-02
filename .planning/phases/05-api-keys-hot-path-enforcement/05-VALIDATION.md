---
phase: 5
slug: api-keys-hot-path-enforcement
status: revised
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-31
updated: 2026-04-02
---

# Phase 5 - Validation Strategy

> Revised Nyquist validation contract after the phase split, execution summaries, and the 2026-04-02 validation-gap audit.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` across focused `apps/control-plane` and `apps/edge-api` packages with targeted named-test runs for each task seam |
| **Config file** | `deploy/docker/docker-compose.yml` |
| **Quick run command** | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/apikeys ./apps/control-plane/internal/accounting ./apps/control-plane/internal/usage ./apps/edge-api/internal/authz ./apps/edge-api/internal/errors ./apps/edge-api/cmd/server -count=1"` |
| **Full suite command** | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/apikeys ./apps/control-plane/internal/accounting ./apps/control-plane/internal/usage -count=1" && docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz ./apps/edge-api/internal/errors ./apps/edge-api/cmd/server -count=1"` |
| **Estimated runtime** | ~90 seconds for the full Phase 5 package set in Docker |

---

## Sampling Rate

- **After every task commit:** Run the most specific touched package tests from the table below.
- **After every wave:** Run the quick run command.
- **Before `$gsd-verify-work`:** Run the full suite command and resolve all failures.
- **Max feedback latency:** 90 seconds.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | Status |
|---------|------|------|-------------|-----------|-------------------|--------|
| 05-01-01 | 01 | 1 | KEY-01, KEY-03 | schema + service | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/apikeys -run 'Test(CreateKeyStoresHashAndRedactedSuffixOnly|RotateKeyCreatesReplacementAndRevokesSource|DisableAndEnableKeyTransitionsState|ExpiredKeyIsReportedWithoutMutatingSiblingKeys|RevokeKeyIsTerminal)' -count=1"` | ✅ green |
| 05-01-02 | 01 | 1 | KEY-01, KEY-03 | HTTP + summaries | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/apikeys -run 'Test(CreateKeyReturnsSecretOnlyOnCreate|ListKeysNeverReturnsSecret|ListKeysReturnsCustomerVisibleSummaries|GetKeyReturnsSummariesWithoutSecret|RotateKeyRevokesOnlyTarget)' -count=1"` | ✅ green |
| 05-02-01 | 02 | 2 | KEY-02, KEY-05 | policy storage + snapshot | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/apikeys -run 'Test(CreateKeyCreatesDefaultPolicyRow|UpdatePolicyResolvesGroupMembersAndOverrides|ResolveSnapshotReturnsAllModelsWhenAllowAllModelsIsSet|ResolveSnapshotReturnsExpiredWhenExpiresAtHasPassed|ListKeyViewsExposeDefaultSummaries)' -count=1"` | ✅ green |
| 05-02-02 | 02 | 2 | KEY-02, KEY-05 | HTTP + resolver | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/apikeys -run 'Test(PolicyRouteUpdatesSummaries|InternalResolveRouteIncludesSeparateRatePolicyFields)' -count=1"` | ✅ green |
| 05-03-01 | 03 | 3 | KEY-02, KEY-05 | edge snapshot resolution | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz -run 'Test(ResolveHydratesRedisFromControlPlane|CheckAccessRejectsProjectedBudgetOverrun|CheckAccessRejectsDisallowedAliasWithoutRemap)' -count=1"` | ✅ green |
| 05-03-02 | 03 | 3 | KEY-02, KEY-05 | edge authorizer + server | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz ./apps/edge-api/cmd/server -run 'Test(AuthorizeReturnsInsufficientQuotaOnProjectedBudgetOverrun|ModelsRouteRequiresValidAPIKey)' -count=1"` | ✅ green |
| 05-04-01 | 04 | 4 | KEY-01, KEY-05 | cache invalidation | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/apikeys -run 'Test(RevokeKeyInvalidatesCachedSnapshot|RotateKeyInvalidatesOldAndNewSnapshots|UpdatePolicyInvalidatesCachedSnapshot)' -count=1"` | ✅ green |
| 05-04-02 | 04 | 4 | KEY-04 | attribution surfaces | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/accounting ./apps/control-plane/internal/usage -run 'Test(CreateReservationPropagatesAPIKeyID|FinalizeReservationRecordsCompletedEventAndUpdatesAPIKeyUsage|ListEventsIncludesAPIKeyIDWhenPresent)' -count=1"` | ✅ green |
| 05-05-01 | 05 | 5 | KEY-04, KEY-05 | accounting + budget windows | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/accounting ./apps/control-plane/internal/usage -run 'Test(FinalizeReservationUpdatesBudgetWindowAndUsageRollup|BudgetProjectionCountsOpenReservations|FinalizeReservationUsesConfiguredBudgetWindowKind)' -count=1"` | ✅ green |
| 05-05-02 | 05 | 5 | KEY-05 | snapshot projection | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/control-plane/internal/apikeys -run 'Test(ResolveSnapshotIncludesLiveBudgetWindow|ResolveSnapshotReturnsSeparateAccountAndKeyRatePolicies|ApplyReservationDeltaUsesConfiguredMonthlyWindow|BudgetAffectingDeltaInvalidatesSnapshot|InternalResolveRouteIncludesSeparateRatePolicyFields)' -count=1"` | ✅ green |
| 05-06-01 | 06 | 6 | KEY-05 | Redis limiter | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz -run 'Test(LimiterUsesSeparateAccountAndKeyThresholds|WindowScoreUsesWeightedFreeTokens|LimiterRejectsMissingAccountPolicy)' -count=1"` | ✅ green |
| 05-06-02 | 06 | 6 | KEY-05 | 429 header handling | `docker compose -f deploy/docker/docker-compose.yml run --rm toolchain "cd /workspace && go test ./apps/edge-api/internal/authz ./apps/edge-api/internal/errors ./apps/edge-api/cmd/server -run 'Test(AuthorizeReturnsRateLimitHeadersOnlyFor429|PermanentFailuresOmitRetryHeaders|ModelsRouteUsesLimiter)' -count=1"` | ✅ green |

*Status: ✅ green · ⚠️ partial · ❌ red*

---

## Wave 0 Requirements

None. Every Phase 5 task now has a concrete automated verification command and all commands run green in the audited workspace.

---

## Manual-Only Verifications

None.

---

## Validation Sign-Off

- [x] All tasks have runnable automated verification commands.
- [x] Sampling continuity: no three consecutive tasks without an automated verify.
- [x] No Wave 0 gaps remain.
- [x] No watch-mode flags are used.
- [x] Feedback latency target is under 90 seconds.
- [x] `nyquist_compliant: true` is set in frontmatter.

**Approval:** ready

---

## Validation Audit 2026-04-02

| Metric | Count |
|--------|-------|
| Gaps found | 3 |
| Resolved | 3 |
| Escalated | 0 |

- Added `TestPolicyRouteUpdatesSummaries` to prove policy mutations update customer-visible key summaries.
- Added `TestResolveHydratesRedisFromControlPlane` to cover Redis cache miss rehydration and cache population through `/internal/apikeys/resolve`.
- Added projected-budget regressions at both the `CheckAccess` and `Authorize` layers, then fixed the edge path so `estimatedCredits` participates in quota denial.
