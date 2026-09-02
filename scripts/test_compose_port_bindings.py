#!/usr/bin/env python3
"""Every published port in the docker compose files must be loopback bound.

Issue #1754. Seven services in deploy/docker/docker-compose.yml published on
every interface while four others in the same file were already on 127.0.0.1:
the safe pattern existed and was applied unevenly, and nothing noticed. Docker
publishes past ufw's default deny, and no firewall or NAT state exists anywhere
in this repository, so the binding is the only control there is.

This check holds the line for the next service somebody adds. A published port
either names 127.0.0.1 or appears in ALLOWED below with the reason it does not.

Text parsing on purpose, same as the other structural checks next to this file:
the staging overlay uses compose's `!override` tag, which PyYAML refuses, and
PyYAML is not a dependency of this repository or of the CI job that runs this.
"""

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
COMPOSE = sorted((ROOT / "deploy" / "docker").glob("docker-compose*.yml"))

# Published-on-all-interfaces entries that are deliberate, keyed by
# "<file>:<service>" so a stale entry is obvious once the service moves.
ALLOWED = {
    # Tracked separately in issue #1442, the same posture question for Grafana.
    "docker-compose.yml:grafana": {"3001:3000"},
    # PR #1749 narrows this one to 127.0.0.1:3005:80 as part of issue #1744.
    # Drop this entry once that merges.
    "docker-compose.yml:caddy-console": {"3005:80"},
    # The relay coordinator is reached by remote client devices by definition:
    # 8085 is the headscale listener clients register against and 3478/udp is
    # STUN. Its metrics port is already loopback bound.
    "docker-compose.relay.yml:headscale": {"8085:8085", "3478:3478/udp"},
}

SERVICE = re.compile(r"^  ([A-Za-z0-9][A-Za-z0-9_.-]*):\s*$")
KEY = re.compile(r"^    ([A-Za-z0-9_-]+):")


def clean(text):
    """Strip a trailing comment and one wrapping layer of quotes."""
    text = re.sub(r"(^|\s)#.*$", "", text.strip()).strip()
    if len(text) >= 2 and text[0] == text[-1] and text[0] in "\"'":
        text = text[1:-1].strip()
    return text


def published(path):
    """Yield (service, port_entry) for every entry under a `ports:` key.

    Anything under `ports:` that this parser cannot reduce to an entry is
    yielded verbatim so main() fails on it. A parser that silently skipped
    what it did not understand would let an all-interface binding through
    while still reporting OK, which is the one way this check can lie.
    """
    service = None
    in_ports = False
    for line in path.read_text().splitlines():
        match = SERVICE.match(line)
        if match:
            service, in_ports = match.group(1), False
            continue
        key = KEY.match(line)
        if key:
            in_ports = key.group(1) == "ports"
            if in_ports:
                value = clean(line.split(":", 1)[1])
                if value.startswith("["):
                    # Flow style: ports: ["8080:8080", "80:80"]
                    for item in value.strip("[]").split(","):
                        if clean(item):
                            yield service, clean(item)
                elif value and not value.startswith(("!", "&", "*")):
                    # Not a tag, anchor or alias, and not a block list either.
                    # Unknown shape: yield it so it fails rather than vanishes.
                    yield service, value
            continue
        if in_ports and line.lstrip().startswith("- "):
            yield service, clean(line.lstrip()[2:])


# Regression fixtures for the parser itself. Every shape below once slipped
# through silently (issue #1754 review): the entry was skipped, `checked` never
# counted it, and the run still printed OK. A check that cannot go red is worse
# than no check, so each wide binding here must be seen.
SELFCHECK = [
    ("block", 'services:\n  api:\n    ports:\n      - "0.0.0.0:8080:8080"\n', True),
    ("trailing comment", 'services:\n  api:\n    ports:\n      - "0.0.0.0:8080:8080" # public\n', True),
    ("unquoted comment", "services:\n  api:\n    ports:\n      - 0.0.0.0:8080:8080  # public\n", True),
    ("single quotes", "services:\n  api:\n    ports:\n      - '0.0.0.0:8080:8080'\n", True),
    ("flow style", 'services:\n  api:\n    ports: ["0.0.0.0:8080:8080"]\n', True),
    ("flow mixed", 'services:\n  api:\n    ports: ["127.0.0.1:80:80", "0.0.0.0:8080:8080"]\n', True),
    ("long form", "services:\n  api:\n    ports:\n      - target: 80\n        published: 8080\n", True),
    ("loopback", 'services:\n  api:\n    ports:\n      - "127.0.0.1:8080:8080" # fine\n', False),
    ("loopback v6", 'services:\n  api:\n    ports:\n      - "[::1]:8080:8080"\n', False),
    ("override tag", 'services:\n  api:\n    ports: !override\n      - "127.0.0.1:8080:8080"\n', False),
    ("empty flow list", "services:\n  api:\n    ports: []\n", False),
    ("key line comment", 'services:\n  api:\n    ports:  # published\n      - "127.0.0.1:80:80"\n', False),
]


def selfcheck():
    import tempfile

    for name, text, expected_wide in SELFCHECK:
        path = pathlib.Path(tempfile.mkdtemp()) / "docker-compose.yml"
        path.write_text(text)
        entries = list(published(path))
        wide = [
            entry
            for _, entry in entries
            if not entry.startswith("127.0.0.1:") and not entry.startswith("[::1]:")
        ]
        assert bool(wide) == expected_wide, (
            f"{name}: parsed {entries}, wide={wide}, expected wide={expected_wide}"
        )
    print(f"OK selfcheck: {len(SELFCHECK)} parser fixtures")
    return 0


def main():
    failures = []
    used = set()
    checked = 0
    for path in COMPOSE:
        for service, entry in published(path):
            checked += 1
            if entry.startswith("127.0.0.1:") or entry.startswith("[::1]:"):
                continue
            key = f"{path.name}:{service}"
            if entry in ALLOWED.get(key, set()):
                used.add((key, entry))
                continue
            failures.append(
                f"{path.name}: service {service!r} publishes {entry!r} on every "
                "interface. Bind it to 127.0.0.1, or add it to ALLOWED in "
                f"{pathlib.Path(__file__).name} with the reason."
            )

    assert checked >= 10, f"parsed only {checked} port entries; the parser is broken"

    # An allowlist entry that stops matching is dead, and a dead entry would go
    # on excusing that service/port pair if somebody re-widened it later. Say so
    # rather than failing: the caddy-console entry goes stale the moment PR #1749
    # merges, and a hard failure there would redden main on merge order alone.
    for key, entries in sorted(ALLOWED.items()):
        for entry in sorted(entries):
            if (key, entry) not in used:
                print(
                    f"WARN stale allowlist entry {key} {entry!r}: no longer present, "
                    "delete it from ALLOWED",
                    file=sys.stderr,
                )

    if failures:
        for failure in failures:
            print(f"FAIL {failure}", file=sys.stderr)
        return 1
    print(f"OK {checked} published port entries, all loopback bound or allowlisted")
    return 0


if __name__ == "__main__":
    if "--selfcheck" in sys.argv:
        sys.exit(selfcheck())
    sys.exit(main())
