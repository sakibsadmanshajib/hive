---
phase: 18-rbac-matrix
plan: 01
wave: 1
depends_on: []
type: execute
milestone: v1.1
branch: a/phase-18-rbac-matrix
nyquist_compliant: true
autonomous: true
total_plans: 7
total_waves: 5
requirements: [RBAC-18-01, RBAC-18-02, RBAC-18-03, RBAC-18-04, RBAC-18-05, RBAC-18-06, RBAC-18-07, RBAC-18-08, RBAC-18-09, RBAC-18-10, RBAC-18-11]
requirements_addressed:
  - RBAC-18-01
  - RBAC-18-02
  - RBAC-18-03
  - RBAC-18-04
  - RBAC-18-05
  - RBAC-18-06
  - RBAC-18-07
  - RBAC-18-08
  - RBAC-18-09
  - RBAC-18-10
  - RBAC-18-11
files_modified:
  - apps/control-plane/internal/authz/permissions.go
  - apps/control-plane/internal/authz/policy.go
  - apps/control-plane/internal/authz/policy_test.go
  - apps/control-plane/cmd/gen-permissions/main.go
  - apps/web-console/lib/control-plane/permissions.generated.ts
  - packages/openai-contract/scripts/lint-no-bare-role-check.mjs
  - Makefile
  - apps/control-plane/cmd/server/main.go
  - apps/control-plane/internal/accounts/service.go
  - apps/control-plane/internal/accounts/service_test.go
  - apps/control-plane/internal/accounts/types.go
  - apps/control-plane/internal/accounts/http.go
  - apps/control-plane/internal/accounts/http_test.go
  - apps/control-plane/internal/accounts/actor_resolver.go
  - apps/control-plane/internal/accounts/actor_resolver_test.go
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
  - apps/web-console/lib/viewer-gates.ts
  - apps/web-console/lib/control-plane/client.ts
  - apps/web-console/lib/control-plane/types.ts
  - apps/web-console/app/console/api-keys/page.tsx
  - apps/web-console/app/console/api-keys/[id]/limits/page.tsx
  - apps/web-console/app/console/members/page.tsx
  - apps/web-console/middleware.ts
  - apps/web-console/tests/unit/viewer-gates.test.ts
  - apps/web-console/tests/unit/permissions.parity.test.ts
  - apps/web-console/tests/e2e/rbac-unverified.spec.ts
  - .github/workflows/ci.yml
  - .planning/REQUIREMENTS.md
  - .planning/phases/18-rbac-matrix/18-VERIFICATION.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-01.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-02.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-03.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-04.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-05.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-06.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-07.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-08.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-09.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-10.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-11.md
  - .planning/STATE.md
  - .planning/todos/done/2026-04-22-design-rbac-authorization-model.md
  - .planning/ROADMAP.md
must_haves:
  truths:
    - "Single Go authz package owns the Permission enum, Actor struct, and Policy.Can decision function."
    - "No control-plane handler outside internal/authz/ contains `chosen.Role == \"owner\"`, `viewer.Role ==`, or a bare `EmailVerified &&` expression."
    - "Viewer JSON response carries `permissions: []string` and contains no `gates` key."
    - "TypeScript `Permission` union type is codegen'd from the Go registry and CI fails on drift."
    - "Web-console `can(viewer, perm)` is the single FE authz helper; `canInviteMembers`, `canManageApiKeys`, `allowedUnverifiedRoutes` no longer exist."
    - "Unverified user redirected/blocked on /console/billing and /console/api-keys via Playwright spec."
    - "STATE.md `v1_1_ship_gate.rbac_matrix = true`; `2026-04-22-design-rbac-authorization-model.md` resolved to `.planning/todos/done/`."
  artifacts:
    - path: "apps/control-plane/internal/authz/permissions.go"
      provides: "Permission consts + registry with RequiresVerified flag + AllPermissions()"
      contains: "PermBillingView|PermBillingWrite|PermAPIKeysRead|PermAPIKeysWrite|PermAnalyticsView|PermMembersInvite|PermMembersManage|PermWorkspaceSettings|PermGrantsCreate|PermLedgerView|PermPlatformAdmin"
    - path: "apps/control-plane/internal/authz/policy.go"
      provides: "Actor struct, Policy.Can, RequirePermission middleware, AllGranted helper"
      contains: "func (p Policy) Can(actor Actor, perm Permission) bool"
    - path: "apps/control-plane/internal/authz/policy_test.go"
      provides: "Table-driven matrix (~55 cases) — the auditable permission spec"
      contains: "TestPolicyMatrix"
    - path: "apps/control-plane/cmd/gen-permissions/main.go"
      provides: "Go-to-TS emitter consuming authz.AllPermissions()"
    - path: "apps/web-console/lib/control-plane/permissions.generated.ts"
      provides: "PERMISSIONS const tuple + Permission union type"
      contains: "export type Permission"
    - path: "apps/web-console/lib/viewer-gates.ts"
      provides: "can(viewer, perm) Set-lookup helper — old exports deleted"
      contains: "export function can"
    - path: "packages/openai-contract/scripts/lint-no-bare-role-check.mjs"
      provides: "CI-blocking lint forbidding bare role/EmailVerified outside allowlist"
    - path: "apps/web-console/tests/e2e/rbac-unverified.spec.ts"
      provides: "Playwright spec for unverified redirect"
    - path: ".planning/phases/18-rbac-matrix/18-VERIFICATION.md"
      provides: "Phase closure evidence per requirement"
    - path: ".planning/phases/18-rbac-matrix/evidence/RBAC-18-01.md"
      provides: "Evidence frontmatter for RBAC-18-01 (matrix package)"
  key_links:
    - from: "apps/control-plane/internal/accounts/service.go:79-82"
      to: "apps/control-plane/internal/authz/policy.go"
      via: "policy.AllGranted(actor) → ViewerContext.Permissions"
      pattern: "policy\\.AllGranted|authz\\.AllGranted"
    - from: "apps/control-plane/internal/accounts/http.go:220-223"
      to: "JSON `permissions` key"
      via: "viewerContextResponse map drops gates, adds permissions"
      pattern: "\\\"permissions\\\":"
    - from: "apps/control-plane/internal/apikeys/http.go:128"
      to: "authz.Policy.Can(actor, PermAPIKeysWrite)"
      via: "RequirePermission middleware OR inline policy.Can"
      pattern: "PermAPIKeysWrite"
    - from: "apps/control-plane/cmd/server/main.go:485"
      to: "RequirePermission(PermPlatformAdmin)"
      via: "Middleware swap replaces RequirePlatformAdmin"
      pattern: "RequirePermission\\(authz\\.PermPlatformAdmin\\)"
    - from: "apps/web-console/lib/viewer-gates.ts"
      to: "apps/web-console/lib/control-plane/permissions.generated.ts"
      via: "import { Permission } from './control-plane/permissions.generated'"
      pattern: "permissions\\.generated"
    - from: "CI workflow"
      to: "packages/openai-contract/scripts/lint-no-bare-role-check.mjs"
      via: "blocking step in .github/workflows alongside existing lint-no-customer-usd"
      pattern: "lint-no-bare-role-check"
---

> NOTE ON FRONTMATTER: This phase ships as a single PLAN.md following the Phase 14
> precedent. The top-level frontmatter satisfies the gsd-tools `plan` schema
> (`plan`, `wave`, `depends_on`, `files_modified` are aggregate values for the
> whole phase). Per-plan frontmatter is repeated below each `## Plan NN`
> heading inside the file so each unit of work has its own wave/depends_on
> declaration. Execute-phase reads the per-plan `<plan_meta>` blocks.

<objective>
Replace the ad hoc `EmailVerified && chosen.Role == "owner"` derivation pattern
(spread across 8 control-plane modules) and the `viewer.gates.can_*` wire shape
with a single, reusable, verification-aware permission matrix. The matrix lives
in a new `internal/authz` package, is mirrored verbatim into the web-console
via codegen'd TypeScript types, and is consumed via a single `Policy.Can(actor,
perm)` decision function on the backend and a single `can(viewer, perm)` helper
on the frontend.

Purpose: Close v1.1 ship-gate `rbac_matrix` and the Phase 17 Do-Not-Repeat
("name fields truthfully; do not let a wire field name lie about its
semantic"). Realise the PAY-14-08 contract stub. Consume HANDOFF-17-01.

Output: New authz package + codegen emitter + CI lint + matrix test +
migrated handlers across 8 modules + flipped viewer wire shape + rewritten
viewer-gates.ts + migrated FE consumers + matrix-parity vitest + unverified
Playwright spec + VERIFICATION.md + evidence files for RBAC-18-01..11 +
STATE.md ship-gate flip + todo resolved.
</objective>

<execution_context>
@.planning/phases/18-rbac-matrix/18-CONTEXT.md
@.planning/phases/18-rbac-matrix/18-RESEARCH.md
@.planning/phases/18-rbac-matrix/18-VALIDATION.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/STATE.md
@.planning/REQUIREMENTS.md
@.planning/phases/14-payments-budget-grant/evidence/PAY-14-08.md
@.planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-05.md
@.planning/todos/pending/2026-04-22-design-rbac-authorization-model.md
@apps/control-plane/internal/platform/role.go
@apps/control-plane/internal/accounts/service.go
@apps/control-plane/internal/accounts/types.go
@apps/control-plane/internal/accounts/http.go
@apps/web-console/lib/viewer-gates.ts
@apps/web-console/lib/control-plane/client.ts
@packages/openai-contract/scripts/lint-no-customer-usd.mjs
@.wolf/cerebrum.md

<interfaces>
<!-- Locked contracts the executor MUST consume verbatim. -->

From apps/control-plane/internal/platform/role.go (Phase 14 — signatures locked by PAY-14-08):
```go
type MembershipRole string
const (
    RoleOwner  MembershipRole = "owner"
    RoleMember MembershipRole = "member"
)
type RoleStore interface {
    GetMembershipRole(ctx context.Context, userID, workspaceID uuid.UUID) (MembershipRole, error)
    IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error)
}
type RoleService struct{ /* unexported */ }
func NewRoleService(store RoleStore) *RoleService
func (s *RoleService) IsWorkspaceOwner(ctx context.Context, userID, workspaceID uuid.UUID) (bool, error)
func (s *RoleService) IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error)
func (s *RoleService) RequirePlatformAdmin(next http.Handler) http.Handler  // SIGNATURE LOCKED — body swap only
```

From apps/control-plane/internal/accounts/types.go (current shape — Gates deleted by Plan 04):
```go
type ViewerContext struct {
    User           ViewerUser
    CurrentAccount AccountSummary
    Memberships    []MembershipSummary
    Gates          Gates                     // DELETE in Plan 04
    // Permissions []string                  // ADD in Plan 04
}
type Gates struct {                          // DELETE in Plan 04
    CanInviteMembers bool
    CanManageAPIKeys bool
}
```

From apps/web-console/lib/viewer-gates.ts (full file replaced by Plan 05):
```typescript
// DELETE all of:
export interface ViewerGates { can_invite_members: boolean; can_manage_api_keys: boolean }
export interface ViewerForGates { gates: ViewerGates }
export function canInviteMembers(viewer): boolean
export function canManageApiKeys(viewer): boolean
export const allowedUnverifiedRoutes: string[]
// REPLACE with:
import { Permission } from "./control-plane/permissions.generated";
export interface ViewerWithPermissions { permissions: string[] }
export function can(viewer: ViewerWithPermissions, perm: Permission): boolean
```

New Permission registry shape (apps/control-plane/internal/authz/permissions.go — Plan 01 creates):
```go
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
```

Registry `RequiresVerified` flags (per RESEARCH §4 — audit-truth, NOT the
CONTEXT default-rule, because audit shows analytics/ledger view already 403
on `!EmailVerified`):

| Permission           | RequiresVerified |
|----------------------|------------------|
| billing.view         | true             |
| billing.write        | true             |
| api_keys.read        | true             |
| api_keys.write       | true             |
| analytics.view       | true             |
| members.invite       | true             |
| members.manage       | true             |
| workspace.settings   | true             |
| grants.create        | true             |
| ledger.view          | true             |
| platform.admin       | true             |

Decision matrix (Plan 01 `TestPolicyMatrix` MUST assert every cell):

| Permission         | owner+ver | owner+unver | member+ver | member+unver | isAdmin |
|--------------------|:-:|:-:|:-:|:-:|:-:|
| billing.view       | Y | N | N | N | Y |
| billing.write      | Y | N | N | N | Y |
| api_keys.read      | Y | N | N | N | Y |
| api_keys.write     | Y | N | N | N | Y |
| analytics.view     | Y | N | Y | N | Y |
| members.invite     | Y | N | N | N | Y |
| members.manage     | Y | N | N | N | Y |
| workspace.settings | Y | N | N | N | Y |
| grants.create      | N | N | N | N | Y |
| ledger.view        | Y | N | Y | N | Y |
| platform.admin     | N | N | N | N | Y |

(Member+verified=Y on `analytics.view` and `ledger.view` because the current
audit allows ANY verified member to read these surfaces — see
`accounting/http.go:347`, `ledger/http.go:165`, `usage/http.go:393`. Member
cannot write anywhere.)
</interfaces>

<resolved_open_questions>
RESEARCH §9 questions resolved as follows (planner decision, recorded here so
the executor does not relitigate):

1. **`analytics.view` / `ledger.view` RequiresVerified flag.** RESOLVED:
   `true`. Audit at `accounting/http.go:347`, `ledger/http.go:165`,
   `usage/http.go:393` all return 403 on `!EmailVerified`. The registry
   must preserve current behaviour — flip to `false` is a deliberate policy
   change and must wait for a separate phase.

2. **`apikeys/http_test.go:105` test-only Gates path.** RESOLVED: migrated
   in the **same** task that migrates the production `apikeys/http.go:128`
   (Plan 03 Task 3A). Splitting into two tasks would leave the suite red
   between commits.

3. **`grants.AdminChecker` interface (`grants/service.go:17`).** RESOLVED:
   left untouched — `RoleService.IsPlatformAdmin` already satisfies it
   (PAY-14-08 contract). Plan 02 Task 2B only swaps the middleware wrapping
   at `cmd/server/main.go:485` from `roleSvc.RequirePlatformAdmin(...)` to
   `policy.RequirePermission(authz.PermPlatformAdmin)(...)`.

4. **Codegen tool placement.** RESOLVED: Go program at
   `apps/control-plane/cmd/gen-permissions/main.go`. Invoked via Makefile
   target `gen-permissions` from repo root. CI runs `make gen-permissions
   && git diff --exit-code`. Matches Phase 17 lint precedent in spirit
   (small repo-local script, plain diff for drift).

5. **Lint mechanism.** RESOLVED: Node ripgrep wrapper at
   `packages/openai-contract/scripts/lint-no-bare-role-check.mjs`, mirroring
   `lint-no-customer-usd.mjs` shape exactly. Allowlist:
   `apps/control-plane/internal/authz/`,
   `apps/control-plane/internal/platform/role.go`,
   `apps/control-plane/internal/platform/role_pgx.go`,
   `**/*_test.go` (tests legitimately exercise role strings via fixtures).
</resolved_open_questions>
</context>

---

## Plan 01 — authz package + matrix test + codegen + lint scaffolds

<plan_meta>
plan: 01
type: execute
wave: 1
depends_on: []
autonomous: true
requirements: [RBAC-18-01, RBAC-18-04, RBAC-18-07]
files_modified:
  - apps/control-plane/internal/authz/permissions.go
  - apps/control-plane/internal/authz/policy.go
  - apps/control-plane/internal/authz/policy_test.go
  - apps/control-plane/cmd/gen-permissions/main.go
  - apps/web-console/lib/control-plane/permissions.generated.ts
  - packages/openai-contract/scripts/lint-no-bare-role-check.mjs
  - Makefile
</plan_meta>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1A: Create authz package — permissions registry + Policy.Can + RequirePermission</name>
  <read_first>
    - .planning/phases/18-rbac-matrix/18-CONTEXT.md §"Implementation Decisions"
    - .planning/phases/18-rbac-matrix/18-RESEARCH.md §4 "Permissions Registry"
    - apps/control-plane/internal/platform/role.go (entire file — Phase 14 stub; signatures locked)
    - apps/control-plane/internal/auth/middleware.go (existing ViewerFromContext shape — confirm before importing)
  </read_first>
  <files>
    - apps/control-plane/internal/authz/permissions.go (CREATE)
    - apps/control-plane/internal/authz/policy.go (CREATE)
  </files>
  <behavior>
    - permissions.go declares exactly 11 `Permission` consts (PermBillingView, PermBillingWrite, PermAPIKeysRead, PermAPIKeysWrite, PermAnalyticsView, PermMembersInvite, PermMembersManage, PermWorkspaceSettings, PermGrantsCreate, PermLedgerView, PermPlatformAdmin) with the exact wire strings shown in the interfaces block.
    - Each Permission has a registry entry with `RequiresVerified: true` per the §"Registry RequiresVerified flags" table above.
    - `AllPermissions() []Permission` returns the 11 permissions in lexically-sorted-by-wire-string order (stable for codegen).
    - `RequiresVerified(perm Permission) bool` returns the registry value; returns `false` for unknown permissions.
    - policy.go declares `Actor{UserID, WorkspaceID uuid.UUID, Role platform.MembershipRole, Verified bool, IsAdmin bool}` exactly.
    - `Policy.Can(actor, perm)` evaluates: (1) if `RequiresVerified(perm) && !actor.Verified` → deny; (2) if `actor.IsAdmin` → allow (overlay grants all); (3) if `perm == PermPlatformAdmin` → only `IsAdmin`; (4) if `perm == PermGrantsCreate` → only `IsAdmin`; (5) for `analytics.view` and `ledger.view` → any verified actor (owner OR member); (6) every other permission → owner only.
    - `AllGranted(actor Actor) []string` returns sorted permission wire strings for which `Can(actor, perm)` returns true.
    - `RequirePermission(perm Permission)` middleware resolves the Actor via an injected `ActorResolver`, calls Can, returns 401 unauthenticated / 403 denied / next.ServeHTTP. Mirror provider-blind error shape of `platform.RequirePlatformAdmin`.
  </behavior>
  <action>
    Create `apps/control-plane/internal/authz/permissions.go` with package `authz`, import `sort`. Declare the 11 `Permission` typed-string constants verbatim. Declare unexported `entry struct { RequiresVerified bool }` and `var registry = map[Permission]entry{...}` with all 11 entries set to `{RequiresVerified: true}`. Implement `AllPermissions() []Permission` returning a sorted slice of registry keys. Implement `RequiresVerified(perm Permission) bool` returning `registry[perm].RequiresVerified` (ok-guarded; `false` for unknown).

    Create `apps/control-plane/internal/authz/policy.go` with imports `context, encoding/json, errors, net/http, github.com/google/uuid, github.com/hivegpt/hive/apps/control-plane/internal/auth, github.com/hivegpt/hive/apps/control-plane/internal/platform`. Declare `Actor` struct verbatim. Declare `type Policy struct{}` and `func NewPolicy() Policy { return Policy{} }`. Implement `(p Policy) Can(actor Actor, perm Permission) bool` with the decision rules. Implement `(p Policy) AllGranted(actor Actor) []string` iterating sorted `AllPermissions()` and emitting wire strings for permitted perms.

    Declare `type ActorResolver func(r *http.Request) (Actor, error)`. Add `type Middleware struct { resolver ActorResolver }`, `func NewMiddleware(resolver ActorResolver) Middleware`, and `func (m Middleware) RequirePermission(perm Permission) func(http.Handler) http.Handler`. Middleware writes provider-blind JSON `{"error":"permission denied"}` on deny / `{"error":"authentication required"}` on `errors.Is(err, ErrNoViewer)`. Define `var ErrNoViewer = errors.New("authz: no viewer in context")`.

    All errors wrapped with `fmt.Errorf("authz: ...: %w", err)`. No bare `EmailVerified &&`. No `Role == "owner"` outside this file.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose --profile tools run --rm toolchain bash -c "cd /workspace &amp;&amp; /usr/local/go/bin/go build ./apps/control-plane/internal/authz/..."</automated>
  </verify>
  <acceptance_criteria>
    - `go build ./apps/control-plane/internal/authz/...` exits 0.
    - `grep -c "Permission = " apps/control-plane/internal/authz/permissions.go` returns 11.
    - `grep -c "RequiresVerified: true" apps/control-plane/internal/authz/permissions.go` returns 11.
    - `grep "func (p Policy) Can(actor Actor, perm Permission) bool" apps/control-plane/internal/authz/policy.go` matches.
    - `grep "func (p Policy) AllGranted(actor Actor) \[\]string" apps/control-plane/internal/authz/policy.go` matches.
    - `grep "ErrNoViewer" apps/control-plane/internal/authz/policy.go` matches.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 1B: Table-driven matrix test — the auditable permission spec</name>
  <read_first>
    - apps/control-plane/internal/authz/permissions.go (just created)
    - apps/control-plane/internal/authz/policy.go (just created)
    - apps/control-plane/internal/platform/role_test.go (table-driven shape reference)
    - .planning/phases/18-rbac-matrix/18-RESEARCH.md §4 "Matrix table"
  </read_first>
  <files>apps/control-plane/internal/authz/policy_test.go (CREATE)</files>
  <behavior>
    - `TestPolicyMatrix` asserts every cell in the §"Decision matrix" table — exactly 11 permissions × 5 actor states = 55 cases.
    - `TestPolicyAllGrantedReturnsSorted` asserts AllGranted returns lexically sorted strings.
    - `TestRequirePermissionMiddleware` covers 3 cases: no viewer → 401 + `{"error":"authentication required"}`, denied → 403 + `{"error":"permission denied"}`, allowed → next.ServeHTTP invoked.
    - `TestUnknownPermissionRequiresVerified` asserts `RequiresVerified(Permission("not.a.perm"))` returns false.
  </behavior>
  <action>
    Create `apps/control-plane/internal/authz/policy_test.go` with `package authz`. Declare `type matrixCase struct { name string; role platform.MembershipRole; verified, isAdmin bool; perm Permission; want bool }`. Build 55 cases enumerating the §"Decision matrix" table (5 actor variants × 11 perms). For admin row, set `IsAdmin=true, Role="", Verified=false` to assert overlay grants regardless. Use stable subtest names: `fmt.Sprintf("%s/%s/v=%t/admin=%t", tc.perm, tc.role, tc.verified, tc.isAdmin)`.

    Each case constructs `Actor{Role: tc.role, Verified: tc.verified, IsAdmin: tc.isAdmin}`, calls `Policy{}.Can(actor, tc.perm)`, asserts `got == tc.want`.

    `TestRequirePermissionMiddleware` uses `httptest.NewRecorder` + fake resolver returning `(Actor, error)`.

    NO `t.Skip`. NO TODO comments. Test file is the spec.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose --profile tools run --rm toolchain bash -c "cd /workspace &amp;&amp; /usr/local/go/bin/go test ./apps/control-plane/internal/authz/... -count=1 -race -run TestPolicy"</automated>
  </verify>
  <acceptance_criteria>
    - `go test ./apps/control-plane/internal/authz/... -run TestPolicy` exits 0 with `--- PASS: TestPolicyMatrix`.
    - `grep -c "want: true" apps/control-plane/internal/authz/policy_test.go` returns at least 21.
    - `grep "TestRequirePermissionMiddleware" apps/control-plane/internal/authz/policy_test.go` matches.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 1C: Go-to-TS codegen emitter + Makefile target + generated TS file</name>
  <read_first>
    - apps/control-plane/internal/authz/permissions.go (just created)
    - .planning/phases/18-rbac-matrix/18-RESEARCH.md §6 "TS Codegen"
    - Makefile (project root — confirm targets format; create if missing)
    - apps/web-console/lib/control-plane/types.ts
  </read_first>
  <files>
    - apps/control-plane/cmd/gen-permissions/main.go (CREATE)
    - apps/web-console/lib/control-plane/permissions.generated.ts (CREATE — committed output)
    - Makefile (CREATE or APPEND target `gen-permissions`)
  </files>
  <behavior>
    - `apps/control-plane/cmd/gen-permissions/main.go` is `package main`, imports `internal/authz`, writes the TS file to the path passed as the single CLI argument (default `apps/web-console/lib/control-plane/permissions.generated.ts`).
    - Output template (so `git diff --exit-code` is the drift check):
      ```
      // AUTO-GENERATED — do not edit. Run `make gen-permissions` to regenerate.
      // Source: apps/control-plane/internal/authz/permissions.go

      export const PERMISSIONS = [
        "<perm1>",
        ...
      ] as const;

      export type Permission = typeof PERMISSIONS[number];
      ```
    - Lines emitted in `authz.AllPermissions()` order. Two-space indentation. Trailing newline.
    - Makefile target `gen-permissions` runs the emitter via Docker toolchain.
    - Initial `permissions.generated.ts` committed.
  </behavior>
  <action>
    Create `apps/control-plane/cmd/gen-permissions/main.go`:
    ```go
    package main

    import (
        "fmt"
        "os"
        "strings"
        "github.com/hivegpt/hive/apps/control-plane/internal/authz"
    )

    const header = "// AUTO-GENERATED — do not edit. Run `make gen-permissions` to regenerate.\n// Source: apps/control-plane/internal/authz/permissions.go\n\n"

    func main() {
        out := "apps/web-console/lib/control-plane/permissions.generated.ts"
        if len(os.Args) > 1 { out = os.Args[1] }
        var b strings.Builder
        b.WriteString(header)
        b.WriteString("export const PERMISSIONS = [\n")
        for _, p := range authz.AllPermissions() {
            fmt.Fprintf(&b, "  %q,\n", string(p))
        }
        b.WriteString("] as const;\n\nexport type Permission = typeof PERMISSIONS[number];\n")
        if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
            fmt.Fprintln(os.Stderr, err); os.Exit(1)
        }
    }
    ```

    Add Makefile target (top-level repo Makefile — create if missing):
    ```
    .PHONY: gen-permissions
    gen-permissions:
    	cd deploy/docker && docker compose --profile tools run --rm toolchain bash -c "cd /workspace && /usr/local/go/bin/go run ./apps/control-plane/cmd/gen-permissions ./apps/web-console/lib/control-plane/permissions.generated.ts"
    ```

    Run `make gen-permissions` once and commit the resulting `apps/web-console/lib/control-plane/permissions.generated.ts`. Output MUST contain exactly 11 entries.
  </action>
  <verify>
    <automated>cd /home/sakib/hive &amp;&amp; make gen-permissions &amp;&amp; git diff --exit-code apps/web-console/lib/control-plane/permissions.generated.ts</automated>
  </verify>
  <acceptance_criteria>
    - `make gen-permissions` exits 0.
    - `git diff --exit-code apps/web-console/lib/control-plane/permissions.generated.ts` exits 0.
    - `grep -c '^  "' apps/web-console/lib/control-plane/permissions.generated.ts` returns 11.
    - `grep "export type Permission = typeof PERMISSIONS\[number\];" apps/web-console/lib/control-plane/permissions.generated.ts` matches.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 1D: CI lint script — lint-no-bare-role-check.mjs</name>
  <read_first>
    - packages/openai-contract/scripts/lint-no-customer-usd.mjs (entire — precedent shape)
    - .planning/phases/18-rbac-matrix/18-RESEARCH.md §7 "Lint Shape"
  </read_first>
  <files>packages/openai-contract/scripts/lint-no-bare-role-check.mjs (CREATE)</files>
  <behavior>
    - Scans every `.go` file under `apps/control-plane/internal/` (recursive).
    - Forbidden line patterns: `\.Role == "owner"`, `\.Role == "member"`, `chosen\.Role`, `\.EmailVerified &&`, `&& .*\.EmailVerified`.
    - Allowlist: `apps/control-plane/internal/authz/`, `apps/control-plane/internal/platform/role.go`, `apps/control-plane/internal/platform/role_pgx.go`, any `*_test.go`.
    - Exit 0 on clean; exit 1 on any offender with `file:line: pattern '<pat>' found in: <trimmed line>` output.
    - Supports `--help`. Default action with no args = scan entire `apps/control-plane/internal/`.
  </behavior>
  <action>
    Create `packages/openai-contract/scripts/lint-no-bare-role-check.mjs` mirroring `lint-no-customer-usd.mjs` exactly. Use Node stdlib only.

    1. Imports identical (`readFileSync`, `readdirSync`, `existsSync`, `statSync` + `lstatSync` for symlink safety).
    2. Anchored to `dirname(fileURLToPath(import.meta.url))`.
    3. `FORBIDDEN_PATTERNS` array of 5 regexes.
    4. `ALLOWLIST_DIRS` array of 3 paths.
    5. Recursive walker yielding `.go` files; skip files whose absolute path starts with an allowlist prefix OR ends with `_test.go`.
    6. Line-by-line scan; for each line test each regex; collect offenders.
    7. `lint(targets) → { results, failed }`; `main()` exits 0 on clean / 1 on offenders.
    8. Default target: `path.resolve(PACKAGE_ROOT, "../../apps/control-plane/internal")`.
  </action>
  <verify>
    <automated>cd /home/sakib/hive &amp;&amp; node packages/openai-contract/scripts/lint-no-bare-role-check.mjs apps/control-plane/internal/authz/</automated>
  </verify>
  <acceptance_criteria>
    - `node packages/openai-contract/scripts/lint-no-bare-role-check.mjs apps/control-plane/internal/authz/` exits 0 (allowlist).
    - `node packages/openai-contract/scripts/lint-no-bare-role-check.mjs apps/control-plane/internal/accounts/service.go` exits **1** (expected red-state until Plan 04).
    - `grep "FORBIDDEN_PATTERNS" packages/openai-contract/scripts/lint-no-bare-role-check.mjs` matches.
    - `grep "ALLOWLIST" packages/openai-contract/scripts/lint-no-bare-role-check.mjs` matches.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

</tasks>

---

## Plan 02 — Wire authz Middleware into main.go + accounts ActorResolver

<plan_meta>
plan: 02
type: execute
wave: 2
depends_on: [01]
autonomous: true
requirements: [RBAC-18-01, RBAC-18-02]
files_modified:
  - apps/control-plane/cmd/server/main.go
  - apps/control-plane/internal/accounts/service.go
  - apps/control-plane/internal/accounts/actor_resolver.go
  - apps/control-plane/internal/accounts/actor_resolver_test.go
</plan_meta>

<tasks>

<task type="auto" tdd="true">
  <name>Task 2A: ActorResolver in accounts package — bridges auth.Viewer → authz.Actor</name>
  <read_first>
    - apps/control-plane/internal/authz/policy.go (signatures, ActorResolver type)
    - apps/control-plane/internal/accounts/service.go (existing EnsureViewerContext at line 31)
    - apps/control-plane/internal/platform/role.go (RoleService.IsPlatformAdmin signature)
    - apps/control-plane/internal/auth/middleware.go (ViewerFromContext shape)
  </read_first>
  <files>
    - apps/control-plane/internal/accounts/actor_resolver.go (CREATE)
    - apps/control-plane/internal/accounts/actor_resolver_test.go (CREATE)
  </files>
  <behavior>
    - `NewActorResolver(svc *Service, roleSvc *platform.RoleService) authz.ActorResolver` returns a closure that:
      1. Calls `auth.ViewerFromContext(r.Context())`; on miss returns `authz.ErrNoViewer`.
      2. Reads workspace ID from `X-Hive-Account-ID` header (parsed as UUID) or falls back to viewer's first active membership.
      3. Resolves workspace role via `svc.repo.ListMembershipsByUserID` (reuse, no new DB call).
      4. Resolves IsAdmin via `roleSvc.IsPlatformAdmin(ctx, viewer.UserID)`.
      5. Returns `authz.Actor{UserID, WorkspaceID, Role: platform.MembershipRole(membership.Role), Verified: viewer.EmailVerified, IsAdmin}`.
    - Test covers no-viewer, owner, member, IsAdmin overlay, missing membership.
  </behavior>
  <action>
    Create `apps/control-plane/internal/accounts/actor_resolver.go` with package `accounts`. Imports: `context, net/http, github.com/google/uuid, .../auth, .../authz, .../platform`. Add accessor on `Service` to expose membership lookup (or refactor `EnsureViewerContext` to extract `func (s *Service) resolveMembership(ctx, viewer, requestedAccountID) (Membership, error)`). DO NOT change `EnsureViewerContext` public signature.

    Resolver MUST read `viewer.EmailVerified` directly from auth context. MUST NOT compute permissions.

    Test file uses `stubRepo` pattern from existing `service_test.go`; stub returns canned memberships. Assert each branch. No real DB.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose --profile tools run --rm toolchain bash -c "cd /workspace &amp;&amp; /usr/local/go/bin/go test ./apps/control-plane/internal/accounts/... -count=1 -race -run TestActorResolver"</automated>
  </verify>
  <acceptance_criteria>
    - `TestActorResolver*` subtests all pass.
    - `grep "NewActorResolver" apps/control-plane/internal/accounts/actor_resolver.go` matches.
    - Resolver returns `authz.ErrNoViewer` on no-viewer; never returns nil-error with empty Actor.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 2B: Wire authz.Middleware in cmd/server/main.go; swap RequirePlatformAdmin</name>
  <read_first>
    - apps/control-plane/cmd/server/main.go lines 177-291 and 485 (RoleService construction + grants mux wrapping)
    - apps/control-plane/internal/accounts/actor_resolver.go (just created)
    - apps/control-plane/internal/authz/policy.go (Middleware constructor)
  </read_first>
  <files>apps/control-plane/cmd/server/main.go</files>
  <behavior>
    - Just after `roleSvc := platform.NewRoleService(...)` (existing — around line 200+), add:
      - `actorResolver := accounts.NewActorResolver(accountsSvc, roleSvc)`
      - `policy := authz.NewPolicy()`
      - `authzMW := authz.NewMiddleware(actorResolver)`
    - At `cmd/server/main.go:485` replace `roleSvc.RequirePlatformAdmin(grantsHandler.AdminMux())` with `authzMW.RequirePermission(authz.PermPlatformAdmin)(grantsHandler.AdminMux())`. Leave existing `RequirePlatformAdmin` method in `platform/role.go` intact for now.
    - Build + vet pass.
  </behavior>
  <action>
    Edit `apps/control-plane/cmd/server/main.go`. Locate the block where `roleSvc` is constructed (around lines 195-220 per audit). Immediately below, add the three lines above. Locate the `RequirePlatformAdmin` call near line 485 and swap as specified. Add import `github.com/hivegpt/hive/apps/control-plane/internal/authz` if not present.

    Confirm `go build ./apps/control-plane/cmd/server/...` succeeds and `go vet ./apps/control-plane/...` is clean.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose --profile tools run --rm toolchain bash -c "cd /workspace &amp;&amp; /usr/local/go/bin/go build ./apps/control-plane/cmd/server/... &amp;&amp; /usr/local/go/bin/go vet ./apps/control-plane/..."</automated>
  </verify>
  <acceptance_criteria>
    - `go build ./apps/control-plane/cmd/server/...` exits 0.
    - `go vet ./apps/control-plane/...` exits 0.
    - `grep "authzMW.RequirePermission(authz.PermPlatformAdmin)" apps/control-plane/cmd/server/main.go` matches.
    - `grep -c "roleSvc.RequirePlatformAdmin" apps/control-plane/cmd/server/main.go` returns 0.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

</tasks>

---

## Plan 03 — Backend Handler Migration (8 modules, module-by-module)

<plan_meta>
plan: 03
type: execute
wave: 2
depends_on: [02]
autonomous: true
requirements: [RBAC-18-02, RBAC-18-08]
files_modified:
  - apps/control-plane/internal/accounts/http.go
  - apps/control-plane/internal/accounts/http_test.go
  - apps/control-plane/internal/accounts/service.go
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
  - apps/control-plane/cmd/server/main.go
</plan_meta>

<tasks>

<task type="auto" tdd="true">
  <name>Task 3A: Migrate accounts + apikeys handlers</name>
  <read_first>
    - apps/control-plane/internal/accounts/http.go lines 60-80 (handleListMembers — `!vc.User.EmailVerified` at line 72)
    - apps/control-plane/internal/accounts/service.go lines 147-171 (CreateInvitation — inline `!viewer.EmailVerified` at line 150, owner-loop at lines 162-168)
    - apps/control-plane/internal/apikeys/http.go lines 100-135 (mutation handlers — `!vc.Gates.CanManageAPIKeys` at 105, 128)
    - apps/control-plane/internal/apikeys/http_test.go line 105 (testVC.Gates path)
    - .planning/phases/18-rbac-matrix/18-RESEARCH.md §2a rows 5-7
  </read_first>
  <files>
    - apps/control-plane/internal/accounts/http.go
    - apps/control-plane/internal/accounts/service.go
    - apps/control-plane/internal/apikeys/http.go
    - apps/control-plane/internal/apikeys/http_test.go
  </files>
  <action>
    **accounts/http.go:72** — Replace `if !vc.User.EmailVerified { ... 403 ... }` with `if !policy.Can(actor, authz.PermMembersInvite) { ... same 403 ... }`. Add `policy authz.Policy` field to handler struct; accept in constructor.

    **accounts/service.go:150** — In `CreateInvitation`, replace `if !viewer.EmailVerified { return ..., &GateError{...} }` with `if !s.policy.Can(authz.Actor{UserID: viewer.UserID, Role: <derived>, Verified: viewer.EmailVerified}, authz.PermMembersInvite) { return ..., &GateError{Code: "permission_denied", Message: "members.invite permission required"} }`. Inject `policy authz.Policy` into `Service` via constructor.

    **accounts/service.go:162-168** — Replace owner-loop with: build Actor from resolved membership (`role := m.Role`), call `s.policy.Can(actor, authz.PermMembersInvite)`. ONE check now.

    **accounts/service.go:79-82** — Leave Gates derivation in place; Plan 04 removes it. DO NOT touch in this task.

    **apikeys/http.go:128** — Replace `if !vc.Gates.CanManageAPIKeys { ... 403 ... }` with `if !policy.Can(actor, authz.PermAPIKeysWrite) { 403 }`.

    **apikeys/http.go:105** (test fixture path) — Same swap.

    **apikeys/http_test.go:105** — Replace `testVC.Gates.CanManageAPIKeys` fixture's Gates field with a `policy authz.Policy` field on the test handler. Construct test viewers via `authz.Actor{Verified: true, Role: platform.RoleOwner}` directly through a stub resolver. Delete `Gates` field from test scaffold.

    Update `apikeys/http_test.go` tests:
    - `TestRequireOwnerOrAdminToCreateKey` → assert 403 for member, 201 for owner-verified, 403 for owner-unverified, 201 for admin.

    Ensure full Go suite for these two packages stays green.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose --profile tools run --rm toolchain bash -c "cd /workspace &amp;&amp; /usr/local/go/bin/go test ./apps/control-plane/internal/accounts/... ./apps/control-plane/internal/apikeys/... -count=1 -race -short"  </verify>
  <acceptance_criteria>
    - `go test ./apps/control-plane/internal/accounts/... ./apps/control-plane/internal/apikeys/... -count=1 -race -short` exits 0.
    - `grep -n 'Gates.CanManageAPIKeys' apps/control-plane/internal/apikeys/http.go` returns nothing.
    - `grep -n 'Gates.CanManageAPIKeys' apps/control-plane/internal/apikeys/http_test.go` returns nothing.
    - `grep -n 'authz.PermAPIKeysWrite' apps/control-plane/internal/apikeys/http.go` matches at least 1 site.
    - `grep -n 'authz.PermMembersInvite' apps/control-plane/internal/accounts/` returns at least 2 hits.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 3B: Migrate accounting + budgets + ledger + profiles + usage handlers</name>
  <read_first>
    - apps/control-plane/internal/accounting/http.go line 347 (`!viewerContext.User.EmailVerified` → 403)
    - apps/control-plane/internal/budgets/http.go lines 147, 486, 506
    - apps/control-plane/internal/ledger/http.go line 165
    - apps/control-plane/internal/profiles/http.go line 127
    - apps/control-plane/internal/usage/http.go line 393
    - .planning/phases/18-rbac-matrix/18-RESEARCH.md §2a rows 8-14
  </read_first>
  <files>
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
  </files>
  <action>
    For each module, inject `policy authz.Policy` and an actor resolver into the handler struct via constructor. Update main.go wiring to pass shared policy/middleware.

    Per-file edits:

    **accounting/http.go:347** — `!viewerContext.User.EmailVerified` → `!policy.Can(actor, authz.PermAnalyticsView)`. Same 403 + provider-blind error.

    **budgets/http.go:147** — `!viewerContext.User.EmailVerified` → `!policy.Can(actor, authz.PermBillingView)`.

    **budgets/http.go:486** — `ok, _ := roleSvc.IsWorkspaceOwner(...); if !ok { 403 }` → `if !policy.Can(actor, authz.PermBillingWrite) { 403 }`.

    **budgets/http.go:506** — Same swap as :486.

    **ledger/http.go:165** — `!viewerContext.User.EmailVerified` → `!policy.Can(actor, authz.PermLedgerView)`.

    **profiles/http.go:127** — `!viewerContext.User.EmailVerified` → `!policy.Can(actor, authz.PermWorkspaceSettings)`.

    **usage/http.go:393** — `!viewerContext.User.EmailVerified` → `!policy.Can(actor, authz.PermAnalyticsView)`.

    **grants/service.go:62** — `s.admin.IsPlatformAdmin(...)` STAYS. The `grants.AdminChecker` interface is unchanged. Middleware wrapping already swapped in Plan 02 Task 2B.

    For each `*_test.go` file: where a test previously seeded `viewer.EmailVerified = true` to bypass the inline check, now seed a complete Actor via stub resolver returning `Actor{Verified: true, Role: platform.RoleOwner}` for owner cases, `Verified: false` for unverified, and `Role: platform.RoleMember` for member. Each module's test file gains at minimum a `TestHandler_AuthzMatrix` table-driven test covering (owner+verified, owner+unverified, member+verified, member+unverified, admin) × {endpoints under test} → expected status code.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose --profile tools run --rm toolchain bash -c "cd /workspace &amp;&amp; /usr/local/go/bin/go test ./apps/control-plane/internal/accounting/... ./apps/control-plane/internal/budgets/... ./apps/control-plane/internal/ledger/... ./apps/control-plane/internal/profiles/... ./apps/control-plane/internal/usage/... -count=1 -race -short"</automated>
  </verify>
  <acceptance_criteria>
    - Test command above exits 0.
    - `grep -rn 'EmailVerified &&' apps/control-plane/internal/accounting/ apps/control-plane/internal/budgets/ apps/control-plane/internal/ledger/ apps/control-plane/internal/profiles/ apps/control-plane/internal/usage/ | grep -v _test.go` returns 0.
    - `grep -rn '\.Role == "owner"' apps/control-plane/internal/accounting/ apps/control-plane/internal/budgets/ apps/control-plane/internal/ledger/ apps/control-plane/internal/profiles/ apps/control-plane/internal/usage/ | grep -v _test.go` returns 0.
    - `grep -rn 'roleSvc.IsWorkspaceOwner' apps/control-plane/internal/budgets/http.go` returns 0.
    - `grep -c 'authz.Perm' apps/control-plane/internal/budgets/http.go apps/control-plane/internal/accounting/http.go apps/control-plane/internal/ledger/http.go apps/control-plane/internal/profiles/http.go apps/control-plane/internal/usage/http.go` returns at least 5.
    - `TestHandler_AuthzMatrix` (or equivalent table) exists in each test file.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

</tasks>

---

## Plan 04 — Viewer response: drop gates, emit permissions

<plan_meta>
plan: 04
type: execute
wave: 3
depends_on: [03]
autonomous: true
requirements: [RBAC-18-03]
files_modified:
  - apps/control-plane/internal/accounts/types.go
  - apps/control-plane/internal/accounts/service.go
  - apps/control-plane/internal/accounts/service_test.go
  - apps/control-plane/internal/accounts/http.go
  - apps/control-plane/internal/accounts/http_test.go
</plan_meta>

<tasks>

<task type="auto" tdd="true">
  <name>Task 4A: Flip ViewerContext + viewer JSON to permissions:[]string</name>
  <read_first>
    - apps/control-plane/internal/accounts/types.go lines 68-103 (ViewerContext + Gates)
    - apps/control-plane/internal/accounts/service.go lines 79-98 (gates derivation + return)
    - apps/control-plane/internal/accounts/http.go lines 200-230 (viewerContextResponse)
    - apps/control-plane/internal/accounts/service_test.go (assertions on Gates fields)
    - .planning/phases/18-rbac-matrix/18-RESEARCH.md §5 "Wire Shape"
    - .wolf/cerebrum.md §"Do-Not-Repeat" — field-name-truthfulness
  </read_first>
  <files>
    - apps/control-plane/internal/accounts/types.go
    - apps/control-plane/internal/accounts/service.go
    - apps/control-plane/internal/accounts/service_test.go
    - apps/control-plane/internal/accounts/http.go
    - apps/control-plane/internal/accounts/http_test.go
  </files>
  <action>
    **types.go:99-103** — Delete `Gates` struct entirely.

    **types.go:68-74** — Modify `ViewerContext`: remove `Gates Gates` line; add `Permissions []string` after `Memberships []MembershipSummary`.

    **service.go:79-82** — Delete the `gates := Gates{ CanInviteMembers: ..., CanManageAPIKeys: ... }` block (4 lines).

    **service.go:84-98** — In the `return ViewerContext{...}` block, replace `Gates: gates,` with `Permissions: s.policy.AllGranted(authz.Actor{UserID: viewer.UserID, WorkspaceID: chosen.AccountID, Role: platform.MembershipRole(chosen.Role), Verified: viewer.EmailVerified, IsAdmin: isAdmin}),`. Resolve `isAdmin` via injected `roleSvc.IsPlatformAdmin(ctx, viewer.UserID)` ABOVE the return; tolerate errors by setting `isAdmin = false` (provider-blind, log only).

    Update `NewService` signature to `(repo Repository, policy authz.Policy, roleSvc *platform.RoleService)`. Update main.go wiring.

    **http.go:220-223** — In `viewerContextResponse`, replace `"gates": map[string]interface{}{"can_invite_members": vc.Gates.CanInviteMembers, "can_manage_api_keys": vc.Gates.CanManageAPIKeys}` with `"permissions": vc.Permissions`. If `vc.Permissions` is nil, emit `[]string{}` (never null — match Phase 17 explicit-empty convention).

    **service_test.go** — Search for `Gates.CanInviteMembers`, `Gates.CanManageAPIKeys`, `.Gates` and replace expectations. `TestEnsureViewerContext_OwnerVerified` must now assert `viewerCtx.Permissions` contains `"members.invite"`, `"api_keys.write"`, etc. `TestEnsureViewerContext_MemberUnverified` must assert `viewerCtx.Permissions` is `[]string{}`.

    **http_test.go** — Tests that asserted `"gates"` must now assert `"permissions"` array AND NOT contain `"gates"` (regex-negative). Add `TestViewerEndpoint_OmitsGatesKey` with `require.NotContains(t, body, "gates")`.

    Adversarial walk: `grep "gates"|"can_invite_members"|"can_manage_api_keys" apps/control-plane/internal/accounts/` — all three return zero non-test, non-comment hits.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose --profile tools run --rm toolchain bash -c "cd /workspace &amp;&amp; /usr/local/go/bin/go test ./apps/control-plane/internal/accounts/... -count=1 -race -short"</automated>
  </verify>
  <acceptance_criteria>
    - `go test ./apps/control-plane/internal/accounts/... -count=1 -race -short` exits 0.
    - `grep -n 'type Gates struct' apps/control-plane/internal/accounts/types.go` returns 0.
    - `grep -rn 'Gates.CanInviteMembers\|Gates.CanManageAPIKeys' apps/control-plane/internal/accounts/` returns 0.
    - `grep -n '"gates":' apps/control-plane/internal/accounts/http.go` returns 0.
    - `grep -n '"permissions":' apps/control-plane/internal/accounts/http.go` returns at least 1.
    - `grep -n 'Permissions \[\]string' apps/control-plane/internal/accounts/types.go` returns 1.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

<task type="auto">
  <name>Task 4B: CI lint passes — verify no bare role/EmailVerified outside allowlist</name>
  <read_first>
    - packages/openai-contract/scripts/lint-no-bare-role-check.mjs (Plan 01 output)
  </read_first>
  <files>(no source edits — verification task)</files>
  <action>
    Run the lint to confirm Wave 2 + Plan 04 left zero bare role/EmailVerified checks outside the allowlist:
    ```
    node packages/openai-contract/scripts/lint-no-bare-role-check.mjs apps/control-plane/internal
    ```
    If exit 1, migrate the missed call site to `policy.Can(...)` OR add the file to allowlist (ONLY if it is platform/role.go-equivalent infrastructure). Re-run until exit 0.

    Then run full control-plane suite:
    ```
    go test ./apps/control-plane/... -count=1 -race -short
    ```
  </action>
  <verify>
    <automated>cd /home/sakib/hive &amp;&amp; node packages/openai-contract/scripts/lint-no-bare-role-check.mjs apps/control-plane/internal &amp;&amp; cd deploy/docker &amp;&amp; docker compose --profile tools run --rm toolchain bash -c "cd /workspace &amp;&amp; /usr/local/go/bin/go test ./apps/control-plane/... -count=1 -race -short"</automated>
  </verify>
  <acceptance_criteria>
    - Lint exits 0.
    - Full Go suite exits 0.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

</tasks>

---

## Plan 05 — viewer-gates.ts + client.ts + page consumer migration

<plan_meta>
plan: 05
type: execute
wave: 4
depends_on: [04]
autonomous: true
requirements: [RBAC-18-05, RBAC-18-06, RBAC-18-09]
files_modified:
  - apps/web-console/lib/viewer-gates.ts
  - apps/web-console/lib/control-plane/client.ts
  - apps/web-console/lib/control-plane/types.ts
  - apps/web-console/app/console/api-keys/page.tsx
  - apps/web-console/app/console/api-keys/[id]/limits/page.tsx
  - apps/web-console/app/console/members/page.tsx
  - apps/web-console/middleware.ts
  - apps/web-console/tests/unit/viewer-gates.test.ts
  - apps/web-console/tests/unit/permissions.parity.test.ts
</plan_meta>

<tasks>

<task type="auto" tdd="true">
  <name>Task 5A: Rewrite viewer-gates.ts; delete old exports; add can() helper</name>
  <read_first>
    - apps/web-console/lib/viewer-gates.ts (entire — full replacement)
    - apps/web-console/lib/control-plane/permissions.generated.ts (Plan 01 output)
    - apps/web-console/lib/control-plane/types.ts (export shim — must drop ViewerGates)
    - apps/web-console/tests/unit/viewer-gates.test.ts (9 tests — to be replaced)
  </read_first>
  <files>
    - apps/web-console/lib/viewer-gates.ts (REPLACE)
    - apps/web-console/lib/control-plane/types.ts (REMOVE ViewerGates; ADD Permission export)
    - apps/web-console/tests/unit/viewer-gates.test.ts (REPLACE)
  </files>
  <action>
    Replace `apps/web-console/lib/viewer-gates.ts` entirely:
    ```typescript
    import { type Permission, PERMISSIONS } from "./control-plane/permissions.generated";
    export type { Permission };
    export { PERMISSIONS };
    export interface ViewerWithPermissions { permissions: string[] }
    export function can(viewer: ViewerWithPermissions, perm: Permission): boolean {
      return new Set(viewer.permissions).has(perm);
    }
    ```

    Update `apps/web-console/lib/control-plane/types.ts:22`:
    - Remove `ViewerGates` from export list.
    - Add `export type { Permission } from "./permissions.generated";`

    Replace `apps/web-console/tests/unit/viewer-gates.test.ts` with a vitest matrix mirroring the Go matrix. Each test: `` `${role}/verified=${verified}/can(${perm}) === ${want}` ``. Defensive check: assert `PERMISSIONS.length === 11`.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose run --rm web-console npx vitest run tests/unit/viewer-gates.test.ts</automated>
  </verify>
  <acceptance_criteria>
    - `vitest run tests/unit/viewer-gates.test.ts` exits 0.
    - `grep -c "canInviteMembers\|canManageApiKeys\|allowedUnverifiedRoutes" apps/web-console/lib/viewer-gates.ts` returns 0.
    - `grep "export function can" apps/web-console/lib/viewer-gates.ts` matches.
    - `grep "ViewerGates" apps/web-console/lib/control-plane/types.ts` returns 0.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 5B: client.ts decoder — gates → permissions</name>
  <read_first>
    - apps/web-console/lib/control-plane/client.ts lines 27-29, 36, 104, 189-258, 448
    - .planning/phases/18-rbac-matrix/18-RESEARCH.md §3c
  </read_first>
  <files>apps/web-console/lib/control-plane/client.ts</files>
  <action>
    1. **Lines 27-29** — Delete `interface ViewerGates { can_invite_members: boolean; can_manage_api_keys: boolean }`.
    2. **Line 36** — On raw `Viewer` interface, replace `gates: ViewerGates` with `permissions: string[]`.
    3. **Line 104** — On decoded `Viewer` interface, same replacement.
    4. **Lines 189, 192, 203-204, 214-215, 256-258** — In decoder, replace the `gates` block (`readObjectField`, two `readBooleanField`) with `readStringArrayField(raw, "permissions", [])`. If helper does not exist, add a small inline reader.
    5. **Line 448** — Replace `gates: rawViewer.gates` with `permissions: Array.isArray(rawViewer.permissions) ? rawViewer.permissions : []`. Use the type-guard pattern already in this file. NO `as unknown`, NO `as any`.

    Confirm strict-TS + vitest still green.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose run --rm web-console bash -c "cd apps/web-console &amp;&amp; npx tsc --noEmit &amp;&amp; npx vitest run"</automated>
  </verify>
  <acceptance_criteria>
    - `tsc --noEmit` exits 0.
    - `vitest run` exits 0.
    - `grep -c 'ViewerGates\|can_invite_members\|can_manage_api_keys' apps/web-console/lib/control-plane/client.ts` returns 0.
    - `grep -c 'permissions' apps/web-console/lib/control-plane/client.ts` returns at least 3.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 5C: Migrate 4 page consumers + Next.js middleware</name>
  <read_first>
    - apps/web-console/app/console/api-keys/page.tsx line 17
    - apps/web-console/app/console/api-keys/[id]/limits/page.tsx line 37
    - apps/web-console/app/console/members/page.tsx lines 11, 61
    - apps/web-console/middleware.ts (confirm no allowedUnverifiedRoutes import — RESEARCH §9 says safe)
    - apps/web-console/lib/viewer-gates.ts (just rewritten)
  </read_first>
  <files>
    - apps/web-console/app/console/api-keys/page.tsx
    - apps/web-console/app/console/api-keys/[id]/limits/page.tsx
    - apps/web-console/app/console/members/page.tsx
    - apps/web-console/middleware.ts (verify only — likely no change)
  </files>
  <action>
    **app/console/api-keys/page.tsx:17** — Replace `viewer.gates.can_manage_api_keys` with `can(viewer, "api_keys.write")`. Add `import { can } from "@/lib/viewer-gates";`.

    **app/console/api-keys/[id]/limits/page.tsx:37** — Same swap.

    **app/console/members/page.tsx:11** — Replace `import { canInviteMembers } from "@/lib/viewer-gates"` with `import { can } from "@/lib/viewer-gates"`.

    **app/console/members/page.tsx:61** — Replace `canInviteMembers(viewer)` with `can(viewer, "members.invite")`.

    **apps/web-console/middleware.ts** — Grep for `allowedUnverifiedRoutes` and any reference to `viewer-gates`. If none found, no edit. If any found, replace with inline literal allowlist (kill the indirection).

    Confirm no remaining imports of legacy helpers:
    ```
    grep -rn 'canInviteMembers\|canManageApiKeys\|ViewerGates\|ViewerForGates\|allowedUnverifiedRoutes' apps/web-console/ --include="*.ts" --include="*.tsx"
    ```
    Output must be empty.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose run --rm web-console bash -c "cd apps/web-console &amp;&amp; npx tsc --noEmit &amp;&amp; npx vitest run"</automated>
  </verify>
  <acceptance_criteria>
    - `tsc --noEmit` exits 0.
    - `vitest run` exits 0.
    - `grep -rn 'canInviteMembers\|canManageApiKeys\|ViewerGates\|allowedUnverifiedRoutes' apps/web-console/ --include='*.ts' --include='*.tsx'` returns 0.
    - `grep -c 'can(viewer, "' apps/web-console/app/console/` returns at least 3.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 5D: Vitest matrix-parity spec — decisions identical to Go</name>
  <read_first>
    - apps/control-plane/internal/authz/policy_test.go (the Go matrix)
    - apps/web-console/lib/viewer-gates.ts (the FE helper)
    - apps/web-console/lib/control-plane/permissions.generated.ts (codegen output)
  </read_first>
  <files>apps/web-console/tests/unit/permissions.parity.test.ts (CREATE)</files>
  <behavior>
    - Test imports `PERMISSIONS` from `permissions.generated`.
    - Fixture `MATRIX: Array<{role, verified, isAdmin, expectedPerms: string[]}>` enumerates same 5 actor states as Go test.
    - For each actor state: construct `viewer = { permissions: expectedPerms }`, iterate ALL 11 permissions in `PERMISSIONS`, assert `can(viewer, perm) === expectedPerms.includes(perm)`.
    - Expected lists derived from §"Decision matrix" table (hardcoded — not dynamically consumed from Go test).
    - Defensive: fails if `PERMISSIONS.length !== 11`.
  </behavior>
  <action>
    Create `apps/web-console/tests/unit/permissions.parity.test.ts`. Hardcode expected lists per the §"Decision matrix" table.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/deploy/docker &amp;&amp; docker compose run --rm web-console bash -c "cd apps/web-console &amp;&amp; npx vitest run tests/unit/permissions.parity.test.ts"</automated>
  </verify>
  <acceptance_criteria>
    - `vitest run tests/unit/permissions.parity.test.ts` exits 0.
    - Test enumerates exactly 5 actor states × 11 perms = 55 assertions.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

</tasks>

---

## Plan 06 — Playwright unverified spec + CI wiring

<plan_meta>
plan: 06
type: execute
wave: 4
depends_on: [05]
autonomous: true
requirements: [RBAC-18-07, RBAC-18-10]
files_modified:
  - apps/web-console/tests/e2e/rbac-unverified.spec.ts
  - .github/workflows/ci.yml
</plan_meta>

<tasks>

<task type="auto" tdd="true">
  <name>Task 6A: Playwright rbac-unverified.spec.ts</name>
  <read_first>
    - apps/web-console/tests/e2e/auth-shell.spec.ts lines 47-75 (existing unverified-members test — reuse signInAsUnverifiedUser)
    - apps/web-console/tests/e2e/support/e2e-auth-fixtures.mjs (fixture infra)
    - .planning/phases/18-rbac-matrix/18-RESEARCH.md §8d
  </read_first>
  <files>apps/web-console/tests/e2e/rbac-unverified.spec.ts (CREATE)</files>
  <behavior>
    - `test.describe("RBAC: unverified user blocked from sensitive routes")`:
      - `beforeEach`: sign in as unverified test user via shared helper.
      - Test 1: `await page.goto("/console/billing")` → assert redirect to `/console/settings/profile` OR a 403/forbidden surface.
      - Test 2: `await page.goto("/console/api-keys")` → assert "Create" button NOT visible.
    - Each test logs final URL + status for evidence on failure.
  </behavior>
  <action>
    Create the spec mirroring `auth-shell.spec.ts:47-75`. Use whichever assertion matches the current redirect target (executor confirms by running and observing). The spec is the LAST automated gate.
  </action>
  <verify>
    <automated>cd /home/sakib/hive/apps/web-console &amp;&amp; npx playwright test tests/e2e/rbac-unverified.spec.ts</automated>
  </verify>
  <acceptance_criteria>
    - Playwright spec exits 0.
    - At least 2 tests pass.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

<task type="auto">
  <name>Task 6B: CI wiring — lint + codegen drift as blocking steps</name>
  <read_first>
    - .github/workflows/*.yml (find where Phase 17 lint-no-customer-usd is invoked — match position)
    - packages/openai-contract/scripts/lint-no-bare-role-check.mjs (Plan 01)
    - Makefile target gen-permissions (Plan 01)
  </read_first>
  <files>.github/workflows/ci.yml (or the equivalent file invoking Phase 17 lint)</files>
  <action>
    Locate the CI workflow step running `node packages/openai-contract/scripts/lint-no-customer-usd.mjs --all`. Add two NEW required steps after it:

    Step 1 — bare-role lint:
    ```yaml
    - name: Lint — no bare role checks outside authz package
      run: node packages/openai-contract/scripts/lint-no-bare-role-check.mjs apps/control-plane/internal
    ```

    Step 2 — codegen drift:
    ```yaml
    - name: Codegen drift — permissions.generated.ts
      run: |
        make gen-permissions
        git diff --exit-code apps/web-console/lib/control-plane/permissions.generated.ts
    ```

    Both required (block merge).
  </action>
  <verify>
    <automated>cd /home/sakib/hive &amp;&amp; node packages/openai-contract/scripts/lint-no-bare-role-check.mjs apps/control-plane/internal &amp;&amp; make gen-permissions &amp;&amp; git diff --exit-code apps/web-console/lib/control-plane/permissions.generated.ts</automated>
  </verify>
  <acceptance_criteria>
    - Both commands exit 0 locally.
    - CI workflow file contains both step names verbatim.
    - `grep "lint-no-bare-role-check" .github/workflows/` returns at least 1.
    - `grep "gen-permissions" .github/workflows/` returns at least 1.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

</tasks>

---

## Plan 07 — REQUIREMENTS + evidence + VERIFICATION + STATE flip + todo resolve

<plan_meta>
plan: 07
type: execute
wave: 5
depends_on: [06]
autonomous: true
requirements: [RBAC-18-11]
files_modified:
  - .planning/REQUIREMENTS.md
  - .planning/phases/18-rbac-matrix/18-VERIFICATION.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-01.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-02.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-03.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-04.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-05.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-06.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-07.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-08.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-09.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-10.md
  - .planning/phases/18-rbac-matrix/evidence/RBAC-18-11.md
  - .planning/STATE.md
  - .planning/todos/done/2026-04-22-design-rbac-authorization-model.md
  - .planning/ROADMAP.md
</plan_meta>

<tasks>

<task type="auto">
  <name>Task 7A: Mint RBAC-18-01..11 rows in REQUIREMENTS.md + create evidence files</name>
  <read_first>
    - .planning/REQUIREMENTS.md (full — find "v1.1 Requirements (in flight)" section)
    - .planning/phases/14-payments-budget-grant/evidence/PAY-14-01.md (frontmatter shape reference)
    - .planning/phases/18-rbac-matrix/18-RESEARCH.md §10 (proposed IDs)
  </read_first>
  <files>
    - .planning/REQUIREMENTS.md
    - .planning/phases/18-rbac-matrix/evidence/RBAC-18-01.md through RBAC-18-11.md (CREATE each)
  </files>
  <action>
    In `.planning/REQUIREMENTS.md`, under `## v1.1 Requirements (in flight)`, add subsection `### RBAC Matrix (Phase 18)` after `### Payments / Budget / Grant (Phase 14)`. Populate with 11 rows:
    ```
    | RBAC-18-01 | 18 | Satisfied | [evidence/RBAC-18-01.md](phases/18-rbac-matrix/evidence/RBAC-18-01.md) |
    ...
    | RBAC-18-11 | 18 | Satisfied | [evidence/RBAC-18-11.md](phases/18-rbac-matrix/evidence/RBAC-18-11.md) |
    ```

    Create each evidence file with frontmatter shape required by `scripts/verify-requirements-matrix.sh`:
    ```
    ---
    requirement_id: RBAC-18-01
    status: Satisfied
    verified_at: 2026-MM-DD
    verified_by: gsd-executor
    evidence:
      - path: apps/control-plane/internal/authz/permissions.go
        contains: PermBillingView
      - path: apps/control-plane/internal/authz/policy.go
        contains: func (p Policy) Can
      - path: apps/control-plane/internal/authz/policy_test.go
        contains: TestPolicyMatrix
    ---
    # RBAC-18-01: authz package — matrix, Actor, Policy.Can
    ...
    ```

    One-line summary per ID from RESEARCH §10 verbatim.

    Run validator: `bash scripts/verify-requirements-matrix.sh`.
  </action>
  <verify>
    <automated>cd /home/sakib/hive &amp;&amp; bash scripts/verify-requirements-matrix.sh</automated>
  </verify>
  <acceptance_criteria>
    - `verify-requirements-matrix.sh` exits 0.
    - `grep -c 'RBAC-18-' .planning/REQUIREMENTS.md` returns at least 11.
    - `ls .planning/phases/18-rbac-matrix/evidence/RBAC-18-*.md | wc -l` returns 11.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

<task type="auto">
  <name>Task 7B: 18-VERIFICATION.md + ROADMAP + STATE flip + todo move</name>
  <read_first>
    - .planning/phases/14-payments-budget-grant/14-VERIFICATION.md (shape reference)
    - .planning/phases/18-rbac-matrix/18-VALIDATION.md §"Per-Task Verification Map"
    - .planning/STATE.md (locate `v1_1_ship_gate` block)
    - .planning/ROADMAP.md (Phase 18 row)
    - .planning/todos/pending/2026-04-22-design-rbac-authorization-model.md
  </read_first>
  <files>
    - .planning/phases/18-rbac-matrix/18-VERIFICATION.md (CREATE)
    - .planning/ROADMAP.md
    - .planning/STATE.md
    - .planning/todos/done/2026-04-22-design-rbac-authorization-model.md (MOVE from pending/)
  </files>
  <action>
    Create `.planning/phases/18-rbac-matrix/18-VERIFICATION.md` with frontmatter:
    ```
    ---
    phase: 18-rbac-matrix
    status: complete
    verified_at: 2026-MM-DD
    requirements_verified: [RBAC-18-01..RBAC-18-11]
    ---
    ```
    Body: one section per ROADMAP success criterion (6 criteria), each linking to evidence file(s) + specific test command output.

    Edit `.planning/ROADMAP.md`:
    - Flip Phase 18 status `Planned` → `Complete`.
    - Update Plans column to `7/7`.
    - Add completion date.

    Edit `.planning/STATE.md`:
    - Find `v1_1_ship_gate.rbac_matrix` (or equivalent — RESEARCH says STATE.md §`v1_1_ship_gate`). Flip from `false` to `true`. If structure differs, flip equivalent flag and add comment `# Phase 18 closed YYYY-MM-DD`.

    Move `.planning/todos/pending/2026-04-22-design-rbac-authorization-model.md` to `.planning/todos/done/` via `git mv`. Add closing note referencing Phase 18 closure.
  </action>
  <verify>
    <automated>cd /home/sakib/hive &amp;&amp; test -f .planning/phases/18-rbac-matrix/18-VERIFICATION.md &amp;&amp; test -f .planning/todos/done/2026-04-22-design-rbac-authorization-model.md &amp;&amp; grep -q 'rbac_matrix: true' .planning/STATE.md &amp;&amp; ! test -f .planning/todos/pending/2026-04-22-design-rbac-authorization-model.md &amp;&amp; echo ok</automated>
  </verify>
  <acceptance_criteria>
    - All four files moved/created/edited.
    - STATE.md contains `rbac_matrix: true`.
    - Pending todo file no longer exists; done version exists.
    - 18-VERIFICATION.md links to all 11 evidence files.
  </acceptance_criteria>
  <done>All acceptance_criteria above pass.</done>
</task>

</tasks>

---

<verification>
Phase-level gate (per 18-VALIDATION.md §"Validation Sign-Off"). All must be green:

1. `cd /home/sakib/hive/deploy/docker && docker compose --profile tools run --rm toolchain bash -c "cd /workspace && /usr/local/go/bin/go test ./apps/control-plane/... -count=1 -race -short"` → exit 0
2. `cd /home/sakib/hive/deploy/docker && docker compose run --rm web-console bash -c "cd apps/web-console && npx tsc --noEmit && npx vitest run && npm run build"` → exit 0
3. `cd /home/sakib/hive/apps/web-console && npx playwright test tests/e2e/rbac-unverified.spec.ts` → exit 0
4. `node packages/openai-contract/scripts/lint-no-bare-role-check.mjs apps/control-plane/internal` → exit 0
5. `cd /home/sakib/hive && make gen-permissions && git diff --exit-code apps/web-console/lib/control-plane/permissions.generated.ts` → exit 0
6. `cd /home/sakib/hive && bash scripts/verify-requirements-matrix.sh` → exit 0
</verification>

<success_criteria>
Mapped to ROADMAP §"Phase 18" success criteria 1-6:

1. **Single Go authz package** ← Plan 01 + RBAC-18-01
2. **No bare role/EmailVerified outside authz** ← Plans 03, 04, 06 + RBAC-18-02 + RBAC-18-07
3. **`can()` helper replaces canInviteMembers/canManageApiKeys/allowedUnverifiedRoutes** ← Plan 05 + RBAC-18-05 + RBAC-18-06
4. **Go integration tests across role × verified × permission** ← Plan 03 (per-module matrices) + RBAC-18-08
5. **Vitest matrix-parity + Playwright unverified spec** ← Plans 05, 06 + RBAC-18-09 + RBAC-18-10
6. **STATE.md ship-gate flipped + todo resolved** ← Plan 07 + RBAC-18-11
</success_criteria>

<output>
After each plan completes, executor creates `.planning/phases/18-rbac-matrix/18-{NN}-SUMMARY.md` with frontmatter (phase, plan, files_modified, tests_passed) and prose covering: what was built, what was verified, what carries forward.
</output>
