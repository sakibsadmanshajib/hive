# Phase 18: RBAC Matrix - Context

**Gathered:** 2026-05-14
**Status:** Ready for planning

<domain>
## Phase Boundary

Replace ad hoc workspace-role + email-verified + `IsPlatformAdmin` checks
(currently inlined in `accounts/service.go` and scattered across 8 control-plane
modules and the web-console `viewer-gates.ts`) with a single, reusable,
verification-aware authorization model.

**In scope:**
- A control-plane authz package that owns the `Permission` enum + a single
  `Policy.Can(actor, permission) bool` decision function.
- Migration of every existing call site in control-plane HTTP handlers and
  services to route through `Policy.Can`.
- A viewer-response wire shape that surfaces a computed `permissions: []`
  array (alongside `role` + `verified` for labels) replacing the
  `gates.{can_invite_members, can_manage_api_keys}` shape and the
  `allowedUnverifiedRoutes` static string list.
- A web-console `can(viewer, permission)` helper backed by a codegen'd
  union type sourced from the Go authz package, plus migration of every
  consumer (sidebar nav, route guards, billing/api-keys gates).
- Regression coverage: Go handler tests across role × verified × permission;
  web-console vitest matrix-parity tests; one Playwright spec for
  unverified-user redirect on a sensitive route.
- Closure: STATE.md `v1_1_ship_gate.rbac_matrix` flipped true; pending todo
  `2026-04-22-design-rbac-authorization-model.md` resolved; PAY-14-08 stub
  contract realised; HANDOFF-17-01 consumed.

**Out of scope (deferred):**
- Account tier (free/pro/enterprise) as an actor attribute — HANDOFF-13
  remains open and routes to a future phase.
- Edge-api hot-path authorization changes (Phase 5 + Phase 12 own that
  surface).
- New roles beyond `member` / `owner` / `platform_admin` (e.g. read-only
  member, billing-only admin).
- Admin UI to assign roles or view audit log of permission decisions.
- 2FA-verified, KYC-verified, or other verification dimensions beyond
  email — `Verified bool` is intentionally one-bit for v1.1.

</domain>

<decisions>
## Implementation Decisions

### Role taxonomy
- Roles are explicit: `member`, `owner`, `platform_admin`. (Existing
  `MembershipRole` in `internal/platform/role.go` is the seed — keep
  `"owner"` and `"member"` as the wire values; introduce `platform_admin`
  alongside as an overlay attribute, not a workspace role.)
- `guest` (unauthenticated) is handled upstream by the auth middleware
  and never reaches `Policy.Can`. Matrix only covers authenticated actors.
- Email-verified state is a cross-cutting `Verified bool` attribute on the
  `Actor` struct, NOT a separate role. Future verification dimensions
  (2FA, KYC) can add bits to the actor without exploding the role enum.
- `platform_admin` is a cross-workspace overlay: a flag on the user
  (already surfaced by `RoleService.IsPlatformAdmin`). It augments the
  workspace role; it does not replace it. `Policy.Can` evaluates the
  overlay after the workspace-role lookup.

### Actor shape
```go
type Actor struct {
    UserID      uuid.UUID
    WorkspaceID uuid.UUID            // zero value for platform-scoped checks
    Role        platform.MembershipRole // "owner" | "member"
    Verified    bool                  // email_verified
    IsAdmin     bool                  // platform_admin overlay
}
```

### Permission granularity
- Permissions are `resource.action` pairs, typed as a Go `Permission`
  string constant. Initial set (12-14 perms, expandable):
  - `billing.view`, `billing.write`
  - `api_keys.read`, `api_keys.write` (write covers create / rotate / revoke
    — finer split deferred until a real use case appears)
  - `analytics.view`
  - `members.invite`, `members.manage`
  - `workspace.settings`
  - `grants.create`
  - `ledger.view`
  - `platform.admin` (only `platform_admin` overlay actors hold this)
- All permission constants live in `apps/control-plane/internal/authz/permissions.go`
  — single central declaration. Each permission carries a
  `requires_verified bool` flag in its registry entry.
- Verification rule: per-permission `RequiresVerified` flag. An actor with
  `Verified=false` is denied any permission whose registry entry has
  `RequiresVerified=true`. Read permissions (`*.view`, `*.read`) default
  to `RequiresVerified=false`; write/manage permissions default to
  `RequiresVerified=true`.

### Wire contract (BE → web-console)
- Viewer response gains `permissions: []string` — the precomputed set
  for the resolved (actor, workspace). String values match Go const
  values exactly.
- Viewer response retains `role: "owner"|"member"` and `verified: bool`
  (already present) for UI labels and CTAs ("Owner badge",
  "Verify email" prompt). FE never derives authz from these — only from
  `permissions`.
- Existing `gates.can_invite_members` / `gates.can_manage_api_keys`
  fields are **dropped** in the same PR. Console is the only consumer
  (no external SDK depends on these fields). `allowedUnverifiedRoutes`
  string list is removed; route guards consult `can(viewer, perm)`
  against a per-route required-permission map.

### TS sync
- `apps/web-console/lib/control-plane/permissions.generated.ts` is
  codegen'd from the Go `Permission` registry at build time. CI fails
  on drift. Mirrors the `openai-contract` codegen pattern.
- Web-console exports `can(viewer, permission): boolean` from
  `apps/web-console/lib/viewer-gates.ts`. Implementation is a `Set`
  lookup over `viewer.permissions`. The old `canInviteMembers` /
  `canManageApiKeys` helpers are deleted (not aliased).

### Enforcement layer
- Per-handler `policy.Can(actor, perm)` for resource-scoped checks
  inside handler bodies (the common case — actor + workspace ID
  resolved from context).
- A thin `RequirePermission(perm)` middleware (parallel to
  `RequirePlatformAdmin` from Phase 14) for routes whose entire group
  is gated on a coarse permission. `RequirePlatformAdmin` is reimplemented
  as `RequirePermission(PermPlatformAdmin)`.

### Migration sequencing (commit-by-commit)
1. **AUDIT** — enumerate every authz call site in
   `internal/{accounts,apikeys,accounting,grants,budgets,ledger,profiles,usage}/*`
   and the equivalent web-console gates. One commit, AUDIT.md artifact.
2. **authz package + matrix table** — `internal/authz/{permissions.go,
   policy.go,policy_test.go}`. RED-first table-driven test asserts every
   (role, verified, permission) combination. One commit.
3. **Backend migration** — module-by-module commits replacing inline
   checks with `policy.Can()` and gate middleware. Each commit keeps
   the test suite green.
4. **Wire flip** — accounts/service.go produces `permissions: []`,
   drops `gates.*`. Old fields hard-cut in this commit. CI lint
   `lint-no-bare-role-check.mjs` (or Go vet rule) added to forbid
   `viewer.Role == "owner"` outside the authz package.
5. **Codegen + FE migration** — generated TS types committed,
   viewer-gates.ts refactored, consumers updated, `allowedUnverifiedRoutes`
   removed.
6. **Regression tests** — Go integration tests (role × verified ×
   permission), web-console vitest matrix-parity (decisions identical
   to Go), one Playwright spec covering unverified redirect on
   `/billing` and `/keys`.
7. **Closure** — REQUIREMENTS.md gains RBAC-18-01..NN, evidence files,
   VERIFICATION.md, STATE.md ship-gate flipped, todo resolved.

### Claude's Discretion
- Exact final permission list — start with the 12-14 above; add as
  audit reveals call sites that need finer cuts.
- Codegen tool choice (a tiny Go program emitting TS vs reusing
  existing `openai-contract` generator vs a shell script). Pick the
  lowest-friction option that survives CI.
- Whether to package the matrix as `internal/authz` or fold into
  `internal/platform/authz` next to the Phase 14 role stub — pick the
  one that keeps the dependency graph cleanest.
- Lint mechanism (Go analyzer vs simple grep-based CI step) — match
  whichever the Phase 17 `lint-no-customer-usd.mjs` precedent prefers.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase 18 inputs (binding contracts)
- `.planning/phases/14-payments-budget-grant/evidence/PAY-14-08.md` —
  PAY-14-08 RBAC contract stub. `RoleService.IsWorkspaceOwner` /
  `IsPlatformAdmin` signatures are the Phase 18 contract; only bodies
  are replaceable.
- `.planning/phases/13-console-integration-fixes/13-AUDIT.md` —
  HANDOFF-13 origin (tier-aware viewer-gates carries forward to Phase 19+,
  NOT this phase).
- `.planning/phases/13-console-integration-fixes/evidence/CONSOLE-13-05.md`
  — current vitest coverage for owner-only routes; Phase 18 must
  preserve all assertions while migrating the underlying mechanism.
- `.planning/todos/pending/2026-04-22-design-rbac-authorization-model.md`
  — the original Phase 18 todo. Solution section enumerates the
  must-have design properties (verification-aware, server authoritative,
  console derived from same model, regression coverage for both flows).

### v1.1 ship-gate context
- `.planning/STATE.md` §`v1_1_ship_gate` — `rbac_matrix: false`
  blocks v1.1 ship. Phase 18 must flip it.
- `.planning/v1.1-DEFERRED-SCOPE.md` §`New Gaps Surfaced` item #8 —
  the gap statement Phase 18 closes.

### Existing implementations referenced by the migration
- `apps/control-plane/internal/platform/role.go` — Phase 14 stub:
  `MembershipRole`, `RoleStore`, `RoleService.IsWorkspaceOwner` /
  `IsPlatformAdmin`, `RequirePlatformAdmin` middleware.
- `apps/control-plane/internal/platform/role_pgx.go` — `pgxRoleStore`
  implementation hitting `account_memberships`.
- `apps/control-plane/internal/accounts/service.go:80-81` —
  current `CanInviteMembers` / `CanManageAPIKeys` inline derivation
  (the canonical ad hoc shape Phase 18 replaces).
- `apps/web-console/lib/viewer-gates.ts` — `ViewerGates`,
  `canInviteMembers`, `canManageApiKeys`, `allowedUnverifiedRoutes`
  (full file replaced by `can(viewer, perm)`).

### Cross-cutting standards
- `apps/control-plane/CLAUDE.md` (project root) §Conventions —
  immutability, error handling, no-bare-secret rules carry through
  to the new authz package.
- Phase 17 Do-Not-Repeat (cerebrum.md): name fields truthfully (no
  `gates.can_*` aliasing a different semantic), and adversarial-walk
  every `map[string]any` written to a customer wire — applies to the
  new viewer response shape.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/platform/role.go` (Phase 14): `MembershipRole` enum,
  `RoleStore` interface, `RoleService` with `IsWorkspaceOwner` +
  `IsPlatformAdmin`, `RequirePlatformAdmin` http.Handler middleware.
  Phase 18 builds the matrix on top of this — does NOT replace it.
- `internal/platform/role_pgx.go`: `pgxRoleStore` already loads role
  from `account_memberships`. Reuse for actor materialisation; no
  new query needed.
- `internal/platform/role_test.go`: existing table-driven coverage
  shape can be copied for the matrix test.
- `apps/web-console/lib/viewer-gates.test.ts` (~18 tests covering
  owner / non-owner role matrix per CONSOLE-13-05): the parity-test
  scaffold for the new `can()` helper.

### Established Patterns
- Go: typed string consts for enums (already used by `MembershipRole`,
  `Permission` follows the same shape).
- Go: handler-level authz via inline checks plus narrow middlewares
  (`RequirePlatformAdmin`). Phase 18 adds a generalised
  `RequirePermission` parallel.
- Web-console: `lib/control-plane/types.ts` re-export shim from
  Phase 13. The codegen'd `permissions.generated.ts` lives under
  the same `lib/control-plane/` namespace.
- CI lint precedent: Phase 17 `packages/openai-contract/scripts/lint-no-customer-usd.mjs`
  pattern (simple ripgrep wrapper that fails CI). Phase 18 adds
  `lint-no-bare-role-check.mjs` in the same shape.

### Integration Points
- 8 backend modules referenced by `all-handlers-authz` grep:
  `accounts`, `apikeys`, `accounting`, `grants`, `budgets`, `ledger`,
  `profiles`, `usage`. Each one needs the AUDIT pass to identify
  every authz call site and the target permission name.
- `accounts/service.go` is the single producer of the viewer response
  — wire-shape change is localised here. `accounts/types.go` owns the
  response struct.
- Web-console consumers of `canInviteMembers` / `canManageApiKeys` /
  `allowedUnverifiedRoutes`: sidebar nav, billing/keys page guards,
  middleware route guard. The codegen'd union type drops in via
  `lib/control-plane/permissions.generated.ts`.

</code_context>

<specifics>
## Specific Ideas

- Wire shape must follow Phase 17 Do-Not-Repeat #1: do NOT name the
  wire field something it is not. `permissions: []` is exact. No
  `gates.can_*` alias should resurface, even as a deprecation shim,
  because the field name lying is the failure mode the project just
  paid for.
- The matrix table-driven test should also be the auditable
  "permission specification" — readers should be able to point at the
  test source and read "this is what platform_admin can do" without
  reading the policy code.
- The CI lint should treat `chosen.Role == "owner"` and
  `viewer.EmailVerified &&` outside `internal/authz/` as a
  hard-fail (allowlist for the package itself, the role store, and
  the matrix test).

</specifics>

<deferred>
## Deferred Ideas

- **Tier-aware permissions (HANDOFF-13)** — account tier feeding into
  the matrix. Routes to a future phase (Phase 19+).
- **Fine-grained API-key write split** — `api_keys.create` /
  `api_keys.rotate` / `api_keys.revoke` as separate perms. Add when
  a real product reason appears (e.g. "rotate but not revoke"
  permission for a limited admin role).
- **Audit log of permission decisions** — record every `Policy.Can`
  decision for compliance. Useful eventually; out of scope for v1.1.
- **Admin UI to view / edit permissions** — out of scope; matrix is
  hard-coded for v1.1.
- **Edge-api hot-path authz** — Phase 5 + Phase 12 own that surface;
  not folded into Phase 18.
- **2FA / KYC verification dimensions** — `Verified bool` stays
  one-bit. Future phases can add `Verified2FA bool` etc. without
  changing the role enum.
- **Read-only member / billing-only admin sub-roles** — out of scope;
  v1.1 ships with member / owner / platform_admin only.

</deferred>

---

*Phase: 18-rbac-matrix*
*Context gathered: 2026-05-14*
