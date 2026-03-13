## Goal

Implement issue `#6` by adding GitHub Issue Forms, a structured PR template, and a repo-managed labels/milestones sync workflow, then document the maintainer operating model and verify the repository and GitHub metadata remain in a consistent state.

## Assumptions

- The preferred plan-writer helper at `.agent/skills/superpowers-workflow/scripts/write_artifact.py` is unavailable in this repository, so this plan is written directly to `docs/plans/`.
- Existing remote labels and milestones are the baseline taxonomy and should be preserved unless they conflict with the new declarative source files.
- This task should use explicit verification checks and GitHub API evidence rather than traditional unit tests for most of the work.
- Unknown labels or milestones that are not managed by the new source files should not be deleted in the first implementation pass.

## Plan

### Step 1

**Files:** `.github/`, `CONTRIBUTING.md`, `README.md`, `docs/README.md`, `AGENTS.md`, `docs/engineering/git-and-ai-practices.md`

**Change:** Re-read the current contribution, verification, and documentation expectations that the issue forms and PR template must enforce, and confirm the final managed label/milestone set from the live repo before writing files.

**Verify:** `gh api repos/{owner}/{repo}/labels --paginate --jq '.[].name' && gh api repos/{owner}/{repo}/milestones --jq '.[] | .title'`

### Step 2

**Files:** `.github/ISSUE_TEMPLATE/bug.yml`, `.github/ISSUE_TEMPLATE/feature.yml`, `.github/ISSUE_TEMPLATE/config.yml`

**Change:** Add GitHub Issue Forms for bugs and feature requests plus template configuration that disables blank issues and redirects security/support traffic to the existing policy docs.

**Verify:** `sed -n '1,220p' .github/ISSUE_TEMPLATE/bug.yml && sed -n '1,220p' .github/ISSUE_TEMPLATE/feature.yml && sed -n '1,220p' .github/ISSUE_TEMPLATE/config.yml`

### Step 3

**Files:** `.github/pull_request_template.md`

**Change:** Add a structured PR checklist covering issue linkage, verification commands run, docs/changelog updates, risk areas touched, and any evidence maintainers need during review.

**Verify:** `sed -n '1,220p' .github/pull_request_template.md`

### Step 4

**Files:** `tools/github/labels.json`, `tools/github/milestones.json`

**Change:** Create declarative source files for the managed labels and milestone catalog, using the current remote metadata as the starting point and documenting only the set the repo intends to manage.

**Verify:** `sed -n '1,240p' tools/github/labels.json && sed -n '1,240p' tools/github/milestones.json`

### Step 5

**Files:** `tools/github/sync-github-meta.sh`

**Change:** Add an idempotent `gh api` sync script that creates or updates labels and milestones from the declarative files without deleting unmanaged remote metadata.

**Verify:** `bash -n tools/github/sync-github-meta.sh`

### Step 6

**Files:** `tools/github/sync-github-meta.sh`, `tools/github/labels.json`, `tools/github/milestones.json`

**Change:** Run the sync workflow against the current repository and confirm the resulting remote labels and milestones match the managed definitions by name, color, and description/title.

**Verify:** `tools/github/sync-github-meta.sh && gh api repos/{owner}/{repo}/labels --paginate --jq '.[] | {name,color,description}' && gh api repos/{owner}/{repo}/milestones --jq '.[] | {title,description,state}'`

### Step 7

**Files:** `docs/runbooks/active/github-triage.md`, `CONTRIBUTING.md`

**Change:** Document the maintainer operating model for issue intake, label usage, milestone assignment, and when to run the metadata sync script; update contributor guidance if forms or PR expectations affect contributor workflow.

**Verify:** `sed -n '1,260p' docs/runbooks/active/github-triage.md && rg -n "issue template|pull request template|label|milestone|sync-github-meta" CONTRIBUTING.md docs/runbooks/active/github-triage.md`

### Step 8

**Files:** `README.md`, `docs/README.md`, `CHANGELOG.md`

**Change:** Update the main discovery docs and changelog so contributors and operators can find the new templates and GitHub metadata workflow from the normal repository entry points.

**Verify:** `rg -n "issue template|pull request|label|milestone|GitHub triage|sync-github-meta" README.md docs/README.md CHANGELOG.md`

### Step 9

**Files:** `.github/ISSUE_TEMPLATE/bug.yml`, `.github/ISSUE_TEMPLATE/feature.yml`, `.github/ISSUE_TEMPLATE/config.yml`, `.github/pull_request_template.md`, `tools/github/labels.json`, `tools/github/milestones.json`, `tools/github/sync-github-meta.sh`, `docs/runbooks/active/github-triage.md`, `CONTRIBUTING.md`, `README.md`, `docs/README.md`, `CHANGELOG.md`

**Change:** Run final repository verification for this docs/config/ops change and capture the exact commands used as implementation evidence.

**Verify:** `pnpm --filter @hive/api build && pnpm --filter @hive/web build`

## Risks & mitigations

- Risk: the sync script updates the wrong repository metadata.
  Mitigation: use `gh` from the repo root, match records by exact label name and milestone title, and keep the first pass non-destructive.
- Risk: issue forms and PR checklist drift from current repo expectations.
  Mitigation: derive fields and checklist items directly from `AGENTS.md`, `CONTRIBUTING.md`, and docs guidance.
- Risk: maintainers forget how milestone routing is intended to work.
  Mitigation: document the priority-to-milestone default mapping in a dedicated runbook and contributor docs.
- Risk: docs/config changes appear complete without remote metadata actually being synced.
  Mitigation: require running the sync script and fetching the live labels/milestones as part of verification.

## Rollback plan

- Revert the issue forms, PR template, metadata source files, sync script, and documentation updates in one revert if the operating model needs redesign.
- If only remote metadata changes need rollback, restore the previous labels/milestones by editing the declarative files and rerunning the sync script rather than making ad hoc GitHub UI edits.
- Because this change does not affect runtime code paths or persisted application data, rollback is limited to repository metadata and documentation state.
