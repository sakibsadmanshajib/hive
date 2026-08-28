# How Hive actually tracks work on GitHub

This describes the process as it is practiced, not an aspirational target. It
was written from a survey of the live repository state on 2026-08-28 (200 open
issues, 16 open PRs, 4 milestones, 75 labels). Update it when the practice
changes; do not let it drift into describing a process nobody follows. Given
how fast this repo files and merges (a multi-agent burst can move the issue
count by dozens within the hour this document was written in), read every
count here as a shape, not a live number: re-run the `gh` queries in each
section below for the current figure.

## Issue lifecycle

1. **Filed.** Most issues are filed by an agent (or the owner) during active
   work: a bug found while fixing something else, a residual finding from a
   security sweep, a gap surfaced by adversarial PR review. It is common for
   one issue to spawn two or three follow-ups (see "Chained findings" below).
   A filed issue should cite the file(s) and function(s) involved and, where
   the finding came from another issue or PR, link back to it in the body.
2. **Labeled.** `kind:*` and `area:*` at minimum; `priority:*` and `risk:*`
   where the severity is clear at filing time. `status:needs-triage` marks an
   issue nobody has yet confirmed the fix direction for.
3. **Milestoned.** Attached to whichever of the four active milestones (below)
   its scope matches. An issue that fits none of them stays unmilestoned
   rather than being force-fit; that is a legitimate state, not an oversight,
   though a growing pile of unmilestoned issues is worth a periodic look.
4. **Fixed on a branch, PR opened immediately** (draft is fine) so review
   comments land on the PR itself rather than a private report. See
   `.claude/rules/orchestrator.md` "Coding pipeline" for the full dispatch
   sequence this repo's agents follow.
5. **Reviewed.** `adversarial-pr-review` in pipeline mode: CodeRabbit, a
   language-specific reviewer, a plain adversarial pass, plus domain
   specialists (security, database, etc.) by diff shape. Findings post as
   inline PR comments; the ones worth a standalone issue get filed rather than
   silently fixed in the same PR, which is why one PR's review commonly
   produces two or three new issues (#1222's review produced both #1235,
   which chained into #1252, and the independent #1237 — see "Chained
   findings" below for the #1235/#1252 thread specifically).
6. **Merged.** Squash merge, branch deleted, once CI is green and every review
   thread is resolved. Full gate: `.github/MERGE-POLICY.md`.
7. **Closed with a citation.** An issue is closed by referencing the PR (or
   commit) that actually resolved it, not by inference. `Fixes #NNNN` /
   `Resolves #NNNN` in a PR body auto-closes on merge; where that syntax was
   not used, closing is a manual, cited action.

### Chained findings

A distinctive pattern in this repo: fixing one issue's exact scope
deliberately surfaces adjacent, out-of-scope instances of the same defect
class, and those get filed as new issues rather than folded into the fix in
flight. Examples from 2026-08-28 alone: #1222 (edge-api provider-identity
leak) left the batch executor out of scope, which became #1235; fixing #1235
surfaced a second batch code path, which became #1252; #1250 (silent body
truncation on one endpoint) triggered a repo-wide sweep, which became #1255.
This is intentional discipline (fix the named scope, name the rest), not
scope creep, and it means a "why are there so many issues" read is often
wrong: it is usually the same investigation getting narrower, not the backlog
growing unbounded.

## Label taxonomy

The scheme in active use, newest first:

| Prefix | Meaning | Values seen |
|---|---|---|
| `kind:` | What the change fundamentally is | `feature`, `bug`, `hardening`, `docs`, `chore`, `test`, `perf` |
| `area:` | Which surface it touches | `api`, `web`, `providers`, `billing`, `auth`, `ops`, `docs`, `oss` |
| `priority:` | How urgently it needs attention | `P0`–`P3` |
| `risk:` | What kind of blast radius a miss has | `security`, `financial`, `availability`, `compliance` |
| `status:` | Where it sits in triage/execution | `needs-triage`, `blocked`, `ready`, `in-progress` |
| `horizon:` | Rough time bucket, looser than a milestone | `v1.2`, `v1.3`, `watch` |
| `theme:` | Cross-cutting initiative, spans milestones | `agentic`, `billing-growth`, `device`, `memory` |

Plus GitHub's stock defaults (`bug`, `enhancement`, `documentation`,
`duplicate`, `good first issue`, `help wanted`, `invalid`, `question`,
`wontfix`), a handful of legacy pre-taxonomy labels still on old issues
(`bug`, `security`, `enhancement` applied bare, without the `kind:`/`risk:`
equivalent), a set of narrow, single-purpose CI/ops labels (`ci-failure:*`,
`verify-negative:*`, `run-*`, `dependencies`, `dummy-noop`, `javascript`,
`go`, `docker`, `github_actions`) that exist to drive or record automated
workflow behavior rather than to classify an issue for a human, and a
smaller, looser tail of one-off or campaign labels (`agents`, `sandbox`,
`edge`, `sovereign`, `pivot`, `quick-win`, `idea`, `roadmap`, `non-eng`,
`audit-2026-07-19`, `deploy-drift-test:simulate-divergence`) that were never
folded into the prefix scheme above. That tail is not a gap worth closing by
renaming; most of those tags mark a single past sweep or a one-time
initiative and are fine left as-is per the no-rename rule below. The table
above is the scheme to use going forward, not a complete inventory of all 75
labels the repo currently carries — run `gh label list` for that.

**Do not rename any of these.** Every rename breaks a saved filter or a piece
of automation keyed on the label's exact name. Extend the taxonomy by adding a
new value under an existing prefix; retire a label by simply no longer
applying it, not by deleting or renaming it out from under open issues.

As of this survey, 98 of 200 open issues carried no label at all and a further
32 carried only legacy/stock labels with none of the `kind:`/`area:` scheme.
That is the real coverage gap: roughly two-thirds of the open backlog is not
classified in a way that supports filtering by area or kind. This document
does not close that gap by itself; it names it so the next pass has a target.

## Milestones

Four active milestones, each with a real due date and a scope description
readable via `gh api repos/sakibsadmanshajib/hive/milestones`:

- **v1.2.1 demo readiness hotfixes** (due 2026-09-05): ship-blocking defects
  found during demo prep, deploy failures, broken surfaces, security P0s that
  gate customer demos. The fast-moving, small-scope milestone: land it,
  close it, move on.
- **v1.2 agentic surface** (due 2026-09-12): the agent-native product surface
  work, composer modes, agent run surface, native agents.
- **Hive Enterprise edge-first v1** (due 2026-10-31): self-hosted data plane
  parity, sovereignty posture, the security P0 family, backups, admin/RBAC
  hardening.
- **v1.3 device era** (due 2026-12-15): desktop app, Windows sandbox, local
  runtime, cross-platform CLI. Gated behind the Enterprise milestone.

A milestone is created only when a genuine new grouping emerges that these
four don't cover; do not open a milestone for a single issue or a short-lived
push, use `theme:` or `horizon:` labels for that instead.

**Note on drift:** the Obsidian vault's roadmap MOC records milestone open
counts from 2026-08-22 (`v1.2 agentic surface` 14 open, `Hive Enterprise
edge-first v1` 7 open). Six days later those stood at 129 and 101 open
respectively. The vault MOC already flags itself as stale and points to
GitHub as the live source; this doc is the confirmation that the pointer is
correct: GitHub, not the vault, is where milestone state should be read from.

## Board

GitHub Project 3, "Hive Roadmap": `https://github.com/users/sakibsadmanshajib/projects/3`.
Status column values: `Backlog`, `Ready`, `In Progress`, `In Review`, `Needs
Testing`, `Done`, `Blocked`. Every open issue and open PR belongs on it; an
item that never got added does not mean it is not being worked, it means the
board under-represents current work, which defeats the board's purpose as a
single view of what's live.

## Definition of done

- Tests pass (unit at minimum; the merge gate below is the real bar).
- CI green: `.github/MERGE-POLICY.md`'s six required checks.
- Every PR review thread resolved, not just the ones with a code change in
  response, a reasoned rebuttal closes a thread too.
- A UI-touching change carries a screenshot or recording posted into the PR
  itself (`.claude/rules/orchestrator.md`, "Visual proof before merge").
- A bug fix carries a buglog entry (`.wolf/buglog.jsonl`), landed on `main`
  through a dedicated buglog-only PR per `.claude/rules/openwolf.md`, never
  appended directly on the fix branch.
- The issue that named the problem is closed with a citation to the PR or
  commit that fixed it.

## Where the rest of this lives

- Merge gate mechanics: `.github/MERGE-POLICY.md`
- Coding pipeline (plan, spec, TDD, review, merge, deploy): `.claude/rules/orchestrator.md`
- OpenWolf state files and bug memory: `.claude/rules/openwolf.md`
- Roadmap and milestone ground truth, when GitHub itself isn't enough context: the Obsidian vault, `hive/MOC-roadmap-current.md` and `hive/MOC-timeline-current.md`
