---
created: 2026-04-22T01:43:51.823Z
title: Design RBAC authorization model
area: planning
files:
  - .planning/PROJECT.md
  - .planning/v1.1-DEFERRED-SCOPE.md
  - apps/control-plane/internal/accounts/types.go
  - apps/control-plane/internal/accounts/service.go
  - apps/web-console/lib/viewer-gates.ts
---

## Problem

Hive does not have a generalized RBAC layer today. The current authorization model is a narrow combination of workspace membership roles (`owner` / `member`) and a small set of derived booleans (`CanInviteMembers`, `CanManageAPIKeys`). That makes new permission cases turn into one-off checks instead of following a shared policy model, and it does not describe guest or unverified-user scopes cleanly.

The gap is already visible in the code:
- `apps/control-plane/internal/accounts/types.go` defines only `owner` / `member` membership roles and two gate booleans.
- `apps/control-plane/internal/accounts/service.go` derives those booleans directly from `viewer.EmailVerified && chosen.Role == "owner"`.
- `apps/web-console/lib/viewer-gates.ts` mirrors the same ad hoc gate shape in the console.

## Solution

Design a verification-aware RBAC model for the next milestone before more permission rules land. The design should:

- define the supported roles and scopes explicitly, including guest, unverified, member, and owner states
- map sensitive capabilities such as billing, API-key management, analytics, member administration, and workspace settings to named permissions
- keep server enforcement authoritative in control-plane handlers
- keep web-console route and navigation gating derived from the same permission model rather than hard-coded page checks
- include regression coverage for both verified and unverified flows
