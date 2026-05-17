---
phase: 18
plan: 04
subsystem: control-plane/accounts
tags: [rbac, wire-shape, permissions, gates-removal, regression-guard]
dependency_graph:
  requires: [18-01, 18-02, 18-03]
  provides: [viewer.permissions[], regression-guard-no-gates]
  affects: [internal/accounts, internal/apikeys]
tech_stack:
  added: []
  patterns: [policy.AllGranted(actor)->[]string, wire-shape regression guard, adversarial JSON walk]
key_files:
  created:
    - apps/control-plane/internal/accounts/service_wire_shape_test.go
  modified:
    - apps/control-plane/internal/accounts/types.go
    - apps/control-plane/internal/accounts/service.go
    - apps/control-plane/internal/accounts/http.go
    - apps/control-plane/internal/accounts/service_test.go
    - apps/control-plane/internal/accounts/http_test.go
    - apps/control-plane/internal/apikeys/http.go
    - apps/control-plane/internal/apikeys/limits_http_test.go
decisions:
  - Gates struct deleted entirely; Permissions []string replaces it in ViewerContext
  - AllGranted(actor) nil-guarded to []string{} never null (Phase 17 explicit-empty convention)
  - Regression guard walks full JSON tree recursively to catch nested re-introduction
  - apikeys/limits_http_test.go nonOwnerVC updated to Permissions:[]string{} (Gates struct gone)
metrics:
  duration: "~30 min"
  completed: "2026-05-15"
  tasks_completed: 2
  files_changed: 7
---

# Phase 18 Plan 04: Viewer Response Wire Flip Summary

**One-liner:** gates.{can_invite_members,can_manage_api_keys} deleted from viewer response; replaced with permissions:[]string populated via policy.AllGranted(actor) — hard flip, no compat aliases.

## Tasks Completed

| Task | Description | Commit |
|------|-------------|--------|
| 4A | Drop Gates struct, emit permissions:[] in viewer response | 6eec4df |
| 4B | Wire-shape regression guard + cross-package audit | ed0aa93 |

## Wire Shape Before / After

**Before (Wave 2 shape):**
```json
{
  "user": {...},
  "current_account": {...},
  "memberships": [...],
  "gates": {
    "can_invite_members": true,
    "can_manage_api_keys": true
  }
}
```

**After (Plan 04 shape):**
```json
{
  "user": {...},
  "current_account": {...},
  "memberships": [...],
  "permissions": ["api_keys.read", "api_keys.write", "billing.view", "members.invite", "members.manage", "workspace.settings"]
}
```

permissions is always a JSON array of strings (never null) — empty [] for unverified/member actors.
Each string is a known authz.Permission constant (enforced by TestViewerResponseWireShape_PermissionsIsArrayOfPermStrings).
gates key entirely absent from the response object (enforced by 3 independent tests).

## What Was Built

### Task 4A (6eec4df)

types.go: Gates struct deleted. ViewerContext.Gates Gates replaced with ViewerContext.Permissions []string.

service.go: Removed 4-line gates derivation block. Now calls s.policy.AllGranted(chosenActor) — sorted []string; nil-guarded to []string{}.

http.go: viewerContextResponse — "gates":map replaced with "permissions":vc.Permissions (nil-safe via inline closure).

service_test.go: Two Gates tests renamed and flipped:
- UnverifiedGatesAreFalse -> UnverifiedPermissionsEmpty: asserts members.invite and api_keys.write absent.
- VerifiedOwnerGatesAreTrue -> VerifiedOwnerHasKeyPermissions: asserts members.invite and api_keys.write present.

http_test.go: TestViewerHandler_ReturnsViewerContext asserts permissions present AND gates absent. Added TestViewerEndpoint_OmitsGatesKey wire-shape guard.

apikeys/http.go: Historical Gates.CanManageAPIKeys comment cleaned.

### Task 4B (ed0aa93)

service_wire_shape_test.go (new):
- TestViewerResponseWireShape_NoGates_NoLegacy: recursive JSON tree walk asserts gates/can_invite_members/can_manage_api_keys/allowedUnverifiedRoutes absent at every level.
- TestViewerResponseWireShape_PermissionsIsArrayOfPermStrings: asserts permissions is []string of known authz.Permission constants; verified owner has members.invite + api_keys.write.

apikeys/limits_http_test.go: nonOwnerVC updated from Gates:{CanManageAPIKeys:false} to Permissions:[]string{}.

## Adversarial Grep Output

```
$ grep -rn '"gates"\|can_invite_members\|can_manage_api_keys\|allowedUnverifiedRoutes\|CanInviteMembers\|CanManageAPIKeys' \
    apps/control-plane/ --include='*.go' | grep -v '_test\.go'
(no output — zero hits)
```

Zero hits in production code. All remaining references are in http_test.go as absence assertions (allowed per spec).

## Cross-Package Wire Audit

All hits from the full audit grep: accounts/http_test.go only (absence assertions). Zero production-code hits.

## Regression-Guard Failure Proof

Injected "gates":{} into http.go viewerContextResponse temporarily:
- TestViewerHandler_ReturnsViewerContext: FAIL (response must NOT contain 'gates' field)
- TestViewerEndpoint_OmitsGatesKey: FAIL (wire-shape violation: 'gates' key must not appear)
- TestViewerResponseWireShape_NoGates_NoLegacy: FAIL (banned key "gates" found at "root.gates")

Reverted -> all 3 pass green.

## Deviations from Plan

**1. [Rule 1 - Bug] apikeys/limits_http_test.go referenced deleted accounts.Gates**
- Found during: Task 4B full suite run
- Issue: nonOwnerVC used accounts.Gates{CanManageAPIKeys: false} — struct deleted in 4A.
- Fix: Updated to Permissions: []string{} — semantically identical.
- Commit: ed0aa93

**2. [Rule 1 - Bug] apikeys/http.go had residual historical comment matching adversarial pattern**
- Found during: adversarial grep (Task 4A)
- Fix: Reworded comment to remove Gates.CanManageAPIKeys reference.
- Commit: 6eec4df

## Verification

- go test ./apps/control-plane/internal/accounts/... -count=1 -v: 21 tests PASS
- go test ./apps/control-plane/... -count=1: 23/23 packages ok
- lint-no-bare-role-check.mjs apps/control-plane/internal: 144 files clean
- grep -n '"gates":' http.go: no match
- grep -n '"permissions":' http.go: 1 match (line 229)
- Adversarial grep: empty (zero production hits)

## Self-Check: PASSED

Files exist:
- apps/control-plane/internal/accounts/service_wire_shape_test.go: created
- Commits 6eec4df + ed0aa93: present in git log
- Adversarial grep: zero production hits confirmed
- Full suite: 23/23 green
