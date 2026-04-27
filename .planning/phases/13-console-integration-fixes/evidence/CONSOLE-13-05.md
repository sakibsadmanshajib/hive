---
requirement_id: CONSOLE-13-05
status: Satisfied
verified_at: 2026-04-27
verified_by: Phase 13 executor
phase_satisfied: 13
evidence: apps/web-console/lib/viewer-gates.ts + tests/unit/viewer-gates.test.ts
---

# CONSOLE-13-05 — viewer-gates honoured on owner-only routes

## Truth

Owner-gated routes (`api-keys/[id]/limits`, `settings/billing`, members invite, console root admin tiles) honour `viewer-gates.ts` — non-owner workspace member receives read-only or 403 surface, not a raw error.

## Evidence

- `apps/web-console/lib/viewer-gates.ts` — 22-line module exporting `canManageApiKeys` / `canInviteMembers` / `canManageBilling` predicate set keyed off `Viewer.gates` payload.
- `apps/web-console/tests/unit/viewer-gates.test.ts` — 9 tests cover owner / non-owner / unverified role matrix; all pass.
- `apps/web-console/tests/unit/api-keys-limits.test.ts` — 9 tests cover the Phase-12 limits-form owner gate.
- `apps/web-console/tests/e2e/auth-shell.spec.ts:50` — verified-only members page redirect spec (existing).

Phase 18 extends this primitive into a tier-aware matrix. Phase 13 keeps the existing two-role surface only (owner / non-owner).

## Command

```
npx vitest run tests/unit/viewer-gates.test.ts tests/unit/api-keys-limits.test.ts
# 18 tests pass
```
