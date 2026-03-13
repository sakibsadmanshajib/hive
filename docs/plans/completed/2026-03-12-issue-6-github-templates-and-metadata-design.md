# Issue #6 GitHub Templates and Metadata Design

## Goal

Define a repo-managed GitHub contribution intake system for issue `#6` that adds issue forms, a structured pull request template, and an automated labels/milestones sync workflow with a documented maintainer operating model.

## Scope

- Add GitHub Issue Forms for at least bug reports and feature requests.
- Add a structured pull request template aligned with existing repo verification and docs-update expectations.
- Treat labels and milestones as declarative repo metadata managed from version-controlled files and applied through `gh api`.
- Document the maintainer operating model for labels, milestones, and sync usage.

## Current State

- The repository already contains `.github/CODEOWNERS` and CI workflows, but no issue template or PR template files.
- GitHub labels already exist remotely for area, kind, priority, risk, and status taxonomy plus standard community labels.
- GitHub milestones already exist remotely:
  - `MVP Public Beta - Blockers`
  - `MVP Public Beta - Stabilization`
  - `Post-MVP - Enhancements`
- Existing contributor/governance docs were added recently for issue `#5`, so issue `#6` should integrate with those entry points instead of creating parallel guidance.

## Recommended Approach

Use repo-managed GitHub configuration as the source of truth:

- Store issue forms under `.github/ISSUE_TEMPLATE/`.
- Store the PR checklist in `.github/pull_request_template.md`.
- Store labels and milestones in declarative data files under `tools/github/`.
- Add a sync script that uses `gh api` to create or update labels and milestones idempotently.
- Add a runbook documenting when maintainers run the sync and how issue labeling/milestoning should work.

This approach keeps policy and metadata reviewable in pull requests, avoids manual drift, and keeps operational complexity lower than a CI-driven sync workflow.

## Alternatives Considered

### 1. Manual metadata management with docs only

Pros:
- Lowest implementation effort

Cons:
- Easy for labels and milestone descriptions to drift from documented policy
- Does not satisfy the requested GH/automation direction

### 2. GitHub Actions auto-sync on every metadata change

Pros:
- Strongest enforcement of consistency

Cons:
- Adds token and workflow-permissions complexity
- Larger blast radius for a repository-ops issue that does not need continuous automation yet

## Repository Shape

- `.github/ISSUE_TEMPLATE/bug.yml`
- `.github/ISSUE_TEMPLATE/feature.yml`
- `.github/ISSUE_TEMPLATE/config.yml`
- `.github/pull_request_template.md`
- `tools/github/labels.json`
- `tools/github/milestones.json`
- `tools/github/sync-github-meta.sh`
- `docs/runbooks/active/github-triage.md`

Potential discoverability updates:

- `README.md`
- `docs/README.md`
- `CHANGELOG.md`
- `CONTRIBUTING.md`

## Issue Form Design

### Bug form

Capture:

- summary
- impact/severity
- environment/context
- reproduction steps
- expected behavior
- actual behavior
- relevant logs/screenshots
- verification evidence

### Feature form

Capture:

- problem statement
- desired outcome
- acceptance criteria
- user/operator impact
- docs impact
- verification expectations

### Template config

- Disable blank issues
- Point security-sensitive reports to `SECURITY.md`
- Point support questions to `SUPPORT.md`

## Pull Request Template Design

Use a structured checklist that mirrors repository policy:

- scope summary
- issue link
- tests/builds run
- docs updated
- changelog updated when notable
- risk areas touched
- screenshots/log samples if relevant

This keeps contributor behavior aligned with `AGENTS.md` and `docs/engineering/git-and-ai-practices.md`.

## Labels and Milestones Operating Model

### Labels

- Preserve the current taxonomy as the managed baseline.
- Sync creates missing labels and updates color/description drift for known labels.
- First pass is non-destructive: unknown labels are left in place.

### Milestones

Preserve the current three milestones and document the default routing rule:

- `priority:P0` -> `MVP Public Beta - Blockers`
- `priority:P1` -> `MVP Public Beta - Stabilization`
- `priority:P2` or `priority:P3` -> `Post-MVP - Enhancements`

The sync script ensures milestone presence and correct descriptions, but does not reassign issues automatically.

## Verification Strategy

This issue is primarily repo metadata and documentation, so verification is explicit rather than unit-test heavy:

- inspect issue form and PR template structure locally
- run the metadata sync script in a safe/idempotent mode against the current repo
- confirm labels and milestones match the declarative files after sync
- run repository sanity builds required by repo policy for metadata/doc changes

## Risks and Mitigations

- Drift between repo files and GitHub metadata
  - Mitigation: make repo files authoritative and provide a single sync script
- Excessively destructive metadata sync
  - Mitigation: do not delete unmanaged labels or milestones in the first pass
- Contributor confusion between issue forms and support/security paths
  - Mitigation: use `config.yml` to redirect users clearly
- PR template checklist diverges from actual repo expectations
  - Mitigation: derive checklist items directly from `AGENTS.md` and current docs

## Out of Scope

- Automatic issue reassignment to milestones based on labels
- Auto-created labels for every future taxonomy experiment
- CI-enforced metadata sync on every merge
