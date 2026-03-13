# Documentation Index

This folder is the canonical documentation hub for Hive's product direction, architecture, implementation plans, and operations.

## Start Here

- Local bootstrap and daily development: `../README.md`
- Architecture: `architecture/system-architecture.md`
- Product direction: `design/active/product-and-routing.md`
- Python MVP migration map: `architecture/2026-02-28-python-mvp-migration-map.md`
- Contributing: `../CONTRIBUTING.md`
- Code of Conduct: `../CODE_OF_CONDUCT.md`
- Security policy: `../SECURITY.md`
- Support: `../SUPPORT.md`
- Governance: `../GOVERNANCE.md`
- Product and design decisions: `design/README.md`
- Changelog: `../CHANGELOG.md`
- Plans overview: `plans/README.md`
- Active roadmap: `plans/active/future-implementation-roadmap.md`
- Current in-flight bootstrap/docs workflow plan: `plans/2026-03-12-bootstrap-and-smoke-doc-fixes.md`
- Current exhaustive audit plan: `plans/2026-03-13-exhaustive-repo-audit.md`
- Current architecture migration design: `plans/active/2026-02-23-supabase-option-a-backend-simplification-design.md`
- Current architecture migration implementation plan: `plans/active/2026-02-23-supabase-option-a-backend-simplification-implementation.md`
- Current chat-first frontend IA design: `plans/active/2026-02-23-chat-first-frontend-information-architecture-design.md`
- Current chat-first frontend IA implementation plan: `plans/active/2026-02-23-chat-first-frontend-information-architecture-implementation.md`
- Current web flow audit: `design/active/2026-02-24-web-flow-critical-review.md`
- Current chat-first guarded-home design: `design/active/2026-02-24-chat-first-guarded-home.md`
- Current chat-first guarded-home implementation plan: `plans/active/2026-02-24-chat-first-guarded-home-implementation.md`
- Current repo audit design: `design/active/2026-02-28-repo-audit-cleanup-design.md`
- Current repo audit decision process: `design/active/2026-02-28-repo-audit-cleanup-decision-process.md`
- Repo audit outputs: `audits/2026-02-28-redundancy-inventory.md`, `audits/2026-02-28-final-audit-report.md`
- Current platform audit: `audits/2026-03-13-platform-audit.md`

## Planning Docs Status

- `plans/` root is for in-flight tracked plans only.
- `plans/active/` contains long-lived current plans that should guide implementation.
- `plans/completed/` contains completed dated execution artifacts retained for history.
- `plans/archive/pre-supabase/` contains pre-Supabase plans kept only for historical reference and should be treated as obsolete for current direction.
- Active design artifacts may live under `design/active/` when they capture UX or product decisions that are not implementation plans.

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

## Conventions

- Add new architecture/design docs in separate files instead of appending huge sections to one file.
- Keep decision logs explicit: what changed, why, and migration impact.
- For major new feature tracks, add a separate roadmap doc under `plans/`.
- Treat historical audits and archived plans as context, not as the current product truth when newer active docs disagree.
