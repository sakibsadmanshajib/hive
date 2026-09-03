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

import ast
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


# The routes the agent proxy is expected to expose, asserted as an exact set.
#
# A guard that only walks whatever it happens to find can go quiet without
# failing: it reports "all routes gated" while silently checking fewer of them.
# Naming the set makes both directions loud. A route removed fails here, and a
# route added fails here until someone states, in this list, that they thought
# about whether the chat session gates it.
EXPECTED_AGENT_ROUTES = {
    "list_tasks",
    "create_task",
    "get_task",
    "cancel_task",
    "list_task_events",
    # The per-run subscription (issue #1622). Same authentication decision as
    # list_task_events beside it, which it replaces on the live path: the chat
    # session is the principal, the route reads one task the caller already
    # owns, and control-plane refuses a task belonging to anyone else with a
    # 404 before a single frame is written. It holds a connection open where
    # the others do not, which is a resource question rather than an
    # authentication one, and it is bounded on all three hops.
    "stream_task_events",
    "list_task_files",
}


def _router_handlers(tree: ast.Module) -> dict[str, ast.AST]:
    """Every function decorated with `@router.<verb>(...)`, by name.

    Parsed rather than pattern matched. A regex over the source drops a handler
    the moment anyone writes it slightly differently, and the two shapes that
    would drop one here are both ordinary: a return type annotation between the
    signature and the colon, which this repository's own style encourages, and a
    signature split across lines. Neither is visible to `ast`, which is the
    point of using it.
    """
    handlers: dict[str, ast.AST] = {}
    for node in ast.walk(tree):
        if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        for dec in node.decorator_list:
            call = dec.func if isinstance(dec, ast.Call) else dec
            if isinstance(call, ast.Attribute) and isinstance(call.value, ast.Name):
                if call.value.id == "router":
                    handlers[node.name] = node
    return handlers


def _is_verified_user_dependency(default: ast.AST | None) -> bool:
    """True for a parameter defaulted to `Depends(get_verified_user)`."""
    if not isinstance(default, ast.Call):
        return False
    if not (isinstance(default.func, ast.Name) and default.func.id == "Depends"):
        return False
    return any(
        isinstance(arg, ast.Name) and arg.id == "get_verified_user"
        for arg in default.args
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
    tree = ast.parse(source)
    handlers = _router_handlers(tree)

    assert set(handlers) == EXPECTED_AGENT_ROUTES, (
        "the agent proxy's route set changed. Every route here is reachable with "
        "nothing but the chat session, so a new one is an authentication decision "
        "and a removed one may leave a caller stranded. Update "
        f"EXPECTED_AGENT_ROUTES deliberately. found={sorted(handlers)} "
        f"expected={sorted(EXPECTED_AGENT_ROUTES)}"
    )

    ungated = sorted(
        name
        for name, node in handlers.items()
        if not any(
            _is_verified_user_dependency(default)
            for default in [
                *node.args.defaults,
                *node.args.kw_defaults,
            ]
        )
    )
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


def test_the_agent_proxy_registers_no_route_the_guard_cannot_see() -> None:
    """The decorator is not the only way to mount a route.

    `router.add_api_route(...)`, `router.api_route(...)` and `router.websocket(...)`
    all register a handler that `_router_handlers` above would not attribute to a
    decorated function, so a route mounted that way would be exempt from the
    dependency check without anything failing. None exist today; this fails if
    one appears, which is the moment to extend the check rather than the moment
    to discover it was never covering anything.
    """
    tree = ast.parse(AGENT_PROXY.read_text())
    smuggled = sorted(
        {
            node.func.attr
            for node in ast.walk(tree)
            if isinstance(node, ast.Call)
            and isinstance(node.func, ast.Attribute)
            and isinstance(node.func.value, ast.Name)
            and node.func.value.id == "router"
            and node.func.attr in {"add_api_route", "add_api_websocket_route", "websocket", "api_route"}
        }
    )
    assert not smuggled, (
        "the agent proxy mounts routes through "
        f"{smuggled}, which the get_verified_user check above does not inspect. "
        "Extend _router_handlers to cover them before this lands (#540)."
    )


def main() -> int:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: one front door on the chat origin (issue #540)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
