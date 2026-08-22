#!/usr/bin/env python3
"""Refuse an OAUTH_SCOPES value the deployed authorization server does not
advertise.

Why this exists
---------------
PR #787 added `offline_access` to OAUTH_SCOPES in deploy/docker/docker-compose.yml
after validating against hosted Supabase, whose discovery document advertised
that scope. The self-hosted GoTrue this stack cut over to does not advertise it:
scopes_supported is openid, profile, email, phone. GoTrue rejects an unknown
scope outright rather than ignoring it, so the authorize request 302'd straight
back to the callback with `error=invalid_request&error_description=unsupported
scope: offline_access`. No consent screen, no authorization code, no session.
Chat sign-in was dead for every user, and every test in the repository was green,
because nothing in the repository knew what the backend could do.

Two sides of one mismatch, and they drift independently:

  * a scope added to the repository that the backend does not know, which is
    #787, caught when this runs on a pull request that touches deploy YAML;
  * a capability the backend stops advertising while the repository stands
    still, which is what the self-hosted cutover actually did, caught when this
    runs after a deploy.

So this reads the live document rather than any checked-in snapshot of it. A
snapshot would have been written from hosted Supabase and would have agreed with
#787 all the way into the outage.

Usage
-----
    check-oauth-scopes.py --discovery https://host/auth/v1/.well-known/openid-configuration
    check-oauth-scopes.py --discovery-file fixture.json    # offline, for tests
    check-oauth-scopes.py --self-check

Exit 0 when every declared scope is advertised, 1 otherwise. An unreachable or
malformed discovery document is also exit 1: this check has nothing useful to
say without one, and saying nothing quietly is the failure mode it exists to
remove.
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DEPLOY_DIR = REPO_ROOT / "deploy"
ENV_KEY = "OAUTH_SCOPES"
TIMEOUT_SECONDS = 30


def declarations(deploy_dir: Path) -> list[tuple[Path, int, str]]:
    """Every OAUTH_SCOPES assignment in deploy YAML, with file and line.

    Scans all deploy YAML rather than one known line, so a second compose file,
    an override or a profile cannot reintroduce the defect somewhere new. This
    mirrors envDeclarations in apps/edge-api/internal/auth/owui_oauth_scope_test.go
    on purpose: same corpus, one side checked offline and one side checked
    against the live server.

    Both Compose environment spellings are recognized, mapping and list:

        environment:
          OAUTH_SCOPES: "openid email profile"

        environment:
          - OAUTH_SCOPES=openid email profile

    Reading only the first would be the same defect in a new place. Compose
    accepts either, so a declaration written the other way would sail past a
    check whose entire job is to have no blind spot, and it would do it
    silently, reporting a clean run over a corpus of zero.
    """
    found: list[tuple[Path, int, str]] = []
    for path in sorted(deploy_dir.rglob("*")):
        if path.suffix not in (".yml", ".yaml") or not path.is_file():
            continue
        for number, line in enumerate(path.read_text().splitlines(), start=1):
            stripped = line.strip()
            if stripped.startswith("#"):
                continue
            if stripped.startswith(ENV_KEY + ":"):
                raw = stripped[len(ENV_KEY) + 1 :]
            elif stripped.startswith("- " + ENV_KEY + "=") or stripped.startswith("-" + ENV_KEY + "="):
                raw = stripped.split("=", 1)[1]
            else:
                continue
            found.append((path, number, raw.strip().strip("\"'")))
    return found


def fetch_supported(url: str) -> list[str]:
    """scopes_supported from a live discovery document.

    Raises RuntimeError with a legible cause. Never returns a default: a
    default here would let an unreachable server read as a pass, which is the
    silent absence this check exists to replace with a loud failure.
    """
    request = urllib.request.Request(url, headers={"User-Agent": "hive-oauth-scope-check"})
    try:
        with urllib.request.urlopen(request, timeout=TIMEOUT_SECONDS) as response:
            document = json.load(response)
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        raise RuntimeError(f"could not fetch {url}: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"{url} did not return JSON: {exc}") from exc
    return read_supported(document, url)


def read_supported(document: object, origin: str) -> list[str]:
    if not isinstance(document, dict):
        raise RuntimeError(f"{origin} is not an OpenID discovery document")
    supported = document.get("scopes_supported")
    if not isinstance(supported, list) or not supported:
        raise RuntimeError(
            f"{origin} declares no usable scopes_supported. An authorization "
            "server that advertises nothing cannot be checked against, and "
            "treating that as a pass is how an unsupported scope ships."
        )
    return [str(scope) for scope in supported]


def report(
    found: list[tuple[Path, int, str]],
    supported: list[str],
    origin: str,
    scanned: Path = DEPLOY_DIR,
) -> int:
    """Print the verdict and return the process exit code."""
    print(f"authorization server: {origin}")
    print(f"scopes_supported:     {' '.join(supported)}")

    if not found:
        print(
            f"::error::no {ENV_KEY} declaration found under {scanned}. Two "
            "causes, and neither is a reason to exit 0: the Open WebUI OIDC "
            "wiring moved, in which case move this check with it; or this "
            "script is being run from a copy outside the repository, since the "
            "corpus is resolved relative to the script's own path and a copy "
            "elsewhere sees an empty one.",
            file=sys.stderr,
        )
        return 1

    advertised = set(supported)
    failures = 0
    for path, number, value in found:
        try:
            relative = path.relative_to(REPO_ROOT)
        except ValueError:
            relative = path
        requested = value.split()
        unknown = [scope for scope in requested if scope not in advertised]
        if unknown:
            failures += 1
            print(
                f"::error file={relative},line={number}::{relative}:{number} requests "
                f"{' '.join(unknown)}, which {origin} does not advertise. GoTrue "
                "rejects an unknown scope outright, so this 302s the authorize "
                "request back to the callback with error=invalid_request and no "
                "session is ever created: chat sign-in is dead for every user "
                f"(#787). Advertised scopes are {' '.join(supported)}.",
                file=sys.stderr,
            )
        else:
            print(f"ok {relative}:{number} {ENV_KEY}={value!r}")
    return 1 if failures else 0


def self_check() -> int:
    """Run the unit tests next to this script, so `--self-check` cannot pass
    over a comparator nobody exercised."""
    test = Path(__file__).with_name("test_check_oauth_scopes.py")
    return subprocess.call([sys.executable, str(test)])


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group()
    source.add_argument("--discovery", help="URL of the OpenID discovery document")
    source.add_argument(
        "--discovery-file",
        help="a discovery document on disk, for tests and offline runs",
    )
    parser.add_argument("--self-check", action="store_true", help="run this script's own tests")
    args = parser.parse_args()

    if args.self_check:
        return self_check()

    try:
        if args.discovery_file:
            origin = args.discovery_file
            supported = read_supported(json.loads(Path(origin).read_text()), origin)
        elif args.discovery:
            origin = args.discovery
            supported = fetch_supported(origin)
        else:
            parser.error("one of --discovery, --discovery-file or --self-check is required")
    except (RuntimeError, OSError, json.JSONDecodeError) as exc:
        print(f"::error::{exc}", file=sys.stderr)
        return 1

    return report(declarations(DEPLOY_DIR), supported, origin, DEPLOY_DIR)


if __name__ == "__main__":
    raise SystemExit(main())
