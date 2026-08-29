#!/usr/bin/env python3
"""Name the SDK conformance tests that actually failed, from the suite logs.

Why this exists (issue #1374). The live-integration job runs three suites in
one step and, on failure, printed `tail -c 30000` of each log and exited 1.
GitHub then showed `Process completed with exit code 1` as the step's only
annotation, and the tracking issue this job files carried whatever the
"Name the real cause when the upstream refused" step had decided. Between
2026-08-29 00:59 and 12:09 that combination filed one issue and eleven
comments on it, six of which said

    Cause identified from the container logs: the provider rate limited us
    (429).

while the real failures over that window were, in order: three stale strict
XPASS markers (issues #1259, #1260, #1261), one more (#1274), one stale
vitest `it.fails` (#1317), `500 Failed to store file` from an S3 endpoint
pointing at a deleted project (#1324), and a broken `total_tokens ==
prompt_tokens + completion_tokens` invariant. Not one of them was an upstream
refusal. The 429 was real but incidental: one free-pool member (the Gemini
key) spends its free-tier daily allowance most days, the pool routes around
it by design, and the "Probe each free pool member" step already says so.

So the lane was red, loud, and wrong about why, and the one issue it keeps
open outlived five unrelated defects. A reader could only find the real cause
by downloading the job log and scrolling. This script makes the run page and
the tracking issue name the failing tests instead.

It never changes what passes or fails. It reads the logs a failing step
already wrote and turns them into annotations.

Exit code: 0 always. This runs inside a step that has already decided the
job's fate; its job is to explain that fate, not to add a second failure of
its own. The same reasoning classify-upstream-refusal.py records.

Self-check: python3 scripts/extract-sdk-failures.py --selfcheck
"""
from __future__ import annotations

import os
import pathlib
import re
import sys
import tempfile
import uuid

# The three suites, in the order the workflow starts them.
SUITES = ("sdk-tests-js", "sdk-tests-py", "sdk-tests-java")

# How many named failures to annotate. A suite that fails wholesale can name
# every test it has, and fifty annotations is a wall nobody reads.
MAX_ANNOTATIONS = 20
# Reasons are one line in the annotation. Vitest diffs run to a screenful.
MAX_REASON = 200

# ── vitest ──────────────────────────────────────────────────────────────────
# The "Failed Tests" block prints one of these per failing test:
#   ` FAIL  tests/usage/usage-accounting.test.ts > Usage accounting > name`
# and, when a file fails to collect at all:
#   ` FAIL  tests/x.test.ts [ tests/x.test.ts ]`
# Both are worth naming, so this matches on `FAIL` and takes the remainder.
VITEST_FAIL = re.compile(r"^\s*FAIL\s+(\S.*?)\s*$")
# The line under it carries the reason. `Error: Expect test to fail` is the
# shape a stale `it.fails` marker takes, which is a real recurring cause here
# and reads as nonsense without the test name in front of it.
VITEST_REASON = re.compile(
    r"^\s*((?:\w*(?:Error|Exception)|expected|Expected)\b.*)$"
)

# ── pytest ──────────────────────────────────────────────────────────────────
# From the "short test summary info" section:
#   `FAILED tests/test_anthropic_messages.py::test_x - [XPASS(strict)] issue ...`
#   `ERROR tests/test_y.py::test_z`
PYTEST_FAIL = re.compile(r"^(?:FAILED|ERROR)\s+(\S+)(?:\s+-\s+(.*))?$")
# pytest truncates the summary line's reason to the terminal width and often
# drops it entirely. The FAILURES section above it carries the full text under
# a rule naming the bare function:
#   `______________ test_streaming_event_sequence_integrity ______________`
#   `[XPASS(strict)] issue #1274: content_block_start omits an empty text ...`
# That reason is the most useful fact in the whole log for the recurring
# stale-marker cause, since it names the issue the marker refers to.
PYTEST_SECTION = re.compile(r"^_{3,}\s+(\S+)\s+_{3,}$")

# ── gradle / junit ──────────────────────────────────────────────────────────
#   `com.hive.ChatCompletionsTest > basicCompletion() FAILED`
# `> Task :test FAILED` is the task, not a test, and must not be named as one.
GRADLE_FAIL = re.compile(r"^(?!\s*> Task\b)\s*(\S.*\s>\s.*?)\s+FAILED\s*$")


def _clip(text: str) -> str:
    text = " ".join(text.split())
    return text if len(text) <= MAX_REASON else text[: MAX_REASON - 1] + "…"


def parse_vitest(text: str) -> list[tuple[str, str]]:
    lines = text.splitlines()
    out: list[tuple[str, str]] = []
    for i, line in enumerate(lines):
        match = VITEST_FAIL.match(line)
        if not match:
            continue
        reason = ""
        for follow in lines[i + 1 : i + 4]:
            if not follow.strip():
                continue
            hit = VITEST_REASON.match(follow)
            if hit:
                reason = _clip(hit.group(1))
            break
        out.append((match.group(1), reason))
    return _dedupe(out)


def parse_pytest(text: str) -> list[tuple[str, str]]:
    lines = text.splitlines()
    detail = _pytest_details(lines)
    out: list[tuple[str, str]] = []
    for line in lines:
        match = PYTEST_FAIL.match(line.strip())
        if not match:
            continue
        nodeid = match.group(1)
        reason = match.group(2) or detail.get(nodeid.rsplit("::", 1)[-1], "")
        out.append((nodeid, _clip(reason)))
    return _dedupe(out)


def _pytest_details(lines: list[str]) -> dict[str, str]:
    """Bare test function name -> the first reason line under its rule."""
    found: dict[str, str] = {}
    for i, line in enumerate(lines):
        match = PYTEST_SECTION.match(line.strip())
        if not match:
            continue
        for follow in lines[i + 1 : i + 3]:
            if follow.strip():
                found.setdefault(match.group(1), follow.strip())
                break
    return found


def parse_gradle(text: str) -> list[tuple[str, str]]:
    out: list[tuple[str, str]] = []
    for line in text.splitlines():
        match = GRADLE_FAIL.match(line)
        if match:
            out.append((match.group(1).strip(), ""))
    return _dedupe(out)


def _dedupe(pairs: list[tuple[str, str]]) -> list[tuple[str, str]]:
    """First occurrence wins, so the reason comes from the detailed block."""
    seen: dict[str, str] = {}
    for name, reason in pairs:
        if name not in seen or (not seen[name] and reason):
            seen[name] = reason
    return list(seen.items())


PARSERS = {
    "sdk-tests-js": parse_vitest,
    "sdk-tests-py": parse_pytest,
    "sdk-tests-java": parse_gradle,
}


def failures_for(suite: str, text: str) -> list[tuple[str, str]]:
    return PARSERS[suite](text) if suite in PARSERS else []


def _read(path: pathlib.Path) -> str:
    try:
        return path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""


def collect(log_dir: pathlib.Path) -> list[str]:
    """One human line per failure, across all three suites, in suite order."""
    named: list[str] = []
    for suite in SUITES:
        rc_text = _read(log_dir / f"{suite}.rc").strip()
        if rc_text == "0":
            continue
        rc = rc_text or "unknown"
        found = failures_for(suite, _read(log_dir / f"{suite}.log"))
        if found:
            for name, reason in found:
                named.append(f"{suite}: {name}" + (f" -- {reason}" if reason else ""))
            continue
        # rc says the suite failed and nothing in its log names a test. That
        # is the wholesale case (container never started, suite timed out,
        # runner killed it), and it is the one case where an upstream refusal
        # IS the likely cause. Saying so is what keeps issue #1088's fix
        # working rather than replacing one blind report with another.
        named.append(
            f"{suite}: exited {rc} with no named test failure, so it died "
            "wholesale rather than failing an assertion. Look at the tail "
            "above and at the compose-logs artifact."
        )
    return named


def report(log_dir: pathlib.Path) -> int:
    named = collect(log_dir)
    if not named:
        # Every suite exited 0. The step that calls this only runs when the
        # job is failing, so a caller reaching here failed somewhere else,
        # and claiming a test failure would be the same fabrication in a new
        # place. Say nothing rather than guess.
        print(
            "no SDK suite reported a non-zero exit, so the failure is not in "
            "the conformance suites"
        )
        return 0

    print(f"named SDK failures ({len(named)}):")
    for line in named[:MAX_ANNOTATIONS]:
        print(f"::error::{line}")
    if len(named) > MAX_ANNOTATIONS:
        print(
            f"::warning::{len(named) - MAX_ANNOTATIONS} further failure(s) not "
            "annotated; the full list is in the step log and the job summary"
        )
        for line in named[MAX_ANNOTATIONS:]:
            print(line)

    _write_env(named)
    _write_summary(named)
    return 0


def _write_env(named: list[str]) -> None:
    """Hand the list to later steps, which file the tracking issue with it.

    A heredoc, because the value is multi-line: the single-line `K=V` form
    that classify-upstream-refusal.py uses would corrupt every later entry.
    The delimiter is random per invocation so no test name can close the
    block early.
    """
    github_env = os.environ.get("GITHUB_ENV")
    if not github_env:
        return
    delimiter = f"SDK_FAILURES_{uuid.uuid4().hex}"
    body = "\n".join(line for line in named if line.strip() != delimiter)
    with open(github_env, "a", encoding="utf-8") as handle:
        handle.write(f"SDK_FAILED_TESTS<<{delimiter}\n{body}\n{delimiter}\n")


def _write_summary(named: list[str]) -> None:
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if not summary:
        return
    with open(summary, "a", encoding="utf-8") as handle:
        handle.write("### Live integration: failing SDK tests\n\n")
        for line in named:
            handle.write(f"- {line}\n")
        handle.write("\n")


# ── Fixtures, copied from the runs on issue #1374 ───────────────────────────

# Run 33251802900 (job 99099024922), the broken total_tokens invariant. This
# is the run whose tracking-issue comment said "the provider rate limited us".
VITEST_ASSERTION = """
 ❯ tests/usage/usage-accounting.test.ts (3 tests | 1 failed) 33617ms
   × Usage accounting > prompt_tokens_details.cached_tokens is present 30314ms
     → expected 31 to be 5 // Object.is equality

⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯

 FAIL  tests/usage/usage-accounting.test.ts > Usage accounting > prompt_tokens_details.cached_tokens is present with a numeric value
AssertionError: expected 31 to be 5 // Object.is equality

 Test Files  1 failed | 21 passed (22)
"""

# Run 33238912385 (job 99065065399), a stale `it.fails` marker. "Expect test
# to fail" is unreadable without the test name in front of it, which is
# exactly what the old report withheld.
VITEST_STALE_MARKER = """
⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯

 FAIL  tests/usage/usage-accounting.test.ts > Usage accounting > stream_options.include_usage emits a terminal usage chunk with prompt_tokens_details
Error: Expect test to fail
"""

# Run 33248100043 (job 99089550787), the dead S3 endpoint (issue #1324).
VITEST_TWO_FAILURES = """
⎯⎯⎯⎯⎯⎯⎯ Failed Tests 2 ⎯⎯⎯⎯⎯⎯⎯

 FAIL  tests/batches/batches.test.ts > Batches > rejects an unsupported batch endpoint value with a structured 4xx error
Error: 500 Failed to store file
 ❯ APIError.generate node_modules/openai/src/core/error.ts:99:14

 FAIL  tests/files/files.test.ts > Files > uploads, lists, retrieves metadata, downloads content, then deletes a file
Error: 500 Failed to store file
"""

# Run 33225137397 (job 99027935246), three stale strict XPASS markers.
PYTEST_XPASS = """
[XPASS(strict)] issue #1260: empty completions serialize content as JSON null
=========================== short test summary info ============================
FAILED tests/test_anthropic_messages.py::test_count_tokens - [XPASS(strict)] issue #1261: count_tokens requires a session principal
FAILED tests/test_anthropic_messages.py::test_models_list - [XPASS(strict)] issue #1259: GET /v1/models does not accept x-api-key
======================== 2 failed, 34 passed in 24.11s =========================
"""

# Run 33238912385 (job 99065065399). pytest dropped the reason from the
# summary line entirely, so the only place the stale marker's issue number
# survives is the FAILURES section. Verbatim, including the rule widths.
PYTEST_REASON_ONLY_IN_SECTION = """
=================================== FAILURES ===================================
___________________ test_streaming_event_sequence_integrity ____________________
[XPASS(strict)] issue #1274: content_block_start omits an empty text field (json:"text,omitempty"), so the SDK's own stream accumulator crashes
=========================== short test summary info ============================
FAILED tests/test_anthropic_messages.py::test_streaming_event_sequence_integrity
======================== 1 failed, 35 passed in 30.85s =========================
"""

GRADLE_FAILURE = """
> Task :compileTestJava
> Task :test

com.hive.sdk.ChatCompletionsTest > basicCompletionReturnsAlias() FAILED
    org.opentest4j.AssertionFailedError at ChatCompletionsTest.java:42

> Task :test FAILED

FAILURE: Build failed with an exception.
"""

GRADLE_PASS = """
> Task :test

BUILD SUCCESSFUL in 17s
2 actionable tasks: 2 executed
"""


def _selfcheck() -> int:
    # The defect this script exists to fix, in its three real shapes. Each of
    # these was reported to the tracking issue as "the provider rate limited
    # us (429)" and nothing else.
    found = parse_vitest(VITEST_ASSERTION)
    assert len(found) == 1, found
    name, reason = found[0]
    assert name.endswith("prompt_tokens_details.cached_tokens is present with a numeric value"), name
    assert reason == "AssertionError: expected 31 to be 5 // Object.is equality", reason

    found = parse_vitest(VITEST_STALE_MARKER)
    assert len(found) == 1, found
    assert found[0][1] == "Error: Expect test to fail", found

    found = parse_vitest(VITEST_TWO_FAILURES)
    assert len(found) == 2, found
    assert all(r == "Error: 500 Failed to store file" for _, r in found), found
    assert "batches.test.ts" in found[0][0] and "files.test.ts" in found[1][0], found

    found = parse_pytest(PYTEST_XPASS)
    assert len(found) == 2, found
    assert found[0][0] == "tests/test_anthropic_messages.py::test_count_tokens", found
    assert "XPASS(strict)" in found[0][1], found
    assert "#1261" in found[0][1], "the reason must survive; it names the stale issue"

    # And when pytest drops the reason from the summary line, it is recovered
    # from the FAILURES section rather than lost. Without this the report says
    # only "test_streaming_event_sequence_integrity", which is the same silence
    # in a smaller place.
    found = parse_pytest(PYTEST_REASON_ONLY_IN_SECTION)
    assert len(found) == 1, found
    assert "#1274" in found[0][1], found
    assert found[0][1].startswith("[XPASS(strict)]"), found

    # A gradle task line is not a test and must never be named as one.
    found = parse_gradle(GRADLE_FAILURE)
    assert len(found) == 1, found
    assert found[0][0] == "com.hive.sdk.ChatCompletionsTest > basicCompletionReturnsAlias()", found
    assert parse_gradle(GRADLE_PASS) == [], parse_gradle(GRADLE_PASS)

    # Nothing at all must not invent a failure.
    assert parse_vitest("") == []
    assert parse_pytest("") == []
    assert parse_gradle("") == []

    with tempfile.TemporaryDirectory() as tmp:
        root = pathlib.Path(tmp)

        # A green run names nothing, whatever is in the logs.
        for suite in SUITES:
            (root / f"{suite}.rc").write_text("0\n", encoding="utf-8")
            (root / f"{suite}.log").write_text(VITEST_ASSERTION, encoding="utf-8")
        assert collect(root) == [], collect(root)

        # The 12:09 run's real shape: js red on one assertion, py and java green.
        (root / "sdk-tests-js.rc").write_text("1\n", encoding="utf-8")
        named = collect(root)
        assert len(named) == 1, named
        assert named[0].startswith("sdk-tests-js: "), named
        assert "expected 31 to be 5" in named[0], named

        # A suite that died wholesale says so, and does NOT claim a test
        # failed. This is the case where an upstream refusal really is the
        # likely cause, which is why the wording sends the reader there.
        (root / "sdk-tests-py.rc").write_text("125\n", encoding="utf-8")
        (root / "sdk-tests-py.log").write_text("Cannot start container\n", encoding="utf-8")
        named = collect(root)
        assert len(named) == 2, named
        assert "exited 125 with no named test failure" in named[1], named

        # A missing .rc file must not be read as a pass.
        (root / "sdk-tests-java.rc").unlink()
        named = collect(root)
        assert len(named) == 3, named
        assert "exited unknown" in named[2], named

    # The cross-step contract: the tracking-issue step reads SDK_FAILED_TESTS
    # out of $GITHUB_ENV. Multi-line values need the heredoc form; the plain
    # K=V form would corrupt every later entry in the file.
    with tempfile.TemporaryDirectory() as tmp:
        env_path = pathlib.Path(tmp) / "env"
        env_path.write_text("", encoding="utf-8")
        previous = os.environ.get("GITHUB_ENV")
        os.environ["GITHUB_ENV"] = str(env_path)
        try:
            _write_env(["sdk-tests-js: a > b -- Error: x", "sdk-tests-py: c::d"])
            written = env_path.read_text(encoding="utf-8")
        finally:
            if previous is None:
                os.environ.pop("GITHUB_ENV", None)
            else:
                os.environ["GITHUB_ENV"] = previous
        assert written.startswith("SDK_FAILED_TESTS<<SDK_FAILURES_"), written
        assert written.endswith("\n"), written
        delimiter = written.splitlines()[0].split("<<", 1)[1]
        assert written.splitlines()[-1] == delimiter, written
        assert "sdk-tests-py: c::d" in written, written

    print("ok: extract-sdk-failures parsers")
    return 0


def main(argv: list[str]) -> int:
    if "--selfcheck" in argv:
        return _selfcheck()
    if len(argv) < 2:
        print("::warning::extract-sdk-failures.py needs the suite log directory")
        return 0
    return report(pathlib.Path(argv[1]))


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
