# Hive North Star Goal (owner-set, orchestrator-executed)

**Last refreshed 2026-08-29.** This file is the terse goal index. Planning ground truth (milestone detail, timeline, requirements traceability, UAT results, deferred scope) lives in the Obsidian vault (hive/ MOCs and timeline), and issue-level truth lives on GitHub. Refresh or retire this file whenever it stops matching either; a document that lies is a defect.

## Mission
Be the AI platform of Bangladesh and a credible data-sovereign option for regulated buyers: a chat workstation plus an OpenAI-compatible developer API, prepaid BDT billing, one product in two modes. Hive is the hosted SaaS; Hive Enterprise is the customer-hosted, data-sovereign deployment. Built to investor-demo quality on a CTO operating budget under $50 per month.

## Current goal: demo readiness against the six locked capabilities
The owner locked demo scope on 2026-08-11 to six capabilities: chat functionality, embeddings, voice to text, knowledge work, Cowork, and the coding agent. Parity between Cowork and the chat surface is part of that requirement, not polish. Anything outside the list is not a demo blocker; anything inside it is. Full entry and its consequences: `.wolf/decisions.md` D-061.

## How work is tracked
The phase-numbering frame (phases 1 through 20) is retired. v1.0 developer-api-core shipped 2026-04-21; v1.1 closed out in late July 2026. Work now tracks through GitHub issues and pull requests against four milestones:

- `v1.2.1 demo readiness hotfixes` (due 2026-09-05)
- `v1.2 agentic surface` (due 2026-09-12)
- `Hive Enterprise edge-first v1` (due 2026-10-31)
- `v1.3 device era` (due 2026-12-15)

Roadmap board: https://github.com/users/sakibsadmanshajib/projects/3. For live counts read the milestones themselves; this file deliberately carries no numbers that go stale between sessions.

The MVP checklist this file used to carry targeted 2026-06-20 and is retired. Git history has it if anyone needs the record.

## Operating commitments (how the orchestrator does not get stuck)
1. `.wolf/fleet.json` updated on every dispatch, completion, and failure (state, reason, relaunched-as). No silent deaths.
2. Every turn opens with a ground-truth sweep when any agent is in flight. Stalled longer than 20 minutes in a wait state is presumed dead; relaunch. Verify against remote refs and `gh` state, never against a delivered message alone.
3. This file states the current goal; the owner can ask "goal status" at any time and get its truth. If the answer would be wrong, fix the file first.
4. No agent runs `git clean`, `git reset --hard`, or any destructive git operation on the shared checkout. This rule exists because `.wolf/` was wiped by exactly that on 2026-06-12. Since D-025 the directory splits by writer: curated files are tracked and reviewed in pull requests, hook-written telemetry is gitignored and must never be committed.
