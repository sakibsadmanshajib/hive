# Issue #10 Contributor Triage and Issue Lifecycle Design

## Goal

Document a maintainer-centered GitHub issue lifecycle workflow that explains how Hive issues move from intake through triage, planning, implementation, review, merge, and closeout.

## Context

- Issue `#10` calls for contributor triage and issue lifecycle documentation as part of public beta stabilization.
- The repository already has a metadata-focused runbook at `docs/runbooks/active/github-triage.md`.
- Current docs explain labels, milestones, issue forms, and sync commands, but they do not define the full operational path an issue should follow after it is filed.
- The user explicitly chose to keep this work primarily maintainer-facing and centered in the runbooks rather than expanding the contributor guide.

## Decision

Add a dedicated maintainer issue-lifecycle runbook under `docs/runbooks/active/` and keep `docs/runbooks/active/github-triage.md` focused on GitHub metadata mechanics.

This separates two operational concerns cleanly:

- triage metadata management: labels, milestones, templates, and sync workflow
- issue lifecycle management: intake, refinement, status transitions, implementation handoff, PR linkage, merge, and closeout

## Rejected Alternatives

### Expand the existing GitHub triage runbook

This keeps the number of files smaller, but it mixes label-taxonomy details with day-to-day issue state handling and makes the runbook harder to scan during triage.

### Put the lifecycle in `CONTRIBUTING.md`

This would increase contributor visibility, but it does not match the stated requirement to keep the workflow primarily maintainer-oriented.

### Document the workflow only in `README.md` or `docs/README.md`

These locations are useful for discovery, but they are poor operational sources of truth and would duplicate runbook-level guidance.

## Architecture

### New lifecycle runbook

Create a runbook that defines:

- issue intake expectations from GitHub issue forms
- initial maintainer triage steps
- required minimum metadata before work starts
- status label transitions and milestone expectations
- when to create or update design and implementation plans
- how to move issues into active work
- PR linkage, verification evidence, and review expectations
- merge and post-merge cleanup behavior
- when and how issues should be closed

### Existing triage runbook updates

Retain current guidance for:

- issue forms
- pull request template
- labels and milestones
- metadata sync workflow

Add cross-links so maintainers can move between metadata guidance and lifecycle guidance without duplicate instructions.

### Documentation index updates

Update top-level docs surfaces so the lifecycle runbook is discoverable:

- `docs/README.md`
- `docs/runbooks/README.md`
- `README.md` if maintainer operations references need the new runbook link

## Lifecycle Scope

The documented lifecycle should cover these operational states:

1. Intake
2. Needs triage
3. Refined / ready
4. In progress
5. Blocked
6. In review
7. Merged
8. Closed

For each state, the runbook should specify:

- expected labels
- milestone posture
- maintainer actions
- required artifacts such as plan docs or PR references
- exit criteria for the next transition

## Error Handling and Edge Cases

The runbook should explicitly address:

- duplicate issues
- support requests misfiled as bugs/features
- security-sensitive reports
- blocked issues waiting on product or architecture decisions
- implementation completed without docs or verification evidence
- merged PRs that should auto-close issues versus manually closed follow-up work

## Testing and Verification Strategy

This issue is documentation-first, so verification should be explicit rather than test-based:

- validate all new links and references
- run required repository builds per doc policy
- confirm the lifecycle guidance matches existing GitHub labels, milestones, templates, and workflow files

## Operational Impact

- Maintainers get a clear operating model for issue progression.
- Triage becomes more consistent across labels, milestones, planning docs, and PR handling.
- Repo policy becomes easier to audit because the workflow is documented as an explicit lifecycle instead of being inferred from scattered docs.
