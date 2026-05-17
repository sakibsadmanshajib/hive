---
phase: 18
plan: 03
subsystem: control-plane/handlers
tags: [rbac, authz, migration, policy.Can, handler-migration]
dependency_graph:
  requires: [18-01, 18-02]
  provides: [authz-migrated-handlers]
  affects: [internal/accounts, internal/apikeys, internal/accounting, internal/budgets, internal/ledger, internal/profiles, internal/usage]
tech_stack:
  added: []
  patterns: [policy.Can(actor, perm) in handler resolveCurrentAccountID, ActorFor in every handler]
key_files:
  created: []
  modified:
    - apps/control-plane/internal/accounts/http.go
    - apps/control-plane/internal/accounts/http_test.go
    - apps/control-plane/internal/accounts/service.go
    - apps/control-plane/internal/accounts/service_test.go
    - apps/control-plane/internal/apikeys/http.go
    - apps/control-plane/internal/apikeys/http_test.go
    - apps/control-plane/internal/accounting/http.go
    - apps/control-plane/internal/accounting/http_test.go
    - apps/control-plane/internal/budgets/http.go
    - apps/control-plane/internal/budgets/http_test.go
    - apps/control-plane/internal/ledger/http.go
    - apps/control-plane/internal/ledger/http_test.go
    - apps/control-plane/internal/profiles/http.go
    - apps/control-plane/internal/profiles/http_test.go
    - apps/control-plane/internal/usage/http.go
    - apps/control-plane/internal/usage/http_test.go
decisions:
  - billing.view has RequiresVerified=false — unverified owners CAN view budget (test updated from 403→200)
  - GateError code changed from email_verification_required to permission_denied for CreateInvitation
  - handleListMembers gates on PermMembersInvite not bare EmailVerified
  - budgets requireWorkspaceOwner replaced with policy.Can(actor, PermBillingWrite)
  - profiles billing-profile gates on PermWorkspaceSettings (verified owner only)
  - analytics.view + ledger.view grant any verified actor (member OR owner) — no role filter
metrics:
  duration: "~45 min"
  completed: "2026-05-14"
  tasks_completed: 2
  files_changed: 16
---

# Phase 18 Plan 03: Handler Migration to policy.Can Summary

**One-liner:** All 8 control-plane handler packages migrated from bare EmailVerified/Gates/roleSvc checks to stateless policy.Can(actor, perm), with full authz matrix test coverage.

## Tasks Completed

| Task | Description | Commit |
|------|-------------|--------|
| 3A | accounts + apikeys handlers migrated | 440e831 |
| 3B | accounting, budgets, ledger, profiles, usage migrated | ac8a3e3 |

## What Was Built

### Task 3A — accounts + apikeys (440e831)

**accounts/service.go CreateInvitation:** resolves membership first, calls policy.Can(actor, PermMembersInvite). Returns GateError{Code: "permission_denied"} instead of ad hoc EmailVerified check.

**accounts/http.go handleListMembers:** builds Actor from viewerContext, gates on PermMembersInvite.

**apikeys/http.go resolveViewerContext:** replaced Gates.CanManageAPIKeys with policy.Can(actor, PermAPIKeysWrite). Added testActor override field for test isolation.

Tests updated: invitation tests now expect code=permission_denied. APIkeys tests add TestHandler_AuthzMatrix (4 cases: owner+v=201, owner+u=403, member+u=403, admin+override=201).

### Task 3B — remaining 6 modules (ac8a3e3)

Each module follows the same pattern in resolveCurrentAccountID (or resolveVerifiedCurrentAccountID for profiles):
1. Build actor via accounts.ActorFor(viewer, Membership{...}, false)
2. Call h.policy.Can(actor, permXxx) — return 403 if denied
3. Return accountID if granted

| Module | Permission | RequiresVerified | Granted to |
|--------|-----------|-----------------|------------|
| accounting | PermAnalyticsView | true | any verified (owner or member) |
| budgets GET | PermBillingView | false | any owner (verified or not) |
| budgets write | PermBillingWrite | true | verified owner only |
| ledger | PermLedgerView | true | any verified (owner or member) |
| profiles billing | PermWorkspaceSettings | true | verified owner only |
| usage | PermAnalyticsView | true | any verified (owner or member) |

Each module gains TestHandler_*AuthzMatrix table-driven tests.

**Key behaviour change:** billing.view (PermBillingView) has RequiresVerified=false per the Phase 18 registry. Unverified owners can view their budget. TestGetBudgetRejectsUnverifiedViewer renamed to TestGetBudgetAllowsUnverifiedOwner and updated to expect 200.

## Deviations from Plan

**1. [Rule 1 - Bug] Test expected email_verification_required got permission_denied**
- Found during: Task 3A
- Issue: accounts/http_test.go + service_test.go expected old error code.
- Fix: Updated both test files to expect permission_denied.

**2. [Rule 1 - Bug] TestGetBudgetRejectsUnverifiedViewer expected 403 got 200**
- Found during: Task 3B
- Issue: billing.view RequiresVerified=false — old test was wrong per Phase 18 matrix.
- Fix: Renamed test, changed expected status to 200 with explanatory comment.

**3. [Rule 1 - Bug] accounting TestHandler_AuthzMatrix used policy_mode=soft**
- Found during: Task 3B verification run
- Issue: Reservation endpoint validates policy_mode; "soft" is invalid (strict or temporary_overage only).
- Fix: Changed test body to use "strict".

## Adversarial Grep

Zero hits for bare role/email checks outside authz/platform:
- grep -rn 'chosen.Role == "owner"\|.EmailVerified &&\|IsPlatformAdmin\b' apps/control-plane/internal/ --include='*.go' | grep -v internal/(authz|platform)/
- Remaining hits are all legitimate: grants/service.go interface definition (PAY-14-08 contract), actor_resolver.go adapter site, test stubs.

## Verification

- go test 7 packages: all PASS
- lint-no-bare-role-check.mjs: 143 files clean
- gates.* fields still present in viewer response (Wave 3 owns removal — not touched)

## Self-Check: PASSED

- All 16 modified files committed in 440e831 + ac8a3e3
- Adversarial grep: 0 gating violations
- Lint: clean
