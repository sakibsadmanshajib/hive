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

import contextlib
import io
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
#   `FAILED tests/test_aliases.py::test_alias[deepseek v4 flash] - Assertion...`
# The id is taken lazily up to the ` - ` reason separator rather than as `\S+`,
# because a parameterized case's id contains spaces and the `\S+` form then
# matched the line not at all: the failure vanished, the suite looked like it
# had named nothing, and collect() below called a plain assertion failure a
# suite that died wholesale.
PYTEST_FAIL = re.compile(r"^(?:FAILED|ERROR)\s+(.+?)(?:\s+-\s+(.*))?$")
# pytest truncates the summary line's reason to the terminal width and often
# drops it entirely. The FAILURES section above it carries the full text under
# a rule naming the bare function:
#   `______________ test_streaming_event_sequence_integrity ______________`
#   `[XPASS(strict)] issue #1274: content_block_start omits an empty text ...`
# That reason is the most useful fact in the whole log for the recurring
# stale-marker cause, since it names the issue the marker refers to.
# The header names a class-based test as `TestChat.test_x` and a parameterized
# one as `test_x[a b]`, so neither the class prefix nor a space may break it.
PYTEST_SECTION = re.compile(r"^_{3,}\s+(.+?)\s+_{3,}$")
# Lines under that rule that are the failing source rather than the reason.
PYTEST_SOURCE = ("def ", ">", "@", "self ", "self=", "_", "=", "E ")

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
    """Bare test function name -> the reason under its rule in the FAILURES block.

    Two shapes reach this. A strict XPASS puts its reason on the first line
    under the rule, and an ordinary assertion puts source there and the reason
    on the `E ` line further down. Taking the first non-blank line
    unconditionally recovered `def test_count_tokens(client):` as the "reason"
    for every ordinary failure, which reads as a reason and is not one.
    """
    found: dict[str, str] = {}
    for i, line in enumerate(lines):
        match = PYTEST_SECTION.match(line.strip())
        if not match:
            continue
        window = [follow.strip() for follow in lines[i + 1 : i + 12] if follow.strip()]
        error = next((text for text in window if text.startswith("E ")), "")
        reason = (
            error[1:].strip()
            if error
            else next((t for t in window if not t.startswith(PYTEST_SOURCE)), "")
        )
        if reason:
            # The rule names `TestChat.test_x`, while the node id's last
            # segment is `test_x`, so a class-based test recovered nothing.
            found.setdefault(match.group(1).rsplit(".", 1)[-1], reason)
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


def collect(log_dir: pathlib.Path) -> tuple[list[str], bool]:
    """(one human line per failure in suite order, whether any test was named).

    The second value exists because the two states differ and the reader of
    this list cannot tell them apart. Every line here is a failure, but a
    wholesale-death line names no test, and classify-upstream-refusal.py
    demotes a provider refusal to a footnote whenever a test was named. Keying
    that on "this list is non-empty" demoted a spent daily token budget, the
    one cause no rerun can fix, in precisely the scenario issue #1088 was
    written for.
    """
    named: list[str] = []
    real = False
    for suite in SUITES:
        rc_text = _read(log_dir / f"{suite}.rc").strip()
        if rc_text == "0":
            continue
        rc = rc_text or "unknown"
        found = failures_for(suite, _read(log_dir / f"{suite}.log"))
        if found:
            real = True
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
    return named, real


def report(log_dir: pathlib.Path) -> int:
    named, real = collect(log_dir)
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

    _write_env(named, real)
    _write_summary(named)
    return 0


def _write_env(named: list[str], real: bool) -> None:
    """Hand the list to later steps, which file the tracking issue with it.

    A heredoc, because the value is multi-line: the single-line `K=V` form
    that classify-upstream-refusal.py uses would corrupt every later entry.
    The delimiter is random per invocation so no test name can close the
    block early.

    SDK_NAMED_TEST_FAILURES is separate and deliberately not derived from the
    list being non-empty. It is the switch classify-upstream-refusal.py uses to
    decide whether a provider refusal is the cause or the weather, and the
    wholesale-death line is a failure that names no test, so a list-emptiness
    test answers the wrong question.
    """
    github_env = os.environ.get("GITHUB_ENV")
    if not github_env:
        return
    delimiter = f"SDK_FAILURES_{uuid.uuid4().hex}"
    body = "\n".join(line for line in named if line.strip() != delimiter)
    with open(github_env, "a", encoding="utf-8") as handle:
        handle.write(f"SDK_FAILED_TESTS<<{delimiter}\n{body}\n{delimiter}\n")
        if real:
            handle.write("SDK_NAMED_TEST_FAILURES=1\n")


def _write_summary(named: list[str]) -> None:
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if not summary:
        return
    # Fenced, because reasons come partly from upstream provider error bodies
    # and a message containing markdown rendered as markdown here. Cosmetic
    # rather than an injection path (GitHub sanitizes HTML in step summaries),
    # but a summary that silently swallows part of an error message is the
    # same class of quiet loss this script exists to remove. A four-backtick
    # fence cannot be closed by a three-backtick run inside an error message.
    with open(summary, "a", encoding="utf-8") as handle:
        handle.write("### Live integration: failing SDK tests\n\n````text\n")
        for line in named:
            handle.write(line.replace("````", "'''") + "\n")
        handle.write("````\n\n")


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

# Synthetic, and the only two in this file that are. No parameterized case and
# no test class exists in packages/sdk-tests/python/tests/ yet, so no run has
# ever produced either shape. They are here because the first one added would
# otherwise parse to nothing at all and be reported as a suite that died
# wholesale, which is this script's own failure mode in the opposite direction.
PYTEST_PARAMETERIZED = """
=========================== short test summary info ============================
FAILED tests/test_aliases.py::test_alias[deepseek v4 flash] - AssertionError: expected 31 to be 5
FAILED tests/test_aliases.py::test_alias[gpt oss 20b]
======================== 2 failed, 34 passed in 24.11s =========================
"""

PYTEST_CLASS_TRACEBACK = """
=================================== FAILURES ===================================
____________________ TestChat.test_streaming_integrity _____________________

self = <tests.test_chat.TestChat object at 0x7f2a1c0>

    def test_streaming_integrity(self, client):
        response = client.chat.completions.create(**payload)
>       assert response.usage.total_tokens == 5
E       AssertionError: assert 31 == 5

tests/test_chat.py:28: AssertionError
=========================== short test summary info ============================
FAILED tests/test_chat.py::TestChat::test_streaming_integrity
======================== 1 failed, 35 passed in 30.85s =========================
"""

# Synthetic, and the third of the three. Unlike the vitest and pytest fixtures
# above, no red sdk-tests-java run exists to capture: the Java suite has not
# failed in the window issue #1374 covers. build.gradle sets useJUnitPlatform()
# with no testLogging block, so this is Gradle's default failed-test console
# shape, with the class and package names this repository actually uses
# (packages/sdk-tests/java/src/test/java/com/hive/sdktests/). Replace it with a
# capture the first time that suite goes red.
GRADLE_FAILURE = """
> Task :compileTestJava
> Task :test

com.hive.sdktests.ErrorShapeTest > rejectsUnknownModelWithStructuredError() FAILED
    org.opentest4j.AssertionFailedError at ErrorShapeTest.java:42

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

    # A class-based test's rule header carries the class, while the summary
    # line's node id does not, and the first line under the rule is source
    # rather than a reason. Both silently recovered nothing useful.
    found = parse_pytest(PYTEST_CLASS_TRACEBACK)
    assert len(found) == 1, found
    assert found[0][0] == "tests/test_chat.py::TestChat::test_streaming_integrity", found
    assert found[0][1] == "AssertionError: assert 31 == 5", (
        "the recovered reason must be the reason, not the source line above it: "
        + repr(found[0][1])
    )

    # A gradle task line is not a test and must never be named as one.
    found = parse_gradle(GRADLE_FAILURE)
    assert len(found) == 1, found
    assert found[0][0] == (
        "com.hive.sdktests.ErrorShapeTest > rejectsUnknownModelWithStructuredError()"
    ), found
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
        assert collect(root) == ([], False), collect(root)

        # The 12:09 run's real shape: js red on one assertion, py and java green.
        (root / "sdk-tests-js.rc").write_text("1\n", encoding="utf-8")
        named, real = collect(root)
        assert len(named) == 1, named
        assert named[0].startswith("sdk-tests-js: "), named
        assert "expected 31 to be 5" in named[0], named
        assert real is True, "a parsed test name is a named failure"

        # A suite that died wholesale says so, and does NOT claim a test
        # failed. This is the case where an upstream refusal really is the
        # likely cause, which is why the wording sends the reader there.
        (root / "sdk-tests-py.rc").write_text("125\n", encoding="utf-8")
        (root / "sdk-tests-py.log").write_text("Cannot start container\n", encoding="utf-8")
        named, real = collect(root)
        assert len(named) == 2, named
        assert "exited 125 with no named test failure" in named[1], named
        assert real is True, "one suite still named a test, so the flag holds"

        # A missing .rc file must not be read as a pass.
        (root / "sdk-tests-java.rc").unlink()
        named, real = collect(root)
        assert len(named) == 3, named
        assert "exited unknown" in named[2], named

        # Every suite dead and nothing named: three failure lines and no claim
        # that a test failed, which is what lets a real refusal stay the cause.
        (root / "sdk-tests-js.log").write_text("Cannot start container\n", encoding="utf-8")
        (root / "sdk-tests-java.log").write_text("Cannot start container\n", encoding="utf-8")
        named, real = collect(root)
        assert len(named) == 3, named
        assert real is False, named

    # The cross-step contract: the tracking-issue step reads SDK_FAILED_TESTS
    # out of $GITHUB_ENV. Multi-line values need the heredoc form; the plain
    # K=V form would corrupt every later entry in the file.
    with tempfile.TemporaryDirectory() as tmp:
        env_path = pathlib.Path(tmp) / "env"
        env_path.write_text("", encoding="utf-8")
        previous = os.environ.get("GITHUB_ENV")
        os.environ["GITHUB_ENV"] = str(env_path)
        try:
            _write_env(["sdk-tests-js: a > b -- Error: x", "sdk-tests-py: c::d"], True)
            written = env_path.read_text(encoding="utf-8")
        finally:
            if previous is None:
                os.environ.pop("GITHUB_ENV", None)
            else:
                os.environ["GITHUB_ENV"] = previous
        assert written.startswith("SDK_FAILED_TESTS<<SDK_FAILURES_"), written
        assert written.endswith("\n"), written
        lines = written.splitlines()
        delimiter = lines[0].split("<<", 1)[1]
        # The heredoc must be closed before the flag, or the flag lands inside
        # the value and SDK_NAMED_TEST_FAILURES is never set at all.
        assert lines[-2] == delimiter, written
        assert lines[-1] == "SDK_NAMED_TEST_FAILURES=1", written
        assert "sdk-tests-py: c::d" in written, written

    # A parameterized case's node id contains spaces. The `$`-anchored pattern
    # that only accepted `\S+` matched nothing at all here, so the suite fell
    # through to the wholesale branch and the report confidently called a plain
    # assertion failure a container that never started.
    found = parse_pytest(PYTEST_PARAMETERIZED)
    assert len(found) == 2, found
    assert found[0][0] == "tests/test_aliases.py::test_alias[deepseek v4 flash]", found
    assert found[0][1] == "AssertionError: expected 31 to be 5", found
    assert found[1][0] == "tests/test_aliases.py::test_alias[gpt oss 20b]", found
    assert found[1][1] == "", "an id with spaces and no reason must still parse"

    # The one fixture in this file that is a committed capture rather than an
    # excerpt retyped from a run page. If the parser only ever sees strings
    # written to match its own regexes it proves nothing about real output.
    real = _captured_log("run-33251802900-sdk-tests-js.log")
    found = parse_vitest(real)
    assert len(found) == 1, found
    assert found[0][0].endswith(
        "prompt_tokens_details.cached_tokens is present with a numeric value"
    ), found
    assert found[0][1] == "AssertionError: expected 31 to be 5 // Object.is equality", found

    # report() itself, which is the deliverable: the annotations, the overflow
    # warning, the job summary and the two $GITHUB_ENV keys. None of this was
    # exercised before, so deleting the `::error::` prefix left this green.
    with tempfile.TemporaryDirectory() as tmp:
        root = pathlib.Path(tmp)
        for suite in SUITES:
            (root / f"{suite}.rc").write_text("0\n", encoding="utf-8")
            (root / f"{suite}.log").write_text("", encoding="utf-8")
        (root / "sdk-tests-js.rc").write_text("1\n", encoding="utf-8")
        (root / "sdk-tests-js.log").write_text(real, encoding="utf-8")
        out, env, summary = _run_report(root)

    assert "::error::sdk-tests-js: " in out, out
    assert "expected 31 to be 5" in out, out
    assert "::warning::" not in out, "one failure must not claim an overflow"
    assert "SDK_FAILED_TESTS<<" in env, env
    assert "SDK_NAMED_TEST_FAILURES=1\n" in env, (
        "a genuinely named test failure must set the flag the refusal "
        "classifier keys its precedence rule on: " + env
    )
    assert "### Live integration: failing SDK tests" in summary, summary
    assert "expected 31 to be 5" in summary, summary

    # A wholesale death names no test, so the flag must stay unset. This is the
    # CRITICAL of issue #1374's review: with the flag set here, a real spent
    # daily allowance is demoted to a note and issue #1088 is reversed in its
    # own scenario.
    with tempfile.TemporaryDirectory() as tmp:
        root = pathlib.Path(tmp)
        for suite in SUITES:
            (root / f"{suite}.rc").write_text("125\n", encoding="utf-8")
            (root / f"{suite}.log").write_text(
                "Error response from daemon: cannot start container\n", encoding="utf-8"
            )
        out, env, summary = _run_report(root)

    assert out.count("::error::") == 3, out
    assert "died wholesale" in out, out
    assert "SDK_FAILED_TESTS<<" in env, env
    assert "SDK_NAMED_TEST_FAILURES" not in env, (
        "no test was named, so nothing may claim one was; that claim is what "
        "suppresses a genuine refusal from being reported as the cause: " + env
    )

    # Over MAX_ANNOTATIONS the run page gets the cap plus one warning, and the
    # remainder still reaches the step log and the summary.
    with tempfile.TemporaryDirectory() as tmp:
        root = pathlib.Path(tmp)
        for suite in SUITES:
            (root / f"{suite}.rc").write_text("0\n", encoding="utf-8")
            (root / f"{suite}.log").write_text("", encoding="utf-8")
        (root / "sdk-tests-js.rc").write_text("1\n", encoding="utf-8")
        (root / "sdk-tests-js.log").write_text(
            "\n".join(f" FAIL  tests/a.test.ts > case {n}" for n in range(25)),
            encoding="utf-8",
        )
        out, env, summary = _run_report(root)

    assert out.count("::error::") == MAX_ANNOTATIONS, out
    assert "::warning::5 further failure(s) not annotated" in out, out
    assert "case 24" in out, "the uncapped remainder still belongs in the step log"
    assert summary.count("case ") == 25, summary

    # Every suite green: say so, write nothing, invent nothing.
    with tempfile.TemporaryDirectory() as tmp:
        root = pathlib.Path(tmp)
        for suite in SUITES:
            (root / f"{suite}.rc").write_text("0\n", encoding="utf-8")
            (root / f"{suite}.log").write_text(real, encoding="utf-8")
        out, env, summary = _run_report(root)

    assert "no SDK suite reported a non-zero exit" in out, out
    assert "::error::" not in out, out
    assert env == "", env
    assert summary == "", summary

    print("ok: extract-sdk-failures parsers")
    return 0


def _captured_log(name: str) -> str:
    return (pathlib.Path(__file__).parent / "fixtures" / "sdk-failures" / name).read_text(
        encoding="utf-8"
    )


def _run_report(root: pathlib.Path) -> tuple[str, str, str]:
    """report() under a temporary $GITHUB_ENV and $GITHUB_STEP_SUMMARY."""
    with tempfile.TemporaryDirectory() as tmp:
        env_path = pathlib.Path(tmp) / "env"
        summary_path = pathlib.Path(tmp) / "summary"
        env_path.write_text("", encoding="utf-8")
        summary_path.write_text("", encoding="utf-8")
        previous = {k: os.environ.get(k) for k in ("GITHUB_ENV", "GITHUB_STEP_SUMMARY")}
        os.environ["GITHUB_ENV"] = str(env_path)
        os.environ["GITHUB_STEP_SUMMARY"] = str(summary_path)
        buffer = io.StringIO()
        try:
            with contextlib.redirect_stdout(buffer):
                assert report(root) == 0
            return (
                buffer.getvalue(),
                env_path.read_text(encoding="utf-8"),
                summary_path.read_text(encoding="utf-8"),
            )
        finally:
            for key, value in previous.items():
                if value is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = value


def main(argv: list[str]) -> int:
    if "--selfcheck" in argv:
        return _selfcheck()
    if len(argv) < 2:
        print("::warning::extract-sdk-failures.py needs the suite log directory")
        return 0
    return report(pathlib.Path(argv[1]))


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
