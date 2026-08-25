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


def report(payload: dict, redact) -> int:
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

    print(f"free pool members: {healthy} healthy, {unhealthy} unhealthy")

    for endpoint in endpoints_unhealthy:
        if not isinstance(endpoint, dict):
            endpoint = {}
        # `model` is the upstream model id, which is the thing that gets retired
        # and therefore the thing worth printing by name.
        model = endpoint.get("model") or "unknown"
        detail = str(endpoint.get("error") or "no error text")[:ERROR_EXCERPT_CHARS]
        print(redact(f"  DEAD MEMBER: {model} -- {detail}"))
        print(
            redact(
                f"::warning::free pool member '{model}' is not answering. If its "
                "provider no longer lists that model, it is RETIRED: replace the "
                "member row in supabase/migrations/ rather than waiting for it to "
                "come back. The pool routes around it for now."
            )
        )

    if healthy == 0:
        print(
            "::error::every free pool member is down, so hive-free can serve "
            "nothing. This is an outage of the free tier, not a degraded pool."
        )
        return 1
    return 0


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
