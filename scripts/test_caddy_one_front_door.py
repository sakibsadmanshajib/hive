#!/usr/bin/env python3
"""One origin, one sign in: the chat listener must serve exactly one front door.

Issue #540. The chat origin used to mount two whole applications behind one
Caddy listener. Open WebUI served the shell and held the browser's only
credential, its own session token; `apps/agent-console` was reverse proxied at
`/agent-workspace/*` and ran an entirely separate `@supabase/ssr` session, which
its own `middleware.ts` documents as deliberate ("this app is a standalone
console with its own Supabase session, not a cookie handoff from Open WebUI").

Those two never shared anything, and they could not: chat login is Open WebUI's
OIDC flow, so the browser holds no Supabase cookie at all on that origin, and
the Supabase access token lives server side inside the chat container where
`deploy/docker/owui-patches/hive_agent_proxy.py` resolves it per request and
deliberately never returns it to the browser. So every visit to
`/agent-workspace/*`, signed into chat or not, produced a full second sign in
page. Measured against the demo box on 2026-08-29:

    GET /agent-workspace       -> 307 /agent-workspace/tasks
    GET /agent-workspace/tasks -> 307 /agent-workspace/auth/sign-in

The fix was not to join the two sessions. PR #951 had already joined the one
that matters by rendering the agent surface natively inside the shell and
routing it through `/api/v1/hive/agent/*` under Open WebUI's own session gate,
and D-045 rules that the separate route, origin and login go away rather than
being restyled. What this file pins is the result: the second door is gone and
cannot come back unnoticed.

Two properties, because the defect needs both to stay fixed:

  * The chat Caddyfile answers `/agent-workspace*` rather than proxying it, and
    proxies no second application at all.
  * Every route on the agent proxy depends on Open WebUI's `get_verified_user`,
    so the one session that signs a user in is the one that signs them out of
    the agent surface too. A route that authenticated itself some other way
    would reintroduce a session that survives a chat sign out, which is exactly
    the defect a naive "share the cookie" fix introduces.

No framework, no network, no Docker. Run via `make test-scripts`.
"""

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
CADDYFILE = ROOT / "deploy" / "docker" / "Caddyfile.owui"
AGENT_PROXY = ROOT / "deploy" / "docker" / "owui-patches" / "hive_agent_proxy.py"

# The application that used to hold the second session. Named as a string
# rather than matched loosely, because the failure this guards against is a
# reverse_proxy pointing back at it under any path.
SECOND_APP_UPSTREAM = "agent-console"


def _removed_surface_pattern() -> re.Pattern:
    """Pull the @removedSurfaces regex out of the Caddyfile.

    Read from the file rather than duplicated here, so editing the Caddyfile is
    what this test measures. Fails loudly if the matcher is renamed or split,
    which is the failure mode that lets a guard quietly stop guarding.
    """
    matches = re.findall(
        r"^\s*path_regexp\s+removed\s+(\S+)\s*$", CADDYFILE.read_text(), re.MULTILINE
    )
    if len(matches) != 1:
        raise AssertionError(
            f"expected exactly one `path_regexp removed <re>` line in {CADDYFILE}, "
            f"found {len(matches)}. If the matcher was renamed or split, update "
            "this test deliberately: do not let it pass by matching nothing."
        )
    return re.compile(matches[0])


def test_the_second_front_door_is_answered_not_proxied() -> None:
    """Every shape of the old console path is refused by the proxy.

    The variants are the same ones the sibling matchers in this Caddyfile
    already tolerate: a bare entry URL with no trailing slash (what a bookmark
    or a typed link looks like), a trailing slash, arbitrary sub paths, mixed
    casing, and the `//` traversal form that a `^/+` anchor collapses.
    """
    pattern = _removed_surface_pattern()
    for path in (
        "/agent-workspace",
        "/agent-workspace/",
        "/agent-workspace/tasks",
        "/agent-workspace/auth/sign-in",
        "/agent-workspace/auth/callback",
        "/agent-workspace/api/deck/abc-123",
        "//agent-workspace",
        "/Agent-Workspace",
        "/AGENT-WORKSPACE/tasks",
    ):
        assert pattern.match(path), path


def test_refusing_the_console_path_spares_the_agent_surface_itself() -> None:
    """The native surface and its API are on this same origin and must survive.

    `/agents` is the page (vendor/open-webui/src/routes/(app)/agents), and the
    two API prefixes are what it calls: `/api/v1/hive/agent/*` inside the chat
    container and `/v1/agent/*` proxied to edge-api. A matcher written as a
    loose `agent` prefix would take all three down, which is a worse outcome
    than the second sign in it removes.
    """
    pattern = _removed_surface_pattern()
    for path in (
        "/agents",
        "/agents/",
        "/api/v1/hive/agent/tasks",
        "/api/v1/hive/agent/tasks/0b3f2c1e-7a5d-4e11-9c8b-2f6a1d3e4b57/events",
        "/api/v1/hive/credits/balance",
        "/v1/agent/tasks",
        "/v1/featuregate",
        # Not a prefix match: only the whole segment counts.
        "/agent-workspaces",
        "/agent-workspace-old",
    ):
        assert not pattern.match(path), path


def test_the_chat_listener_proxies_no_second_application() -> None:
    """A 404 on the path is only half the removal.

    Answering `/agent-workspace*` while some other matcher still proxies the
    same upstream would leave the second sign in reachable under a new name.
    Caddy sorts `respond` ahead of `reverse_proxy`, so the order in the file
    would not even make the collision visible.
    """
    offenders = [
        line.strip()
        for line in CADDYFILE.read_text().splitlines()
        if "reverse_proxy" in line
        and not line.lstrip().startswith("#")
        and SECOND_APP_UPSTREAM in line
    ]
    assert not offenders, (
        "Caddyfile.owui reverse-proxies the standalone agent console again, which "
        f"restores a second sign in on the chat origin (#540): {offenders}"
    )


def test_every_agent_route_is_gated_on_the_chat_session() -> None:
    """One session in, one session out.

    The agent surface must hold no credential of its own, so that signing out of
    chat leaves nothing on it authenticated. In practice that means every route
    on the proxy router depends on Open WebUI's `get_verified_user` and none
    reads an identity from the request. A route added without that dependency
    would be reachable by a session Open WebUI has already invalidated.
    """
    source = AGENT_PROXY.read_text()

    routes = re.findall(
        r"^@router\.(get|post|put|patch|delete)\((.*?)\)\s*$\n"
        r"(?:async\s+)?def\s+(\w+)\s*\((.*?)\)\s*:",
        source,
        re.MULTILINE | re.DOTALL,
    )
    assert routes, (
        f"found no @router routes in {AGENT_PROXY}. If the router was renamed or "
        "restructured, update this test deliberately: do not let it pass by "
        "matching nothing."
    )

    ungated = [
        name for _verb, _path, name, params in routes
        if "Depends(get_verified_user)" not in params
    ]
    assert not ungated, (
        "these agent proxy routes do not depend on Open WebUI's get_verified_user, "
        "so they answer a caller the chat session no longer covers and survive a "
        f"chat sign out (#540): {ungated}"
    )

    assert "from open_webui.utils.auth import get_verified_user" in source, (
        f"{AGENT_PROXY} no longer imports get_verified_user from Open WebUI's own "
        "auth module, so the dependency named on every route may be a local stand "
        "in rather than the chat session gate"
    )


def main() -> int:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: one front door on the chat origin (issue #540)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
