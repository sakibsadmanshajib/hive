---
phase: 02-identity-account-foundation
plan: "05"
subsystem: api
tags:
  - control-plane
  - profiles
  - postgres
  - tenancy
  - onboarding
dependency_graph:
  requires:
    - 02-02
    - 02-04
  provides:
    - current-account core profile API
    - durable profile setup completion calculation
    - profile reads and writes scoped to current workspace
  affects:
    - apps/control-plane
    - apps/web-console
tech_stack:
  added: []
  patterns:
    - Current-account profile service updates public.accounts and public.account_profiles together
    - Profiles handler resolves the active workspace through accounts service before profile reads and writes
key_files:
  created:
    - apps/control-plane/internal/profiles/types.go
    - apps/control-plane/internal/profiles/repository.go
    - apps/control-plane/internal/profiles/service.go
    - apps/control-plane/internal/profiles/http.go
  modified:
    - apps/control-plane/internal/platform/http/router.go
    - apps/control-plane/internal/profiles/service_test.go (RED commit)
    - apps/control-plane/internal/profiles/http_test.go (RED commit)
decisions:
  - "Core profile completion remains limited to owner name, login email, display name, account type, country, and state/province."
  - "Profile writes update public.accounts display_name/account_type alongside public.account_profiles so the viewer contract stays consistent after edits."
  - "The profiles handler derives the current account from the authenticated viewer context instead of trusting a raw request body account identifier."
metrics:
  duration: "7min"
  completed_date: "2026-03-29"
  tasks_completed: 1
  files_created: 4
requirements:
  - AUTH-04
---

# Phase 02 Plan 05: Current-Account Core Profile API Summary

**Current-account profile API that persists minimal pre-billing identity data and computes onboarding completion from the six allowed core fields**

## What Was Built

### Task 1: Implement the current-account core profile API

- **`apps/control-plane/internal/profiles/types.go`** — Defines the `AccountProfile` response DTO, `UpdateAccountProfileInput`, validation error type, and not-found sentinel.
- **`apps/control-plane/internal/profiles/repository.go`** — Loads the current-account profile by joining `public.account_profiles` with `public.accounts`, and updates both tables transactionally.
- **`apps/control-plane/internal/profiles/service.go`** — Validates required core fields, restricts `account_type` to `personal` or `business`, and computes `profile_setup_complete` only from the six allowed pre-billing fields.
- **`apps/control-plane/internal/profiles/http.go`** — Serves `GET` and `PUT /api/v1/accounts/current/profile`, resolving the active account through the authenticated viewer context.
- **`apps/control-plane/internal/platform/http/router.go`** — Registers the protected current-account profile route under the existing auth middleware.
- **Tests** — The committed RED test pair and the GREEN implementation verify durable core profile persistence, completion-state transition, and invalid `account_type` rejection.

## Task Commits

1. **Task 1 RED: add failing tests for the current-account profile API** — `94f4017` (`test`)
2. **Task 1 GREEN: implement the current-account core profile API** — `9f5415a` (`feat`)

## Decisions Made

1. **`profile_setup_complete` stays narrow** — only the six allowed core profile fields influence setup completion, so billing and tax data remain out of Phase 2 onboarding.
2. **Account summary fields update in the same repository write** — changing profile display name or account type updates `public.accounts` and `public.account_profiles` together to avoid stale viewer responses.
3. **Current-account resolution stays server-side** — the profile handler uses the existing viewer/account resolution path rather than introducing a client-controlled account identifier in the payload.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Corrected the Docker verification package path**
- **Found during:** Task 1 verification
- **Issue:** The plan's literal Docker test command used `./internal/profiles/...`, but the control-plane container runs from `/app` with `apps/control-plane` as a workspace module.
- **Fix:** Verified with `go test ./apps/control-plane/internal/profiles/... -count=1` inside the rebuilt `control-plane` image.
- **Files modified:** None
- **Verification:** Rebuilt `docker-control-plane` from the current working tree and confirmed the package test passed.
- **Committed in:** None (verification-only correction)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** No product-scope change. The implementation matches the plan; only the verification command needed the workspace-correct package path.

## Issues Encountered

- The initial executor stalled without producing file or git progress, so execution resumed locally from the existing TDD state already present in the worktree.

## Next Phase Readiness

- `02-06` can consume `GET` and `PUT /api/v1/accounts/current/profile` for the setup flow and profile settings screen.
- The backend now exposes `profile_setup_complete`, which the web console can use to show a reminder card without forcing a redirect loop.

## Self-Check

- [x] `apps/control-plane/internal/profiles/http.go` contains `/api/v1/accounts/current/profile`
- [x] `apps/control-plane/internal/profiles/service.go` contains `profile_setup_complete`
- [x] `apps/control-plane/internal/profiles/service_test.go` contains `TestUpdateAccountProfile`
- [x] Docker verification passed with `docker compose -f deploy/docker/docker-compose.yml run --build --rm control-plane go test ./apps/control-plane/internal/profiles/... -count=1`
- [x] RED commit recorded: `94f4017`
- [x] GREEN commit recorded: `9f5415a`
