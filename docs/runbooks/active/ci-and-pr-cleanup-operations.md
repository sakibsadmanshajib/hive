# CI and PR Cleanup Operations

## Scope

This runbook covers repository automation for CI quality checks and post-merge PR cleanup.

## Operational Artifacts

- CI quality workflow: `.github/workflows/ci.yml`
  - Runs lint, test, and build checks for API/web based on path-filtered change scopes.
  - Uses pnpm and Next.js cache steps to reduce runtime cost.
- PR cleanup workflow: `.github/workflows/pr-cleanup.yml`
  - Runs on merged pull requests and invokes cleanup logic.
- Cleanup script: `.github/scripts/pr-cleanup.sh`
  - Deletes merged source branches when safe.
  - Removes `status:in-progress` label from merged PRs.

## Verification

1. Confirm workflows are visible:
   - `gh workflow list`
2. Confirm latest workflow runs:
   - `gh run list --limit 20`
3. For merged PR cleanup behavior:
   - Verify merged branch deletion in the repository branch list.
   - Verify `status:in-progress` label is removed from merged PR.

## Troubleshooting

- Cleanup job did not run:
  - Confirm PR was merged (not closed without merge).
  - Confirm workflow file exists on default branch.
- Cleanup script failed with permissions error:
  - Ensure workflow uses `bash .github/scripts/pr-cleanup.sh` (not direct exec bit dependency).
- Branch was not deleted:
  - Check script guards for default branch and fork PRs.
  - Confirm branch still exists via `gh api repos/<owner>/<repo>/branches`.
- Label was not removed:
  - Confirm the merged PR had `status:in-progress` at close time.
  - Confirm workflow token has `pull-requests: write` permission.
