# Hive Tracking Discipline

The backlog is the plan. A backlog that is not navigable by priority is the same as no backlog: every question about what to do next restarts as a fresh conversation, and work that was real gets lost because nothing was holding it. Issues, labels, milestones and the board are load bearing, not bookkeeping.

Enforced by `.github/workflows/pr-tracking-gate.yml`, a required check on `main`. Read the last section for what that gate cannot see.

## An issue exists before a fix does

1. Every change starts from a GitHub issue. A one line fix noticed while doing something else is still a change and still starts from an issue.
2. Discovering a defect mid task means filing it, not folding it silently into an unrelated pull request. A fix smuggled into someone else's diff has no priority judgement, no milestone, and no way back from the symptom to the change.
3. A pull request body links its issue with `Closes #N`, or `Refs #N` when it delivers only part of that issue. `Fixes` and `Resolves` are accepted as GitHub's synonyms for `Closes`, and `Part of` as a synonym for `Refs`. A bare `#N` in prose is not a link and the gate does not read it as one.
4. Never `Closes` an issue whose acceptance criteria are not all met. An issue closed early is how work is lost: the tracker says done, the behaviour says otherwise, and the remainder exists only in a transcript nobody will reread. Use `Refs #N` and leave it open.

## Exactly one priority label

Every issue carries exactly one of these four, and they already exist in this repository:

| Label | Meaning |
| --- | --- |
| `priority:critical` | A demo blocker or a live outage. Drop everything. |
| `priority:high` | Needed before the demo, not blocking today. |
| `priority:medium` | Real work, schedule it. |
| `priority:low` | Correct but not urgent. |

Two priority labels is the same failure as none, because neither answers what to do next. The older `priority:P0` through `priority:P3` set is retired and does not satisfy this rule; the gate rejects it by name so an issue wearing the old set is corrected rather than quietly counted.

## At least one area label

Every issue carries at least one of `demo-surface`, `money-path`, `internal`. The definitions live on the labels themselves, so they are not restated here and cannot drift from a second copy. More than one is fine, and a change that touches billing on a screen the owner will walk through is genuinely both.

## A scheduled issue carries a milestone

The open milestones are `v1.2.1 demo readiness hotfixes`, `v1.2 agentic surface`, `Hive Enterprise edge-first v1`, `v1.3 device era`.

An issue with no milestone is unscheduled by definition. That is a legitimate state and most of the backlog lives there. A `priority:critical` or `priority:high` issue with no milestone is not a legitimate state, it is a tracking failure: it claims urgency and names no release that carries it, so it will be urgent forever and shipped never.

## Pull requests wear the labels of the issue they close

A pull request carries the same priority label and the same area labels as every issue it closes, so the board reads the same from either side. Someone filtering the board by `priority:critical` sees the open work and the fix in flight for it, instead of having to open each pull request to find out which side of the line it falls on.

## Priority is judged against the demo spine

The owner's locked list of six capabilities is chat, embeddings, voice to text, knowledge work, Cowork, and the coding agent.

Anything that breaks one of those on stage is `priority:critical` regardless of how small the code change is. A one character fix to a broken sign in button is critical; the size of the diff is not evidence about the size of the consequence. An internal correctness issue is not critical however elegant the fix would be, and however much more interesting it is to work on. Elegance is not urgency, and the pull toward the interesting problem is exactly what this rule is here to resist.

## The orchestrator re-triages every session

At the start of each session, before dispatching anything:

1. Open issues with no priority label, or no area label. Label them.
2. Issues whose priority no longer matches reality. A `priority:critical` that has sat for a week was never critical, or is still an outage and nothing was dispatched to it. Both readings demand an edit.
3. Pull requests older than two weeks. Merge, close, or say in the thread what they are waiting on.

None of this is optional maintenance to be done when there is time. There is never time, which is how the backlog got here.

## What the gate checks, and what it cannot

`.github/workflows/pr-tracking-gate.yml` runs `scripts/check-pr-tracking.py` on every pull request and fails when the body links no issue, when a linked issue carries no valid priority label, when it carries no area label, when a `critical` or `high` issue has no milestone, or when the pull request does not wear the labels of an issue it closes. `scripts/test_check_pr_tracking.py` runs inside the required repo policy lints job and proves the check can still go red, because a gate that only ever runs against a corpus it agrees with is indistinguishable from one that exits 0 unconditionally.

Two carve outs, both narrow and both visible in the run log. Dependabot pull requests are exempt, since a bot cannot file an issue first and blocking dependency updates on a human writing one would end with the gate switched off. Pull requests whose entire diff is `.wolf/buglog.jsonl` are exempt, because the buglog only pull request is mandated by `.claude/rules/openwolf.md` and is a record of a fix that already had its own issue.

What the gate cannot check: whether the link is honest. It confirms an issue is referenced and that the issue is triaged, not that the issue describes the diff. `Refs #N` pointing at any well labelled issue passes. That gap is deliberate and unfixable by a linter, which is why the rules above are addressed to the agent writing the pull request rather than to the machine reading it. The gate catches the accident. Only discipline catches the shortcut.
