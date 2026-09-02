#!/usr/bin/env python3
"""Self-checks for scripts/check-pr-tracking.py.

No framework, no network, matching the other scripts/test_*.py in this repo.

The point of this file is narrow: prove the gate can go RED. A tracking check
that only ever runs against pull requests it agrees with is indistinguishable
from one that exits 0 unconditionally, and this repository has shipped that
shape before. So the assertions that matter here are the negative ones, and
each replays a real habit the owner named on 2026-09-02: a fix folded into an
unrelated diff, an issue nobody labelled, a critical with no release attached.
"""
from __future__ import annotations

import importlib.util
from pathlib import Path

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("check_pr_tracking", HERE / "check-pr-tracking.py")
assert SPEC and SPEC.loader
check = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check)

TRIAGED = {
    "labels": ["priority:high", "internal"],
    "milestone": "v1.2.1 demo readiness hotfixes",
    "is_pull_request": False,
}


def pull(body, labels=("priority:high", "internal"), author="sakibsadmanshajib", files=()):
    return {"body": body, "labels": list(labels), "author": author, "files": list(files)}


# --- the link parser -------------------------------------------------------

assert check.links("Closes #1234") == {1234: True}
assert check.links("Fixes #12\nResolves #13") == {12: True, 13: True}
assert check.links("Refs #99") == {99: False}
assert check.links("Part of #99") == {99: False}
assert check.links("Closes https://github.com/sakibsadmanshajib/hive/issues/7") == {7: True}
# Cited both ways in one body: the closing reading wins, because it is stricter.
assert check.links("Refs #5. Closes #5.") == {5: True}
# A bare number in prose is not a link. This is the assertion that keeps the
# gate honest: nearly every pull request body in this repository mentions an
# issue number in passing, and counting those would pass everything.
assert check.links("as issue #873 explains, the union driver is ignored") == {}
assert check.links("see #873") == {}
assert check.links("") == {}

# --- an untracked pull request is rejected ---------------------------------

problems = check.verdict(pull("Refactors the reaper. No issue for this one."), {})
assert problems and "links no issue" in problems[0], problems

# --- an untriaged issue is rejected ----------------------------------------

no_priority = check.verdict(pull("Closes #1"), {1: {"labels": ["internal"], "milestone": None}})
assert any("no priority label" in line for line in no_priority), no_priority

retired = check.verdict(pull("Closes #1"), {1: {"labels": ["priority:P1", "internal"], "milestone": None}})
assert any("retired priority:P1" in line for line in retired), retired

two = check.verdict(
    pull("Closes #1", labels=("priority:high", "priority:low", "internal")),
    {1: {"labels": ["priority:high", "priority:low", "internal"], "milestone": "v1.2 agentic surface"}},
)
assert any("2 priority labels" in line for line in two), two

no_area = check.verdict(
    pull("Closes #1"),
    {1: {"labels": ["priority:high"], "milestone": "v1.2.1 demo readiness hotfixes"}},
)
assert any("no area label" in line for line in no_area), no_area

# --- urgency with no release attached is rejected --------------------------

for urgent in ("priority:critical", "priority:high"):
    unscheduled = check.verdict(
        pull("Closes #1", labels=(urgent, "demo-surface")),
        {1: {"labels": [urgent, "demo-surface"], "milestone": None}},
    )
    assert any("no milestone" in line for line in unscheduled), (urgent, unscheduled)

# A medium with no milestone is unscheduled, which is a legitimate state.
assert check.verdict(
    pull("Closes #1", labels=("priority:medium", "internal")),
    {1: {"labels": ["priority:medium", "internal"], "milestone": None}},
) == []

# --- the board must read the same from either side -------------------------

mismatch = check.verdict(
    pull("Closes #1", labels=("internal",)),
    {1: {"labels": ["priority:critical", "demo-surface"], "milestone": "v1.2.1 demo readiness hotfixes"}},
)
assert any("does not carry its labels" in line for line in mismatch), mismatch

# Refs, not Closes, so the pull request is not required to mirror the labels:
# it delivers part of the issue and may well be a different shape of work.
assert check.verdict(
    pull("Refs #1", labels=[]),
    {1: {"labels": ["priority:critical", "demo-surface"], "milestone": "v1.2.1 demo readiness hotfixes"}},
) == []

# --- a link that points at a pull request is not a link to an issue --------

not_an_issue = check.verdict(pull("Closes #1"), {1: {"labels": [], "is_pull_request": True}})
assert any("is a pull request, not an issue" in line for line in not_an_issue), not_an_issue

# An unreadable issue fails rather than passing quietly.
unreadable = check.verdict(pull("Closes #4242"), {})
assert any("could not be read" in line for line in unreadable), unreadable

# --- the tracked case still passes -----------------------------------------

assert check.verdict(pull("Closes #1234"), {1234: TRIAGED}) == []
assert check.verdict(pull("Refs #1234\n\nPartial delivery."), {1234: TRIAGED}) == []

# --- carve outs are narrow -------------------------------------------------

assert check.exempt("dependabot[bot]", ["apps/web-console/package-lock.json"])
assert check.exempt("app/dependabot", [])
assert check.exempt("sakibsadmanshajib", [".wolf/buglog.jsonl"])
# One file too many, and the buglog carve out is gone. Otherwise it becomes a
# way to attach any diff to an exempt path.
assert check.exempt("sakibsadmanshajib", [".wolf/buglog.jsonl", "apps/edge-api/main.go"]) is None
assert check.exempt("sakibsadmanshajib", ["docs/README.md"]) is None
assert check.exempt("sakibsadmanshajib", []) is None

print("scripts/check-pr-tracking.py self-checks passed")
