---
phase: 02-identity-account-foundation
plan: "07"
subsystem: identity-billing-profiles
tags:
  - billing-profile
  - control-plane
  - web-console
  - typescript
  - onboarding
dependency_graph:
  requires:
    - 02-05
    - 02-06
  provides:
    - durable optional billing-profile storage
    - current-account billing profile API
    - optional billing settings UI with unverified-user redirect
  affects:
    - apps/control-plane
    - apps/web-console
    - supabase
tech_stack:
  added: []
  patterns:
    - Billing profile reads fall back to core profile values until a dedicated billing row exists
    - Billing settings stay off the dashboard reminder path and out of the setup gate
    - Web-console control-plane responses use explicit JSON decoders instead of type assertions
key_files:
  created:
    - supabase/migrations/20260328_02_billing_identity_profiles.sql
    - apps/web-console/app/console/settings/billing/page.tsx
    - apps/web-console/components/profile/billing-contact-form.tsx
    - apps/web-console/components/profile/business-tax-form.tsx
  modified:
    - apps/control-plane/internal/platform/http/router.go
    - apps/control-plane/internal/profiles/types.go
    - apps/control-plane/internal/profiles/repository.go
    - apps/control-plane/internal/profiles/service.go
    - apps/control-plane/internal/profiles/http.go
    - apps/control-plane/internal/profiles/service_test.go
    - apps/control-plane/internal/profiles/http_test.go
    - apps/web-console/lib/control-plane/client.ts
    - apps/web-console/lib/profile-schemas.ts
    - apps/web-console/tests/e2e/profile-completion.spec.ts
decisions:
  - "Billing-profile storage is durable but optional; reads fall back to existing core-profile contact and location data before the first billing save."
  - "Personal accounts default `legal_entity_type` to `individual`, while business billing settings stay partially saveable without becoming a completion gate."
  - "Unverified users are redirected from `/console/settings/billing` to `/console/settings/profile`, keeping profile maintenance reachable without widening the restricted-console allowlist."
metrics:
  duration: "23min"
  completed_date: "2026-03-29"
  tasks_completed: 2
  files_created: 4
requirements:
  - AUTH-04
---

# Phase 02 Plan 07: Billing Profile Summary

**Durable billing identity storage and optional billing settings that stay outside the Phase 2 setup gate**

## What Was Built

### Task 1: Add billing-profile persistence and API support

- **`supabase/migrations/20260328_02_billing_identity_profiles.sql`** — Adds `public.account_billing_profiles` for billing contact, legal-entity, tax, and billing-location fields.
- **`apps/control-plane/internal/profiles/{types,repository,service,http}.go`** — Adds `BillingProfile`, billing-profile validation, fallback reads, and `GET` / `PUT /api/v1/accounts/current/billing-profile`.
- **`apps/control-plane/internal/platform/http/router.go`** — Registers the new current-account billing-profile route behind the auth middleware.
- **Go coverage** — `service_test.go` and `http_test.go` now cover durable billing persistence, partial business saves, and the personal `legal_entity_type = individual` default.

### Task 2: Add optional billing settings UI and strict TypeScript-safe client parsing

- **`apps/web-console/app/console/settings/billing/page.tsx`** — Server-rendered billing settings screen with the exact helper copy `Optional until checkout or invoicing.` and an unverified-user redirect to `/console/settings/profile`.
- **`apps/web-console/components/profile/{billing-contact-form,business-tax-form}.tsx`** — Billing contact and legal/tax form UI that branches between personal and business account messaging.
- **`apps/web-console/lib/profile-schemas.ts`** — Adds `billingProfileSchema.safeParse()` for optional billing fields, personal defaults, and provided-value validation only.
- **`apps/web-console/lib/control-plane/client.ts`** — Adds typed billing-profile client helpers and replaces assertion-based response handling with explicit decoders.
- **E2E coverage** — `profile-completion.spec.ts` now covers partial business billing saves, the unverified billing redirect, and the absence of a dashboard billing reminder.

## Task Commits

1. **Task 1 RED: add failing billing profile api coverage** — `b06393c` (`test`)
2. **Task 1 GREEN: add billing profile api** — `a60fb21` (`feat`)
3. **Task 2 RED: add billing settings coverage** — `2438cc4` (`test`)
4. **Task 2 GREEN: add optional billing settings** — `0da3606` (`feat`)

## Decisions Made

1. **Billing reads fall back to core profile data** — billing settings can render meaningful defaults before the first billing-specific save.
2. **Billing completeness stays optional** — partial business saves are accepted, and the dashboard setup reminder remains scoped to the core profile only.
3. **Strict TypeScript cleanup stayed in the same frontend surface** — the control-plane client and adjacent tests were moved off assertion-based parsing while this plan was touching them.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Quality] Replaced assertion-based JSON parsing in the web-console control-plane client**
- **Found during:** Task 2 implementation
- **Issue:** The current client used `as ...`, `unknown`, and loosely typed `response.json()` handling, which conflicts with the stricter TypeScript policy.
- **Fix:** Added explicit JSON parsing and field decoders for viewer, core profile, billing profile, and members responses.
- **Files modified:** `apps/web-console/lib/control-plane/client.ts`
- **Verification:** `npx tsc --noEmit` passed in the web-console container after the decoder rewrite.
- **Committed in:** `0da3606`

**2. [Rule 3 - Quality] Fixed adjacent test mocks instead of reintroducing type assertions**
- **Found during:** Task 2 verification
- **Issue:** Existing auth-route and Supabase-helper tests used `as never` cookie/request assertions that broke the stricter policy once TypeScript checking was rerun.
- **Fix:** Replaced those assertions with parameter-typed request and cookie-store mocks.
- **Files modified:** `apps/web-console/__tests__/auth-routes.test.ts`, `apps/web-console/__tests__/supabase-helpers.test.ts`
- **Verification:** `npx tsc --noEmit` passed after updating the mocks.
- **Committed in:** `0da3606`

---

**Total deviations:** 2 auto-fixed (0 blocking)
**Impact on plan:** The user-facing plan stayed the same. The extra work kept the touched web-console surface aligned with the stricter TypeScript policy.

## Issues Encountered

- Containerized verification requires Docker access; all backend, unit, type-check, and Playwright verification ran inside the existing Docker workflow.
- The filtered Playwright billing scenarios executed but skipped all three tests because E2E credentials are not configured in this environment.

## Next Phase Readiness

- Phase 2 is now complete: durable identity, session, profile, invitation, and optional billing-profile hooks are in place.
- Phase 3 can build prepaid credits and usage accounting on top of the completed Supabase-backed account and control-plane foundation.

## Self-Check

- [x] `supabase/migrations/20260328_02_billing_identity_profiles.sql` contains `create table public.account_billing_profiles`
- [x] `apps/control-plane/internal/profiles/http.go` contains `/api/v1/accounts/current/billing-profile`
- [x] `apps/control-plane/internal/profiles/service_test.go` contains `TestPartialBusinessBillingProfile`
- [x] `apps/web-console/app/console/settings/billing/page.tsx` contains `Optional until checkout or invoicing.`
- [x] `apps/web-console/app/console/settings/billing/page.tsx` contains `redirect("/console/settings/profile")`
- [x] `apps/web-console/lib/profile-schemas.ts` contains `billingProfileSchema`
- [x] `docker compose -f deploy/docker/docker-compose.yml run --build --rm control-plane go test ./apps/control-plane/internal/profiles/... -count=1` passed
- [x] `docker compose -f deploy/docker/docker-compose.yml run --build --rm web-console npm run test:unit -- tests/unit/profile-schemas.test.ts` passed
- [x] `docker compose -f deploy/docker/docker-compose.yml run --build --rm web-console npx tsc --noEmit` passed
- [x] `docker compose -f deploy/docker/docker-compose.yml run --build --rm web-console npm run test:e2e -- --grep "(billing settings save partial business profile|unverified billing settings redirect to profile|dashboard does not introduce a billing reminder)"` executed and skipped because E2E credentials are not configured
