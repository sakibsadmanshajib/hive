---
phase: 02-identity-account-foundation
plan: "02"
subsystem: identity-accounts
tags:
  - auth
  - accounts
  - invitations
  - workspace-bootstrap
dependency_graph:
  requires:
    - 02-01 (control-plane scaffold, DB schema)
  provides:
    - auth.Viewer type and Supabase LookupUser client
    - Bearer auth middleware for /api/v1/* routes
    - Workspace bootstrap on first login
    - Viewer context API with capability gates
    - Invitation create and accept flows
    - Explicit current-account selection via X-Hive-Account-ID
  affects:
    - 02-03 (console session — depends on viewer API)
    - 02-04 and later (API key / billing phases depend on gates)
tech_stack:
  added:
    - github.com/google/uuid v1.6.0
  patterns:
    - Repository interface with pgxRepository production impl and stubRepo for tests
    - Service layer owns all business logic; HTTP handlers are thin
    - GateError typed error for policy enforcement
    - SHA-256 token hashing (token_hash stored, not raw token)
    - context.Value viewer propagation through request context
key_files:
  created:
    - apps/control-plane/internal/auth/types.go
    - apps/control-plane/internal/auth/client.go
    - apps/control-plane/internal/auth/middleware.go
    - apps/control-plane/internal/accounts/types.go
    - apps/control-plane/internal/accounts/repository.go
    - apps/control-plane/internal/accounts/service.go
    - apps/control-plane/internal/accounts/http.go
    - apps/control-plane/internal/accounts/service_test.go
    - apps/control-plane/internal/accounts/http_test.go
  modified:
    - apps/control-plane/cmd/server/main.go
    - apps/control-plane/internal/platform/config/config.go
    - apps/control-plane/internal/platform/http/router.go
    - apps/control-plane/go.mod
    - apps/control-plane/go.sum
    - deploy/docker/Dockerfile.control-plane
decisions:
  - HashToken (SHA-256 hex) used consistently in service and tests — raw token returned once at invitation creation, never stored
  - X-Hive-Account-ID fallback is silent — invalid or unauthorized account IDs fall back to default membership without error
  - AcceptInvitation does not alter current-account selection on the same request — switching is an explicit user action
  - EnsureViewerContext is idempotent — subsequent calls for an existing viewer reuse existing memberships
  - GateError is a typed error exported for errors.As checks — enables deterministic code in HTTP responses
metrics:
  duration: 8min
  completed_date: "2026-03-29"
  tasks_completed: 2
  files_changed: 15
---

# Phase 02 Plan 02: Identity APIs — Viewer Bootstrap and Invitation Flows Summary

**One-liner:** Supabase-backed identity APIs with first-login workspace provisioning, verification-aware capability gates, explicit account selection via header, and invitation create/accept flows using SHA-256 token hashing.

## What Was Built

The hosted Supabase-backed identity layer for the Hive control plane:

1. **Auth layer** (`internal/auth/`)
   - `Viewer` struct carrying `UserID`, `Email`, `EmailVerified`, `FullName`
   - `Client.LookupUser` calls `GET ${SUPABASE_URL}/auth/v1/user` forwarding the caller bearer token
   - `Middleware.Require` wraps handlers, returns 401 JSON on missing/invalid tokens, stores `Viewer` in request context

2. **Accounts service** (`internal/accounts/service.go`)
   - `EnsureViewerContext` provisions a default personal workspace + owner membership + profile on first login (no existing memberships)
   - Workspace display name seeds from `FullName` or email local part
   - `X-Hive-Account-ID` selects current account explicitly; falls back silently on invalid/unauthorized values
   - `Gates.CanInviteMembers` and `Gates.CanManageAPIKeys` are both true only for verified owners
   - `CreateInvitation` enforces `email_verification_required` gate, generates 72h expiry token, stores SHA-256 hash
   - `AcceptInvitation` validates email match (case-insensitive), creates active `member` membership, returns joined account ID without altering current account

3. **HTTP handlers** (`internal/accounts/http.go`)
   - `GET /api/v1/viewer` — viewer context with user, current_account, memberships, gates
   - `GET /api/v1/accounts/current/members` — member list for current account
   - `POST /api/v1/accounts/current/invitations` — invitation creation with 403 + code on gate violation
   - `POST /api/v1/invitations/accept` — accepts invitation, returns joined account_id

4. **Repository** (`internal/accounts/repository.go`)
   - `Repository` interface with 9 methods
   - `pgxRepository` production implementation using pgx/v5
   - `stubRepo` in tests enables fully in-memory testing without a live DB

5. **Router and main.go** updated to wire auth middleware and accounts handler

## Test Coverage

16 tests covering:
- First-login workspace bootstrap creates account + owner membership + profile
- Display name falls back from full name to email local part
- Unverified user gates are false; verified owner gates are true
- Unverified owner invitation blocked with `email_verification_required`
- Verified owner can create invitation with token in response
- Explicit account selection via `X-Hive-Account-ID`
- Fallback when requested account is invalid or unauthorized
- Invitation acceptance creates active member membership, returns account_id
- Email mismatch on accept is rejected

All 16 tests pass: `ok github.com/hivegpt/hive/apps/control-plane/internal/accounts 0.006s`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Dockerfile.control-plane missing edge-api go.mod copy**
- **Found during:** Build step
- **Issue:** `go.work` references `./apps/edge-api` but Dockerfile only copied `apps/control-plane/go.mod` — workspace resolution failed during `go mod download`
- **Fix:** Added `COPY apps/edge-api/go.mod apps/edge-api/go.sum* ./apps/edge-api/` to Dockerfile
- **Files modified:** `deploy/docker/Dockerfile.control-plane`
- **Commit:** 43febdb

**2. [Rule 3 - Blocking] google/uuid not in go.sum**
- **Found during:** First Docker build
- **Issue:** `go.mod` required `github.com/google/uuid v1.6.0` but `go.sum` had no entry for it
- **Fix:** Ran `go mod tidy` in a one-off golang:1.24-alpine container to update `go.sum`
- **Files modified:** `apps/control-plane/go.mod`, `apps/control-plane/go.sum`
- **Commit:** 43febdb

**3. [Rule 3 - Blocking] Test run path mismatch with go.work root**
- **Found during:** First test run
- **Issue:** Plan's verify command used `./internal/accounts/...` relative to go.work root `/app` — pattern didn't match
- **Fix:** Run tests as `cd /app/apps/control-plane && go test ./internal/accounts/...`
- **Note:** Plan's automated verify path needs updating for future runs

## Self-Check: PASSED
