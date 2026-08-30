"""Carry the signed-in user's credential on every server-originated completion.

Issue #1567. Open WebUI has two ways of decorating an outgoing chat completion
and this deployment was only using one of them.

`deploy/docker/pipelines/hive_jwt_forward.py` is a native Functions Filter. Open
WebUI builds and runs that chain in `process_chat_payload` and
`process_filter_functions`, which is the main chat path and nothing else.
`routers/tasks.py` runs `process_pipeline_inlet_filter` instead, the legacy
external Pipelines mechanism, and then calls
`utils/chat.py::generate_chat_completion` directly. So every `/api/task/*`
background completion (title, tags, follow-ups, autocomplete, image prompt,
emoji, MOA, web-search query generation and retrieval query generation) left the
chat container with the static shim key on Authorization and no
`__metadata.upstream_auth` in the body. edge-api's `OWUIUnwrap` middleware
treats `/v1/chat/completions` as unconditionally requiring a per-user token, so
it failed closed with 401 UNAUTHENTICATED, deterministically, for every model.

This module is that same injection moved to a seam every one of those callers
passes through. It is deliberately NOT a relaxation of the check that rejected
them: the shim key gains nothing, `requiresPerUserAuth` is unchanged, and
edge-api is untouched. Background task work is performed on the signed-in user's
behalf and billed to them, so it should carry their identity, and the fix is to
supply the credential rather than to widen who may go without one.

Two properties are load-bearing and are asserted by
scripts/test_owui_task_upstream_auth.py:

  * idempotence. On the main chat path the Filter has already injected a token
    by the time the request reaches here, and displacing it would both discard a
    credential resolved earlier in the turn and pay for a second OAuth session
    lookup on the hot path. An existing `upstream_auth` always wins.
  * fail closed. When no OAuth session resolves, nothing is attached, the shim
    key stays on Authorization by itself, and edge-api answers 401. Attaching
    anything else, or letting the request through under the shim's principal,
    would mis-bill and mis-attribute the completion, which is the whole reason
    the unwrap middleware exists.

The payload is copied rather than mutated, per the repository's immutability
convention: the caller in `routers/tasks.py` keeps its own dict, and only the
copy that leaves carries a bearer token.
"""

from __future__ import annotations

import logging
from typing import Any

log = logging.getLogger(__name__)


async def resolve_upstream_auth(request: Any, user: Any) -> str:
    """The signed-in user's OAuth access token, or "" when none resolves.

    Delegates to Open WebUI's own resolver, which is the same one that produces
    the `__oauth_token__` parameter the chat Filter reads: it prefers the
    request's `oauth_session_id` cookie and falls back to the user's most recent
    stored OAuth session, refreshing an expired token on the way. Using it keeps
    the two injection sites on one definition of "this user's credential"
    instead of two that can drift.

    Imported inside the function on purpose. `utils/middleware.py` imports
    `utils/chat.py`, which is where this module is called from, so a module-level
    import would close a cycle at interpreter start.

    ONLY the access token is forwarded. An id_token carries different `aud` and
    lifetime semantics and is an identity assertion rather than resource-server
    authorization; forwarding one would invite a confused-deputy or
    audience-mismatch failure. Same reasoning as hive_jwt_forward.py, stated
    again here because the two sites are read independently.
    """
    from open_webui.utils.middleware import get_system_oauth_token

    token = await get_system_oauth_token(request, user)
    if not isinstance(token, dict):
        return ""
    access_token = token.get("access_token")
    return access_token.strip() if isinstance(access_token, str) else ""


async def attach_upstream_auth(request: Any, form_data: Any, user: Any) -> Any:
    """form_data with the caller's own bearer token in `__metadata`.

    Returns the input unchanged when a credential is already present, when none
    can be resolved, or when the payload is not a dict. Never raises: a failure
    to resolve a token must degrade to the pre-existing 401, which names the
    real cause at edge-api, rather than replacing it with a stack trace from
    inside the task handler.
    """
    if not isinstance(form_data, dict):
        return form_data

    metadata = form_data.get("__metadata")
    metadata = metadata if isinstance(metadata, dict) else {}

    existing = metadata.get("upstream_auth")
    if isinstance(existing, str) and existing.strip():
        return form_data

    try:
        access_token = await resolve_upstream_auth(request, user)
    except Exception:
        log.exception("hive: could not resolve the signed-in user's upstream credential")
        access_token = ""

    if not access_token:
        # Loud on the way out. edge-api already logs the rejected request, but it
        # cannot say which user or which task it belonged to, and the chat
        # container's own logs said nothing at all for the whole life of #1567.
        log.warning(
            "hive: no upstream credential for user %s on model %s; this "
            "completion will be rejected by the gateway (see issue #1567)",
            getattr(user, "id", None),
            form_data.get("model"),
        )
        return form_data

    return {
        **form_data,
        "__metadata": {**metadata, "upstream_auth": f"Bearer {access_token}"},
    }
