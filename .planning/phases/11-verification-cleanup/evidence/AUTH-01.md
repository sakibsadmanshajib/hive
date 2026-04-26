---
requirement_id: AUTH-01
status: satisfied
verified_at: 2026-04-25
verified_by: gsd-execute-phase agent (Phase 11 Task 1)
phase_satisfied: 02-identity-account-foundation
evidence:
  code_paths:
    - apps/web-console/app/auth/sign-up/
    - apps/web-console/app/auth/sign-in/
    - apps/web-console/app/auth/callback/
    - apps/web-console/middleware.ts
    - apps/control-plane/internal/accounts/
    - supabase/migrations/20260328_01_identity_foundation.sql
    - supabase/migrations/20260328_02_billing_identity_profiles.sql
  integration_tests:
    - cd deploy/docker && docker compose --env-file ../../.env --profile test up --build
    - cd apps/web-console && npx playwright test tests/e2e/auth
  summary_refs:
    - .planning/phases/02-identity-account-foundation/02-01-SUMMARY.md
    - .planning/phases/02-identity-account-foundation/02-03-SUMMARY.md
    - .planning/phases/02-identity-account-foundation/02-UAT.md
---

# AUTH-01 Evidence — signup / signin via Supabase

## Behavior

A developer can sign up + sign in to the Hive web console using Supabase auth.
On first signin the control-plane provisions an account row + default
membership. The Next.js middleware enforces the session gate on every console
route.

## Code paths

- `apps/web-console/app/auth/sign-up/` — Next.js signup route + server action
  posting to Supabase auth.
- `apps/web-console/app/auth/sign-in/` — signin route.
- `apps/web-console/app/auth/callback/` — OAuth/email confirmation callback that
  hands the session back to the console.
- `apps/web-console/middleware.ts` — session gate; unauthenticated requests
  redirect to `/auth/sign-in`.
- `apps/control-plane/internal/accounts/` — account + membership provisioning
  (`service.go`, `repository.go`, `http.go`) that runs on first authenticated
  request.
- `supabase/migrations/20260328_01_identity_foundation.sql` — base auth schema
  (accounts, memberships, identity links).
- `supabase/migrations/20260328_02_billing_identity_profiles.sql` — billing
  identity profile rows seeded alongside the auth account.

## Reproduce

```bash
# 1. Bring the local stack up
cd deploy/docker && docker compose --env-file ../../.env --profile local up --build

# 2. Visit the web console
open http://localhost:3000/auth/sign-up

# 3. Or run the SDK + console integration suite
cd deploy/docker && docker compose --env-file ../../.env --profile test up --build
```

For e2e:
```bash
cd apps/web-console && npx playwright test tests/e2e/auth
```

## Phase 02 summary references

- `02-01-SUMMARY.md` — Control-plane foundation + tenancy migration (account /
  membership tables + service layer).
- `02-03-SUMMARY.md` — Web console foundation (auth routes + middleware
  bootstrap).
- `02-UAT.md` — Phase 02 UAT confirming signup + signin flows live.

## Known Caveats

None for AUTH-01 itself. AUTH-03 (session-persists-across-refresh) and AUTH-04
(billing contact / legal entity / country / VAT profile) are tracked
separately and remain Pending in `.planning/REQUIREMENTS.md`.
