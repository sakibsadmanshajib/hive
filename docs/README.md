# Documentation Index

This folder is the canonical documentation hub for Hive's product direction, architecture, implementation plans, and operations.

## Start Here

- Local bootstrap and daily development: `../README.md`
- Architecture: `architecture/system-architecture.md`
- Product direction: `design/active/product-and-routing.md`
- Contributing: `../CONTRIBUTING.md`
- Code of Conduct: `../CODE_OF_CONDUCT.md`
- Security policy: `../SECURITY.md`
- Support: `../SUPPORT.md`
- Governance: `../GOVERNANCE.md`
- Changelog: `../CHANGELOG.md`

## Active Work

- Plans overview: `plans/README.md`
- Active roadmap: `plans/active/future-implementation-roadmap.md`
- Public beta MVP gap analysis: `plans/active/2026-02-24-public-beta-mvp-gap-and-oss-organization-design.md`
- Current in-flight plans: see `plans/README.md` → In Flight

## Design

- Product and design decisions: `design/README.md`
- Product direction and routing: `design/active/product-and-routing.md`
- Chat history persistence design (in-flight): `design/active/2026-03-15-persist-chat-history-across-guest-and-user-design.md`

## Operations

- Canonical local bootstrap and stack startup: `../README.md`
- Runbooks index: `runbooks/README.md`
- Maintainer issue lifecycle runbook: `runbooks/active/issue-lifecycle.md`
- Provider circuit-breaker and startup readiness runbook: `runbooks/active/provider-circuit-breaker.md`
- Payments reconciliation runbook: `runbooks/active/payments-reconciliation.md`
- API key lifecycle runbook: `runbooks/active/api-key-lifecycle.md`
- GitHub triage runbook: `runbooks/active/github-triage.md`
- Web e2e smoke runbook: `runbooks/active/web-e2e-smoke.md`
- CI quality workflow: `.github/workflows/ci.yml` (troubleshooting: `runbooks/active/ci-and-pr-cleanup-operations.md`)
- PR cleanup workflow: `.github/workflows/pr-cleanup.yml` + `.github/scripts/pr-cleanup.sh` (troubleshooting: `runbooks/active/ci-and-pr-cleanup-operations.md`)
- Release docs index: `release/README.md`

## Engineering Standards

- Git and AI practices: `engineering/git-and-ai-practices.md`

## Reference / Historical

- Python MVP migration map (historical): `architecture/archive/2026-02-28-python-mvp-migration-map.md`
- Repo audit outputs: `audits/2026-02-28-redundancy-inventory.md`, `audits/2026-02-28-final-audit-report.md`
- Platform audit: `audits/2026-03-13-platform-audit.md`
- Completed plans: `plans/completed/`
- Archived designs: `design/archive/`
- Pre-Supabase plans: `plans/archive/pre-supabase/`

## Docs Structure Conventions

- **In-flight session plans**: `plans/YYYY-MM-DD-<task-name>.md` (root). Keep to a small handful at any time.
- **Long-lived planning tracks**: `plans/active/` — canonical guides for ongoing streams of work.
- **Completed plans**: `plans/completed/` — no longer the active execution surface.
- **Archived/obsolete plans**: `plans/archive/pre-supabase/` — historical reference only.
- **Active product/UX designs**: `design/active/` — link from `design/README.md`.
- **Completed/superseded designs**: `design/archive/` — kept for historical reference.
- **Runbooks**: `runbooks/active/` for current, `runbooks/archive/` for obsolete.
- **Architecture**: `architecture/` for current ground truth, `architecture/archive/` for historical snapshots.

## Conventions

- Add new architecture/design docs in separate files instead of appending huge sections to one file.
- Keep decision logs explicit: what changed, why, and migration impact.
- For major new feature tracks, add a separate roadmap doc under `plans/`.
- Treat historical audits and archived plans as context, not as the current product truth when newer active docs disagree.
