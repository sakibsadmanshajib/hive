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
- `Agent console (type + unit + build)`
- `Live integration (SDK tests + smoke)`
- `Web E2E (full stack)`
- `PR is attached to a triaged issue`

The first nine are jobs in `.github/workflows/ci.yml`. The tenth is the single
job in `.github/workflows/pr-tracking-gate.yml`, which enforces
`.claude/rules/tracking-discipline.md`: a pull request links an issue, and that
issue is triaged. No other workflow may publish any of these names, and
`.github/ci/lint-workflow-check-names.mjs` fails the build if one does.
`strict` is `false`: checks must pass, but a PR is not forced to be rebased onto
the latest `main` first.

### How path filtering interacts with the gate

`ci.yml` has no `paths:` or `paths-ignore:` filter on any trigger, so it starts
on every pull request and never deadlocks one. Whether the real suite runs is
decided inside the workflow by the `changes` job, which lists the changed files
and skips the work only when every one of them is documentation. Everything
else, including an unrecognized path or an API error, runs the full suite.

This shape matters for the gate. A workflow skipped by a trigger path filter
never creates its check runs, so its required checks stay Pending and block the
merge. A job skipped by a conditional does create a check run, but it reports
the conclusion `skipped`, and whether branch protection accepts that for a
required context is the one thing that cannot be tested on a pull request into a
protected branch until after the change is merged. The jobs publishing required
contexts therefore do not rely on it: each carries `if: always()` and gates its
individual steps, so it always concludes on its own merits. Filtering inside the
workflow keeps a docs-only pull request mergeable without a second workflow
standing in for the real one.

One consequence to expect: on a docs-only or hooks-only pull request the nine
`ci.yml` required checks legitimately go green in a few seconds, because each
required job runs to completion with every step skipped. The tracking gate is
the exception and really does run there, since a documentation change needs an
issue exactly as much as a code change does. That is the same *shape* as the
issue #553 defect below, so do not read duration as evidence either way. The
property that matters is one producer per required context, which the guard
below enforces on every pull request.

The previous arrangement did use a second workflow (`ci-noop.yml`) that fired on
the inverse path set and published the same job names as trivial `echo` steps.
Because `paths-ignore` matches whenever any changed file falls outside the
ignore set, a pull request touching both code and docs fired both workflows, and
the echo could report success on every required context in seconds. That was
issue #553 and it is why the rule below is now enforced automatically.

> Exactly one job, in exactly one pull-request workflow, may publish a given
> required check name. `.github/ci/lint-workflow-check-names.mjs` runs inside
> the `Repo policy lints (tenant + audit)` required check and fails the build
> when any of these holds:
>
> - two pull-request jobs publish the same check name;
> - a required context in the config below has no producer, or more than one;
> - a job publishing a required context can be skipped (no `if: always()`);
> - a workflow publishing a required context path-filters its triggers;
> - a workflow publishing a required context does not list every
>   `pull_request` type the gate needs (`opened`, `synchronize`, `reopened`,
>   `ready_for_review`). Without `ready_for_review`, a draft marked ready with
>   no further pushes fires no event, so no required check is ever published
>   and the pull request blocks forever with nothing visibly failing.
>
> Matrix job names are expanded against the real `strategy.matrix` values, so
> `Go tests (${{ matrix.module }})` is compared as the four concrete names it
> publishes. A job name built from anything else the guard cannot resolve
> (`github.*`, `env.*`) is itself an error, because a name the guard cannot
> compute is a name it cannot verify.

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
