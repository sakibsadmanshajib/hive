#!/usr/bin/env python3
"""Pin a bounded dial retry on every user-facing reverse_proxy upstream (#1407).

Both public origins on the demo box serve 502s during a deploy, and only during
a deploy. Over the 24 hours to 2026-08-29T10:00Z the chat origin served 55 and
the console origin 3, in bursts of 4 to 40 seconds, every burst inside a
`deploy-demo-box.yml` run window and none of them against a healthy upstream.
They arrive as two different error strings depending on where in a container
recreate the request landed -- `lookup <name> on 127.0.0.11:53: server
misbehaving` while the old container's name is deregistered, and
`connect: connection refused` once the new one has registered but is not
listening yet -- but they are one defect, and one bounded retry across the
recreate covers both.

What this test is for is the recurrence, not the original fix. `Caddyfile.owui`
has grown three reverse_proxy routes since it was written (the agent console,
the agent API and the featuregate probe), and each was added by copying the
block above it. A fourth added the same way, from a block that predates this
change or from another file, would silently ship without the retry and serve
502s again on the next deploy. This fails when that happens.

Structural only: no network, no Docker, no Caddy binary. Run via
`make test-scripts`.
"""

import pathlib
import re
import sys

REPO = pathlib.Path(__file__).resolve().parent.parent
DOCKER = REPO / "deploy" / "docker"

# The two public origins. Deliberately not the whole directory:
#
#   * Caddyfile.supabase and Caddyfile.artifacts both measured zero 502s over
#     the same window, so requiring a retry there would be speculative.
#   * Caddyfile.agent-proof is a scratch proof harness, not a product surface,
#     and fails fast on purpose.
#
# Add a file here when it starts fronting a user-facing origin, not before.
GUARDED = ["Caddyfile.owui", "Caddyfile.console"]

# A retry that never fires is decoration and one that runs for minutes is a
# hang. The upper bound also keeps a held request under Cloudflare's 100s
# origin timeout, so it can never turn into a 524 instead of the 502 it is
# meant to replace.
MIN_TRY_DURATION_S = 5
MAX_TRY_DURATION_S = 60

# Caddy takes Go durations, which compose: `1m30s` is as valid as `90s`, and a
# guard that rejected the first would fail a legitimate config.
DURATION_PART = re.compile(r"(\d+(?:\.\d+)?)(ms|us|µs|ns|s|m|h)")
UNIT_SECONDS = {
    "ns": 1e-9,
    "us": 1e-6,
    "µs": 1e-6,
    "ms": 0.001,
    "s": 1.0,
    "m": 60.0,
    "h": 3600.0,
}


def parse_duration_s(raw):
    """Caddy duration to seconds, or None if it is not one we understand."""
    total = 0.0
    consumed = 0
    for match in DURATION_PART.finditer(raw):
        if match.start() != consumed:
            return None
        total += float(match.group(1)) * UNIT_SECONDS[match.group(2)]
        consumed = match.end()
    if consumed != len(raw) or consumed == 0:
        return None
    return total


def strip_inline_comment(line):
    """Drop a Caddyfile comment from the end of a line.

    Caddy treats `#` as a comment only where it begins a token, so a `#` that
    is glued to preceding non-space text is data, not a comment. This does not
    understand quoting, which is fine for the two files it reads: neither has a
    `#` inside a quoted value, and the only cost of getting it wrong would be
    an over-eager truncation that makes the guard louder, never quieter.
    """
    if line.startswith("#"):
        return ""
    cut = re.search(r"\s#", line)
    return line[: cut.start()].rstrip() if cut else line


def proxy_blocks(text):
    """Yield (line_no, upstream, body_lines) for each reverse_proxy with a block.

    Caddyfile nesting is plain braces, so a depth counter is enough here. Only
    whole-line comments are stripped, which is all these two files use.
    """
    lines = text.splitlines()
    out = []
    i = 0
    while i < len(lines):
        stripped = strip_inline_comment(lines[i].strip())
        tokens = stripped.split()
        if not tokens or tokens[0] != "reverse_proxy":
            i += 1
            continue
        start = i + 1
        if not stripped.endswith("{"):
            # A bare one-line `reverse_proxy host:port` with no options block.
            # Reported as a block with an empty body so it fails the retry
            # assertion rather than being skipped.
            out.append((start, tokens[-1], []))
            i += 1
            continue
        # Upstream is the last token before the opening brace. Any leading
        # matcher token (`@agentApi`) sits before it.
        upstream = stripped[: -len("{")].strip().split()[-1]
        depth = 1
        body = []
        i += 1
        while i < len(lines) and depth > 0:
            line = strip_inline_comment(lines[i].strip())
            i += 1
            # Comments are removed before the depth arithmetic, not after. An
            # unbalanced brace inside one would otherwise close the block early
            # and hide every directive below it, which is the one way this
            # parser could silently report a pass it should not.
            if not line:
                continue
            # Placeholders (`{$HIVE_CHAT_EXTERNAL_SCHEME:http}`, `{remote_host}`)
            # are balanced on their own line, so they cancel out here.
            depth += line.count("{") - line.count("}")
            if depth > 0:
                body.append(line)
        out.append((start, upstream, body))
    return out


def check_file(path):
    failures = []
    blocks = proxy_blocks(path.read_text())
    if not blocks:
        failures.append(f"{path.name}: no reverse_proxy block found at all")
        return failures
    for line_no, upstream, body in blocks:
        directives = {}
        for line in body:
            parts = line.split()
            if len(parts) >= 2 and parts[0] in ("lb_try_duration", "lb_try_interval"):
                directives[parts[0]] = parts[1]

        where = f"{path.name}:{line_no} (upstream {upstream})"

        if "lb_try_duration" not in directives:
            failures.append(
                f"{where}: no lb_try_duration. A request arriving while this "
                f"upstream is being recreated fails with a 502 instead of "
                f"waiting for it to come back. See #1407."
            )
            continue

        seconds = parse_duration_s(directives["lb_try_duration"])
        if seconds is None:
            failures.append(
                f"{where}: lb_try_duration {directives['lb_try_duration']!r} is "
                f"not a duration this check understands"
            )
        elif seconds < MIN_TRY_DURATION_S:
            failures.append(
                f"{where}: lb_try_duration {directives['lb_try_duration']} is "
                f"below {MIN_TRY_DURATION_S}s, shorter than the recreate window "
                f"it exists to cover"
            )
        elif seconds > MAX_TRY_DURATION_S:
            failures.append(
                f"{where}: lb_try_duration {directives['lb_try_duration']} is "
                f"above {MAX_TRY_DURATION_S}s. A held request must stay under "
                f"Cloudflare's 100s origin timeout, or a 502 becomes a 524"
            )

        if "lb_try_interval" not in directives:
            failures.append(
                f"{where}: lb_try_duration without lb_try_interval. The 250ms "
                f"default issues over a hundred lookups per held request "
                f"against the same resolver that is already failing"
            )
    return failures


# Three blocks on purpose. The first carries a nested block and a placeholder,
# so a parser that lost track of brace depth there would swallow the two below
# it and report a clean pass. The middle one is the omission this guard exists
# to catch. The last proves depth came back to zero. The comment inside the
# middle block carries a deliberately unbalanced brace, which is the other way
# a naive parser closes a block early and reports a pass.
SYNTHETIC_UNGUARDED = """\
:80 {
  reverse_proxy @api edge-api:8080 {
    lb_try_duration 30s
    lb_try_interval 1s
    header_up X-Forwarded-Proto {$SOME_SCHEME:http}
    transport http {
      dial_timeout 5s
    }
  }

  reverse_proxy open-webui:8080 {
    # a comment with an unbalanced { brace
    header_up X-Real-IP {remote_host}
    transport http {
      dial_timeout 5s
    }
  }

  reverse_proxy web-console-prod:3000 {  # inline comment on the opening line
    lb_try_duration 30s
    lb_try_interval 1s
  }

  reverse_proxy caddy-supabase:8080 {
    lb_try_duration 30s
  }
}
"""


DURATION_CASES = [
    ("30s", 30.0),
    ("1m30s", 90.0),  # Go durations compose; rejecting this would be a false red
    ("250ms", 0.25),
    ("1m", 60.0),
    ("banana", None),
    ("30", None),
    ("30sx", None),
    ("", None),
]


def self_check():
    """Prove the checker can go red, not only green, before trusting it."""
    import tempfile

    for raw, expected in DURATION_CASES:
        got = parse_duration_s(raw)
        if got != expected:
            print(
                f"SELF-CHECK FAILED: parse_duration_s({raw!r}) returned {got!r}, "
                f"expected {expected!r}",
                file=sys.stderr,
            )
            return False

    with tempfile.TemporaryDirectory() as tmp:
        bad = pathlib.Path(tmp) / "Caddyfile.synthetic"
        bad.write_text(SYNTHETIC_UNGUARDED)
        found = check_file(bad)

    # Two failures, from two different blocks and two different causes: the
    # block with no retry at all, and the block with a duration but no
    # interval. The second is what proves the interval branch is reachable
    # rather than dead code that no test ever enters.
    if len(found) != 2:
        print(
            "SELF-CHECK FAILED: the synthetic Caddyfile should report exactly "
            f"two failures, got {len(found)}: {found}",
            file=sys.stderr,
        )
        return False
    if "open-webui:8080" not in found[0] or "no lb_try_duration" not in found[0]:
        print(
            f"SELF-CHECK FAILED: first failure is not the unguarded block: {found[0]}",
            file=sys.stderr,
        )
        return False
    if "caddy-supabase:8080" not in found[1] or "without lb_try_interval" not in found[1]:
        print(
            f"SELF-CHECK FAILED: second failure is not the missing interval: {found[1]}",
            file=sys.stderr,
        )
        return False
    return True


def main():
    if not self_check():
        return 1

    failures = []
    for name in GUARDED:
        path = DOCKER / name
        if not path.exists():
            failures.append(f"{name}: guarded Caddyfile is missing from {DOCKER}")
            continue
        failures.extend(check_file(path))

    if failures:
        print("Caddy upstream retry guard FAILED:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1

    print(f"Caddy upstream retry guard OK ({', '.join(GUARDED)})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
