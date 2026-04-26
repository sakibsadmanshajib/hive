---
requirement_id: AUTH-02
status: satisfied
verified_at: 2026-04-25
verified_by: gsd-execute-phase agent (Phase 11 Task 1)
phase_satisfied: 02-identity-account-foundation
evidence:
  code_paths:
    - apps/web-console/app/auth/forgot-password/
    - apps/web-console/app/auth/reset-password/
    - apps/web-console/app/auth/callback/
    - apps/web-console/middleware.ts
    - supabase/migrations/20260328_01_identity_foundation.sql
  integration_tests:
    - cd deploy/docker && docker compose --env-file ../../.env --profile test up --build
    - cd apps/web-console && npx playwright test tests/e2e/auth
  summary_refs:
    - .planning/phases/02-identity-account-foundation/02-03-SUMMARY.md
    - .planning/phases/02-identity-account-foundation/02-UAT.md
---

# AUTH-02 Evidence — email verification + password reset

## Behavior

The Hive web console exposes the email-verification callback and a
forgot-password / reset-password flow. Both rely on Supabase auth's email
templates + token issuance. The callback route handles the verification token
and the reset-password route accepts the recovery token, sets a new password,
and re-establishes the session via the same middleware gate as AUTH-01.

## Code paths

- `apps/web-console/app/auth/forgot-password/` — request a recovery email.
- `apps/web-console/app/auth/reset-password/` — accept Supabase recovery token
  + write the new password.
- `apps/web-console/app/auth/callback/` — confirms the email-verification link
  + recovery link; hands the established session back to the console.
- `apps/web-console/middleware.ts` — session gate retains coverage post-reset.
- `supabase/migrations/20260328_01_identity_foundation.sql` — auth schema
  hosting the recovery + verification token surface (delegated to Supabase
  managed auth).

## Reproduce

```bash
# 1. Bring the local stack up
cd deploy/docker && docker compose --env-file ../../.env --profile local up --build

# 2. Trigger forgot-password
open http://localhost:3000/auth/forgot-password
# (configure SMTP via Supabase project settings before)

# 3. Or run the SDK + console integration suite
cd deploy/docker && docker compose --env-file ../../.env --profile test up --build
```

For e2e:
```bash
cd apps/web-console && npx playwright test tests/e2e/auth
```

## Phase 02 summary references

- `02-03-SUMMARY.md` — Web console foundation including the auth route family.
- `02-UAT.md` — Phase 02 UAT confirming password-reset round-trip.

## Known Caveats

Email delivery itself depends on the Supabase project SMTP configuration; the
Hive code only emits the `signInWithOtp` / `resetPasswordForEmail` calls. SMTP
config is operational, not in-scope for AUTH-02 satisfaction.
