# Plans Index

This folder is organized by plan status to reduce ambiguity and keep active work discoverable.

## Canonical Placement

- Create new tracked plans in `docs/plans/YYYY-MM-DD-<task-name>.md`.
- Continue an existing task in its current plan file instead of creating an untracked side artifact.
- Move long-lived current-track plans into `docs/plans/active/` when they become the canonical implementation guide for that stream.
- Move completed dated plans out of the root into `docs/plans/completed/` once they are no longer the current execution surface.

## In Flight

The root `docs/plans/` folder should stay small and contain only currently active session plans.

- `docs/plans/2026-03-12-bootstrap-and-smoke-doc-fixes.md`
- `docs/plans/2026-03-13-exhaustive-repo-audit.md`

## Active Track Docs

Use these documents for current planning and execution:

- `docs/plans/active/2026-02-23-supabase-option-a-backend-simplification-design.md`
- `docs/plans/active/2026-02-23-supabase-option-a-backend-simplification-implementation.md`
- `docs/plans/active/2026-02-23-chat-first-frontend-information-architecture-design.md`
- `docs/plans/active/2026-02-23-chat-first-frontend-information-architecture-implementation.md`
- `docs/plans/active/future-implementation-roadmap.md`
- `docs/plans/active/2026-02-24-chat-first-guarded-home-implementation.md`
- `docs/plans/active/2026-02-24-public-beta-mvp-gap-and-oss-organization-design.md`

## Completed

Completed dated plans live under `docs/plans/completed/`.

Examples:

- `docs/plans/completed/2026-02-24-ci-quality-and-pr-cleanup-implementation.md`
- `docs/plans/completed/2026-03-13-unified-local-stack.md`

## Archive (Obsolete)

These plans are retained for history only and are obsolete for the current architecture direction:

- `docs/plans/archive/pre-supabase/`

If an archived plan conflicts with an active plan, always follow the active plan.
