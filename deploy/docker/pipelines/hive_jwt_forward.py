"""
hive_jwt_forward: Open WebUI Filter function.

For every outgoing OpenAI-compatible request, copies the signed-in user's
OAuth access token into request metadata so edge-api's OWUI unwrap
middleware (apps/edge-api/internal/auth/owui_unwrap.go) can validate the
real user instead of the static Open WebUI shim key. Without this, every
chat/embeddings request originating from OWUI carries the shim key in
Authorization, routes through the API-key path, and binds to the shim's
principal, defeating per-user audit attribution, RLS, and tenant scoping.

Install target (#269): Open WebUI's native Functions system (Admin >
Functions), NOT the separate, deprecated "Pipelines" microservice
(ghcr.io/open-webui/pipelines) this file's name and prior docstring
suggested. Open WebUI detects a Function's type purely by class name at
exec time, one of `Filter`, `Pipe`, `Action`, or `Event` (see
open_webui.utils.plugin.load_function_module_by_id), so a class named
`Pipeline` is never picked up at all. The ENABLE_PIPELINES/PIPELINES_URLS
docker-compose env vars this file used to be mounted alongside are also
not real Open WebUI config keys on this image. Both facts combined meant
this filter never ran, in production or in CI.

Functions are installed via a database row created through the Functions
REST API (POST /api/v1/functions/create, then POST .../toggle and
.../toggle/global), authenticated as an Open WebUI admin; there is no
file-mount or env-var based auto-load. See
apps/web-console/e2e/phase-19/owui/owui.setup.ts for the reference
installer used in CI, which runs right after the e2e user's first OIDC
login (Open WebUI auto-promotes the very first signed-in user to admin).
"""

from __future__ import annotations

import os
from typing import Any

from pydantic import BaseModel


def _log_oauth_token_shape(oauth_token: dict[str, Any] | None) -> None:
    """#269 diagnostic (e2e-mode only, temporary): logs the SHAPE of the
    __oauth_token__ OWUI resolved for this request -- never the token
    value itself. Disambiguates "OWUI never had an OAuth session for this
    call" (oauth_token is None) from "session found but no access_token
    field" (dict without access_token) from "token present" (has it,
    length only). See open_webui.utils.middleware.get_system_oauth_token /
    utils.oauth.OAuthManager.get_oauth_token upstream.
    """
    if oauth_token is None:
        print("hive_jwt_forward diagnostic: __oauth_token__ is None", flush=True)
        return
    token = oauth_token.get("access_token")
    print(
        f"hive_jwt_forward diagnostic: __oauth_token__ keys={sorted(oauth_token.keys())!r} "
        f"has_access_token={bool(token)} access_token_len={len(token) if token else 0}",
        flush=True,
    )


class Filter:
    class Valves(BaseModel):
        priority: int = 0

    def __init__(self) -> None:
        self.valves = self.Valves()
        self.e2e_mode = os.environ.get("OWUI_E2E_MODE", "").lower() in ("1", "true")

    async def inlet(
        self,
        body: dict[str, Any],
        __oauth_token__: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        if not isinstance(body, dict):
            return body

        # __oauth_token__ is resolved by Open WebUI's OAuthManager from the
        # signed-in user's stored OAuth session, auto-refreshing it if
        # expired (see open_webui.utils.middleware.get_system_oauth_token).
        # Forward ONLY the access_token. An id_token carries different `aud`
        # and lifetime semantics than an access_token and is intended for
        # OIDC identity assertions, not resource-server authorization; using
        # it here would invite confused-deputy / audience-mismatch attacks.
        # If no OAuth session is available (e.g. a non-OAuth admin account)
        # leave the body untouched; edge-api's OWUI unwrap then logs a
        # warning and the selector routes the shim-key Authorization to the
        # API-key path, which 401s loudly instead of silently misattributing
        # the request.
        if self.e2e_mode:
            _log_oauth_token_shape(__oauth_token__)
        token = (__oauth_token__ or {}).get("access_token")
        if token:
            body.setdefault("__metadata", {})["upstream_auth"] = f"Bearer {token}"

        if self.e2e_mode:
            body.setdefault("temperature", 0)
            body.setdefault("top_p", 1)

        return body

    async def outlet(self, body: Any) -> Any:
        # Strip the upstream auth header so the bearer token does not survive
        # into Open WebUI's response-side logging or get echoed back to the
        # browser. The inlet injects it; the outlet erases it.
        if isinstance(body, dict):
            metadata = body.get("__metadata")
            if isinstance(metadata, dict):
                metadata.pop("upstream_auth", None)
                if not metadata:
                    body.pop("__metadata", None)
        return body
