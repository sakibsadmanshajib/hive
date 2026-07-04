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

import base64
import json
import os
from typing import Any

from pydantic import BaseModel


def _log_claim_keys(token: str) -> None:
    """#269 diagnostic (e2e-mode only): logs which top-level claims are
    present on the forwarded access_token, never the token itself or any
    claim value. edge-api's jwtMiddleware 401s with "missing principal
    claims" whenever `sub` or `tenant_id` fails to parse (see
    apps/edge-api/internal/auth/middleware.go); this tells us, from a real
    OWUI-issued OAuth token, which one is actually missing without ever
    printing the token or its contents. Uses print() (not logging) so it
    reaches container stdout regardless of OWUI's logger configuration.
    """
    try:
        payload_b64 = token.split(".")[1]
        padded = payload_b64 + "=" * (-len(payload_b64) % 4)
        claims = json.loads(base64.urlsafe_b64decode(padded))
        print(
            f"hive_jwt_forward diagnostic: claim_keys={sorted(claims.keys())!r}",
            flush=True,
        )
    except Exception as exc:  # noqa: BLE001 -- diagnostic only, never fatal
        print(f"hive_jwt_forward diagnostic: decode failed: {exc}", flush=True)


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
        token = (__oauth_token__ or {}).get("access_token")
        if token:
            body.setdefault("__metadata", {})["upstream_auth"] = f"Bearer {token}"
            if self.e2e_mode:
                _log_claim_keys(token)

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
