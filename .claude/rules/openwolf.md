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

- .wolf/buglog.jsonl = source of truth, deliberate entries only. One JSON object per line, append only. .gitattributes declares `merge=union`, so parallel branches merge appends automatically. Never rewrite an existing line.
- .wolf/buglog.json = generated aggregate, gitignored. Keeps `openwolf bug search` and the dashboard working. Rebuilt by the session-start hook; rebuild by hand w/ `node .wolf/hooks/bugstore.js sync`
- The post-write hook's `auto-detected` guesses land in the aggregate only, never in the tracked .jsonl. Searchable for the session, dropped on rebuild. Do not promote them by hand: a real bug gets a real entry.
- BEFORE fix any bug/error: `openwolf bug search <term>`, or grep .wolf/buglog.jsonl (one entry per line, grep-friendly)
- AFTER fix any bug, error, failed test, failed build, user-reported problem: ALWAYS log it w/ error_message, root_cause, fix, tags. Use `node .wolf/hooks/bugstore.js add '<json>'`, or append one line to .wolf/buglog.jsonl
- Edit file >2x per session → likely bug → log it
- Store self-check: `node .wolf/hooks/bugstore.selfcheck.js`

## UI

- User ask check/evaluate UI design: run `openwolf designqc` capture screenshots, read from .wolf/designqc-captures/
- User ask change/pick/migrate UI framework: read .wolf/reframe-frameworks.md, ask decision questions, recommend framework, execute w/ framework's prompt
