---
description: OpenWolf protocol enforcement — active on all files
globs: **/*
---

## What is tracked

- Tracked + curated (edit deliberately, reviewed in PRs): .wolf/cerebrum.md, .wolf/decisions.md, .wolf/buglog.jsonl, .wolf/GOAL.md, .wolf/fleet.json, .wolf/cost-ledger.md, .wolf/hooks/*.js
- Untracked telemetry (hook-owned, gitignored — NEVER hand-edit, NEVER commit, NEVER `git add -f`): .wolf/anatomy.md, .wolf/memory.md, .wolf/token-ledger.json, .wolf/hooks/_session.json, .wolf/buglog.json
- Invariant: anything a hook rewrites on every tool call stays untracked. Tracking it blocks `git pull --ff-only` on the shared checkout and conflicts across parallel worktrees.

## Reading

- Check .wolf/anatomy.md before read any project file. Hook-maintained per checkout; regenerate w/ `openwolf scan` if missing or stale
- Check .wolf/cerebrum.md Do-Not-Repeat list before generate code
- Check .wolf/decisions.md before any design, spec, plan, or implementation

## Writing

- After write/edit files, do NOTHING by hand: the post-write hook updates .wolf/anatomy.md + appends .wolf/memory.md automatically
- After user correction, update .wolf/cerebrum.md immediately (Preferences, Learnings, or Do-Not-Repeat)
- LEARN every interaction: discover convention, user preference, project pattern → add .wolf/cerebrum.md. Low threshold — doubt = log.

## Bug memory

- .wolf/buglog.jsonl = source of truth, deliberate entries only. One JSON object per line, append only. Never rewrite an existing line.
- .wolf/buglog.json = generated aggregate, gitignored. Keeps `openwolf bug search` and the dashboard working. Rebuilt by the session-start hook; rebuild by hand w/ `node .wolf/hooks/bugstore.js sync`
- The post-write hook's `auto-detected` guesses land in the aggregate only, never in the tracked .jsonl. Searchable for the session, dropped on rebuild. Do not promote them by hand: a real bug gets a real entry.
- BEFORE fix any bug/error: `openwolf bug search <term>`, or grep .wolf/buglog.jsonl (one entry per line, grep-friendly)
- AFTER fix any bug, error, failed test, failed build, user-reported problem: ALWAYS log it w/ error_message, root_cause, fix, tags. The requirement is unchanged. What changed is where the line is written: see "Entries land on main" below.
- Edit file >2x per session → likely bug → log it
- Store self-check: `node .wolf/hooks/bugstore.selfcheck.js`

### Entries land on main, never on a feature branch (issue #873)

- NEVER append to .wolf/buglog.jsonl on a fix or feature branch. Not "prefer not to": never. A branch that appends a line conflicts with every other branch that appended one, and while a pull request is unmergeable GitHub builds no `refs/pull/N/merge`, creates no `pull_request` run, and shows zero checks. The required-status gate then blocks the merge for a reason the page never states, which reads as a broken workflow and sends the next agent to debug CI triggers that were never broken.
- `merge=union` in .gitattributes stays, and still resolves concurrent appends in local merges and rebases. It does NOT apply on GitHub: the server-side merge ignores the union driver, so two branches that both appended are in hard conflict there. Do not re-derive this the hard way.
- The route today is manual, and there is no post-merge automation yet. After the fix has merged to main, open a separate pull request whose diff is .wolf/buglog.jsonl and nothing else, and merge that. Keep one such pull request open at a time across all agents, so no two of them ever hold competing versions of the file. Batching several entries into one of these is fine and preferred.
- Those pull requests merge cheaply: .wolf/buglog.jsonl is on the inert-path allowlist in .github/workflows/ci.yml, so the six required checks report green without running their heavy steps.
- While the fix is still in flight, carry the entry in the fix pull request's body, under a "Buglog entry" heading, as the JSON line to be appended. That keeps the record attached to the fix and reviewable, and it is what the follow-up pull request copies from. Do not let the entry live only in a session transcript.
- Automating this (a post-merge job that appends the entry from the merged pull request body) is tracked in issue #873. Until it exists, do it by hand rather than skipping it.
- `node .wolf/hooks/bugstore.js add '<json>'` appends to the tracked .wolf/buglog.jsonl itself, so do NOT run it while on a fix branch: it dirties the tracked file in the working tree and the next `git add -A` sweeps it into the fix commit. Run it on the buglog-only branch cut from main, where appending is the whole point of the branch.

## UI

- User ask check/evaluate UI design: run `openwolf designqc` capture screenshots, read from .wolf/designqc-captures/
- User ask change/pick/migrate UI framework: read .wolf/reframe-frameworks.md, ask decision questions, recommend framework, execute w/ framework's prompt
