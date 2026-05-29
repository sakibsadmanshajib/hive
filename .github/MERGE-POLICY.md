# Merge Policy (enforced on `main`)

A pull request **cannot** be merged into `main` unless **both** hold:

1. **No failed/missing tests** — every required status check is green.
2. **No unresolved review comments** — all PR review conversations are resolved.

This is enforced server-side by GitHub branch protection on `main`, including
for repository admins (`enforce_admins: true`). It is not advisory and cannot be
bypassed from the CLI or UI.

## What is enforced

| Setting | Value | Effect |
|---------|-------|--------|
| `required_conversation_resolution` | `true` | Any unresolved review thread blocks merge |
| `required_status_checks.contexts` | see below | Any failed/missing required check blocks merge |
| `enforce_admins` | `true` | Admins are subject to the same rules |
| `allow_force_pushes` | `false` | No force-push to `main` |
| `allow_deletions` | `false` | `main` cannot be deleted |

### Required status checks
- `Go tests (control-plane)`
- `Go tests (edge-api)`
- `Go tests (storage)`
- `Repo policy lints (tenant + audit)`
- `Web console (type + unit + build)`
- `Live integration (SDK tests + smoke)`
- `Web E2E (full stack)`

These are the jobs in `.github/workflows/ci.yml`, which runs on every
same-repo pull request with no path filter (so it never deadlocks a PR).
`chat-app-ci.yml` jobs are path-filtered and therefore **not** required.
`strict` is `false`: checks must pass, but a PR is not forced to be rebased
onto the latest `main` first.

> Note: `required_pull_request_reviews` is not set — this repo currently has no
> mandatory human approver (solo/small team). Add it later by setting
> `required_approving_review_count` in the config below.

## Re-applying / updating

The canonical config lives in `.github/branch-protection-main.json`. Re-apply with:

```bash
gh api -X PUT repos/sakibsadmanshajib/hive/branches/main/protection \
  -H "Accept: application/vnd.github+json" \
  --input .github/branch-protection-main.json
```

Verify:

```bash
gh api repos/sakibsadmanshajib/hive/branches/main/protection \
  --jq '{conversation_resolution_required:.required_conversation_resolution.enabled, enforce_admins:.enforce_admins.enabled, required_checks:.required_status_checks.contexts}'
```

If a required check name changes in `ci.yml`, update both the workflow and the
`contexts` array here in the same PR, otherwise merges will block on a check
that never reports.
