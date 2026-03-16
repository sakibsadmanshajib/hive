# Maintainer Issue Lifecycle Runbook

## Purpose

This runbook defines how maintainers move GitHub issues through the Hive repository workflow from intake to closure.

Use this document for issue state transitions, planning expectations, PR linkage, verification evidence, and closeout. Use `docs/runbooks/active/github-triage.md` for labels, milestones, issue forms, and metadata sync mechanics.

## Source of Truth

- Intake forms: `.github/ISSUE_TEMPLATE/`
- Pull request checklist: `.github/pull_request_template.md`
- Triage metadata model: `docs/runbooks/active/github-triage.md`
- Engineering verification policy: `docs/engineering/git-and-ai-practices.md`
- Persisted plans and design docs: `docs/plans/`

## Lifecycle States

The maintainer workflow for most issues is:

1. Intake
2. `status:needs-triage`
3. `status:ready`
4. `status:in-progress`
5. `status:blocked` when work cannot advance
6. In review through an open pull request
7. Merged
8. Closed

Not every issue needs every state, but maintainers should be able to explain the current state from labels, linked artifacts, and the latest issue or PR activity.

## State Details

### 1. Intake

Entry criteria:

- A new issue is opened through a repo issue form.
- The issue may have incomplete metadata or unclear scope.

Maintainer actions:

- Confirm the issue belongs in this repository.
- Redirect support requests to `SUPPORT.md`.
- Redirect security-sensitive reports to `SECURITY.md`.
- Mark duplicates early when a matching tracked issue already exists.

Required artifacts:

- Original issue body with enough context to understand the request or defect.

Exit criteria:

- The issue is accepted for triage and carries `status:needs-triage`, or it is closed/redirected as duplicate, support, or security intake.

### 2. `status:needs-triage`

Entry criteria:

- The issue is accepted for repo review but has not yet been fully classified.

Maintainer actions:

- Apply one `area:*` label, one `kind:*` label when intent is clear, and one `priority:*` label once importance is assessed.
- Assign the correct milestone based on current planning horizon.
- Clarify acceptance criteria or blockers if the issue body is incomplete.

Required artifacts:

- Correct labels and milestone.
- Any maintainer clarifications needed to make the scope actionable.

Exit criteria:

- The issue is well-scoped, has clear acceptance criteria, and is ready for planning or direct implementation.

### 3. `status:ready`

Entry criteria:

- Scope is clear enough that a maintainer or contributor can execute without guessing.

Maintainer actions:

- Confirm the issue has the minimum metadata set.
- Decide whether the work needs a design doc, an implementation plan, or both.
- Create or request a persisted plan under `docs/plans/` when the task spans multiple steps, operational risks, or cross-file changes.

Required artifacts:

- Stable acceptance criteria.
- Design and plan docs when needed for multi-step or higher-risk work.

Exit criteria:

- An implementer is ready to begin work and there is no unresolved planning ambiguity.

### 4. `status:in-progress`

Entry criteria:

- A maintainer or contributor has actively started implementation.

Maintainer actions:

- Remove `status:ready`.
- Add `status:in-progress`.
- Link the issue to the active branch or pull request when available.
- Keep the issue updated if scope or verification changes materially.

Required artifacts:

- Active implementation branch or pull request.
- Verification plan appropriate to the touched scope.

Exit criteria:

- Work is either submitted for review, blocked, or intentionally paused with clear next steps.

### 5. `status:blocked`

Entry criteria:

- Work cannot continue because of an unresolved dependency, decision, outage, or missing context.

Maintainer actions:

- Remove `status:in-progress` when the blocker is expected to last.
- Add `status:blocked`.
- Record the blocker explicitly in the issue or PR thread.
- Link the blocking issue, PR, or decision when possible.

Required artifacts:

- Written blocker description with the unblock condition.

Exit criteria:

- The blocker is resolved and the issue can return to `status:ready` or `status:in-progress`.

### 6. In Review

Entry criteria:

- A pull request exists and is the active review vehicle for the issue.

Maintainer actions:

- Ensure the PR body links the issue or states the explicit scope.
- Confirm verification commands and outcomes are recorded in the PR.
- Check docs and changelog expectations for the touched behavior.
- Route review feedback back into the branch before merge.

Required artifacts:

- Open PR with issue linkage.
- Verification evidence matching the changed scope.

Exit criteria:

- The PR is approved and merged, or review feedback sends the issue back to active work.

### 7. Merged

Entry criteria:

- The linked PR has been merged to the target branch.

Maintainer actions:

- Confirm the merge actually satisfies the issue acceptance criteria.
- Remove stale workflow labels if they remain.
- Let auto-closing keywords close the issue when appropriate, or close it manually with a brief maintainer note.

Required artifacts:

- Merged PR reference.

Exit criteria:

- The issue is closed, or it remains open intentionally for follow-up scope that was not delivered by the merged PR.

### 8. Closed

Entry criteria:

- The accepted scope is complete, or the issue has been explicitly declined, superseded, or redirected.

Maintainer actions:

- Leave a closing note when the reason would otherwise be ambiguous.
- Point to the merged PR, duplicate issue, support path, or security path when relevant.

Required artifacts:

- Clear close reason in issue history.

Exit criteria:

- None.

## Planning and Documentation Expectations

- Use a persisted design or implementation plan under `docs/plans/` when the work spans multiple steps, has operational risk, or benefits from an approval gate.
- Example: a rolling DB schema migration that spans multiple deploys, carries operational risk, and needs stakeholder approval should get a persisted plan such as `docs/plans/rolling-db-migration.md` with the objective, step-by-step rollout tasks, risks, rollback steps, approvers, and exact verification commands.
- Keep documentation updates in the same change when behavior, maintainer workflow, or operational guidance changes.
- Record verification commands explicitly in the PR or implementation notes.

## Pull Request Linkage

- Prefer one PR per tracked issue when practical.
- Reference the issue directly in the PR body.
- Example PR body lines:
  `Fixes #123`
  `Summary: document the maintainer issue lifecycle runbook and link it from the triage docs`
- Keep issue status aligned with actual PR state.
- Example follow-through: move the issue to the appropriate status label for review or merge state, and re-run the touched verification commands after review feedback changes the branch.
- Re-run relevant verification after review feedback changes the implementation.

## Edge Cases

### Duplicate issues

- Close duplicates with a pointer to the canonical issue.
- Keep the canonical issue open unless the duplicate fully supersedes it.

### Support requests filed as issues

- Redirect the reporter to `SUPPORT.md`.
- Close the issue unless a real product or documentation gap should remain tracked.

### Security-sensitive reports

- Redirect to `SECURITY.md`.
- Do not keep exploit details in a public issue.

### Missing planning artifacts

- If implementation starts without the needed design or plan doc, move the issue back to `status:ready` until planning is captured.

### Missing verification evidence

- Do not treat the issue as review-ready if required verification commands or explicit checks are absent.

### Merge without close

- Keep the issue open when the merged PR intentionally ships only part of the accepted scope.
- Add a maintainer note describing what remains and what artifact tracks the follow-up.

## Verification Checklist

Before closing an issue as completed, confirm:

- labels and milestone reflect the final state
- acceptance criteria are satisfied
- linked PR verification matches the touched scope
- required docs and changelog updates are present when behavior or workflow changed

Runnable checks:

- Verify issue metadata:
  `gh issue view <issue-number> --json labels,milestone,state,url`
- Verify linked PR:
  `gh pr view <pr-number> --json state,mergeStateStatus,closingIssuesReferences,url`
- Run required builds (Docker only; stack must be up):
  `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`
  `docker compose exec web sh -c "cd /app && pnpm --filter @hive/web build"`

## Maintenance Notes

- Keep this runbook aligned with `docs/runbooks/active/github-triage.md`.
- If labels or milestone policy changes, update both runbooks in the same change.
- Prefer short maintainer comments that explain why an issue changed state when the transition is not obvious from labels or PR history.
