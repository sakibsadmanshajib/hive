# Plans Index

This folder is organized by plan status to reduce ambiguity and keep active work discoverable.

## Canonical Placement

- Create new tracked plans in `docs/plans/YYYY-MM-DD-<task-name>.md`.
- Continue an existing task in its current plan file instead of creating an untracked side artifact.
- Move long-lived current-track plans into `docs/plans/active/` when they become the canonical implementation guide for that stream.
- Move completed dated plans out of the root into `docs/plans/completed/` once they are no longer the current execution surface.

## In Flight

The root `docs/plans/` folder should stay small and contain only currently active session plans for work that is being executed right now. At any given moment, there should be only a small handful of dated plan files directly under `docs/plans/` (not in a subfolder); those are the in-flight plans.

Current in-flight plans:

- `docs/plans/2026-03-15-docs-structure-cleanup.md`
- `docs/plans/2026-03-15-persist-chat-history-across-guest-and-user-plan.md`

## Active Track Docs

Long-lived planning tracks that guide ongoing streams of work:

- `docs/plans/active/future-implementation-roadmap.md`
- `docs/plans/active/2026-02-24-public-beta-mvp-gap-and-oss-organization-design.md`

## Completed

Completed dated plans live under `docs/plans/completed/`.

## Archive (Obsolete)

These plans are retained for history only and are obsolete for the current architecture direction:

- `docs/plans/archive/pre-supabase/`

If an archived plan conflicts with an active plan, always follow the active plan.
