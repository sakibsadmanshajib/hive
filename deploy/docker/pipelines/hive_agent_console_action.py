"""
hive_agent_console_action: Open WebUI Action function.

Adds an "Open Agent Workspace" button under chat messages that hands the
signed-in user off to the standalone agent-console sidecar (blueprint Step
3.1, ratified 2026-07-16: a dedicated Next.js app, NOT a fork of Open
WebUI). Gated on the tenant's ENABLE_COWORK feature flag, read live from
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

DISCOVERY NOTE FOR REVIEWERS (mirrors the PR #322 pattern of flagging
unverified upstream behaviour instead of guessing silently):
  * `__oauth_token__` injection into `action()`: verified only by analogy
    to hive_jwt_forward.py's `Filter.inlet`, which confirmed this parameter
    name is injected by Open WebUI's generic special-parameter framework
    (open_webui.utils.plugin / open_webui.utils.middleware), not by reading
    Action-specific source. High confidence, not first-hand confirmed for
    Action.
  * Opening the console in a new tab: emitting a `type: "message"` event
    to append a clickable Markdown link is the most conservative mechanism
    that is definitely rendered by Open WebUI's chat view (it's plain
    assistant-visible content), so that's what this file does. A "run this
    JS in the browser" event type would give a real window.open() but its
    exact name/shape was not confirmed against the pinned OWUI digest in
    docker-compose.yml, so this file does not attempt it. If a reviewer
    confirms such an event exists on the pinned image, swapping to it is a
    small follow-up, not a redesign.
  * Per-tenant button visibility (hiding the button entirely when the gate
    is off) needs an Open WebUI manifest-level capability this file did not
    find documented; instead the gate is checked at click time and an error
    is shown in place of the link. Functionally equivalent for the demo
    (the workspace never opens when disabled) but not a hidden button.
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

import os
from typing import Any, Awaitable, Callable, Optional

import aiohttp
from pydantic import BaseModel


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
    ) -> Optional[dict[str, Any]]:
        if __event_emitter__ is None:
            return None

        token = (__oauth_token__ or {}).get("access_token")
        if not token:
            await __event_emitter__(
                {
                    "type": "message",
                    "data": {
                        "content": "\n\n_Could not open the agent workspace: no active Hive session._\n"
                    },
                }
            )
            return None

        if not await self._cowork_enabled(token):
            await __event_emitter__(
                {
                    "type": "message",
                    "data": {
                        "content": "\n\n_The agent workspace is not enabled for your organization._\n"
                    },
                }
            )
            return None

        await __event_emitter__(
            {
                "type": "message",
                "data": {
                    "content": f"\n\n[Open Agent Workspace]({self.valves.console_path})\n"
                },
            }
        )
        return None

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
