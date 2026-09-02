#!/usr/bin/env python3
"""Refuse a pull request that is not attached to a triaged issue.

Why this exists
---------------
The owner's finding, 2026-09-02: pull requests, milestones, the board and the
labels were not being maintained. Fixes were folded into unrelated diffs, so
the backlog could not answer what to work on next, and issues that were real
existed only in a transcript. The rules are in
.claude/rules/tracking-discipline.md; this is the half of them a machine can
check.

What it enforces, per pull request:

  * the body links an issue in this repository with Closes/Fixes/Resolves #N
    or Refs/Part of #N, either bare or as a full github.com URL;
  * every linked issue carries exactly one of the four priority labels;
  * every linked issue carries at least one area label;
  * a priority:critical or priority:high issue carries a milestone, because an
    issue claiming urgency with no release attached is urgent forever;
  * the pull request wears the priority and area labels of every issue it
    closes, so the board reads the same from either side.

Two carve outs, both printed in the run log rather than silent. Dependabot,
and only Dependabot, cannot file an issue, and a gate that blocks dependency
updates gets switched off. A pull request whose whole diff is .wolf/buglog.jsonl is the buglog only
pull request that .claude/rules/openwolf.md mandates, recording a fix that
already had an issue of its own.

What it cannot check: whether the link is honest. It confirms a reference to a
triaged issue, not that the issue describes the diff.

Usage
-----
    check-pr-tracking.py --repo owner/name --pr 123
    check-pr-tracking.py --fixture path.json     # offline, for tests

Exit 0 when the pull request is tracked, 1 otherwise. A pull request or issue
that cannot be read is also exit 1: this check has nothing to say without one,
and saying nothing quietly is the failure mode it exists to remove.
"""
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys

PRIORITY_LABELS = (
    "priority:critical",
    "priority:high",
    "priority:medium",
    "priority:low",
)
# The predecessor set, retired but still on old issues. Named explicitly so an
# issue wearing it gets a useful message instead of "no priority label", which
# reads like the label was forgotten rather than superseded.
RETIRED_PRIORITY = re.compile(r"^priority:P[0-3]$", re.IGNORECASE)
AREA_LABELS = ("demo-surface", "money-path", "internal")
NEEDS_MILESTONE = ("priority:critical", "priority:high")
# The carve out is Dependabot, not bots in general: matching every `*[bot]` or
# `app/*` author would hand the bypass to any other app installed on the
# repository, and to a human pull request opened through one. GitHub reports
# the author as `dependabot[bot]` on the REST API and `app/dependabot` in some
# `gh` output shapes, so both spellings are named, with the retired preview app
# alongside them.
DEPENDABOT_AUTHORS = frozenset(
    {"dependabot[bot]", "app/dependabot", "dependabot", "dependabot-preview[bot]", "app/dependabot-preview"}
)
BUGLOG_PATH = ".wolf/buglog.jsonl"

# Closing verbs bind the pull request to the whole issue; reference verbs claim
# only a part of it. A bare "#N" is deliberately not matched: this repository's
# pull request bodies cite issue numbers in prose constantly ("issue #873
# explains why"), and treating those as the tracking link would pass every
# untracked pull request that happened to mention history.
CLOSING_VERBS = ("close", "closes", "closed", "fix", "fixes", "fixed", "resolve", "resolves", "resolved")
REFERENCE_VERBS = ("refs", "ref", "references", "part of", "re")
_VERBS = "|".join(sorted((*CLOSING_VERBS, *REFERENCE_VERBS), key=len, reverse=True))
LINK_RE = re.compile(
    rf"\b(?P<verb>{_VERBS})\b\s*:?\s*"
    r"(?:#|https://github\.com/(?P<repo>[\w.-]+/[\w.-]+)/issues/)(?P<number>\d+)",
    re.IGNORECASE,
)


def links(body: str, repo: str | None = None) -> dict[int, bool]:
    """Issue number to "this pull request closes it".

    A number cited both ways in one body counts as closing, which is the
    stricter reading and so the safe one.

    A link written as a full URL only counts when it points at `repo`. The
    number in `https://github.com/someone-else/thing/issues/7` says nothing
    about this repository's #7, and without this the gate would validate the
    local issue that happens to share the number and pass. Called with no
    `repo` the parse is repository blind, which is how the caller distinguishes
    "linked nothing" from "linked somewhere else".
    """
    found: dict[int, bool] = {}
    for match in LINK_RE.finditer(body or ""):
        target = match.group("repo")
        if target and repo and target.lower() != repo.lower():
            continue
        number = int(match.group("number"))
        closing = match.group("verb").lower() in CLOSING_VERBS
        found[number] = found.get(number, False) or closing
    return found


def priorities(labels: list[str]) -> list[str]:
    return [label for label in labels if label in PRIORITY_LABELS]


def areas(labels: list[str]) -> list[str]:
    return [label for label in labels if label in AREA_LABELS]


def exempt(author: str, files: list[str]) -> str | None:
    """The reason this pull request is exempt, or None."""
    if author.lower() in DEPENDABOT_AUTHORS:
        return f"author {author} is Dependabot and cannot file an issue first"
    if files and set(files) == {BUGLOG_PATH}:
        return f"the whole diff is {BUGLOG_PATH}, the buglog only pull request openwolf.md mandates"
    return None


def verdict(pull: dict, issues: dict[int, dict]) -> list[str]:
    """Every reason to reject, as printable lines. Empty means tracked."""
    problems: list[str] = []
    repo = pull.get("repo")
    referenced = links(pull.get("body") or "", repo)
    if not referenced:
        if repo and links(pull.get("body") or ""):
            return [
                f"the body links an issue in another repository, and none in {repo}. "
                "This gate can only confirm that an issue here is triaged. See "
                ".claude/rules/tracking-discipline.md"
            ]
        return [
            "the body links no issue. Add `Closes #N`, or `Refs #N` when this "
            "delivers only part of the issue. See .claude/rules/tracking-discipline.md"
        ]

    pull_labels = list(pull.get("labels") or [])
    for number in sorted(referenced):
        issue = issues.get(number)
        if issue is None:
            problems.append(f"#{number} could not be read")
            continue
        if issue.get("is_pull_request"):
            problems.append(f"#{number} is a pull request, not an issue")
            continue

        labels = list(issue.get("labels") or [])
        found = priorities(labels)
        if len(found) > 1:
            problems.append(f"#{number} carries {len(found)} priority labels ({', '.join(sorted(found))}); exactly one is allowed")
        elif not found:
            retired = [label for label in labels if RETIRED_PRIORITY.match(label)]
            if retired:
                problems.append(f"#{number} carries the retired {retired[0]}; replace it with one of {', '.join(PRIORITY_LABELS)}")
            else:
                problems.append(f"#{number} carries no priority label; add one of {', '.join(PRIORITY_LABELS)}")

        if not areas(labels):
            problems.append(f"#{number} carries no area label; add one of {', '.join(AREA_LABELS)}")

        if any(label in NEEDS_MILESTONE for label in found) and not issue.get("milestone"):
            problems.append(f"#{number} is {found[0]} with no milestone; urgent work names the release that carries it")

        if referenced[number]:
            missing = [label for label in priorities(labels) + areas(labels) if label not in pull_labels]
            if missing:
                problems.append(f"this pull request closes #{number} but does not carry its labels: {', '.join(missing)}")

    return problems


def gh_json(args: list[str]) -> dict:
    result = subprocess.run(["gh", *args], capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"gh {' '.join(args)} failed: {result.stderr.strip()}")
    return json.loads(result.stdout)


def fetch(repo: str, number: int) -> tuple[dict, dict[int, dict]]:
    raw = gh_json(["api", f"repos/{repo}/pulls/{number}"])
    pull = {
        "repo": repo,
        "author": (raw.get("user") or {}).get("login", ""),
        "body": raw.get("body") or "",
        "labels": [label["name"] for label in raw.get("labels") or []],
        "files": [],
    }
    # Only needed for the buglog carve out, and only worth a request when the
    # diff is small enough to possibly be that one file.
    if (raw.get("changed_files") or 0) == 1:
        pull["files"] = [entry["filename"] for entry in gh_json(["api", f"repos/{repo}/pulls/{number}/files"])]

    issues: dict[int, dict] = {}
    for target in links(pull["body"], repo):
        try:
            item = gh_json(["api", f"repos/{repo}/issues/{target}"])
        except RuntimeError:
            continue
        issues[target] = {
            "labels": [label["name"] for label in item.get("labels") or []],
            "milestone": (item.get("milestone") or {}).get("title"),
            "is_pull_request": "pull_request" in item,
        }
    return pull, issues


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--repo", help="owner/name")
    parser.add_argument("--pr", type=int, help="pull request number")
    parser.add_argument("--fixture", help="read the pull request and its issues from a JSON file instead of the API")
    args = parser.parse_args()

    if args.fixture:
        data = json.loads(open(args.fixture, encoding="utf-8").read())
        pull = data
        issues = {int(key): value for key, value in (data.get("issues") or {}).items()}
    elif args.repo and args.pr:
        try:
            pull, issues = fetch(args.repo, args.pr)
        except RuntimeError as error:
            print(f"FAIL: {error}", file=sys.stderr)
            return 1
    else:
        parser.error("need --repo with --pr, or --fixture")

    reason = exempt(pull.get("author") or "", list(pull.get("files") or []))
    if reason:
        print(f"EXEMPT: {reason}")
        return 0

    problems = verdict(pull, issues)
    if not problems:
        print("OK: tracked. Every linked issue is triaged and the labels agree.")
        return 0

    print("This pull request is not tracked:", file=sys.stderr)
    for problem in problems:
        print(f"  - {problem}", file=sys.stderr)
    print("\nRules: .claude/rules/tracking-discipline.md", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
