#!/usr/bin/env python3
"""Turn LiteLLM's `/health?model=route-free-pool` answer into a named verdict.

Why this exists (issue #1064). The live-integration job already asserts that
the gateway SERVES the route-free-pool group, but serving a group only proves
the group exists, not that its four members answer. Free model ids churn
constantly on every provider in the pool, and a retired one answers 404 --
which is precisely the error LiteLLM refuses to fail over on
(litellm/router.py::should_retry_this_error re-raises NotFoundError, and
RetryPolicy carries no NotFoundErrorRetries knob to lift it). Before this
script, that surfaced as a generic "hive-free is not available." with nothing
anywhere naming which of the four models had gone away.

Exit codes, and the reasoning behind them:

  0  every member healthy, or SOME members down. Surviving a dead member is
     the pool's entire purpose, so failing the job on one would make the
     resilience worthless. The dead one is named as a ::warning:: and the SDK
     suites remain the real gate.
  1  every member down. hive-free then serves nothing, which is a free-tier
     outage rather than a degraded pool, and it should stop the run loudly.
  0  a malformed or unreadable payload, reported as a ::warning::. A probe that
     cannot parse its input has not discovered an outage, and turning "I could
     not tell" into a red run is how a check starts crying wolf.

Self-check: python3 scripts/report-free-pool-health.py --selfcheck
"""
from __future__ import annotations

import json
import pathlib
import re
import sys

def _load_redactor():
    """Import redact-log-credentials.py, whose filename is not a Python name.

    That module is the published-log redactor this repo already uses for every
    container dump. LiteLLM strips api_key from health output itself
    (ILLEGAL_DISPLAY_PARAMS in litellm/proxy/health_check.py), but `error` is
    free-form provider text and this job's log is world-readable on a public
    repository, so it goes through the same filter as everything else.
    """
    import importlib.util

    path = pathlib.Path(__file__).resolve().parent / "redact-log-credentials.py"
    spec = importlib.util.spec_from_file_location("redact_log_credentials", path)
    if spec is None or spec.loader is None:
        return lambda line: line
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    for name in ("redact_line", "redact", "scrub_line", "scrub"):
        fn = getattr(module, name, None)
        if callable(fn):
            return fn
    return lambda line: line


ERROR_EXCERPT_CHARS = 300

# A member answering 429 is rate limited, not gone. The distinction decides the
# remedy and the two remedies are opposites: a retired model needs its row
# replaced, a rate-limited one needs the window to pass or the account funded,
# and replacing its row would delete a member that is coming back. On
# 2026-08-29 the Gemini member was 429 for at least six consecutive runs while
# this script advised replacing it.
RATE_LIMITED = re.compile(r"RateLimitError|\b429\b|quota|RESOURCE_EXHAUSTED", re.I)

# The one fact that decides whether a rerun can pass. Google puts it in a
# `quotaId` like `GenerateRequestsPerDayPerProjectPerModel-FreeTier`, and pairs
# it with a `retryDelay`. Both sit past the excerpt cap, so they are pulled out
# of the FULL error text and printed separately rather than being truncated
# away: the whole point of this line is that the reader can tell a per-minute
# window from a spent daily allowance without opening a log artifact.
WINDOW_HINT = re.compile(
    r"(?:quotaId'?\s*:?\s*'?)([A-Za-z0-9_.-]*Per(?:Day|Minute|Hour)[A-Za-z0-9_.-]*)"
    r"|(PerDay|PerMinute|PerHour|tokens per day|requests per day|TPD|RPD|RPM)"
    r"|(?:retryDelay'?\s*:?\s*'?)(\d+(?:\.\d+)?[smh])",
    re.I,
)


# The error string is provider-controlled text of unbounded length. WINDOW_HINT
# has no nested quantifier so it cannot backtrack catastrophically, but it is
# still O(n) per start position, so the input is bounded before scanning rather
# than trusting a remote service to keep its error messages short.
WINDOW_SCAN_LIMIT = 8000


def _window_hint(error_text: str) -> str:
    """Pull the quota window and retry hint out of the full provider message."""
    found: list[str] = []
    for match in WINDOW_HINT.finditer(error_text[:WINDOW_SCAN_LIMIT]):
        token = next((g for g in match.groups() if g), None)
        if token and token not in found:
            found.append(token)
    return ", ".join(found)


def _render(payload: dict, redact) -> list[str]:
    endpoints_healthy = payload.get("healthy_endpoints") or []
    endpoints_unhealthy = payload.get("unhealthy_endpoints") or []
    # Prefer the explicit counts, fall back to the lists. A payload that
    # carries one but not the other must not read as "zero unhealthy".
    healthy = payload.get("healthy_count")
    unhealthy = payload.get("unhealthy_count")
    if not isinstance(healthy, int):
        healthy = len(endpoints_healthy)
    if not isinstance(unhealthy, int):
        unhealthy = len(endpoints_unhealthy)

    lines = [f"free pool members: {healthy} healthy, {unhealthy} unhealthy"]

    for endpoint in endpoints_unhealthy:
        if not isinstance(endpoint, dict):
            endpoint = {}
        # `model` is the upstream model id, which is the thing that gets retired
        # and therefore the thing worth printing by name.
        model = endpoint.get("model") or "unknown"
        error_text = str(endpoint.get("error") or "no error text")
        detail = error_text[:ERROR_EXCERPT_CHARS]

        if RATE_LIMITED.search(error_text):
            lines.append(redact(f"  RATE LIMITED MEMBER: {model} -- {detail}"))
            hint = _window_hint(error_text)
            if hint:
                lines.append(redact(f"    quota window: {hint}"))
            lines.append(
                redact(
                    f"::warning::free pool member '{model}' is RATE LIMITED, not "
                    "gone. Do NOT replace its row: the model still exists and the "
                    "member returns when the window resets or the account is "
                    "funded. Read the quota window above to tell a transient "
                    "per-minute limit from a spent daily allowance. The pool "
                    "routes around it meanwhile."
                )
            )
        else:
            lines.append(redact(f"  DEAD MEMBER: {model} -- {detail}"))
            lines.append(
                redact(
                    f"::warning::free pool member '{model}' is not answering. If "
                    "its provider no longer lists that model, it is RETIRED: "
                    "replace the member row in supabase/migrations/ rather than "
                    "waiting for it to come back. The pool routes around it for now."
                )
            )

    if healthy == 0:
        lines.append(
            "::error::every free pool member is down, so hive-free can serve "
            "nothing. This is an outage of the free tier, not a degraded pool."
        )
    return lines


def _exit_code(payload: dict) -> int:
    """1 only when every member is down. Unchanged from before the refactor.

    Deliberately computed from the payload rather than by scanning the rendered
    lines for a `::error::` prefix: an exit code that depends on output
    formatting breaks the next time somebody rewords a message, and this one
    decides whether a free-tier outage stops the run.
    """
    healthy = payload.get("healthy_count")
    if not isinstance(healthy, int):
        healthy = len(payload.get("healthy_endpoints") or [])
    return 1 if healthy == 0 else 0


def report(payload: dict, redact) -> int:
    for line in _render(payload, redact):
        print(line)
    return _exit_code(payload)


def _selfcheck() -> int:
    plain = lambda line: line  # noqa: E731

    one_dead = {
        "healthy_count": 3,
        "unhealthy_count": 1,
        "healthy_endpoints": [{"model": "groq/openai/gpt-oss-20b"}],
        "unhealthy_endpoints": [
            {"model": "openai/gemini-flash-latest", "error": "Error code: 404"}
        ],
    }
    assert report(one_dead, plain) == 0, "one dead member must not fail the job"

    all_dead = {
        "healthy_count": 0,
        "unhealthy_count": 4,
        "healthy_endpoints": [],
        "unhealthy_endpoints": [{"model": f"m{i}"} for i in range(4)],
    }
    assert report(all_dead, plain) == 1, "a fully dead pool must fail the job"

    all_healthy = {"healthy_count": 4, "unhealthy_count": 0}
    assert report(all_healthy, plain) == 0

    # A RATE LIMITED member is not a retired one, and telling the reader to
    # replace its row would delete a member that is coming back. Text taken
    # from the real 2026-08-29 probe (runs 33243396287 and 33244166031), with
    # Google's quota detail restored: the shipped 300-character excerpt cut it
    # off, so six consecutive red runs could not say whether the window was
    # per minute or per day. That is the exact question the failure raised.
    rate_limited = {
        "healthy_count": 3,
        "unhealthy_count": 1,
        "unhealthy_endpoints": [
            {
                "model": "openai/gemini-flash-latest",
                "error": (
                    "litellm.RateLimitError: RateLimitError: OpenAIException - "
                    "Error code: 429 - [{'error': {'code': 429, 'message': 'You "
                    "exceeded your current quota, please check your plan and "
                    "billing details. For more information on this error, head "
                    "to: https://ai.google.dev/gemini-api/docs/rate-limits. To "
                    "monitor your usage see the console.', 'status': "
                    "'RESOURCE_EXHAUSTED', 'details': [{'@type': "
                    "'type.googleapis.com/google.rpc.QuotaFailure', "
                    "'violations': [{'quotaMetric': 'generativelanguage.googleapis"
                    ".com/generate_content_free_tier_requests', 'quotaId': "
                    "'GenerateRequestsPerDayPerProjectPerModel-FreeTier'}]}, "
                    "{'@type': 'type.googleapis.com/google.rpc.RetryInfo', "
                    "'retryDelay': '34s'}]}}]"
                ),
            }
        ],
    }
    lines = _render(rate_limited, plain)
    joined = "\n".join(lines)
    assert "RATE LIMITED" in joined, joined
    assert "RETIRED" not in joined, (
        "a rate-limited member must not be reported as retired: acting on that "
        "advice deletes a member that is not gone"
    )
    assert "PerDay" in joined, (
        "the quota window must survive into the log, or nobody can tell a "
        "transient per-minute limit from a spent daily allowance"
    )
    assert "34s" in joined, "the provider's own retry hint must survive too"

    # A retired member keeps the original advice, which is correct for a 404.
    retired = {
        "healthy_count": 3,
        "unhealthy_count": 1,
        "unhealthy_endpoints": [
            {
                "model": "openrouter/some/retired-model:free",
                "error": "litellm.NotFoundError: Error code: 404 - model not found",
            }
        ],
    }
    joined = "\n".join(_render(retired, plain))
    assert "RETIRED" in joined, joined
    assert "RATE LIMITED" not in joined, joined

    # Counts missing entirely: fall back to the lists rather than reading as
    # "0 healthy" and failing a perfectly good pool.
    no_counts = {"healthy_endpoints": [{"model": "a"}], "unhealthy_endpoints": []}
    assert report(no_counts, plain) == 0, "missing counts must fall back to the lists"

    # A non-dict endpoint must not crash the reporter.
    junk = {"healthy_count": 1, "unhealthy_count": 1, "unhealthy_endpoints": ["nope"]}
    assert report(junk, plain) == 0

    print("ok: report-free-pool-health verdicts and fallbacks")
    return 0


def main(argv: list[str]) -> int:
    if "--selfcheck" in argv:
        return _selfcheck()
    if len(argv) < 2:
        print("::warning::report-free-pool-health.py needs a path to the health JSON")
        return 0

    try:
        payload = json.loads(pathlib.Path(argv[1]).read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        print(f"::warning::could not read the free pool health payload: {exc}")
        return 0
    if not isinstance(payload, dict):
        print("::warning::the free pool health payload was not a JSON object")
        return 0

    return report(payload, _load_redactor())


if __name__ == "__main__":
    sys.exit(main(sys.argv))
