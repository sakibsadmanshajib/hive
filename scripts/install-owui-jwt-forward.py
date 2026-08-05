#!/usr/bin/env python3
"""Install, refresh and activate the hive_jwt_forward Open WebUI Function.

Issue #556. Per-user chat attribution depends on this Filter existing in Open
WebUI, active and global. It is not a file the container auto-loads: Open WebUI
has no mount or env var that installs a Function, only the Functions REST API
(see deploy/docker/docker-compose.yml's open-webui NOTE and the module docstring
of deploy/docker/pipelines/hive_jwt_forward.py). Until this script existed the
only caller of that API was CI's Playwright setup, so no deployment ever had the
filter and every chat completion from Open WebUI was refused by edge-api's
OWUIUnwrap middleware with "This chat session is not carrying a signed-in user
token".

This is the single implementation of that install. Both callers use it:

  * .github/workflows/deploy-demo-box.yml runs it on the demo box on every
    deploy, with a token minted by scripts/owui-mint-admin-token.py.
  * apps/web-console/e2e/phase-19/owui/owui.setup.ts runs it in the nightly
    OWUI e2e job with the token of the admin session it just signed in as,
    which is what exercises this script end to end before a deploy uses it.

Idempotency is the whole point, because a deploy re-runs it every time:

  * Absent               -> POST /api/v1/functions/create.
  * Present, stale body  -> POST /api/v1/functions/id/<id>/update, so an edit to
                            hive_jwt_forward.py actually reaches a long-lived
                            deployment instead of being pinned to whatever was
                            installed first.
  * Present, same body   -> no write at all.
  * is_active/is_global  -> toggled only when currently false. Both toggle
                            endpoints FLIP the flag rather than set it
                            (routers/functions.py, v0.10.2), so calling them
                            unconditionally would switch the filter OFF on the
                            second deploy.

The end state is then re-read from the API and asserted. A 2xx from the install
calls is not proof: a filter only runs when it is both active and global (see
open_webui.utils.filter.get_sorted_filter_ids upstream), and #556 is a bug about
an absence nobody noticed, so this script must never report success on anything
it has not read back.

Auth: OWUI_ADMIN_TOKEN in the environment, sent as a Bearer token. Every
Functions endpoint used here is admin-only (Depends(get_admin_user)).

Base URL: the caddy-owui origin works (deploy/docker/Caddyfile.owui blocks
mutation verbs only on paths that do not cross a slash, so /api/v1/functions/...
passes through while /api/v1/auths/signin does not). Open WebUI's own port also
works when reachable. Plaintext http is accepted only for a loopback host,
where the token never leaves the machine; any remote origin must be https. See
require_safe_origin.

Run: OWUI_ADMIN_TOKEN=... python3 scripts/install-owui-jwt-forward.py
"""
import argparse
import ipaddress
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

FUNCTION_ID = "hive_jwt_forward"
FUNCTION_NAME = "Hive JWT Forward"
FUNCTION_DESCRIPTION = (
    "Injects the signed-in user's OAuth access token into "
    "__metadata.upstream_auth for edge-api's OWUI unwrap middleware (#269)."
)
DEFAULT_BASE_URL = "http://localhost:3003"
DEFAULT_SOURCE = (
    Path(__file__).resolve().parent.parent
    / "deploy"
    / "docker"
    / "pipelines"
    / "hive_jwt_forward.py"
)


class OwuiError(RuntimeError):
    pass


def require_safe_origin(base_url: str) -> None:
    """Refuse to put an admin Bearer token on the wire in cleartext.

    Plaintext is fine, and is what both callers use, when the request never
    leaves the machine: the deploy step and the e2e setup both talk to Open
    WebUI's published port on their own host, so there is no network hop to
    intercept. Rejecting http:// outright would reject exactly that case and
    break the deploy while protecting nothing.

    What is genuinely unsafe is pointing this at a REMOTE host over http,
    which would ship an Open WebUI admin session across a network in the
    clear. That is refused here.

    The host is parsed rather than substring-matched, so http://localhost.
    evil.example -- whose hostname merely starts with "localhost" -- is
    refused like any other remote origin.
    """
    parsed = urllib.parse.urlsplit(base_url)
    if parsed.scheme == "https":
        return
    host = parsed.hostname or ""
    if host == "localhost":
        return
    try:
        if ipaddress.ip_address(host).is_loopback:
            return
    except ValueError:
        pass
    raise OwuiError(
        f"refusing to send an Open WebUI admin token to {base_url!r} over "
        f"{parsed.scheme or 'no'}: the host {host or '(none)'} is not loopback, "
        "so the token would cross a network in cleartext. Use https, or point "
        "this at Open WebUI's port on the same host."
    )


def api(
    base_url: str,
    token: str,
    method: str,
    path: str,
    body: dict | None = None,
) -> tuple[int, object]:
    """One Functions API call. Returns (status, parsed body).

    A non-2xx is returned rather than raised so callers can treat the
    documented "not installed" statuses as a state instead of a failure.
    """
    data = None
    headers = {"Authorization": f"Bearer {token}", "Accept": "application/json"}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(
        f"{base_url}{path}", data=data, headers=headers, method=method
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            raw = response.read().decode()
            status = response.status
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode()
        status = exc.code
    except urllib.error.URLError as exc:
        raise OwuiError(f"{method} {path} could not reach {base_url}: {exc}") from exc
    try:
        return status, json.loads(raw) if raw else None
    except json.JSONDecodeError:
        return status, raw


def read_function(base_url: str, token: str) -> dict | None:
    """Current stored state, or None when the function is not installed.

    v0.10.2 answers an unknown id with 401 NOT_FOUND rather than 404, so both
    are treated as absence. Anything else is a real failure: a 403 from a
    non-admin token must not be mistaken for "not installed yet" and then
    reported as a successful install.
    """
    status, body = api(
        base_url, token, "GET", f"/api/v1/functions/id/{FUNCTION_ID}"
    )
    if status == 200:
        return body if isinstance(body, dict) else None
    if status in (401, 404):
        return None
    raise OwuiError(
        f"unexpected status reading {FUNCTION_ID}: {status} {body!r}"
    )


def write(base_url: str, token: str, path: str, body: dict | None, what: str) -> None:
    status, response = api(base_url, token, "POST", path, body)
    if status < 200 or status >= 300:
        raise OwuiError(f"{what} failed: {status} {response!r}")


def form(content: str) -> dict:
    return {
        "id": FUNCTION_ID,
        "name": FUNCTION_NAME,
        "content": content,
        "meta": {"description": FUNCTION_DESCRIPTION},
    }


def ensure_installed(base_url: str, token: str, content: str) -> None:
    """Create or refresh the function, then activate and globalize it."""
    state = read_function(base_url, token)
    # ponytail: byte comparison against the file. Open WebUI's
    # utils.plugin.replace_imports rewrites `from utils|apps|main|config`
    # prefixes before storing, and hive_jwt_forward.py has none of them
    # (checked). If one is ever added, this reads as permanent drift and
    # pushes a harmless update every deploy; mirror replace_imports here if
    # that noise ever matters.
    if state is None:
        write(
            base_url,
            token,
            "/api/v1/functions/create",
            form(content),
            f"creating {FUNCTION_ID}",
        )
        print(f"{FUNCTION_ID}: created")
    elif state.get("content") != content:
        write(
            base_url,
            token,
            f"/api/v1/functions/id/{FUNCTION_ID}/update",
            form(content),
            f"updating {FUNCTION_ID}",
        )
        print(f"{FUNCTION_ID}: content refreshed from source")

    # Both endpoints flip, so they are called only when the flag is false. A
    # freshly created function starts inactive and non-global.
    if not (state or {}).get("is_active"):
        write(
            base_url,
            token,
            f"/api/v1/functions/id/{FUNCTION_ID}/toggle",
            None,
            f"activating {FUNCTION_ID}",
        )
        print(f"{FUNCTION_ID}: activated")
    if not (state or {}).get("is_global"):
        write(
            base_url,
            token,
            f"/api/v1/functions/id/{FUNCTION_ID}/toggle/global",
            None,
            f"globalizing {FUNCTION_ID}",
        )
        print(f"{FUNCTION_ID}: made global")


def verify(base_url: str, token: str, content: str) -> dict:
    """Re-read the end state and assert it, or raise."""
    state = read_function(base_url, token)
    if state is None:
        raise OwuiError(
            f"{FUNCTION_ID} is still not installed after the install calls "
            "reported success"
        )
    problems = []
    if not state.get("is_active"):
        problems.append("is_active is false")
    if not state.get("is_global"):
        problems.append("is_global is false")
    if state.get("content") != content:
        problems.append("stored content does not match the repo source")
    if problems:
        raise OwuiError(
            f"{FUNCTION_ID} is installed but not usable: " + ", ".join(problems)
        )
    return state


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--base-url",
        default=DEFAULT_BASE_URL,
        help=f"Open WebUI origin (default {DEFAULT_BASE_URL})",
    )
    parser.add_argument(
        "--source",
        default=str(DEFAULT_SOURCE),
        help="path to hive_jwt_forward.py",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    token = os.environ.get("OWUI_ADMIN_TOKEN", "").strip()
    if not token:
        print(
            "OWUI_ADMIN_TOKEN is empty. Mint one on a deployment box with "
            "scripts/owui-mint-admin-token.py.",
            file=sys.stderr,
        )
        return 2
    base_url = args.base_url.rstrip("/")
    content = Path(args.source).read_text(encoding="utf-8")
    try:
        # Before the token is put in a header, not after.
        require_safe_origin(base_url)
        ensure_installed(base_url, token, content)
        state = verify(base_url, token, content)
    except OwuiError as exc:
        print(f"hive_jwt_forward install FAILED: {exc}", file=sys.stderr)
        return 1
    print(
        f"{FUNCTION_ID}: verified present, active and global on {base_url} "
        f"(type={state.get('type')})"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
