---
requirement_id: CONSOLE-13-06
status: Satisfied
verified_at: 2026-04-27
verified_by: Phase 13 executor
phase_satisfied: 13
evidence: apps/web-console/__tests__/auth-routes.test.ts + apps/web-console/tests/e2e/unauth.spec.ts + apps/web-console/tests/e2e/_probe/staging-flows.spec.ts
---

# CONSOLE-13-06 — Auth flows green

## Truth

Sign-in, sign-up, forgot-password, reset-password, sign-out, OAuth callback all complete end-to-end against the staging Supabase fixture; a Playwright spec exercises each happy-path.

## Evidence

- `__tests__/auth-routes.test.ts` (12 tests) covers sign-in / sign-up / forgot-password / reset-password / sign-out / callback route handlers + happy-path + error states. All pass.
- `tests/e2e/unauth.spec.ts` (5 tests) covers unauthenticated redirects for sign-in, sign-up, console route gate, 404 page. All pass.
- `tests/e2e/_probe/staging-flows.spec.ts` (env-gated) covers full sign-in lands on `/console` with credit balance + workspace banner.
- Phase 11 `AUTH-01` / `AUTH-02` evidence files already cover the underlying Supabase auth migrations + middleware session gate.

## Command

```bash
npm run test:unit                  # 9 files, 45 tests, exit 0
CI=true npx playwright test \
  tests/e2e/unauth.spec.ts          # 5 tests pass
```
