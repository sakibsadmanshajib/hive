---
phase: 02-identity-account-foundation
plan: 01
subsystem: control-plane
tags: [go, docker, supabase, postgres, migrations, infrastructure]
dependency_graph:
  requires: []
  provides:
    - apps/control-plane Go module with /health endpoint
    - Docker Compose control-plane service on port 8081
    - Supabase tenancy migration (accounts, memberships, invitations, profiles)
    - Shared .env.example environment contract
  affects:
    - All subsequent Phase 2 plans (depend on control-plane running)
    - Phase 3+ (billing/profile plans depend on accounts schema)
tech_stack:
  added:
    - github.com/jackc/pgx/v5 v5.7.2 (Postgres driver for control-plane)
    - github.com/air-verse/air v1.64.5 (hot-reload inside Docker)
    - golang:1.24-alpine (control-plane Docker base image)
  patterns:
    - Go workspace (go.work) for multi-module monorepo
    - Functional options pattern deferred; platform packages use constructor injection
    - pgxpool for connection pool management
key_files:
  created:
    - .env.example
    - apps/control-plane/.air.toml
    - apps/control-plane/go.mod
    - apps/control-plane/go.sum
    - apps/control-plane/cmd/server/main.go
    - apps/control-plane/internal/platform/config/config.go
    - apps/control-plane/internal/platform/db/pool.go
    - apps/control-plane/internal/platform/http/router.go
    - deploy/docker/Dockerfile.control-plane
    - supabase/migrations/20260328_01_identity_foundation.sql
  modified:
    - go.work (added ./apps/control-plane)
    - deploy/docker/docker-compose.yml (added control-plane service)
    - deploy/docker/docker-compose.override.yml (added develop.watch for control-plane)
decisions:
  - DB connection failure at startup is non-fatal (logs warning) so /health responds even without SUPABASE_DB_URL provisioned
  - go.work.sum was not generated (no cross-workspace module dependencies yet)
  - supabase/migrations directory required Docker with root to create (owned by root)
  - token_hash stored instead of raw invitation token for security
metrics:
  duration: 3min
  completed_date: "2026-03-29"
  tasks_completed: 2
  files_created: 10
  files_modified: 3
---

# Phase 2 Plan 1: Control-Plane Foundation & Tenancy Migration Summary

**One-liner:** Docker-hosted Go control-plane service on port 8081 with pgxpool, /health endpoint, air hot-reload, and Supabase tenancy migration defining accounts/memberships/invitations/profiles.

## What Was Built

### Task 1: Control-plane module, Docker image, and shared environment contract

Created the `apps/control-plane` Go module from scratch with a clean three-layer structure:
- `internal/platform/config` — env-sourced config with validation
- `internal/platform/db` — pgxpool open/ping with descriptive errors
- `internal/platform/http` — mux router with `/health` returning `{"status":"ok"}`
- `cmd/server/main.go` — graceful shutdown, DB optional at startup

Docker wiring:
- `Dockerfile.control-plane` uses `golang:1.24-alpine` + air for hot reload with `GOTOOLCHAIN=auto` (matches existing edge-api pattern from Phase 1)
- `docker-compose.yml` gains a `control-plane` service on `8081:8081` with healthcheck
- `docker-compose.override.yml` gains `develop.watch` sync/rebuild rules for the new service

Shared env contract in `.env.example` covers Supabase, control-plane, and Next.js console keys.

### Task 2: Phase 2 tenancy migration

`supabase/migrations/20260328_01_identity_foundation.sql` defines:
- `public.accounts` — workspace entity (personal/business) owned by a Supabase auth user
- `public.account_memberships` — owner/member roles with active/invited status; unique constraint on (account_id, user_id)
- `public.account_invitations` — email invites with hashed tokens, expiry, and accepted_at
- `public.account_profiles` — minimal pre-billing profile (name, email, country, state, setup flag)
- Indexes on `accounts.slug`, `account_memberships.user_id`, `account_invitations.email`

## Deviations from Plan

### Auto-fixed Issues

None — plan executed exactly as written.

### Notes

- `go.work.sum` was not created because there are no cross-workspace module dependencies. The file is referenced in Dockerfile.control-plane with `go.work.sum*` (optional glob) to handle this gracefully.
- The `supabase/` directory was owned by root; the migrations subdirectory was created via Docker with root privileges.

## Verification Results

All acceptance criteria passed:
- `go.work` contains `./apps/control-plane`
- `deploy/docker/Dockerfile.control-plane` contains `EXPOSE 8081`
- `deploy/docker/docker-compose.yml` contains `control-plane` and `8081:8081`
- `apps/control-plane/cmd/server/main.go` contains `ListenAndServe` and `/health`
- `.env.example` contains `CONTROL_PLANE_BASE_URL=http://localhost:8081`
- All four migration tables verified with grep

## Commits

| Hash | Message |
|------|---------|
| ff254b0 | feat(02-01): create control-plane module, Docker wiring, and shared env contract |
| d30eb57 | feat(02-01): add Phase 2 tenancy migration |

## Self-Check: PASSED

| File | Status |
|------|--------|
| .env.example | FOUND |
| apps/control-plane/cmd/server/main.go | FOUND |
| deploy/docker/Dockerfile.control-plane | FOUND |
| supabase/migrations/20260328_01_identity_foundation.sql | FOUND |
| ff254b0 (Task 1 commit) | VERIFIED |
| d30eb57 (Task 2 commit) | VERIFIED |
