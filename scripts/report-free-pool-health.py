#!/usr/bin/env python3
"""Turn LiteLLM's `/health?model=route-free-pool` answer into a named verdict.

Why this exists (issue #1064). The live-integration job already asserts that
the gateway SERVES the route-free-pool group, but serving a group only proves
the group exists, not that its members answer. Membership is not fixed (it was
four routes when this was written and is fewer now), so nothing here counts
them. Free model ids churn constantly on every provider in the pool, and a
retired one answers 404 --
which is precisely the error LiteLLM refuses to fail over on
(litellm/router.py::should_retry_this_error re-raises NotFoundError, and
RetryPolicy carries no NotFoundErrorRetries knob to lift it). Before this
script, that surfaced as a generic "hive-free is not available." with nothing
anywhere naming which of the pool's models had gone away.

Exit codes, and the reasoning behind them:

  0  every member healthy, or SOME members down. Surviving a dead member is
     the pool's entire purpose, so failing the job on one would make the
     resilience worthless. The dead one is named as a ::warning:: and the SDK
     suites remain the real gate.

     That rationale reads oddly next to the ALLOWANCE EXHAUSTED verdict below,
     which says outright that the pool does NOT route around a member whose
     window outlasts the cooldown, so it is worth stating why the exit code is
     still 0 there. The pool is degraded, not down: the other members answer,
     and this probe cannot tell whether the exhausted member is one of four or
     one of two. What it produces is a named, actionable warning for a human,
     and the thing that should go red when the free tier actually stops serving
     is the all-members-down branch below, plus the SDK suites. Turning a
     degraded pool red here is how a check starts crying wolf, and a check
     nobody reads would have caught this defect no faster.
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
#
# A STATUS signal is required, not the bare word "quota". The word appears in
# billing links and in "check your plan and billing details" prose that a 404
# can carry too, and a 404 reaching the rate-limited branch is now worse than a
# wrong label: the daily verdict below tells the reader to disable the route,
# which would leave the pool a member short over a model that is simply retired
# and needs its row replaced.
RATE_LIMITED = re.compile(
    r"RateLimitError|\b429\b|RESOURCE_EXHAUSTED|quota[ _]exceeded|exceeded your current quota",
    re.I,
)

# The one fact that decides whether a rerun can pass. Google puts it in a
# `quotaId` like `GenerateRequestsPerDayPerProjectPerModel-FreeTier`, and pairs
# it with a `retryDelay`. Both sit past the excerpt cap, so they are pulled out
# of the FULL error text and printed separately rather than being truncated
# away: the whole point of this line is that the reader can tell a per-minute
# window from a spent daily allowance without opening a log artifact.
#
# Three alternatives, and which one fired is load-bearing, so they are read as
# separate groups rather than flattened into one string:
#
#   1. a STRUCTURED quotaId. The provider names the quota it actually refused
#      on, one per violation, and can name several at once.
#   2. PROSE or an abbreviation. This is what picks up a figure the provider
#      merely QUOTES ("...you are allowed 1000 requests per day"), which is not
#      evidence that the daily quota is the one that refused.
#   3. the retryDelay, which is NOT a window and must never be read as one:
#      Google returned '34s' on a spent per-day quota.
#
# The abbreviations are anchored HERE, which is the only place an anchor does
# anything. Anchoring them downstream against the extracted token is inert,
# because by then the token is a bare `RPD` whose boundaries are always
# satisfied; unanchored here, `RPD` fired inside a completion id like
# `chatcmpl-RPDx8121` and fabricated a daily verdict, and `RPM` fired inside a
# request id like `req_9fRPMk3zQ` and suppressed a real one.
#
# The prose vocabulary covers minute, hour, second and the hyphenated and bare
# daily forms because providers use all of them: OpenRouter refuses with
# `free-models-per-day`, Groq spells `on requests per minute`, and a plain
# `daily limit` carries no abbreviation at all. A vocabulary that only knows the
# forms one vendor happens to use produces an empty hint on every other vendor,
# and an empty hint used to read as "per-minute burst".
WINDOW_HINT = re.compile(
    r"(?:quotaId'?\s{0,8}:?\s{0,8}'?)([A-Za-z0-9_.-]*Per(?:Day|Minute|Hour|Second)[A-Za-z0-9_.-]*)"
    r"|((?:(?:tokens|requests)\s+)?per[- ](?:day|hour|minute|min|second|sec)\b"
    r"|PerDay|PerMinute|PerHour|daily\s+(?:limit|cap|quota)|\bdaily\b"
    r"|\bTPD\b|\bRPD\b|\bRPM\b|\bTPM\b)"
    r"|(?:retryDelay'?\s{0,8}:?\s{0,8}'?)(\d+(?:\.\d+)?[smh])",
    re.I,
)


# The error string is provider-controlled text of unbounded length. WINDOW_HINT
# has no nested quantifier so it cannot backtrack catastrophically, but it is
# still O(n) per start position, so the input is bounded before scanning rather
# than trusting a remote service to keep its error messages short.
#
# The separators around `quotaId` and `retryDelay` are bounded at 8 characters
# each rather than left as `\s*`, and that bound is load-bearing rather than
# tidiness. With `\s*` the whitespace split is retried at every position in a
# whitespace run, which measured 0.135s on an 8000-character run before this
# widened vocabulary and 3.04s after it, since each failed attempt now costs
# more. Bounded, the same input measures in milliseconds. A real quota payload
# separates its key from its value with one or two characters.
WINDOW_SCAN_LIMIT = 8000

# Which side of LiteLLM's cooldown the window falls on. This is the distinction
# that decides the remedy (issue #1566), and the line is not "day versus
# minute", it is whether the window reopens inside the run: the effective
# cooldown here is 5 seconds, so a member refused on any window longer than that
# is put straight back into rotation and drawn again until the window resets.
# One member on a 20-requests-per-day cap produced 435 rate-limit failures in 48
# hours that way while contributing at most 20 successes a day.
#
# An HOUR is on the long side, not the short one. It fails both tests a day
# fails: it does not reopen inside the run and the cooldown cannot absorb it.
# The remedy differs in urgency, not in shape.
LONG_WINDOW = re.compile(r"PerDay|PerHour|per[- ](?:day|hour)|\bdaily\b|\bRPD\b|\bTPD\b", re.I)
BURST_WINDOW = re.compile(r"PerMinute|PerSecond|per[- ](?:minute|min|second|sec)|\bRPM\b|\bTPM\b", re.I)


def _window(error_text: str) -> tuple[list[str], list[str], str]:
    """Read the quota window out of a provider message.

    Returns the window tokens, the retry hints, and one of three verdicts.

    "long"     the member is out until the provider's window resets, which is
               longer than the cooldown can absorb.
    "burst"    the window reopens in seconds and the pool really does route
               around it meanwhile.
    "unknown"  the message does not say. This is a THIRD verdict rather than a
               fallback into "burst", because an absent window is not evidence
               of a short one, and folding it into the burst branch is how a
               genuinely daily-exhausted member got told it was being routed
               around: an OpenRouter `free-models-per-day` refusal, a prose
               `daily limit`, a body whose quotaId sits past WINDOW_SCAN_LIMIT
               and a truncated `Error code: 429` all produce no window at all.

    Structured evidence outranks prose, in both directions, because the two
    answer different questions. A quotaId is a quota the provider REFUSED on; a
    prose figure may be one it merely QUOTES. So a structured long window wins
    even when a structured burst is named alongside it (Google reports one
    violation per exceeded quota and the daily one is then the binding
    constraint, the one that is still spent five seconds later), while among
    prose alone the SHORTER window wins, because "on requests per minute (RPM):
    Limit 30 ... you are allowed 1000 requests per day" is a burst quoting a
    daily allowance. The two misreadings are not symmetric: calling a long
    exhaustion a burst leaves the pool where issue #1566 found it, while calling
    a burst an exhaustion prints a remedy that destroys healthy capacity.
    """
    structured: list[str] = []
    prose: list[str] = []
    delays: list[str] = []

    for match in WINDOW_HINT.finditer(error_text[:WINDOW_SCAN_LIMIT]):
        for token, bucket in zip(match.groups(), (structured, prose, delays)):
            if token and token not in bucket:
                bucket.append(token)

    def _verdict(tokens: list[str], long_first: bool) -> str:
        long_hit = any(LONG_WINDOW.search(t) for t in tokens)
        burst_hit = any(BURST_WINDOW.search(t) for t in tokens)
        if long_first:
            return "long" if long_hit else ("burst" if burst_hit else "")
        return "burst" if burst_hit else ("long" if long_hit else "")

    verdict = _verdict(structured, long_first=True) or _verdict(prose, long_first=False)
    return structured + prose, delays, verdict or "unknown"


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
            windows, delays, verdict = _window(error_text)
            label = {
                "long": "ALLOWANCE EXHAUSTED MEMBER",
                "burst": "RATE LIMITED MEMBER",
                "unknown": "QUOTA WINDOW UNREADABLE MEMBER",
            }[verdict]
            lines.append(redact(f"  {label}: {model} -- {detail}"))
            if windows:
                lines.append(redact(f"    quota window: {', '.join(windows)}"))
            if delays:
                # Printed on its own line and never as the window. Google
                # returned 34s on a spent per-day quota, so this field says
                # nothing about which verdict is right.
                lines.append(redact(f"    provider retry hint (not a window): {', '.join(delays)}"))
            if verdict == "long":
                lines.append(
                    redact(
                        f"::warning::free pool member '{model}' has spent an "
                        "allowance whose window is longer than the cooldown, "
                        "not a per-minute burst. It does not come back inside "
                        "this run, and the pool does NOT route around it: the "
                        "effective LiteLLM cooldown is 5 seconds, so the "
                        "exhausted member goes straight back into rotation and "
                        "is drawn again until the provider's window resets "
                        "(issue #1566: one member on a 20-per-day cap produced "
                        "435 rate-limit failures in 48 hours this way). Waiting "
                        "is not the remedy and neither is replacing the model. "
                        "Take the member OUT of the pool by setting its "
                        "provider_routes.health_state to 'disabled' in a new "
                        "migration, or repoint it at a model whose allowance "
                        "the pool can actually use. Read the quota window above "
                        "for how long it is out."
                    )
                )
            elif verdict == "burst":
                lines.append(
                    redact(
                        f"::warning::free pool member '{model}' is RATE LIMITED, not "
                        "gone. Do NOT replace its row: the model still exists and the "
                        "member returns when the window resets or the account is "
                        "funded. The quota window above named a window shorter than "
                        "the cooldown, so the pool routes around it meanwhile."
                    )
                )
            else:
                truncated = (
                    " The message is longer than this probe scans "
                    f"({WINDOW_SCAN_LIMIT} characters), so the window may be past the cut."
                    if len(error_text) > WINDOW_SCAN_LIMIT
                    else ""
                )
                lines.append(
                    redact(
                        f"::warning::free pool member '{model}' is rate limited and "
                        "this message does not name the window, so it is NOT known "
                        "whether it returns in seconds or only when the provider's "
                        "daily allowance resets." + truncated + " Do not act on either "
                        "reading: check the raw error, and if it is a spent daily or "
                        "hourly allowance the remedy is to take the member out of the "
                        "pool rather than to wait (issue #1566). An unread window is "
                        "reported as unknown rather than assumed to be a burst, "
                        "because assuming the burst is what let a member fail for two "
                        "days."
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
    assert "RETIRED" not in joined, (
        "a rate-limited member must not be reported as retired: acting on that "
        "advice deletes a member that is not gone"
    )
    assert "PerDay" in joined, (
        "the quota window must survive into the log, or nobody can tell a "
        "transient per-minute limit from a spent daily allowance"
    )
    assert "34s" in joined, "the provider's own retry hint must survive too"

    # Issue #1566. Telling the two windows apart was step one and shipped;
    # ACTING on the difference is this assertion. A spent DAILY allowance is not
    # waited out, because the wait runs to the provider's daily reset and the
    # member is drawn again every few seconds until then: LiteLLM's effective
    # cooldown is 5 seconds (deploy/litellm/config.yaml records the runtime
    # confirmation of that number), so the exhausted member goes straight back
    # into rotation. That is how one member contributing at most 20 successes a
    # day produced 435 rate-limit failures in 48 hours. The remedy is
    # membership, and the report has to say so where the reader is standing.
    assert "ALLOWANCE EXHAUSTED" in joined, (
        "a spent daily allowance must be called out separately from a "
        "per-minute burst, or the reader is told to wait for a window that "
        "does not reopen today"
    )
    assert "health_state" in joined, (
        "the long-window verdict must name the remedy that actually stops the "
        "traffic, which is taking the member out of the pool"
    )
    assert "routes around it" not in joined, (
        "the pool does NOT route around a daily-exhausted member: it is cooled "
        "down for 5 seconds and drawn again all day (issue #1566)"
    )
    assert "RATE LIMITED MEMBER" not in joined, (
        "the two labels are alternatives, so a change that emitted both, or "
        "appended the burst warning after the long one, must fail here"
    )
    assert "quota window: GenerateRequestsPerDay" in joined, (
        "the retryDelay is not the window and must never be printed as one; "
        "the window line carries the quotaId"
    )

    # --- The provider bodies the verdict is actually decided on --------------
    #
    # Every row below is a message shape a provider in this pool really sends,
    # and every one of them was misclassified by an earlier revision of this
    # file. They live in the self-check rather than in a review comment for the
    # reason the earlier revision existed at all: the classification was written
    # against two hand-picked bodies, both of which it got right, and a review
    # that ran fifteen real ones found three separate ways to reach the wrong
    # verdict. Provider error text drifts. When it drifts again, it should turn
    # this check red rather than turn a demo red.
    #
    # The expected label is the whole assertion. It encodes which of the three
    # verdicts the body earns, and the labels are mutually exclusive.
    long_ = "ALLOWANCE EXHAUSTED MEMBER"
    burst = "RATE LIMITED MEMBER"
    unread = "QUOTA WINDOW UNREADABLE MEMBER"
    dead = "DEAD MEMBER"

    provider_bodies = [
        # -- long: the window outlasts the cooldown, so membership is the remedy
        (
            "openrouter spells its daily refusal with a hyphen",
            "litellm.RateLimitError: Error code: 429 - {'error': {'message': "
            "'Rate limit exceeded: free-models-per-day'}}",
            long_,
        ),
        (
            "a daily refusal in bare prose, no abbreviation anywhere",
            "Error code: 429 - You have exceeded your daily limit of 50 free requests",
            long_,
        ),
        (
            "a real quotaId next to a request id that contains RPM",
            "Error code: 429 req_9fRPMk3zQ 'quotaId': "
            "'GenerateRequestsPerDayPerProjectPerModel-FreeTier'",
            long_,
        ),
        (
            "an hour is on the long side: it fails both tests a day fails",
            "Error code: 429 'quotaId': 'GenerateRequestsPerHourPerProject-FreeTier', "
            "'retryDelay': '3600s'",
            long_,
        ),
        (
            "both quotas violated at once: the daily one is what is still spent",
            "Error code: 429 violations: [{'quotaId': "
            "'GenerateRequestsPerMinutePerProjectPerModel-FreeTier'}, {'quotaId': "
            "'GenerateRequestsPerDayPerProjectPerModel-FreeTier'}]",
            long_,
        ),
        (
            "a prose hour window beside a quoted daily cap",
            "Error code: 429 - limit is 100 requests per hour; daily cap 10000 "
            "requests per day",
            long_,
        ),
        # -- burst: reopens inside the run, so the pool really does route around it
        (
            "a prose minute window quoting the daily allowance, no abbreviation",
            "Error code: 429 - Rate limit exceeded: 20 requests per minute. Your plan "
            "allows 10000 requests per day.",
            burst,
        ),
        (
            "the same shape at second granularity",
            "Error code: 429 - Too many requests: 10 requests per second. Plan allows "
            "500000 requests per day.",
            burst,
        ),
        (
            "the abbreviation abbreviated further, which no vocabulary of one vendor's "
            "spelling would catch",
            "Error code: 429 - Rate limit reached on requests per min. You are allowed "
            "1000 requests per day on this plan.",
            burst,
        ),
        (
            "groq's tokens-per-minute refusal, whose TPM used to produce no window at all",
            "Error code: 429 - Rate limit reached: on tokens per minute (TPM): Limit 6000",
            burst,
        ),
        # -- unknown: say so, rather than assuming the harmless-looking answer
        (
            "a quotaId past the scan bound",
            "Error code: 429 - " + ("x" * (WINDOW_SCAN_LIMIT + 200)) + " 'quotaId': "
            "'GenerateRequestsPerDayPerProjectPerModel-FreeTier'",
            unread,
        ),
        (
            "a body truncated to the status line",
            "Error code: 429",
            unread,
        ),
        (
            "a retryDelay and nothing else: the delay is not a window",
            "Error code: 429 - {'retryDelay': '34s'}",
            unread,
        ),
        (
            "a transient limit next to a completion id that contains RPD",
            "Error code: 429 id chatcmpl-RPDx8121 rate limit reached, try again in 2s",
            unread,
        ),
        # -- dead: a 404 that merely mentions quotas is retired, not rate limited
        (
            "a 404 quoting billing prose and a daily figure",
            "litellm.NotFoundError: Error code: 404 - {'error': {'message': 'model not "
            "found; see quota and billing docs, 1000 requests per day on the free plan'}}",
            dead,
        ),
    ]

    for name, error_text, want in provider_bodies:
        payload = {
            "healthy_count": 1,
            "unhealthy_count": 1,
            "unhealthy_endpoints": [{"model": "m", "error": error_text}],
        }
        rendered = "\n".join(_render(payload, plain))
        assert want in rendered, f"{name}: expected {want!r}\n{rendered}"
        for other in (long_, burst, unread, dead):
            if other != want:
                assert other not in rendered, (
                    f"{name}: expected {want!r} but also got {other!r}; the verdicts "
                    f"are alternatives\n{rendered}"
                )
        if want == long_:
            assert "health_state" in rendered, f"{name}: the remedy must be named\n{rendered}"
            assert "routes around it" not in rendered, f"{name}\n{rendered}"
        if want == unread:
            assert "NOT known" in rendered, (
                f"{name}: an unread window must be reported as unknown, not folded "
                f"into either verdict\n{rendered}"
            )
            assert "routes around it" not in rendered, f"{name}\n{rendered}"
            # An unread verdict means no window token matched, so there is
            # nothing to print on a window line. This is what stops the
            # retryDelay being dressed up as one: a body carrying only
            # 'retryDelay': '34s' used to print "quota window: 34s" and then
            # point the reader at it as the decider, and Google returned that
            # exact 34s on a spent PER-DAY quota.
            assert "quota window" not in rendered, (
                f"{name}: nothing may be printed as the quota window when none was "
                f"read, least of all the retry delay\n{rendered}"
            )

    # The one body above that is only one edit away from a different verdict, kept
    # as its own case because it is the input the previous revision's rule passed
    # on for the wrong reason. Groq's real wording carries the literal "(RPM)",
    # and the rule used to need it; drop those four characters and the identical
    # prose has to still classify as a burst.
    groq_real = (
        "litellm.RateLimitError: RateLimitError: Error code: 429 - {'error': "
        "{'message': 'Rate limit reached for model qwen/qwen3.8-27b in organization "
        "org_redacted on requests per minute (RPM): Limit 30, Used 30. You are "
        "allowed 1000 requests per day on this plan. Please try again in 2s.', "
        "'code': 'rate_limit_exceeded'}}"
    )
    for label, body in (("with (RPM)", groq_real), ("without (RPM)", groq_real.replace(" (RPM)", ""))):
        rendered = "\n".join(
            _render(
                {
                    "healthy_count": 1,
                    "unhealthy_count": 1,
                    "unhealthy_endpoints": [{"model": "groq/qwen/qwen3.8-27b", "error": body}],
                },
                plain,
            )
        )
        assert burst in rendered, f"groq {label}: {rendered}"
        assert long_ not in rendered, (
            f"groq {label}: a per-minute refusal that merely QUOTES the daily "
            f"allowance must not print the membership remedy\n{rendered}"
        )
        assert "routes around it" in rendered, rendered
    joined = rendered

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
