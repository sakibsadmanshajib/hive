---
name: PR CI & Merge Status
description: Use when a GitHub pull request reports mergeStateStatus BLOCKED with no explanation, when you need to wait for CI to finish before merging, or before reaching for `gh pr checks` to read status at all. Covers telling apart the three real causes of BLOCKED (unresolved threads, a required check that never ran, a genuinely failing check) and the polling bugs that make an agent report a stale or wrong result.
---

# PR CI & Merge Status

## Do not use `gh pr checks` to decide anything

In this environment `gh pr checks` has **no `--json` flag**. Its text output
is tab-separated with check names that themselves contain spaces, so a naive
`awk '$2==...'` or column-index parse silently matches the wrong column and
reports the wrong check as passing or failing. This mistake has been made
repeatedly on this project. Do not parse `gh pr checks` output at all.

Use structured JSON instead, always pinned to a specific PR number or commit
SHA, never to "the branch" in the abstract:

```bash
gh pr view <N> --json statusCheckRollup,mergeable,mergeStateStatus
```

`statusCheckRollup` gives you `{name, status, conclusion}` per check, machine
parseable. `mergeable` and `mergeStateStatus` together tell you whether GitHub
thinks the PR can merge and why not.

## Waiting for CI: the loop that lies

A polling loop whose exit condition is already true when it starts reports
success for a stale run, not the one you just pushed. If you push a fix and
immediately poll `statusCheckRollup`, you can observe the *previous* run's
green `COMPLETED`/`SUCCESS` before the new run has even been queued.

Pin to the commit you actually care about, not just the PR number:

```bash
gh pr view <N> --json headRefOid,statusCheckRollup \
  --jq '.headRefOid, (.statusCheckRollup[] | "\(.name)\t\(.status)\t\(.conclusion)")'
```

Confirm `headRefOid` is the SHA you just pushed before trusting the rollup
underneath it. Then poll on a bounded loop (or the Monitor tool) until every
entry's `status` is `COMPLETED`. Do not end the turn on a single snapshot and
assume a later resume will re-check it.

## Diagnosing BLOCKED: three causes, three different fixes

`mergeable=MERGEABLE, mergeStateStatus=BLOCKED` explains nothing on its own.
Work through these in order; they are not mutually exclusive but usually only
one applies.

### 1. Unresolved review threads

This repo's branch protection requires every review thread resolved before
merge. Check directly, do not guess from the PR page render:

```bash
gh api graphql -f query='
  query($owner:String!, $repo:String!, $pr:Int!) {
    repository(owner:$owner, name:$repo) {
      pullRequest(number:$pr) {
        reviewThreads(first:100) {
          nodes { id isResolved isOutdated path line }
        }
      }
    }
  }' -f owner=sakibsadmanshajib -f repo=hive -F pr=<N> \
  --jq '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved == false)'
```

Any row in the output is a merge blocker. Resolve it (fix + reply, or a
reasoned rebuttal) before anything else.

### 2. A required check that never ran

Absent is not the same as failing, and `statusCheckRollup` only lists checks
that actually started. Diff what branch protection requires against what
actually ran for this PR:

```bash
gh api repos/sakibsadmanshajib/hive/branches/main/protection \
  --jq '.required_status_checks.contexts[]' | sort > /tmp/required.txt

gh pr view <N> --json statusCheckRollup \
  --jq '.statusCheckRollup[].name' | sort -u > /tmp/ran.txt

comm -23 /tmp/required.txt /tmp/ran.txt
```

Anything left in that `comm` output is a required check GitHub never
triggered for this commit, and that alone is enough to leave a PR permanently
BLOCKED with a page that looks otherwise green.

Known cause: webhook delivery failure. On 2026-08-28 GitHub failed to deliver
push events for roughly 2.5 hours; PRs merged or pushed to during that window
never got a `pull_request` run for any workflow, required or not. Closing and
reopening the PR did **not** re-fire the checks. A fresh push to the branch
did (`git commit --allow-empty -m "..." && git push`, or push any real
change). If a check is missing and there is no config reason for it, suspect
delivery, not your workflow YAML, and test the fix with a real push before
concluding the workflow itself is broken.

### 3. A genuinely failing check

Only after ruling out 1 and 2: read the actual failing check's log
(`gh run view <run-id> --log-failed`) and fix the real failure. This is the
only one of the three that is actually about the code in the PR.

## Related

- `.wolf/decisions.md` D-050 (merge policy: zero unresolved threads, both
  review streams, green checks).
- `docs/proof/` visual-proof skill (`.claude/skills/pr-visual-proof.md`) for
  what must accompany a UI-touching PR before it can merge at all.
