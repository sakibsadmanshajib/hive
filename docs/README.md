# Documentation Index

This folder is the canonical documentation hub for Hive's product direction, architecture, implementation plans, and operations.

## Start Here

- Local quickstart and daily development: `../README.md`
- Architecture: `docs/architecture/system-architecture.md`
- Product direction: `docs/design/active/product-and-routing.md`
- Python MVP migration map: `docs/architecture/2026-02-28-python-mvp-migration-map.md`
- Contributing: `../CONTRIBUTING.md`
- Code of Conduct: `../CODE_OF_CONDUCT.md`
- Security policy: `../SECURITY.md`
- Support: `../SUPPORT.md`
- Governance: `../GOVERNANCE.md`
- Product and design decisions: `docs/design/README.md`
- Changelog: `../CHANGELOG.md`
- Plans overview: `docs/plans/README.md`
- Active roadmap: `docs/plans/active/future-implementation-roadmap.md`
- Current architecture migration design: `docs/plans/active/2026-02-23-supabase-option-a-backend-simplification-design.md`
- Current architecture migration implementation plan: `docs/plans/active/2026-02-23-supabase-option-a-backend-simplification-implementation.md`
- Current chat-first frontend IA design: `docs/plans/active/2026-02-23-chat-first-frontend-information-architecture-design.md`
- Current chat-first frontend IA implementation plan: `docs/plans/active/2026-02-23-chat-first-frontend-information-architecture-implementation.md`
- Current web flow audit: `docs/design/active/2026-02-24-web-flow-critical-review.md`
- Current chat-first guarded-home design: `docs/design/active/2026-02-24-chat-first-guarded-home.md`
- Current chat-first guarded-home implementation plan: `docs/plans/active/2026-02-24-chat-first-guarded-home-implementation.md`
- Current repo audit design: `docs/design/active/2026-02-28-repo-audit-cleanup-design.md`
- Current repo audit decision process: `docs/design/active/2026-02-28-repo-audit-cleanup-decision-process.md`
- Current repo audit execution plan: `docs/plans/2026-02-28-repo-audit-cleanup-plan.md`
- Current PR #36 unresolved-comments implementation plan: `docs/plans/2026-03-11-pr36-remaining-unresolved-comments.md`
- Repo audit outputs: `docs/audits/2026-02-28-redundancy-inventory.md`, `docs/audits/2026-02-28-final-audit-report.md`
- Current platform audit: `docs/audits/2026-03-13-platform-audit.md`

## Planning Docs Status

- `docs/plans/active/` contains current plans that should guide implementation.
- New session plans and tracked implementation plans belong in `docs/plans/`.
- `docs/plans/archive/pre-supabase/` contains pre-Supabase plans kept only for historical reference and should be treated as obsolete for current direction.
- Active design artifacts may live under `docs/design/active/` when they capture UX or product decisions that are not implementation plans.

## Operations

- Canonical local stack startup: `../README.md`
- Runbooks index: `docs/runbooks/README.md`
- Maintainer issue lifecycle runbook: `docs/runbooks/active/issue-lifecycle.md`
- Provider circuit-breaker and startup readiness runbook: `docs/runbooks/active/provider-circuit-breaker.md`
- Payments reconciliation runbook: `docs/runbooks/active/payments-reconciliation.md`
- API key lifecycle runbook: `docs/runbooks/active/api-key-lifecycle.md`
- GitHub triage runbook: `docs/runbooks/active/github-triage.md`
- Web e2e smoke runbook: `docs/runbooks/active/web-e2e-smoke.md`
- CI quality workflow: `.github/workflows/ci.yml` (troubleshooting: `docs/runbooks/active/ci-and-pr-cleanup-operations.md`)
- PR cleanup workflow: `.github/workflows/pr-cleanup.yml` + `.github/scripts/pr-cleanup.sh` (troubleshooting: `docs/runbooks/active/ci-and-pr-cleanup-operations.md`)
- Release docs index: `docs/release/README.md`

## Engineering Standards

- Git and AI practices: `docs/engineering/git-and-ai-practices.md`

## Conventions

- Add new architecture/design docs in separate files instead of appending huge sections to one file.
- Keep decision logs explicit: what changed, why, and migration impact.
- For major new feature tracks, add a separate roadmap doc under `docs/plans/`.
- Treat historical audits and archived plans as context, not as the current product truth when newer active docs disagree.
