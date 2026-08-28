---
name: Verify Deploy Happened
description: Use before reporting a merge to main as deployed, or when confirming the demo box actually runs a specific commit. Merged is not deployed — a push to main can produce no deploy-demo-box run at all, with only unrelated workflows (CodeQL) firing, and the PR page shows nothing wrong.
---

# Verify Deploy Happened

## The failure this guards against

Pushing to `main` triggers `deploy-demo-box.yml`, and this repo treats that as
the deploy mechanism (`.claude/rules/orchestrator.md` coding pipeline stage
10). But a merge can produce **no deploy run at all**: on 2026-08-28, PR
#1222's squash-merge commit got only three CodeQL analyses and zero
`deploy-demo-box` or `CI` runs, for no config reason (paths matched, no
concurrency block) — an apparent one-off GitHub delivery anomaly, the third
occurrence of the same symptom with a different cause each time (#786 and
#971 were both paths-filter gaps, since fixed; this one wasn't).

The danger: **a workflow that never runs does not fail. It is absent, and
absence is indistinguishable from success on a green PR page.** "The last
deploy-demo-box run was green" proves nothing about the commit you care
about unless you check which commit it ran against. A scheduled watchdog for
this exists (issue #1238, shipped as #1246) but do not rely on it alone —
verify the specific commit yourself before claiming a task done.

## Verify a specific commit deployed

Never accept "a deploy completed" as proof "this commit deployed." Check runs
against the exact SHA:

```bash
sha=$(git rev-parse HEAD)   # or the PR's merge commit SHA
gh run list --workflow=deploy-demo-box.yml --json headSha,status,conclusion,databaseId \
  --jq ".[] | select(.headSha == \"$sha\")"
```

Empty output means no run exists for that commit at all — not "still
running," not "failed," genuinely absent. That is the #1238 failure mode. Do
not fall back to "the most recent run was green": find the run whose
`headSha` matches your commit, or conclude none exists and dispatch one:

```bash
gh workflow run deploy-demo-box.yml --ref main
```

## A green run is still not proof the box changed

`docker compose up -d` exits 0 whether or not it actually recreated a
container (unchanged image + unchanged config = no-op). A green `deploy`
job does not by itself mean the running containers changed. Read the job's
own log for the two lines that actually say so:

```bash
gh run view <run-id> --log | grep -A5 "Rebuild + recreate changed services"
gh run view <run-id> --log | grep -A20 "Assert the running containers are on the images just built"
```

And confirm the box's own HEAD moved, from the `migrate` job's "Pull latest
main" step, which echoes the commit it just checked out:

```bash
gh run view <run-id> --log | grep "deployed commit:"
```

That line's SHA must equal the commit you are trying to verify. If it
doesn't, the deploy ran against a different (usually older) commit than the
one you think shipped.

## Summary checklist before claiming "deployed"

1. `gh run list --workflow=deploy-demo-box.yml --json headSha,...` has an
   entry for your exact commit SHA. If not, dispatch one and wait.
2. That run's conclusion is `success`.
3. Its log shows the box's "deployed commit:" line matching your SHA.
4. Its log's "Assert the running containers are on the images just built"
   step passed (containers actually got recreated, not skipped as a no-op
   against a stale image).

Skipping any of these four and reporting "merged to main, so it's deployed"
is exactly the mistake this skill exists to catch.
