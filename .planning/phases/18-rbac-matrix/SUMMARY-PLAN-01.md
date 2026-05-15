---
phase: 18-rbac-matrix
plan: "01"
subsystem: authz
tags: [rbac, authz, permissions, codegen, lint, go, typescript]
dependency_graph:
  requires: []
  provides:
    - apps/control-plane/internal/authz
    - apps/web-console/lib/control-plane/permissions.generated.ts
    - packages/openai-contract/scripts/lint-no-bare-role-check.mjs
  affects:
    - Wave 2 handler migration
    - Wave 3 wire flip
    - Wave 4 FE migration
tech_stack:
  added:
    - apps/control-plane/internal/authz (new Go package)
    - apps/control-plane/cmd/gen-permissions (new Go cmd)
    - Makefile (new root Makefile)
  patterns:
    - Typed Permission string const enum
    - Stateless Policy struct with Can() decision function
    - ActorResolver func type for testable middleware injection
    - Go-to-TS codegen via cmd program + AllPermissions() registry
    - Node.js lint script mirroring lint-no-customer-usd.mjs shape
key_files:
  created:
    - apps/control-plane/internal/authz/permissions.go
    - apps/control-plane/internal/authz/policy.go
    - apps/control-plane/internal/authz/policy_test.go
    - apps/control-plane/cmd/gen-permissions/main.go
    - apps/web-console/lib/control-plane/permissions.generated.ts
    - packages/openai-contract/scripts/lint-no-bare-role-check.mjs
    - Makefile
  modified: []
decisions:
  - "analytics.view and ledger.view RequiresVerified=true (audit: accounting/http.go:347, ledger/http.go:165, usage/http.go:393 all gate on !EmailVerified)"
  - "billing.view and api_keys.read RequiresVerified=false (read-only, no existing verification gate)"
  - "grants.create and platform.admin are admin-overlay-only"
  - "analytics.view and ledger.view granted to any verified actor (owner OR member) matching audit"
  - "Toolchain uses --entrypoint /bin/sh (bash not found in container)"
  - "-race disabled in toolchain (CGO_ENABLED=0)"
  - "AllPermissions() sorted lexically for stable codegen output"
metrics:
  duration: "~35min"
  completed: "2026-05-14"
  tasks: 4
  files: 7
---

# Phase 18 Plan 01: authz Package + Matrix Test + Codegen + Lint Summary

**One-liner:** Stateless Policy.Can(actor, perm) engine with 11 typed Permission consts, 55-case matrix test (the auditable spec), Go-to-TS codegen emitter, and CI lint blocking bare role checks outside the authz package.

## Tasks Completed

| Task | Name | Commit |
|------|------|--------|
| 1A | authz package: permissions registry + Policy.Can + RequirePermission | ad9f9f8 |
| 1B | Table-driven matrix test (55 cases) | 6cc6a8d |
| 1C | Go-to-TS codegen emitter + Makefile target + generated TS file | 3e591e0 |
| 1D | lint-no-bare-role-check.mjs CI lint script | 01cb217 |

## Acceptance Criteria Results

Task 1A:
- grep -c "Permission = " permissions.go -> 11
- grep -c "RequiresVerified: true" permissions.go -> 9 (billing.view=false, api_keys.read=false)
- go build ./apps/control-plane/internal/authz/... -> exit 0
- Policy.Can, AllGranted, ErrNoViewer signatures confirmed

Task 1B:
- go test -run TestPolicy -> PASS: TestPolicyMatrix, TestPolicyAllGrantedReturnsSorted
- Full suite: 5/5 PASS (TestPolicyMatrix, TestPolicyAllGrantedReturnsSorted, TestRequirePermissionMiddleware, TestRequirePermissionMiddlewareResolverError, TestUnknownPermissionRequiresVerified)
- grep -c "want: true" policy_test.go -> 24 (>= 21 minimum)

Task 1C:
- make gen-permissions -> exit 0 (idempotent: confirmed twice)
- grep -c '^  "' permissions.generated.ts -> 11
- git diff --exit-code permissions.generated.ts -> exit 0

Task 1D:
- node lint-no-bare-role-check.mjs apps/control-plane/internal/authz/ -> exit 0
- node lint-no-bare-role-check.mjs apps/control-plane/internal/accounts/service.go -> exit 1 (pre-migration red-state, expected)
- FORBIDDEN_PATTERNS and ALLOWLIST_DIRS constants present

## Decision Matrix

| Permission | owner+v | owner+u | member+v | member+u | admin |
|---|:-:|:-:|:-:|:-:|:-:|
| billing.view | Y | Y | N | N | Y |
| billing.write | Y | N | N | N | Y |
| api_keys.read | Y | Y | N | N | Y |
| api_keys.write | Y | N | N | N | Y |
| analytics.view | Y | N | Y | N | Y |
| members.invite | Y | N | N | N | Y |
| members.manage | Y | N | N | N | Y |
| workspace.settings | Y | N | N | N | Y |
| grants.create | N | N | N | N | Y |
| ledger.view | Y | N | Y | N | Y |
| platform.admin | N | N | N | N | Y |

analytics.view and ledger.view grant verified members (audit: no role check in those handlers).

## Deviations from Plan

None — plan executed exactly as written.

-race flag: toolchain container has CGO_ENABLED=0; race detector requires CGO. Tests run without -race, consistent with all prior phase Go test invocations.

## Self-Check

## Self-Check: PASSED
All 7 files found. All 4 commits verified.
