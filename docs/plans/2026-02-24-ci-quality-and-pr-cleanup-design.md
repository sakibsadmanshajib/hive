# CI Quality and PR Cleanup Design

## Context

The repository currently has no GitHub Actions workflows, so pull requests do not enforce automated quality gates. We need stronger CI quality checks while minimizing GitHub Actions usage. We also need an automated post-merge cleanup flow and issue tracking that follows the repository's label/milestone conventions.

## Goals

- Add monorepo CI checks for API and web lint, tests, and builds.
- Keep Actions usage efficient with path filters, caching, and run cancellation.
- Add a post-merge cleanup automation that deletes merged branches safely.
- Open a tracking GitHub issue with full context, planning, and standardized metadata.
- Document issue-label/project expectations in `AGENTS.md` to prevent future drift.

## Approach

### 1) Single workflow for quality checks

Use one workflow for monorepo quality (`pull_request` + `push`) to reduce runner startup overhead while still enforcing complete checks. This gives full coverage at lower cost than splitting checks into multiple workflows.

### 2) Cost controls

- `paths-ignore` for docs-only changes.
- `concurrency` cancellation for superseded runs.
- pnpm + Node cache reuse.
- single-job execution to avoid repeated setup costs.

### 3) PR cleanup automation

Add a `pull_request.closed` workflow that runs only when PR is merged and calls a script. The script:

- verifies event metadata and merge state
- refuses to delete the default branch
- skips fork branches (not owned by base repository)
- deletes merged source branch when safe
- removes temporary `status:in-progress` PR label if present

### 4) Issue governance

Create an issue with:

- labels: one `kind:*`, one `area:*`, one `priority:*`, and one `status:*` (plus optional risk)
- milestone: matching release/stabilization milestone
- sections: Context, Problem, Why this matters, Acceptance Criteria, Verification, Dependencies

If project assignment is not possible due missing token scopes, note that in execution output and keep label/milestone compliance.

## Risks and Mitigations

- Running both `push` and `pull_request` can duplicate work.
  - Mitigation: concurrency cancellation and scoped branch triggers.
- Auto branch deletion can be dangerous.
  - Mitigation: strict guardrails in cleanup script.
- Missing project scope can prevent project assignment.
  - Mitigation: proceed with required labels/milestone and explicitly note limitation.

## Validation

- CI workflow YAML validates and triggers as designed.
- PR cleanup workflow is guarded and invokes executable script.
- Tracking issue exists with required metadata and planning context.
- `AGENTS.md` includes explicit GitHub issue hygiene section.
