"""
hive_agent_console_action: Open WebUI Action function.

Adds an "Open Agent Workspace" button under chat messages that hands the
signed-in user off to the standalone agent-console sidecar (blueprint Step
3.1, ratified 2026-07-16: a dedicated Next.js app, NOT a fork of Open
WebUI).

One click, and nothing is written to the conversation (#541). The button
navigates the browser straight to the workspace and every failure is a
transient toast, so a click that cannot succeed leaves no trace in the
user's history. See the upstream API notes below for how that is done on
the pinned image and what was ruled out.

This is the per-message entry point, not the primary one. The persistent
launcher in deploy/docker/owui-static/loader.js is what users find first;
it is styled to match Open WebUI's own header icon buttons per decision
D-013 (forking Open WebUI for a real nav slot was rejected). That launcher
is hidden below 768px, where Open WebUI's unclamped model selector would
collide with it, so this Action is the only entry point at those widths.

Gated on the tenant's ENABLE_COWORK feature flag, read live from
edge-api's GET /v1/featuregate (added in #322 for exactly this: a
Bearer-authenticated end user reading their own gate map).

Install target (#269, same as hive_jwt_forward.py): Open WebUI's native
Functions system (Admin > Functions), installed via the Functions REST API
(POST /api/v1/functions/create, then POST .../toggle and .../toggle/global)
authenticated as an OWUI admin. There is no file-mount or env-var auto-load
-- see apps/web-console/e2e/phase-19/owui/owui.setup.ts for the reference
installer, and README-agent-console.md in this directory for the one-time
production/EnterpriseEdge install step (same pattern as hive_jwt_forward,
which that README documents as a manual post-first-admin-login step until
non-CI deployments automate it).

Open WebUI detects a Function's type by class name at exec time (`Filter`,
`Pipe`, `Action`, or `Event` -- see open_webui.utils.plugin), so the class
below must be named exactly `Action`.

UPSTREAM API NOTES (#541). Confirmed first-hand against the pinned image
`ghcr.io/open-webui/open-webui:v0.10.2@sha256:9fcea9c6...` named in
deploy/docker/Dockerfile.open-webui, by reading the image's own
`/app/backend/open_webui/utils/actions.py` and the shipped frontend bundle,
cross-checked against upstream tag v0.10.2. Hive's own patches to that image
touch branding, the page title and OAuth role resolution only, never event
handling.
  * `__event_call__` IS injected into an Action's `action()`. It is built in
    `open_webui/utils/actions.py` and placed in `extra_params`, then copied
    into the call kwargs only when the parameter is declared in the
    `action()` signature, which is why it appears below.
  * `type: "execute"` is handled by the chat view, which runs the payload's
    `code` through `new Function("return (async () => { ... })()")` and
    returns the awaited result to the caller. That is the only mechanism on
    this version that can navigate the browser, and it is what replaces the
    Markdown link this file used to append.
  * `type: "notification"` is rendered as a transient svelte-sonner toast
    (`toast.error` / `warning` / `success` / `info` keyed off `data.type`).
    Nothing is written to the message, which is why every failure path below
    uses it. The previous `type: "message"` events became permanent
    assistant content in the user's history.
  * Returning `None` writes nothing to the transcript. Only a returned
    `{"messages": [...]}` mutates it, and this Action never returns one.
  * Per-tenant button visibility is still not available. Action manifests
    expose `id`, `name`, `description` and `icon` only, the Functions table
    has no access-control column, and `Valves` are read for `priority`
    ordering rather than visibility. Gating therefore stays a click-time
    check, now surfaced as a toast instead of as chat content. The only
    upstream alternative is attaching the Action to a restricted custom
    model via `meta.actionIds`, which is a catalogue change, not a change
    to this file.
  * `aiohttp` availability: not a declared dependency of this repo. Assumed
    present because Open WebUI's own backend uses aiohttp internally for
    its HTTP client paths, and Functions execute in the same interpreter/
    dependency set as the main app (no separate requirements file, no
    per-Function venv) -- not confirmed by reading the pinned image's
    installed package list directly. If this assumption is wrong, the fix
    is swapping to stdlib `urllib.request` in `_cowork_enabled` only; the
    test in test_hive_agent_console_action.py exercises the real import
    path so a bad assumption here fails loudly instead of silently
    (see the CRITICAL bug this exact gap caused, fixed in review).
"""

from __future__ import annotations

import json
import os
from typing import Any, Awaitable, Callable, Optional

import aiohttp
from pydantic import BaseModel

# Navigation performed in the browser, via the `execute` event described above.
#
# `window.open` is attempted first because leaving chat in the same tab loses
# the user's place in the conversation. It can legitimately return null: this
# code runs from a socket callback rather than from a click handler, so it has
# no transient user activation and a popup blocker may refuse it. Falling back
# to a same-tab navigation is better than stranding the user, and the return
# value tells the Python side which happened so an unexpected result can be
# surfaced instead of silently doing nothing.
#
# `w.opener = null` severs the new tab's handle on the chat window. The target
# is same-origin (Caddy serves both -- deploy/docker/Caddyfile.owui), so this is
# hygiene rather than a trust boundary, and it is wrapped because assigning to
# `opener` is not universally permitted. The `noopener` window feature would do
# the same thing, but it forces `window.open` to return null, which would make
# a blocked popup indistinguishable from a successful one.
#
# The URL is injected with json.dumps, never with an f-string: this string is
# evaluated as JavaScript source, so a value carrying a quote would otherwise
# escape the literal and run as code.
_OPEN_IN_NEW_TAB_JS = """
const url = {url};
const w = window.open(url, "_blank");
if (!w) {{ window.location.assign(url); return "same-tab"; }}
try {{ w.opener = null; }} catch (e) {{}}
return "new-tab";
"""

_NAVIGATED = ("new-tab", "same-tab")


class Action:
    class Valves(BaseModel):
        # Same-origin default: Caddy serves the console under
        # /agent-workspace/* on the same host:port OWUI itself answers on
        # (see deploy/docker/Caddyfile.owui), so a relative path needs no
        # per-deployment config. Override only if the console is ever
        # split onto a different host.
        console_path: str = "/agent-workspace/tasks"
        edge_api_base_url: str = os.environ.get(
            "EDGE_API_INTERNAL_BASE_URL", "http://edge-api:8080"
        )

    def __init__(self) -> None:
        self.valves = self.Valves()

    async def action(
        self,
        body: dict[str, Any],
        __user__: Optional[dict[str, Any]] = None,
        __oauth_token__: Optional[dict[str, Any]] = None,
        __event_emitter__: Optional[Callable[[dict[str, Any]], Awaitable[None]]] = None,
        __event_call__: Optional[Callable[[dict[str, Any]], Awaitable[Any]]] = None,
    ) -> Optional[dict[str, Any]]:
        # Always returns None. Every outcome, success or failure, is delivered
        # as a transient event; nothing this Action does adds a message to the
        # conversation (#541).
        token = (__oauth_token__ or {}).get("access_token")
        if not token:
            await self._notify(
                __event_emitter__,
                "error",
                "Could not open the agent workspace: no active Hive session.",
            )
            return None

        if not await self._cowork_enabled(token):
            await self._notify(
                __event_emitter__,
                "error",
                "The agent workspace is not enabled for your organization.",
            )
            return None

        await self._open_workspace(__event_call__, __event_emitter__)
        return None

    async def _open_workspace(
        self,
        event_call: Optional[Callable[[dict[str, Any]], Awaitable[Any]]],
        event_emitter: Optional[Callable[[dict[str, Any]], Awaitable[None]]],
    ) -> None:
        path = self.valves.console_path

        # Degradation path, not the expected one: on the pinned image an Action
        # always receives __event_call__. If a future image stops injecting it,
        # name the destination in a toast rather than reintroducing a permanent
        # message in the transcript.
        if event_call is None:
            await self._notify(
                event_emitter,
                "info",
                f"Open the agent workspace at {path}",
            )
            return

        result = await event_call(
            {
                "type": "execute",
                "data": {"code": _OPEN_IN_NEW_TAB_JS.format(url=json.dumps(path))},
            }
        )

        if result in _NAVIGATED:
            return

        # The browser neither opened a tab nor navigated: the frontend reported
        # an error running the snippet, or returned something this version does
        # not document. Say so, rather than leaving a click that did nothing.
        await self._notify(
            event_emitter,
            "error",
            f"Could not open the agent workspace. It is available at {path}",
        )

    @staticmethod
    async def _notify(
        event_emitter: Optional[Callable[[dict[str, Any]], Awaitable[None]]],
        level: str,
        message: str,
    ) -> None:
        # `notification` renders as a toast and writes nothing to the message,
        # so these are visible to the user and absent from their history.
        if event_emitter is None:
            return
        await event_emitter(
            {"type": "notification", "data": {"type": level, "content": message}}
        )

    async def _cowork_enabled(self, access_token: str) -> bool:
        url = f"{self.valves.edge_api_base_url}/v1/featuregate"
        try:
            async with aiohttp.ClientSession() as session:
                async with session.get(
                    url,
                    headers={"Authorization": f"Bearer {access_token}"},
                    timeout=aiohttp.ClientTimeout(total=5),
                ) as resp:
                    if resp.status != 200:
                        return False
                    payload = await resp.json()
        except (aiohttp.ClientError, TimeoutError, ValueError):
            # Fail closed, matching the Gate.Fetch posture in
            # apps/edge-api/internal/featuregate/gate.go.
            return False

        gates = payload.get("gates") if isinstance(payload, dict) else None
        return bool(isinstance(gates, dict) and gates.get("ENABLE_COWORK") is True)
