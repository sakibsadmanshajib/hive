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
ENTRY = re.compile(r'^\s*-\s*"?([^"#]+?)"?\s*$')


def published(path):
    """Yield (service, port_entry) for every list item under a `ports:` key."""
    service = None
    in_ports = False
    for line in path.read_text().splitlines():
        match = SERVICE.match(line)
        if match:
            service, in_ports = match.group(1), False
            continue
        if KEY.match(line):
            in_ports = line.strip().startswith("ports:")
            continue
        if in_ports and line.strip().startswith("- "):
            entry = ENTRY.match(line)
            if entry:
                yield service, entry.group(1)


def main():
    failures = []
    checked = 0
    for path in COMPOSE:
        for service, entry in published(path):
            checked += 1
            if entry.startswith("127.0.0.1:") or entry.startswith("[::1]:"):
                continue
            if entry in ALLOWED.get(f"{path.name}:{service}", set()):
                continue
            failures.append(
                f"{path.name}: service {service!r} publishes {entry!r} on every "
                "interface. Bind it to 127.0.0.1, or add it to ALLOWED in "
                f"{pathlib.Path(__file__).name} with the reason."
            )

    assert checked >= 10, f"parsed only {checked} port entries; the parser is broken"

    if failures:
        for failure in failures:
            print(f"FAIL {failure}", file=sys.stderr)
        return 1
    print(f"OK {checked} published port entries, all loopback bound or allowlisted")
    return 0


if __name__ == "__main__":
    sys.exit(main())
