---
requirement_id: CONSOLE-13-07
status: Partial
verified_at: 2026-04-27
verified_by: Phase 13 executor
phase_satisfied: 13
evidence: apps/web-console/tests/e2e/auth-shell.spec.ts + 13-AUDIT.md
---

# CONSOLE-13-07 — Workspace switch + invitation accept round-trip

## Truth

Workspace switcher + invitation accept flow round-trip against control-plane `/v1/accounts` and `/v1/invitations` endpoints; switching workspace updates session cookies + nav-shell content.

## Evidence

- `tests/e2e/auth-shell.spec.ts:63` — invitation accept spec (passes — flaky 1× retry).
- `tests/e2e/auth-shell.spec.ts:88` — workspace switcher persistence spec (currently FAILING in baseline E2E run; root cause is multi-account fixture seed race).
- `__tests__/invitation-accept.test.tsx` — unit-test for invitations/accept page.
- HANDOFF-13-01 filed against Phase 14 fixture-stability work for the workspace-switcher seed race.

## Status

Marked `Partial` because the round-trip code paths exist + are exercised, but the workspace switcher E2E spec fails on a pre-existing fixture-seed race. Phase 14 owns the fix; Phase 13 inherited the failure (not introduced).
