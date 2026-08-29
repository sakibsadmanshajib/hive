# Visual proof: Cowork launch liveness, PR #1257

Date: 2026-08-28
Branch: fix/rag-chat-post-finish-chunk-and-demo-readiness
PR: https://github.com/sakibsadmanshajib/hive/pull/1257

## Why this log exists, and what it is proof of

The images this log backs were already posted to the pull request as three
`## Cowork visual proof, captured in CI` comments from `github-actions`,
produced by `.github/workflows/agent-visual-proof.yml` (dispatched manually
with `gh workflow run "agent visual proof" -f pr=1257`, three times across
the review cycle as fixes landed). That workflow is the standing mechanism
for orchestrator rule 8 on this repo: a Cowork task can only really launch
through the socket arm of `buildAgentEngine`, which needs a real Apptainer
sandbox and a real host launcher, neither of which exists on this WSL2 dev
box, so the workflow stands up a throwaway stack on a GitHub-hosted runner
per run and captures against that.

This directory did not previously exist on this PR's branch. `npm run
lint:proof-tokens` scans `docs/proof/` in the branch's own git tree, and the
CI workflow's captures are written to an ephemeral runner directory, then
pushed straight to a side branch (`ci-proof/pr-1257-run-<id>`, never merged,
never touching this PR's branch) via the GitHub contents API, not committed
here. The screenshots and their redacted run log were real and already
public on the PR, but nothing under `docs/proof/` on this branch backed
them, so the linter was scanning an empty directory for this change and
reporting a green that proved nothing. This file closes that gap by
committing the same text log the workflow already posted and redacted, so
the linter has something to scan.

## What was captured (three runs, same scenario, same route)

Scenario: `launch-liveness` (`apps/agent-console/proof/harness/capture-live.mjs`),
the floor scenario that proves the Cowork task surface still launches: an
empty console, a task creation call, and the launched sandbox appearing in
the task list.

Captured URL: `http://127.0.0.1:3030/agent-workspace/tasks` (loopback on the
hosted runner; `PROOF_BASE_URL=http://127.0.0.1:3030`, `BASE_PATH=
/agent-workspace`, both from the workflow and the harness source, not
guessed). No credential in that URL: it carries no query string, and the
session is established via cookies (`sessionCookies()` from the admin
one-time-token flow), not a URL parameter, so there is nothing for
`lint:proof-tokens` or a human reviewer to find in either the URL or the
screenshot chrome.

Tool: Playwright, driven by `capture-live.mjs`, against a stack booted from
`refs/pull/1257/merge` (so the proof is of what would land, not just the
branch tip in isolation) on a GitHub-hosted `ubuntu-latest` runner, with a
real Apptainer sandbox behind the socket arm of the agent engine (not the
in-process arm, which cannot run under this workflow's container either).

Three runs, same scenario and route, across the review cycle as fixes were
pushed:

- Run [`33219175434`](https://github.com/sakibsadmanshajib/hive/actions/runs/33219175434) — superseded by later fixes on the branch.
- Run [`33220069531`](https://github.com/sakibsadmanshajib/hive/actions/runs/33220069531) — superseded by later fixes on the branch.
- Run [`33220730222`](https://github.com/sakibsadmanshajib/hive/actions/runs/33220730222) — final, against the commit under test (`436d4f372164c5c7ad8f119e118ac53103251db7`, "fix: close four gaps the second adversarial review found").

All three passed (`scenario launch-liveness: ok`); each is kept here rather
than only the last, since each backs a screenshot already visible in the
PR's own comment history.

## Screenshots (already posted to the PR, not re-uploaded here)

- `launch-liveness-01-empty-console` — the task list before creation.
- `launch-liveness-02-sandbox-launched` — the task row after the real
  Apptainer sandbox launch answers `HTTP 201 status=queued`.

Both are visible inline on the PR via the three `github-actions` comments
above; this file intentionally does not re-upload them, since they already
render and are hosted on the `ci-proof/pr-1257-run-<id>` side branches the
workflow created, which are never merged and therefore never touched by
this repo's squash-and-delete-branch merge policy on the PR's own head
branch.

## Run log (redacted by the workflow's own `lint:proof-tokens` gate before posting; reproduced verbatim here)

### Run 33219175434

```
scenarios: launch-liveness
launcher runtime dir: /mnt/agent-runtime, live sessions at start: 0
signed in as user 96b3fd38-0c19-41f2-92ae-cbce2731b5b1
token claims: aal,amr,app_metadata,aud,email,exp,iat,is_anonymous,iss,owui_role,phone,role,session_id,sub,tenant_id,tenants,user_metadata
token tenant claim: 343d76e0-6f54-eb32-3a70-451fb91d790d · role=OWNER · aal=aal1
--- scenario: launch-liveness ---
wrote /home/runner/work/hive/hive/docs/proof/agent-visual-proof/run-33219175434/launch-liveness-01-empty-console.png
create answered HTTP 201 in 66ms, status=queued
wrote /home/runner/work/hive/hive/docs/proof/agent-visual-proof/run-33219175434/launch-liveness-02-sandbox-launched.png
row "proof-33219175434 liveness: list the fil…" reached Cancelled
scenario launch-liveness: ok
```

### Run 33220069531

```
scenarios: launch-liveness
launcher runtime dir: /mnt/agent-runtime, live sessions at start: 0
signed in as user 4be24adf-7850-462e-af30-c88a4c22f888
token claims: aal,amr,app_metadata,aud,email,exp,iat,is_anonymous,iss,owui_role,phone,role,session_id,sub,tenant_id,tenants,user_metadata
token tenant claim: 67231c28-6929-c822-d048-831a6a2f1e77 · role=OWNER · aal=aal1
--- scenario: launch-liveness ---
wrote /home/runner/work/hive/hive/docs/proof/agent-visual-proof/run-33220069531/launch-liveness-01-empty-console.png
create answered HTTP 201 in 64ms, status=queued
wrote /home/runner/work/hive/hive/docs/proof/agent-visual-proof/run-33220069531/launch-liveness-02-sandbox-launched.png
row "proof-33220069531 liveness: list the fil…" reached Cancelled
scenario launch-liveness: ok
```

### Run 33220730222 (final, backs the commit under test)

```
scenarios: launch-liveness
launcher runtime dir: /mnt/agent-runtime, live sessions at start: 0
signed in as user 59c5a51b-4bba-40bf-a668-bfb2e1ad5803
token claims: aal,amr,app_metadata,aud,email,exp,iat,is_anonymous,iss,owui_role,phone,role,session_id,sub,tenant_id,tenants,user_metadata
token tenant claim: 64cfc86a-0f2b-8575-9199-fa6b40c7809f · role=OWNER · aal=aal1
--- scenario: launch-liveness ---
wrote /home/runner/work/hive/hive/docs/proof/agent-visual-proof/run-33220730222/launch-liveness-01-empty-console.png
create answered HTTP 201 in 55ms, status=queued
wrote /home/runner/work/hive/hive/docs/proof/agent-visual-proof/run-33220730222/launch-liveness-02-sandbox-launched.png
row "proof-33220730222 liveness: list the fil…" reached Cancelled
scenario launch-liveness: ok
```

No token, JWT, or query-string credential appears in any of the three logs
above (the workflow's own credential gate, `npm run lint:proof-tokens`, ran
against the ephemeral capture directory before the comment was posted, and
this file reproduces that same already-redacted text verbatim, not the raw
pre-redaction log).

## Cleanup performed

None required on this side: the capture ran entirely inside three ephemeral
GitHub-hosted runners, each torn down at the end of its job. Nothing from
any run touched this dev worktree.
