# Issue #10 Contributor Triage and Issue Lifecycle Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Document a maintainer-focused GitHub issue lifecycle workflow and link it into the existing runbook/docs surfaces without changing product behavior.

**Architecture:** Add one dedicated lifecycle runbook for issue progression, keep the existing GitHub triage runbook focused on metadata operations, and update the docs indexes plus changelog so maintainers can find the new workflow from the current entry points.

**Tech Stack:** Markdown documentation, repository runbooks, GitHub metadata conventions

---

## Goal

Document the maintainer issue lifecycle for GitHub work in Hive and make it discoverable from the existing runbook and documentation indexes.

## Assumptions

- Issue `#10` remains documentation-only and does not require workflow YAML or template changes.
- The maintainer wants the workflow centered in runbooks, not primarily in contributor-facing docs.
- Repository verification policy still requires `pnpm --filter @hive/api build` and `pnpm --filter @hive/web build` for doc changes in this repo.

## Plan

### Step 1

Files: `docs/runbooks/active/github-triage.md`, `docs/runbooks/README.md`, `docs/README.md`, `README.md`, `CHANGELOG.md`

Change: Read the current docs entry points and record exactly where the new lifecycle runbook should be linked so there is one canonical workflow and no duplicated instructions.

Verify: `sed -n '1,260p' docs/runbooks/active/github-triage.md`

### Step 2

Files: `docs/runbooks/active/github-triage.md`

Change: Write the lifecycle outline and transition model to align with the existing label taxonomy, milestone model, PR checklist expectations, and closeout hygiene already documented in the triage runbook.

Verify: `rg -n "status:needs-triage|status:ready|status:in-progress|status:blocked|milestone|pull request" docs/runbooks/active/github-triage.md`

### Step 3

Files: Create `docs/runbooks/active/issue-lifecycle.md`

Change: Draft the new maintainer runbook covering intake, triage, ready, in-progress, blocked, review, merged, and closed states, including entry criteria, required artifacts, and transition guidance.

Verify: `sed -n '1,260p' docs/runbooks/active/issue-lifecycle.md`

### Step 4

Files: `docs/runbooks/active/issue-lifecycle.md`

Change: Add edge-case guidance for duplicates, support misroutes, security reports, blocked work, missing verification evidence, and issue-close behavior after merge.

Verify: `rg -n "duplicate|support|security|blocked|verification|close" docs/runbooks/active/issue-lifecycle.md`

### Step 5

Files: `docs/runbooks/active/github-triage.md`

Change: Trim or adjust the existing triage runbook so it stays metadata-focused and links maintainers to the new lifecycle runbook for issue progression rules.

Verify: `sed -n '1,260p' docs/runbooks/active/github-triage.md`

### Step 6

Files: `docs/runbooks/README.md`, `docs/README.md`, `README.md`

Change: Add concise links to the new lifecycle runbook from the relevant documentation indexes so maintainers can discover it from current entry points without duplicating the workflow text.

Verify: `rg -n "issue-lifecycle|GitHub triage|runbook" docs/runbooks/README.md docs/README.md README.md`

### Step 7

Files: `CHANGELOG.md`

Change: Add a short changelog entry under the appropriate section noting the new maintainer issue lifecycle documentation and runbook linkage.

Verify: `rg -n "issue lifecycle|triage workflow|runbook" CHANGELOG.md`

### Step 8

Files: `docs/runbooks/active/issue-lifecycle.md`, `docs/runbooks/active/github-triage.md`, `docs/runbooks/README.md`, `docs/README.md`, `README.md`, `CHANGELOG.md`

Change: Run a final consistency pass to make sure terminology, label names, milestone references, and linked paths match the repository's current GitHub metadata model.

Verify: `rg -n "status:needs-triage|status:ready|status:in-progress|status:blocked|MVP Public Beta - Stabilization|tools/github/sync-github-meta.sh" docs/runbooks/active/issue-lifecycle.md docs/runbooks/active/github-triage.md docs/runbooks/README.md docs/README.md README.md`

### Step 9

Files: none

Change: Run the required repository verification commands for this documentation change.

Verify: `pnpm --filter @hive/api build`

### Step 10

Files: none

Change: Run the required web build verification because the repo policy calls for it on doc updates in this workflow.

Verify: `pnpm --filter @hive/web build`

## Risks & mitigations

- Risk: The new runbook duplicates too much content from the triage runbook.
  Mitigation: Keep the lifecycle doc focused on state transitions and use links for metadata details.
- Risk: The workflow drifts from actual labels or milestones.
  Mitigation: Cross-check every named label and milestone against the existing triage runbook and managed metadata references.
- Risk: Maintainers treat the new workflow as contributor-facing policy.
  Mitigation: Frame the document explicitly as a maintainer operations runbook and avoid broad contributor language.

## Rollback plan

- Revert the new lifecycle runbook file.
- Remove added links from docs indexes and README.
- Remove the changelog entry.
- Re-run `pnpm --filter @hive/api build` and `pnpm --filter @hive/web build` to confirm the docs rollback leaves the repo in a clean state.
