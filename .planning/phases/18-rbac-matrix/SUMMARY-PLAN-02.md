---
phase: 18
plan: 02
subsystem: control-plane/accounts
tags: [rbac, authz, actor-resolver, middleware, platform-admin]
dependency_graph:
  requires: [18-01]
  provides: [ActorFor, NewActorResolver, authzMW.RequirePermission]
  affects: [cmd/server/main.go, internal/accounts, internal/grants]
tech_stack:
  added: []
  patterns: [ActorFor pure adapter, NewActorResolver closure, middleware RequirePermission]
key_files:
  created:
    - apps/control-plane/internal/accounts/actor_resolver.go
    - apps/control-plane/internal/accounts/actor_resolver_test.go
  modified:
    - apps/control-plane/cmd/server/main.go
    - packages/openai-contract/scripts/lint-no-bare-role-check.mjs
decisions:
  - ActorFor pure mapping auth.Viewer + Membership + isAdmin to authz.Actor, no DB
  - NewActorResolver takes *platform.RoleService directly for simplicity
  - authzMW hoisted before if pool != nil so grants wiring outside block can reference it
  - RequirePlatformAdmin replaced by authzMW.RequirePermission(PermPlatformAdmin)
  - actor_resolver.go + service.go added to lint allowlist
metrics:
  duration: "~20 min"
  completed: "2026-05-14"
  tasks_completed: 2
  files_changed: 4
---

# Phase 18 Plan 02: Actor Resolver + Middleware Wire-Up Summary

**One-liner:** Pure ActorFor() adapter + NewActorResolver closure wired into main.go, replacing RequirePlatformAdmin with RequirePermission(PermPlatformAdmin).

## Tasks Completed

| Task | Description | Commit |
|------|-------------|--------|
| 2A | accounts/actor_resolver.go — ActorFor + NewActorResolver | 39adcf3 |
| 2B | cmd/server/main.go — wire authzMW, swap RequirePlatformAdmin | 40ad4ff |

## What Was Built

### Task 2A (39adcf3)

- ActorFor: pure stateless mapping from auth.Viewer + Membership + isAdmin bool to authz.Actor. No DB.
- NewActorResolver: closure calling EnsureViewerContext + IsPlatformAdmin, returns ActorFor result.
- Table-driven tests: owner+verified, member+unverified, admin overlay, 6 combined combos, ErrNoViewer sentinel.

### Task 2B (40ad4ff)

- Hoisted var authzMW authz.Middleware before if pool != nil block.
- Inside pool: actorResolver := accounts.NewActorResolver(accountsSvc, roleSvc); authzMW = authz.NewMiddleware(actorResolver).
- Replaced roleSvc.RequirePlatformAdmin(grantsHandler.AdminMux()) with authzMW.RequirePermission(authz.PermPlatformAdmin)(grantsHandler.AdminMux()).

## Deviations from Plan

**1. [Rule 1 - Bug] declared-and-not-used compile error for authzMW**
- Found during: Task 2B
- Issue: := inside if pool != nil block, but grants wiring outside block references it.
- Fix: Hoisted var authzMW authz.Middleware before block; used = inside.

**2. [Rule 2 - Missing] Lint allowlist for actor_resolver.go + service.go**
- Found during: Task 2A
- Issue: lint-no-bare-role-check.mjs flagged chosen.Role at adapter sites (DTO, not gate).
- Fix: Added both files to ALLOWLIST_DIRS.

## Self-Check: PASSED

- accounts/actor_resolver.go: EXISTS
- accounts/actor_resolver_test.go: EXISTS
- Commits 39adcf3 + 40ad4ff: EXIST
