# Phase 18: RBAC Matrix — Research

**Researched:** 2026-05-14
**Domain:** Authorization, Go authz package, TypeScript codegen, viewer-wire shape
**Confidence:** HIGH (all findings from direct code inspection of the live codebase)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Actor = `{ UserID, WorkspaceID, Role (member|owner), Verified bool, IsAdmin bool }`; `guest` upstream-only; tier deferred.
- 12-14 resource.action permissions (initial set in CONTEXT.md); typed Go `Permission` const; centralized `internal/authz/permissions.go`; per-perm `RequiresVerified` flag.
- Wire: `viewer.permissions: []string` + retain `role` + `verified`. `gates.*` and `allowedUnverifiedRoutes` HARD-DROPPED same PR. No backward-compat aliases.
- TS codegen: build step emits `apps/web-console/lib/control-plane/permissions.generated.ts` from Go registry. CI fails on drift.
- Enforcement: per-handler `policy.Can(actor, perm)` + `RequirePermission(perm)` middleware. `RequirePlatformAdmin` becomes `RequirePermission(PermPlatformAdmin)`.
- 7-commit migration sequence (AUDIT → matrix package → BE migrate → wire flip → FE migrate → tests → closure).

### Claude's Discretion
- Exact final permission list — start with the 12-14 above; add as audit reveals call sites.
- Codegen tool choice (tiny Go program vs shell script).
- Whether to package matrix as `internal/authz` or `internal/platform/authz`.
- Lint mechanism (Go analyzer vs grep-based CI step) — match Phase 17 precedent.

### Deferred Ideas (OUT OF SCOPE)
- Tier-aware permissions (HANDOFF-13)
- Fine-grained api_keys.create / rotate / revoke split
- Audit log of permission decisions
- Admin UI to view/edit permissions
- Edge-api hot-path authz (Phase 5 + 12)
- 2FA / KYC verification dimensions
- Read-only member / billing-only admin sub-roles
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| RBAC-18-01 | authz package: `Actor` struct, typed `Permission` consts with `RequiresVerified` flag, `Policy.Can()` decision function | §4 Permissions Registry |
| RBAC-18-02 | Backend handler migration: replace all 10 ad-hoc authz expressions across 6 modules with `policy.Can()` or `RequirePermission()` | §2 Backend Audit |
| RBAC-18-03 | Wire flip: `accounts/service.go` emits `permissions: []string`, drops `Gates` struct; `accounts/http.go` drops `gates.*` JSON block | §5 Wire Shape |
| RBAC-18-04 | TS codegen: Go emitter produces `permissions.generated.ts` from registry; CI drift-check blocks merge on mismatch | §6 TS Codegen |
| RBAC-18-05 | Web-console `can(viewer, permission)` helper replaces `canInviteMembers` / `canManageApiKeys`; `allowedUnverifiedRoutes` removed | §3 FE Audit + §5 |
| RBAC-18-06 | Consumer migration: 4 page files + `lib/control-plane/client.ts` + `viewer-gates.ts` updated to use `can()` | §3 FE Audit |
| RBAC-18-07 | CI lint: `lint-no-bare-role-check.mjs` blocks `viewer.Role ==`, `chosen.Role ==`, `EmailVerified &&` outside authz package | §7 Lint |
| RBAC-18-08 | Go regression tests: table-driven matrix (`policy_test.go`) + per-module integration tests (role × verified × permission) | §8 Test Strategy |
| RBAC-18-09 | Vitest matrix-parity: same (role, verified, permission) table as Go; subsumes existing 9-test `viewer-gates.test.ts` | §8 Test Strategy |
| RBAC-18-10 | Playwright unverified flow: one spec asserting redirect on `/console/billing` and `/console/api-keys` for unverified user | §8 Test Strategy |
</phase_requirements>

---

## 1. Domain Summary

Phase 18 replaces two distinct ad-hoc authorization mechanisms — (a) inline `EmailVerified && Role == "owner"` expressions scattered across 6 control-plane modules and (b) the `viewer.gates.can_*` boolean pair surfaced by `accounts/service.go` and consumed by 4 web-console pages — with a single, centralized `Policy.Can(actor, permission)` decision function in a new `internal/authz` package. The wire shape switches from `gates: {can_invite_members, can_manage_api_keys}` to `permissions: []string` (precomputed server-side for the resolved actor+workspace). A Go-to-TypeScript codegen step keeps the permission union type in sync across the boundary. All decisions are locked in 18-CONTEXT.md; this document provides the concrete audit tables and implementation details the planner needs to assign tasks.

**Primary recommendation:** Create `internal/authz` as a standalone package (not folded into `internal/platform/authz`) — it has no circular dependency risk and keeps `platform` as a pure role-store primitive. The authz package imports `platform` types; `platform` does not import `authz`.

---

## 2. Backend Authz Call-Site AUDIT

Complete enumeration of every ad-hoc authz expression in control-plane (non-test, non-platform-package files):

### 2a. Inline `EmailVerified` / Role checks (service + handler bodies)

| File:Line | Current Expression | Target Permission | RequiresVerified |
|-----------|-------------------|-------------------|-----------------|
| `accounts/service.go:80` | `viewer.EmailVerified && chosen.Role == "owner"` → `Gates.CanInviteMembers` | `members.invite` | true |
| `accounts/service.go:81` | `viewer.EmailVerified && chosen.Role == "owner"` → `Gates.CanManageAPIKeys` | `api_keys.write` | true |
| `accounts/service.go:150` | `!viewer.EmailVerified` → GateError in `CreateInvitation` | `members.invite` (policy check replaces this inline) | true |
| `accounts/service.go:164` | `m.Role == "owner"` loop check in `CreateInvitation` | `members.invite` (policy check replaces this inline) | true |
| `accounts/http.go:72` | `!vc.User.EmailVerified` → 403 in `handleListMembers` | `members.invite` | true |
| `apikeys/http.go:128` | `!vc.Gates.CanManageAPIKeys` → 403 in mutation handlers | `api_keys.write` | true |
| `apikeys/http.go:105` | `!h.testVC.Gates.CanManageAPIKeys` (test fixture path) | `api_keys.write` | true |
| `accounting/http.go:347` | `!viewerContext.User.EmailVerified` → 403 | `analytics.view` | true |
| `budgets/http.go:147` | `!viewerContext.User.EmailVerified` → 403 | `billing.view` | true |
| `budgets/http.go:486` | `roleSvc.IsWorkspaceOwner(...)` → owner gate on budget write | `billing.write` | true |
| `budgets/http.go:506` | `roleSvc.IsWorkspaceOwner(...)` → owner gate (second handler) | `billing.write` | true |
| `ledger/http.go:165` | `!viewerContext.User.EmailVerified` → 403 | `ledger.view` | true |
| `profiles/http.go:127` | `!viewerContext.User.EmailVerified` → 403 | `workspace.settings` | true |
| `usage/http.go:393` | `!viewerContext.User.EmailVerified` → 403 | `analytics.view` | true |

### 2b. Middleware / service-layer admin checks

| File:Line | Current Expression | Target Permission | Notes |
|-----------|-------------------|-------------------|-------|
| `grants/service.go:62` | `s.admin.IsPlatformAdmin(ctx, in.GrantedByUserID)` | `platform.admin` | Via `grants.AdminChecker` interface |
| `main.go:485` | `roleSvc.RequirePlatformAdmin(grantsHandler.AdminMux())` | `platform.admin` | Middleware wrapping grants admin mux |

### 2c. Wire serialisation (single producer — accounts/http.go)

| File:Line | Current JSON Key | Post-Phase-18 |
|-----------|-----------------|---------------|
| `accounts/http.go:220-223` | `gates.can_invite_members`, `gates.can_manage_api_keys` | DROPPED — replaced by `permissions: []string` |
| `accounts/http.go:211` | `email_verified` (user.email_verified) | RETAINED |
| `accounts/http.go:217` | `current_account.role` | RETAINED |

### 2d. Modules with NO current authz call sites

| Module | Status |
|--------|--------|
| `accounts` (provisioning, switch) | Only viewer/membership reads — no write-gate |
| `catalog`, `routing` | No authz gates |
| `payments` | Verified via Supabase auth session; no role gate |

---

## 3. Web-Console Viewer-Gates Consumer AUDIT

### 3a. Direct `viewer.gates.*` field access (no helper)

| File:Line | Current Call | Replacement |
|-----------|-------------|-------------|
| `app/console/api-keys/page.tsx:17` | `viewer.gates.can_manage_api_keys` | `can(viewer, "api_keys.write")` |
| `app/console/api-keys/[id]/limits/page.tsx:37` | `viewer.gates.can_manage_api_keys` | `can(viewer, "api_keys.write")` |
| `app/console/billing/invoices/page.tsx:5` | `viewer.gates` referenced indirectly via layout comment | verify at implementation; likely no gate needed |

### 3b. Helper function calls (via `viewer-gates.ts`)

| File:Line | Current Call | Replacement |
|-----------|-------------|-------------|
| `app/console/members/page.tsx:61` | `canInviteMembers(viewer)` | `can(viewer, "members.invite")` |
| `app/console/members/page.tsx:11` | `import { canInviteMembers } from "@/lib/viewer-gates"` | `import { can } from "@/lib/viewer-gates"` |

### 3c. Type / decoder in `lib/control-plane/client.ts`

| Lines | Current | Post-Phase-18 |
|-------|---------|---------------|
| 27-29 | `ViewerGates` interface with `can_invite_members`, `can_manage_api_keys` | DELETE — replaced by `permissions: string[]` on `Viewer` |
| 36, 104 | `gates: ViewerGates` on raw + decoded `Viewer` | REPLACE with `permissions: string[]` |
| 189, 192, 203-204, 214-215, 256-258 | `gates` decode block (readObjectField + readBooleanField) | REPLACE with `permissions` array decode |
| 448 | `gates: rawViewer.gates` passthrough path | REPLACE with `permissions: rawViewer.permissions ?? []` |

### 3d. Re-export shim

| File:Line | Current | Post-Phase-18 |
|-----------|---------|---------------|
| `lib/control-plane/types.ts:22` | `export { ViewerGates, ... }` | Remove `ViewerGates` export; add `Permission` from generated file |

### 3e. `lib/viewer-gates.ts` — full replacement

Current exports:
- `ViewerGates` interface (DELETE)
- `ViewerForGates` interface (REPLACE with `ViewerWithPermissions`)
- `canInviteMembers(viewer)` (DELETE)
- `canManageApiKeys(viewer)` (DELETE)
- `allowedUnverifiedRoutes: string[]` (DELETE)

New exports:
- `Permission` (re-export from `./control-plane/permissions.generated`)
- `can(viewer: ViewerWithPermissions, perm: Permission): boolean` — Set lookup over `viewer.permissions`

### 3f. `tests/unit/viewer-gates.test.ts` (9 tests) — must be subsumed

Current tests check `canInviteMembers`, `canManageApiKeys`, `allowedUnverifiedRoutes`. All 9 must be replaced by matrix-parity tests in the new spec. The old spec file is deleted; the new one must cover at least the same (owner/member) × (verified/unverified) × permission combinations.

---

## 4. Permissions Registry — Concrete Go Declaration

### Package placement

`apps/control-plane/internal/authz/` (new package, not folded into `platform`).
- `authz` imports `platform` for `MembershipRole` types.
- `platform` does NOT import `authz`.
- Dependency: `main.go` → wires both; handlers import `authz`.

### `internal/authz/permissions.go` — concrete shape

```go
package authz

import (
    "github.com/google/uuid"
    "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// Permission is a typed resource.action authorization token.
type Permission string

const (
    PermBillingView       Permission = "billing.view"
    PermBillingWrite      Permission = "billing.write"
    PermAPIKeysRead       Permission = "api_keys.read"
    PermAPIKeysWrite      Permission = "api_keys.write"
    PermAnalyticsView     Permission = "analytics.view"
    PermMembersInvite     Permission = "members.invite"
    PermMembersManage     Permission = "members.manage"
    PermWorkspaceSettings Permission = "workspace.settings"
    PermGrantsCreate      Permission = "grants.create"
    PermLedgerView        Permission = "ledger.view"
    PermPlatformAdmin     Permission = "platform.admin"
)

// entry describes one row in the permission registry.
type entry struct {
    RequiresVerified bool
}

// registry maps every Permission to its metadata.
// This is the single source of truth for codegen + Policy.
var registry = map[Permission]entry{
    PermBillingView:       {RequiresVerified: false},
    PermBillingWrite:      {RequiresVerified: true},
    PermAPIKeysRead:       {RequiresVerified: false},
    PermAPIKeysWrite:      {RequiresVerified: true},
    PermAnalyticsView:     {RequiresVerified: false},  // adjust if audit says true
    PermMembersInvite:     {RequiresVerified: true},
    PermMembersManage:     {RequiresVerified: true},
    PermWorkspaceSettings: {RequiresVerified: true},
    PermGrantsCreate:      {RequiresVerified: true},
    PermLedgerView:        {RequiresVerified: false},
    PermPlatformAdmin:     {RequiresVerified: true},
}

// AllPermissions returns every registered Permission in stable order.
// Used by codegen.
func AllPermissions() []Permission { /* sorted iteration over registry */ }

// RequiresVerified reports whether perm requires email verification.
func RequiresVerified(perm Permission) bool {
    e, ok := registry[perm]
    return ok && e.RequiresVerified
}
```

**Note on `analytics.view` / `ledger.view`:** Audit shows `accounting/http.go:347`, `ledger/http.go:165`, `usage/http.go:393` all check `!EmailVerified` and return 403. This means these view permissions currently require verification. The registry should reflect this: `RequiresVerified: true` for these three. The read-default-false rule is for future permissions; existing policy is derived from the audit.

### `internal/authz/policy.go` — concrete shape

```go
package authz

// Actor represents the authenticated caller resolved before any authz check.
type Actor struct {
    UserID      uuid.UUID
    WorkspaceID uuid.UUID            // zero value for platform-scoped checks
    Role        platform.MembershipRole // "owner" | "member"
    Verified    bool
    IsAdmin     bool
}

// Policy is the stateless decision engine. Construct once; share across handlers.
type Policy struct{}

// Can reports whether actor holds permission.
// Decision order:
//  1. IsAdmin → grants platform.admin only (for non-platform perms, still needs role/verified)
//  2. RequiresVerified && !Verified → deny
//  3. platform.admin perm → only IsAdmin actors
//  4. owner role → full workspace set
//  5. member role → read-only subset
func (p Policy) Can(actor Actor, perm Permission) bool { ... }

// RequirePermission returns middleware that calls Policy.Can and returns 403 on deny.
func (p Policy) RequirePermission(perm Permission) func(http.Handler) http.Handler { ... }
```

### Matrix table (owner / member / admin × verified / unverified)

| Permission | owner+verified | owner+unverified | member+verified | member+unverified | admin (any) |
|------------|:-:|:-:|:-:|:-:|:-:|
| billing.view | Y | Y | N | N | Y |
| billing.write | Y | N | N | N | Y |
| api_keys.read | Y | Y | N | N | Y |
| api_keys.write | Y | N | N | N | Y |
| analytics.view | Y | N | N | N | Y |
| members.invite | Y | N | N | N | Y |
| members.manage | Y | N | N | N | Y |
| workspace.settings | Y | N | N | N | Y |
| grants.create | N | N | N | N | Y |
| ledger.view | Y | N | N | N | Y |
| platform.admin | N | N | N | N | Y |

**Rationale from audit:** All current write gates are `EmailVerified && owner`. All current view gates are `EmailVerified` only (no role check), meaning any verified member can view — hence `member+verified = Y` for view permissions. Members cannot write. Platform admin overrides all workspace checks.

---

## 5. Wire Shape

### Before (current)

```json
{
  "user": { "id": "...", "email": "...", "email_verified": true },
  "current_account": { "id": "...", "display_name": "...", "account_type": "...", "role": "owner" },
  "memberships": [...],
  "gates": {
    "can_invite_members": true,
    "can_manage_api_keys": true
  }
}
```

### After (Phase 18)

```json
{
  "user": { "id": "...", "email": "...", "email_verified": true },
  "current_account": { "id": "...", "display_name": "...", "account_type": "...", "role": "owner" },
  "memberships": [...],
  "permissions": [
    "billing.view", "billing.write",
    "api_keys.read", "api_keys.write",
    "analytics.view",
    "members.invite", "members.manage",
    "workspace.settings",
    "ledger.view"
  ]
}
```

**Single producer:** `accounts/service.go` `EnsureViewerContext` — builds `Actor` from `chosen` membership + `viewer.EmailVerified` + (optionally) `RoleService.IsPlatformAdmin`, then calls `policy.AllGranted(actor)` to produce `[]string`.

**Changes in `accounts/types.go`:**
- Delete `Gates` struct.
- Add `Permissions []string` to `ViewerContext`.

**Changes in `accounts/service.go:79-81`:**
- Delete `gates := Gates{...}` block.
- Add `perms := policy.AllGranted(actor)` → `ViewerContext.Permissions`.

**Changes in `accounts/http.go` `viewerContextResponse`:**
- Delete `"gates": map[string]interface{}{...}` block (lines 220-223).
- Add `"permissions": vc.Permissions`.

**Adversarial walk (Phase 17 Do-Not-Repeat):** `permissions` is a `[]string` derived entirely server-side from the resolved actor. No `map[string]any` is traversed during serialisation. No field name lies about its semantic.

---

## 6. TS Codegen

### Approach (lowest friction matching Phase 17 precedent)

A tiny Go `cmd` program: `apps/control-plane/cmd/gen-permissions/main.go`. It imports `internal/authz`, calls `authz.AllPermissions()`, and emits the TS file to stdout. Build step in `Makefile` or `package.json` script invokes it via Docker toolchain.

### Output file

`apps/web-console/lib/control-plane/permissions.generated.ts`

```typescript
// AUTO-GENERATED — do not edit. Run `make gen-permissions` to regenerate.
// Source: apps/control-plane/internal/authz/permissions.go

export const PERMISSIONS = [
  "billing.view",
  "billing.write",
  "api_keys.read",
  "api_keys.write",
  "analytics.view",
  "members.invite",
  "members.manage",
  "workspace.settings",
  "grants.create",
  "ledger.view",
  "platform.admin",
] as const;

export type Permission = typeof PERMISSIONS[number];
```

### CI drift-check

Add a step to the CI pipeline (e.g., `package.json` `test:codegen-drift` script):
1. Run the Go emitter into a temp file.
2. `diff` against the committed `permissions.generated.ts`.
3. Exit non-zero on any diff.

This mirrors how `openai-contract` codegen drift is caught. No separate drift-check tool needed — plain `diff` is sufficient and auditable.

### Build hook options

Option A (recommended): `Makefile` target `gen-permissions` in the workspace root; CI calls `make gen-permissions && git diff --exit-code`.

Option B: `package.json` `prebuild` script calling the Docker toolchain — heavier but consistent with existing web-console patterns.

---

## 7. Lint Shape

### `packages/openai-contract/scripts/lint-no-bare-role-check.mjs`

Mirror of `lint-no-customer-usd.mjs`. Uses `ripgrep` (or Node `fs.readFileSync`) over Go source files.

**Forbidden patterns (outside allowlist):**

```
\.Role == "owner"
\.Role == "member"
chosen\.Role ==
viewer\.Role ==
EmailVerified &&
\.EmailVerified {
```

**Allowlist (these files MAY contain the patterns):**

```
apps/control-plane/internal/authz/          ← the matrix itself
apps/control-plane/internal/platform/role   ← MembershipRole declarations
apps/control-plane/internal/platform/role_pgx  ← SQL queries returning role strings
apps/control-plane/internal/accounts/service_test  ← historical test stubs (delete when migrated)
```

**CI wiring:** Add `node scripts/lint-no-bare-role-check.mjs --all` as a blocking step in `.github/workflows/` (or Docker toolchain CI script), same position as the existing `lint-no-customer-usd.mjs` call.

**Grep command the linter uses:**

```bash
rg --type go -n '\.Role == "owner"|\.Role == "member"|chosen\.Role|EmailVerified &&|\bEmailVerified\b.*{' \
  apps/control-plane/internal/ \
  --glob '!**/authz/**' \
  --glob '!**/platform/role*.go' \
  --glob '!**_test.go'
```

---

## 8. Test Strategy

### 8a. Go — table-driven matrix (`internal/authz/policy_test.go`)

This file IS the permission specification. Structure:

```go
type matrixCase struct {
    role     platform.MembershipRole
    verified bool
    isAdmin  bool
    perm     Permission
    want     bool
}
```

Every cell in the §4 matrix table → one test case. Total: ~55 cases (11 perms × 5 actor states). Run as a single table-driven `TestPolicyMatrix`. This test must pass before any handler migration commit.

### 8b. Go — per-module integration tests

Existing test files to extend (all already have `http_test.go`):

| Module | Existing Test File | Phase 18 Extension |
|--------|-------------------|-------------------|
| `accounts` | `accounts/http_test.go` | Wire `Policy` into test handler; assert `permissions: []` in viewer JSON; assert `gates` key absent |
| `apikeys` | `apikeys/http_test.go` | Replace `Gates.CanManageAPIKeys` fixture with `policy.Can(actor, PermAPIKeysWrite)` |
| `budgets` | `budgets/http_test.go` | Replace `roleSvc.IsWorkspaceOwner` mock with `policy.Can` mock; assert 403 for member |
| `grants` | `grants/http_test.go` | `RequirePermission(PermPlatformAdmin)` middleware replaces `RequirePlatformAdmin` |
| `accounting` | (extend) | Assert 403 for unverified actor on analytics endpoint |
| `ledger` | (extend) | Assert 403 for unverified actor on ledger endpoint |
| `profiles` | (extend) | Assert 403 for unverified actor on profile endpoint |
| `usage` | (extend) | Assert 403 for unverified actor on usage endpoint |

Pattern: `httptest.NewRecorder` + table-driven (role, verified) × expected status. Match `internal/platform/role_test.go` table shape.

### 8c. Vitest matrix-parity (`tests/unit/viewer-gates.test.ts` — replace)

New file replaces the old 9-test spec. Same (role, verified, permission) table as Go policy_test:

```typescript
// parity fixture generated from same source as Go test table
const cases: Array<{role: string, verified: boolean, perms: Permission[], perm: Permission, want: boolean}> = [...]

cases.forEach(({role, verified, perms, perm, want}) => {
  it(`can(${role}, ${verified}, ${perm}) = ${want}`, () => {
    const viewer = makeViewer({ role, verified, permissions: perms });
    expect(can(viewer, perm)).toBe(want);
  });
});
```

Must cover all cases the Go matrix covers (owner/member × verified/unverified × each permission). Old `canInviteMembers` / `canManageApiKeys` / `allowedUnverifiedRoutes` assertions deleted.

### 8d. Playwright — unverified-user redirect spec

New file: `tests/e2e/rbac-unverified.spec.ts`

Pattern from `auth-shell.spec.ts:47-75` (existing unverified-members test):

```typescript
test.describe("unverified user cannot reach sensitive routes", () => {
  test.beforeEach(async ({ page }) => {
    await signInAsUnverifiedUser(page, UNVERIFIED_EMAIL, UNVERIFIED_PASSWORD);
  });

  test("/console/billing redirects unverified user", async ({ page }) => {
    await page.goto("/console/billing");
    // assert redirect or 403 surface
  });

  test("/console/api-keys blocks unverified user from write actions", async ({ page }) => {
    await page.goto("/console/api-keys");
    // assert no "Create" button or write surface
  });
});
```

Reuse the `signInAsUnverifiedUser` helper already in `auth-shell.spec.ts`. No new fixture infrastructure needed.

---

## 9. Risks and Mitigations

| Risk | Likelihood | Mitigation |
|------|-----------|-----------|
| `analytics.view` / `ledger.view` RequiresVerified flip — audit says true, CONTEXT.md default says false | HIGH — conflict | Use audit truth: set `RequiresVerified: true` for analytics.view, ledger.view. Planner must confirm. |
| `testVC.Gates.CanManageAPIKeys` in `apikeys/http_test.go:105` — test-only path using old Gates struct | HIGH | Migrate test fixture in same commit as handler migration; replace Gates field with actor+policy |
| `grants.AdminChecker` interface in `grants/service.go:17` — caller passes `roleSvc` which satisfies it | MEDIUM | Phase 18 does not break this; `RoleService.IsPlatformAdmin` signature is unchanged (PAY-14-08 contract). Only the middleware wrapping changes. |
| `allowedUnverifiedRoutes` removal — if any Next.js middleware uses it for server-side redirect | LOW — grep confirms middleware.ts does NOT import viewer-gates | Confirmed: middleware.ts has no viewer-gates import. Safe to delete. |
| Codegen drift — Go registry changes without regenerating TS | MEDIUM | CI drift-check blocks merge. `gen-permissions` must run in CI before diff check. |
| Backward-compat: external consumers of `gates.*` wire fields | LOW — confirmed console is only consumer; no external SDK depends on gates | Hard-cut is safe per CONTEXT.md decision. |

---

## 10. Proposed RBAC-18-* Requirement IDs

| ID | One-line Scope |
|----|---------------|
| RBAC-18-01 | `internal/authz` package: Actor, Permission consts, registry with RequiresVerified, Policy.Can, RequirePermission middleware |
| RBAC-18-02 | Backend handler migration: 14 ad-hoc authz expressions across accounts, apikeys, accounting, budgets, ledger, profiles, usage replaced with policy.Can or RequirePermission |
| RBAC-18-03 | Wire flip: accounts/service.go produces permissions:[]string; accounts/http.go drops gates.*; accounts/types.go drops Gates struct |
| RBAC-18-04 | TS codegen: cmd/gen-permissions emitter + permissions.generated.ts committed; CI drift-check blocking |
| RBAC-18-05 | viewer-gates.ts rewritten: can(viewer, perm) Set-lookup helper; old canInviteMembers / canManageApiKeys / allowedUnverifiedRoutes deleted |
| RBAC-18-06 | Web-console consumer migration: 4 page files + client.ts decoder updated to permissions shape |
| RBAC-18-07 | CI lint: lint-no-bare-role-check.mjs blocks bare role/EmailVerified checks outside authz package |
| RBAC-18-08 | Go tests: policy_test.go table-driven matrix (~55 cases) + 8 module integration tests (role × verified × permission) |
| RBAC-18-09 | Vitest: matrix-parity tests subsuming 9-test viewer-gates.test.ts; verifies can() identical to Go policy |
| RBAC-18-10 | Playwright: rbac-unverified.spec.ts asserting /console/billing and /console/api-keys block unverified user |

---

## 11. Validation Architecture

> `workflow.nyquist_validation` not explicitly false in config — section included.

### Test Framework

| Property | Value |
|----------|-------|
| Go framework | `go test` (stdlib), table-driven, `-race` |
| Go config | `go.work` workspace; run via Docker toolchain |
| Go quick run | `docker compose --profile tools run toolchain bash -c "cd /workspace && go test ./apps/control-plane/internal/authz/... -count=1 -short"` |
| Go full suite | `docker compose --profile tools run toolchain bash -c "cd /workspace && go test ./apps/control-plane/... -count=1 -short"` |
| TS framework | Vitest (existing: `npm run test:unit`) |
| E2E framework | Playwright (existing: `npx playwright test`) |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| RBAC-18-01 | Policy.Can returns correct bool for all (role, verified, perm) combinations | unit | `go test ./apps/control-plane/internal/authz/... -run TestPolicyMatrix` | ❌ Wave 0 |
| RBAC-18-02 | Handler returns 403 for actor without permission | integration | `go test ./apps/control-plane/internal/apikeys/... ./apps/control-plane/internal/budgets/... -run TestHandler` | ✅ (extend existing) |
| RBAC-18-03 | Viewer JSON has `permissions` key, no `gates` key | integration | `go test ./apps/control-plane/internal/accounts/... -run TestViewerEndpoint` | ✅ (extend existing) |
| RBAC-18-04 | Generated TS matches Go registry | codegen drift | `make gen-permissions && git diff --exit-code apps/web-console/lib/control-plane/permissions.generated.ts` | ❌ Wave 0 |
| RBAC-18-05 | can() returns true iff permission in viewer.permissions | unit | `npx vitest run tests/unit/viewer-gates.test.ts` | ✅ (rewrite) |
| RBAC-18-06 | Page renders correctly for owner vs member | unit | `npx vitest run tests/unit/api-keys-limits.test.ts` | ✅ (extend) |
| RBAC-18-07 | CI lint rejects bare role checks outside authz package | lint | `node packages/openai-contract/scripts/lint-no-bare-role-check.mjs --all` | ❌ Wave 0 |
| RBAC-18-08 | All module integration tests green (role × verified) | integration | `go test ./apps/control-plane/... -count=1 -short` | ✅ (extend) |
| RBAC-18-09 | Vitest matrix matches Go matrix exactly | unit | `npx vitest run tests/unit/viewer-gates.test.ts tests/unit/api-keys-limits.test.ts` | ✅ (rewrite) |
| RBAC-18-10 | Unverified user blocked on /billing and /api-keys | e2e | `npx playwright test tests/e2e/rbac-unverified.spec.ts` | ❌ Wave 0 |

### Sampling Rate

- **Per task commit:** `go test ./apps/control-plane/internal/authz/... && npx vitest run tests/unit/viewer-gates.test.ts`
- **Per wave merge:** Full Go suite + full Vitest suite
- **Phase gate:** All of the above green + Playwright `rbac-unverified.spec.ts` passes before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `apps/control-plane/internal/authz/policy_test.go` — covers RBAC-18-01 (55 matrix cases)
- [ ] `apps/control-plane/cmd/gen-permissions/main.go` — codegen emitter; covers RBAC-18-04
- [ ] `apps/web-console/lib/control-plane/permissions.generated.ts` — generated output; covers RBAC-18-04
- [ ] `packages/openai-contract/scripts/lint-no-bare-role-check.mjs` — covers RBAC-18-07
- [ ] `apps/web-console/tests/e2e/rbac-unverified.spec.ts` — covers RBAC-18-10
- [ ] `apps/control-plane/internal/authz/` package directory with `permissions.go`, `policy.go`

*(All other test infrastructure exists — extend, don't create.)*

---

## Sources

### Primary (HIGH confidence — direct code inspection)

- `apps/control-plane/internal/accounts/service.go` — lines 79-81, 150, 164 (current Gates derivation)
- `apps/control-plane/internal/accounts/http.go` — lines 72, 211-223 (viewer serialiser)
- `apps/control-plane/internal/accounts/types.go` — Gates struct
- `apps/control-plane/internal/apikeys/http.go` — lines 105, 128 (CanManageAPIKeys gate)
- `apps/control-plane/internal/budgets/http.go` — lines 147, 486, 506 (EmailVerified + IsWorkspaceOwner)
- `apps/control-plane/internal/accounting/http.go:347`, `ledger/http.go:165`, `profiles/http.go:127`, `usage/http.go:393` (EmailVerified gates)
- `apps/control-plane/internal/grants/service.go:62` (IsPlatformAdmin)
- `apps/control-plane/cmd/server/main.go:177-291,485` (RoleService wiring)
- `apps/control-plane/internal/platform/role.go` — MembershipRole, RoleService, RequirePlatformAdmin
- `apps/control-plane/internal/platform/role_pgx.go` — pgxRoleStore queries
- `apps/web-console/lib/viewer-gates.ts` — full file (22 lines)
- `apps/web-console/tests/unit/viewer-gates.test.ts` — 9 tests
- `apps/web-console/lib/control-plane/client.ts` — lines 27-29, 36, 104, 189-258, 448
- `apps/web-console/app/console/api-keys/page.tsx:17`
- `apps/web-console/app/console/api-keys/[id]/limits/page.tsx:37`
- `apps/web-console/app/console/members/page.tsx:61`
- `packages/openai-contract/scripts/lint-no-customer-usd.mjs` — lint precedent
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-08.md` — binding contract
- `.planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-05.md`
- `.planning/phases/13-console-integration-fixes/13-AUDIT.md`
- `.planning/phases/18-rbac-matrix/18-CONTEXT.md` — locked decisions

---

## Metadata

**Confidence breakdown:**
- Backend audit (call sites): HIGH — exhaustive grep + line-by-line inspection
- Web-console audit (consumers): HIGH — exhaustive grep + file inspection
- Permissions registry shape: HIGH — derived from audit + CONTEXT.md locked decisions
- Wire shape before/after: HIGH — single producer inspected
- Codegen approach: MEDIUM — no existing precedent in this repo for Go→TS; shape is simple enough that risk is low
- Lint mechanism: HIGH — mirrors Phase 17 pattern exactly

**Research date:** 2026-05-14
**Valid until:** Stable (no external dependencies; all findings from repo source)

---

## RESEARCH COMPLETE
