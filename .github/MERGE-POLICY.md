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
- `Go tests (agent-engine)`
- `Go tests (control-plane)`
- `Go tests (edge-api)`
- `Go tests (storage)`
- `Repo policy lints (tenant + audit)`
- `Web console (type + unit + build)`

These are the jobs in `.github/workflows/ci.yml`, which is the only workflow
allowed to publish them. `strict` is `false`: checks must pass, but a PR is not
forced to be rebased onto the latest `main` first.

### How path filtering interacts with the gate

`ci.yml` has no `paths:` or `paths-ignore:` filter on any trigger, so it starts
on every pull request and never deadlocks one. Whether the real suite runs is
decided inside the workflow by the `changes` job, which lists the changed files
and skips the work only when every one of them is documentation. Everything
else, including an unrecognized path or an API error, runs the full suite.

This shape matters for the gate. A workflow skipped by a trigger path filter
never creates its check runs, so its required checks stay Pending and block the
merge; a job skipped by a conditional reports Success. Filtering inside the
workflow therefore keeps a docs-only pull request mergeable without a second
workflow standing in for the real one.

The previous arrangement did use a second workflow (`ci-noop.yml`) that fired on
the inverse path set and published the same job names as trivial `echo` steps.
Because `paths-ignore` matches whenever any changed file falls outside the
ignore set, a pull request touching both code and docs fired both workflows, and
the echo could report success on every required context in seconds. That was
issue #553 and it is why the rule below is now enforced automatically.

> Exactly one job, in exactly one pull-request workflow, may publish a given
> required check name. `.github/ci/lint-workflow-check-names.mjs` runs inside
> the `Repo policy lints (tenant + audit)` required check and fails the build on
> a duplicate name, or on a required context in the config below that no job
> publishes.

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
`contexts` array in `.github/branch-protection-main.json` in the same PR,
otherwise merges will block on a check that never reports. The guard described
above fails the build if you change one without the other, but note that
`.github/branch-protection-main.json` is only the checked-in copy: applying it to
the live branch protection still requires the `gh api -X PUT` call above.
