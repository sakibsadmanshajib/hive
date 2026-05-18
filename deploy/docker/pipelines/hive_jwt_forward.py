"""
hive_jwt_forward - Open WebUI pipeline filter.

For every outgoing OpenAI-compatible request, copy the signed-in user's
Supabase token into request metadata so the edge-api JWT path can validate the
real user instead of the static Open WebUI shim key.
"""

from __future__ import annotations

import os
from typing import Any


class Pipeline:
    class Valves:
        priority: int = 0

    def __init__(self) -> None:
        self.name = "hive_jwt_forward"
        self.valves = self.Valves()
        self.e2e_mode = os.environ.get("OWUI_E2E_MODE", "").lower() in ("1", "true")

    async def on_startup(self) -> None:
        return None

    async def on_shutdown(self) -> None:
        return None

    async def inlet(self, body: dict[str, Any], user: dict[str, Any] | None = None) -> dict[str, Any]:
        if not isinstance(body, dict):
            return body

        token = None
        if user is not None:
            oauth = user.get("oauth_sub") or {}
            if isinstance(oauth, dict):
                token = oauth.get("access_token") or oauth.get("id_token")
            token = token or user.get("token")

        metadata = body.setdefault("__metadata", {})
        if isinstance(metadata, dict) and token:
            metadata["upstream_auth"] = f"Bearer {token}"

        if self.e2e_mode:
            body.setdefault("temperature", 0)
            body.setdefault("top_p", 1)

        return body

    async def outlet(self, body: Any, user: dict[str, Any] | None = None) -> Any:
        return body
