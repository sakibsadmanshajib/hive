#!/usr/bin/env python3
"""Decide whether an upstream provider actually refused, from container logs.

Why this exists (issue #1088). On 2026-08-23 the live-integration job went red
with `Test timed out in 60000ms` as the only visible fact, while the actual
cause, a provider refusing on an exhausted daily token budget, existed only
inside a log artifact nobody reads first. A red check that misreports its own
cause is worse than a slow one, so the job learned to name the cause.

Why it is a script rather than the bash heredoc it used to be. On 2026-08-29
the heredoc misreported the cause in the other direction, which is the same
defect wearing the opposite sign. Run 33243396287 printed

    UPSTREAM REFUSAL, not a performance regression: the provider rate limited
    us (429).

over a job whose actual failures were `500 Failed to store file` and one
strict XPASS. The line that triggered it was

    alias="deepseek-v4-flash" status=429 ... "No deployments available for
    selected model, Try again in 5 seconds. Passed model=route-deepseek-v4-flash
    ... cooldown_list=['7916fd0b...','e644bb2c...']"

which is wrong evidence twice over: the alias is not the one this job serves,
and the message is LiteLLM's OWN router cooldown rather than any provider
refusing anything. It matched only because the regex carried the bare token
`status=429`, and a 2000-line window of two containers' logs will contain a 429
on some alias most of the time. Six of the seven failures on issue #1374 that
morning were the same underlying storage defect; only two of them classified,
which is the signature of an incidental grep hit and not of a spent allowance.

Bash could not carry a regression test for any of that. Python here can, and
`make test-scripts` already runs these self-checks as a required check.

Exit codes:

  0  always, including when a refusal IS found. This step runs under
     `if: failure()` on a job that has already failed; its job is to explain
     that failure, not to add a second one. The shipped heredoc returned 2 on
     a SIGPIPE from its own `grep ... | head -5` under `set -o pipefail`,
     visible in run 33243396287 as `grep: write error: Broken pipe` followed by
     `##[error]Process completed with exit code 2`. Reading a file into memory
     removes that failure mode by construction.

Self-check: python3 scripts/classify-upstream-refusal.py --selfcheck
"""
from __future__ import annotations

import os
import pathlib
import re
import sys

# Kept verbatim from the bash this replaces, in the same order, because the
# order encodes a real judgement: the three spend-related refusals are more
# specific than "a member is dead" and should win when both are present.
DAILY_BUDGET = re.compile(r"tokens per day|per day \(TPD\)|\bTPD\b", re.I)
RATE_LIMIT = re.compile(
    r"rate.?limit|RateLimitError|rate_limit_exceeded|status_code=429|status=429", re.I
)
OUT_OF_CREDIT = re.compile(r"insufficient.?credit|402 Payment Required", re.I)

EVIDENCE = re.compile(
    r"tokens per day|rate.?limit|RateLimitError|rate_limit_exceeded"
    r"|insufficient.?credit|402 Payment Required|No fallback model group found",
    re.I,
)

ALIAS = re.compile(r'alias="([^"]{1,120})"')

# LiteLLM's own cooldown bookkeeping, in the two shapes it reaches the log.
# Neither is a provider refusing anything, and both carry provider error text
# verbatim inside them, which is what made them match.
#
#   1. The router declining to dispatch to deployments it has already benched:
#      `No deployments available for selected model ... cooldown_list=[...]`,
#      surfaced to the client as a 429. No upstream call was made at all.
#   2. `Cooldown Deployments=[('<hash>', {'exception_received': '...',
#      'status_code': '429', ...})]`, which is the cooldown TABLE being printed.
#      It quotes the exception that caused the cooldown, so it re-reports one
#      past event on every subsequent line for as long as the entry lives.
#
# Both halves of pattern 1 are required: "No deployments available" alone is
# also how a genuinely empty model group reads, and that IS worth reporting.
GATEWAY_COOLDOWN = re.compile(
    r"No deployments available for selected model.*cooldown_list="
    r"|Cooldown Deployments=\[",
    re.I | re.S,
)

# The dead-member case this job actually cares about, per issue #1064: a
# RETIRED free model answering 404, which LiteLLM refuses to fail over on.
#
# `No fallback model group found` alone is NOT that signature. LiteLLM emits it
# on every failed dispatch to a group with no configured fallback, whatever the
# underlying error, so in run 33243396287's own artifact it appears on a 400
# (a deliberate over-length prompt), on a 404 (deepseek not supporting image
# input) and on a Groq TTS 400 (a test asserting an invalid response_format is
# refused). All three are expected behaviour under test. Requiring both the
# NotFoundError shape and the free pool's own group name keeps the branch on
# the case its message describes.
DEAD_POOL_MEMBER = re.compile(
    r"NotFoundError.*model_group=route-free-pool"
    r"|model_group=route-free-pool.*NotFoundError",
    re.I | re.S,
)

DAILY_CLASS = (
    "the provider refused on a DAILY token budget (TPD). It resets on the "
    "provider's own schedule, so retrying or re-running will not fix it; the "
    "allowance is gone until then. If that provider is also the live demo's, "
    "the demo is answering nothing right now for the same reason."
)
CREDIT_CLASS = (
    "the provider account is out of credit (402). No retry fixes this; it "
    "needs funding."
)
DEAD_CLASS = (
    "a POOL MEMBER IS DEAD, not the whole provider. LiteLLM answered 'No "
    "fallback model group found', which is what it says when one deployment in "
    "a load-balanced group returns 404 and the group has no fallback group "
    "configured. A 404 is what a RETIRED free model returns, and LiteLLM will "
    "not fail over on it (litellm/router.py::should_retry_this_error re-raises "
    "NotFoundError, and RetryPolicy has no NotFoundErrorRetries knob). The edge "
    "now retries this so the pool routes around the dead member; seeing it here "
    "means the retry ladder was also exhausted, so more than one member is "
    "down. The 'Probe each free pool member' step earlier in this job names "
    "them."
)


def classify(text: str) -> tuple[str | None, list[str]]:
    """Return (cause sentence or None, the evidence lines that support it)."""
    # Dropped before any branch reads them, not just before the 429 branch: a
    # gateway cooldown is never evidence that an upstream refused, whichever
    # signature it happens to carry.
    lines = [
        line
        for line in text.splitlines()
        if line.strip() and not GATEWAY_COOLDOWN.search(line)
    ]
    evidence = [line for line in lines if EVIDENCE.search(line)]

    if any(DAILY_BUDGET.search(line) for line in lines):
        return DAILY_CLASS, evidence

    limited = [line for line in lines if RATE_LIMIT.search(line)]
    if limited:
        return _rate_limit_class(limited), evidence

    if any(OUT_OF_CREDIT.search(line) for line in lines):
        return CREDIT_CLASS, evidence

    if any(DEAD_POOL_MEMBER.search(line) for line in lines):
        return DEAD_CLASS, evidence

    return None, evidence


def _rate_limit_class(limited: list[str]) -> str:
    aliases = sorted({m.group(1) for line in limited for m in [ALIAS.search(line)] if m})
    named = f" The refused alias(es): {', '.join(aliases)}." if aliases else ""
    return (
        "the provider rate limited us (429). Check whether the window is per "
        "minute (transient, so a rerun may pass) or per day (an allowance that "
        "is spent)." + named
    )


def report(text: str) -> int:
    verdict, evidence = classify(text)
    if verdict is None:
        print(
            "no upstream refusal signature in the litellm or edge-api logs, so "
            "this failure is something else. The compose-logs artifact below is "
            "the next place to look."
        )
        return 0

    print(f"::error::UPSTREAM REFUSAL, not a performance regression: {verdict}")
    print("matching lines (redacted, first 5):")
    for line in evidence[:5]:
        print(line)

    github_env = os.environ.get("GITHUB_ENV")
    if github_env:
        with open(github_env, "a", encoding="utf-8") as handle:
            handle.write(f"UPSTREAM_FAILURE_CLASS={verdict}\n")
    return 0


# ── Fixtures, copied from run 33243396287's own output ──────────────────────

GATEWAY_COOLDOWN_429 = (
    'edge-api-1  | provider_blind_upstream_error request_id="req-cf3e9005" '
    'alias="deepseek-v4-flash" status=429 raw_message="No deployments available '
    "for selected model, Try again in 5 seconds. Passed model="
    "route-deepseek-v4-flash. pre-call-checks=False, cooldown_list=['7916fd0b', "
    "'e644bb2c']\" client_message=\"requested model is temporarily rate limited.\""
)
STORE_FILE_500 = 'edge-api-1  | store_file_failed error="Failed to store file"'
CONTEXT_LENGTH_400 = (
    'edge-api-1  | provider_blind_upstream_error alias="hive-free" status=400 '
    "raw_message=\"litellm.BadRequestError: This endpoint's maximum context "
    "length is 512000 tokens. No fallback model group found for original "
    'model_group=route-free-pool."'
)
# The cooldown TABLE, verbatim from run 33243396287's compose-logs artifact
# (hashes and the quoted exception truncated). It quotes 'status_code': '429'
# and RateLimitError, and LiteLLM reprints it on every dispatch for as long as
# the entry lives, so one past cooldown re-reports itself indefinitely.
COOLDOWN_TABLE = (
    "litellm     | Cooldown Deployments=[('7916fd0b', {'exception_received': "
    "'litellm.NotFoundError: NotFoundError: OpenrouterEx***', 'status_code': "
    "'404', 'timestamp': 1787994661.9574306, 'cooldown_time': 5}), ('e644bb2c', "
    "{'exception_received': 'litellm.RateLimitError: RateLimitError: "
    "OpenAIExce***', 'status_code': '429', 'timestamp': 1787994661.0012524, "
    "'cooldown_time': 5})]"
)
# The genuine dead-member text, copied from
# apps/edge-api/internal/inference/retry_test.go, which took it from CI run
# 32830060362's artifact. Keeping the two copies textually identical is the
# point: if a LiteLLM image bump rewords this, both notice.
DEAD_POOL_MEMBER_LINE = (
    'edge-api-1  | provider_blind_upstream_error alias="hive-free" status=404 '
    'raw_message="{\\"error\\":{\\"message\\":\\"litellm.NotFoundError: '
    "NotFoundError: OpenAIException - Error code: 404No fallback model group "
    "found for original model_group=route-free-pool. Received Model Group="
    'route-free-pool\\nAvailable Model Group Fallbacks=None\\"}}"'
)
# Expected refusals that the shipped `No fallback model group found` signature
# also matched, all three from the same artifact. None is a dead pool member.
IMAGE_INPUT_404 = (
    'edge-api-1  | provider_blind_upstream_error alias="deepseek-v4-flash" '
    'status=404 raw_message="litellm.NotFoundError: No endpoints found that '
    "support image input. No fallback model group found for original "
    'model_group=route-deepseek-v4-flash."'
)
GROQ_TTS_400 = (
    "litellm     | litellm.BadRequestError: GroqException - response_format "
    "must be one of [wav]No fallback model group found for original "
    "model_group=route-groq-tts."
)
# RECONSTRUCTED, not copied: the 2026-08-23 artifact this shape comes from is
# past its five-day retention. The workflow comment that describes it records
# that the log said "on tokens per day (TPD)" eighteen times, which is the
# substring the branch keys on, so the match is faithful even though the
# surrounding words are not verbatim.
REAL_TPD = (
    "litellm     | litellm.RateLimitError: GroqException - Rate limit reached "
    "on tokens per day (TPD). Limit 100000, used 100000."
)
REAL_UPSTREAM_429 = (
    'edge-api-1  | provider_blind_upstream_error alias="hive-free" status=429 '
    'raw_message="litellm.RateLimitError: OpenAIException - Error code: 429 - '
    "You exceeded your current quota, please check your plan and billing "
    'details."'
)


def _selfcheck() -> int:
    # The defect this script exists to fix. LiteLLM's own router cooldown is a
    # gateway decision, not a provider refusal, and the alias it names is not
    # even the one this job serves. Classifying it turned a storage failure
    # into a fake allowance emergency on 2026-08-29.
    verdict, _ = classify("\n".join([GATEWAY_COOLDOWN_429, STORE_FILE_500]))
    assert verdict is None, f"a gateway cooldown must not classify, got: {verdict}"

    # LiteLLM reprinting its cooldown table is bookkeeping about one past
    # event, not a refusal, however many times it appears.
    verdict, _ = classify("\n".join([COOLDOWN_TABLE] * 3 + [STORE_FILE_500]))
    assert verdict is None, f"the cooldown table must not classify, got: {verdict}"

    # The whole of run 33243396287's evidence set, in one go. Its real cause
    # was a storage failure and a stale expected-failure marker, so the honest
    # verdict is that there was no upstream refusal at all.
    verdict, _ = classify(
        "\n".join(
            [
                CONTEXT_LENGTH_400,
                GATEWAY_COOLDOWN_429,
                IMAGE_INPUT_404,
                GROQ_TTS_400,
                COOLDOWN_TABLE,
                STORE_FILE_500,
            ]
        )
    )
    assert verdict is None, (
        "run 33243396287 had no upstream refusal; classifying one is the "
        f"defect this script exists to fix. Got: {verdict}"
    )

    # Each expected-refusal line alone, since any one of them could reappear.
    for name, line in (
        ("over-length prompt", CONTEXT_LENGTH_400),
        ("no image-input endpoint", IMAGE_INPUT_404),
        ("invalid TTS response_format", GROQ_TTS_400),
    ):
        verdict, _ = classify(line)
        assert verdict is None, f"{name} must not classify, got: {verdict}"

    # The genuine dead pool member still does classify. This is issue #1064's
    # case and the branch must not have been tightened into uselessness.
    verdict, _ = classify(DEAD_POOL_MEMBER_LINE)
    assert verdict == DEAD_CLASS, f"a real dead pool member must classify: {verdict}"

    # A real upstream 429 still classifies, and now names the alias it saw, so
    # a refusal on a paid alias can never be read as one on the free pool.
    verdict, lines = classify(REAL_UPSTREAM_429)
    assert verdict is not None and "hive-free" in verdict, verdict
    assert lines, "a classified refusal must carry its evidence lines"

    # A daily token budget still beats the generic 429 branch.
    verdict, _ = classify("\n".join([REAL_TPD, REAL_UPSTREAM_429]))
    assert "DAILY" in verdict, verdict

    # A cooldown 429 sitting next to a real one classifies on the real one,
    # and names only the real one.
    verdict, _ = classify("\n".join([GATEWAY_COOLDOWN_429, REAL_UPSTREAM_429]))
    assert verdict is not None, "a real refusal must survive a neighbouring cooldown"
    assert "hive-free" in verdict, verdict
    assert "deepseek-v4-flash" not in verdict, (
        "the cooldown line must not contribute its alias to the verdict: " + verdict
    )

    # Nothing at all in the logs stays unclassified rather than guessing.
    verdict, _ = classify(STORE_FILE_500)
    assert verdict is None, verdict

    # Reporting must never raise on an empty or whitespace-only log.
    assert report("") == 0
    assert report("   \n\n  ") == 0

    print("ok: classify-upstream-refusal verdicts")
    return 0


def main(argv: list[str]) -> int:
    if "--selfcheck" in argv:
        return _selfcheck()
    if len(argv) < 2:
        print("::warning::classify-upstream-refusal.py needs a path to the log file")
        return 0
    try:
        text = pathlib.Path(argv[1]).read_text(encoding="utf-8", errors="replace")
    except OSError as exc:
        print(f"::warning::could not read the upstream log: {exc}")
        return 0
    return report(text)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
