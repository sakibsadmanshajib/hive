# GitHub Triage Runbook

## Purpose

This runbook defines how maintainers use the repository's GitHub issue forms, pull request template, labels, milestones, and metadata sync workflow.

## Source of Truth

- Issue forms live under `.github/ISSUE_TEMPLATE/`.
- The pull request checklist lives at `.github/pull_request_template.md`.
- Managed labels live in `tools/github/labels.json`.
- Managed milestones live in `tools/github/milestones.json`.
- The sync command is `tools/github/sync-github-meta.sh`.

Version-controlled files are the source of truth. Avoid editing managed label names, colors, descriptions, or managed milestone descriptions directly in the GitHub UI unless you immediately mirror the change back into the repo.

## Contributor Intake

### Issue forms

- `Bug report` is for reproducible defects.
- `Feature request` is for scoped improvements with acceptance criteria.
- Blank issues are disabled.
- Security-sensitive reports should be redirected to `SECURITY.md`.
- Support questions should be redirected to `SUPPORT.md`.

### Pull request template

Reviewers should expect every PR to include:

- issue linkage or explicit scope statement
- a scoped PR title, preferably Conventional-Commit-style where practical
- exact verification commands and outcomes
- docs/changelog status
- risk review for sensitive areas
- supporting evidence when useful

## Issue Lifecycle Summary

See the canonical Issue Lifecycle runbook at `docs/runbooks/active/issue-lifecycle.md` for the full state machine and transition criteria.

Use this runbook for metadata rules. Use `docs/runbooks/active/issue-lifecycle.md` for the operational workflow, transition criteria, planning expectations, PR linkage, and closeout guidance.

## Label Taxonomy

Use labels deliberately. The default triage set is:

- Area labels: `area:*`
- Work type labels: `kind:*`
- Priority labels: `priority:P0` through `priority:P3`
- Risk labels: `risk:*`
- Workflow labels: `status:*`

Suggested minimum triage for most new issues:

1. One `area:*` label
2. One `kind:*` label when the issue intent is clear
3. One `priority:*` label once importance is assessed
4. `status:needs-triage` until the issue is refined

Move workflow labels as the issue advances:

- `status:needs-triage` -> newly filed / awaiting maintainer review
- `status:ready` -> ready for implementation
- `status:in-progress` -> actively being worked
- `status:blocked` -> waiting on dependency or decision

## Milestone Operating Model

Default milestone routing should follow priority unless maintainers intentionally override it:

- `priority:P0` -> `MVP Public Beta - Blockers`
- `priority:P1` -> `MVP Public Beta - Stabilization`
- `priority:P2` or `priority:P3` -> `Post-MVP - Enhancements`

Milestones indicate planning horizon, not ownership. Labels still carry the main operational meaning.

## Metadata Sync Workflow

Run the sync when:

- changing managed label or milestone definitions
- bootstrapping the repo in a new GitHub destination
- correcting drift between GitHub UI state and repo metadata

Command:

```bash
tools/github/sync-github-meta.sh
```

What it does:

- creates missing managed labels and milestones
- updates managed label color/description drift
- updates managed milestone description/state drift
- leaves unmanaged labels and milestones untouched

## Verification

After metadata changes:

```bash
tools/github/sync-github-meta.sh
# `gh api` resolves {owner}/{repo} from the repo remote. Example explicit form: repos/sakibsadmanshajib/hive/labels
gh api repos/{owner}/{repo}/labels --paginate --jq '.[] | {name,color,description}'
gh api repos/{owner}/{repo}/milestones --jq '.[] | {title,description,state}'
```

After template or runbook changes, also run the repository sanity builds required by policy (Docker only; stack must be up):

```bash
docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"
docker compose exec web sh -c "cd /app && pnpm --filter @hive/web build"
```

## Maintenance Notes

- Keep the declarative metadata files small and intentional; do not add labels without an operating need.
- If GitHub API calls fail transiently, retry the sync before changing the script.
- If the operating model changes, update `CONTRIBUTING.md`, `README.md`, `docs/README.md`, and `CHANGELOG.md` in the same change.
