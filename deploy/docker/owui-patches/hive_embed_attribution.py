"""Bill a chat embedding to the user who caused it, not to the shim account.

Issue #1696. Open WebUI's Python retrieval path is configured with
`RAG_OPENAI_API_BASE_URL=http://edge-api:8080/v1` and
`RAG_OPENAI_API_KEY=${OWUI_SHIM_KEY}` (deploy/docker/docker-compose.yml), so
every embedding it produces is a real, metered call on Hive's own gateway. Web
search indexing is the loudest of them: one search fetches several pages,
chunks them and embeds every chunk, which is where issue #1609's burst of
roughly two hundred embedding calls came from.

All of that spend settled against whichever account owns OWUI_SHIM_KEY. The
searching customer's balance and usage showed nothing they had done, one
platform account absorbed the embedding spend of every tenant at once, and the
per-tenant budget work was defeated because the spend never reached the tenant
the cap applies to.

The fix is the mechanism this deployment already has, not a second one. Open
WebUI cannot set a per-user Authorization header on an upstream embedding call,
so edge-api reads the signed-in user's token off `X-Hive-Upstream-Auth` instead,
honoured only when Authorization carries the shim key
(apps/edge-api/internal/auth/owui_unwrap.go). deploy/docker/owui-patches/
hive_agent_proxy.py already uses that carrier for the agent-task endpoints. This
module attaches the same carrier to embedding calls.

Fail closed, deliberately. A call with no resolvable user raises here rather
than going out under the shim key, because falling back to the shim is exactly
the defect (D-034). The raise is redundant with edge-api, which now refuses a
shim-key embeddings call with no carrier outright, and it is kept anyway: it
names the real cause in the chat container's own log, which is the one place the
gateway's 401 cannot explain itself.

The credential is the same one the chat Filter and the task seam forward: the
signed-in user's OAuth access token, resolved server side through Open WebUI's
own `get_system_oauth_token`. Only the access token, never an id_token, for the
audience and confused-deputy reasons hive_upstream_auth.py states.

Applied to the pinned image rather than to vendor/open-webui, because the chat
image builds only the FRONTEND from the vendored tree (see
Dockerfile.open-webui) and a backend edit under vendor/ is inert.
"""

from __future__ import annotations

import logging
import time
from typing import Any

log = logging.getLogger(__name__)

# Must match `UpstreamAuthHeader` in apps/edge-api/internal/auth/owui_unwrap.go.
UPSTREAM_AUTH_HEADER = 'X-Hive-Upstream-Auth'

# How long a resolved token is reused before it is looked up again.
#
# Not an optimisation of a rare call. Open WebUI batches by
# RAG_EMBEDDING_BATCH_SIZE and issues one HTTP request per batch, so a single
# web search reaches this function tens to hundreds of times in a few seconds;
# resolving per call would mean that many OAuth session reads against Postgres
# on a path that already runs against a 15-client pool shared with live chat.
#
# Thirty seconds is safe against expiry rather than merely short: Open WebUI's
# own get_oauth_token refreshes a token five minutes before it expires, so a
# value cached for thirty seconds cannot outlive its own validity window.
_TOKEN_TTL_SECONDS = 30.0

# ponytail: a plain dict with a hard reset, not an LRU. Entries are keyed by
# user id and expire in thirty seconds, so the natural size is "users embedding
# right now" and the cap only exists so a pathological workload cannot grow it
# without bound. Swap in functools.lru_cache-style eviction if the reset ever
# shows up as a latency spike, which at this size it cannot.
_CACHE_MAX_ENTRIES = 512
_token_cache: dict[str, tuple[str, float]] = {}


class AttributionUnavailable(Exception):
    """No signed-in user could be attached to an embedding call.

    Raised rather than returning the headers unchanged: unchanged headers carry
    the shim key alone, which is the mis-attribution this module exists to end.
    """


class _RequestStandIn:
    """The two attributes `get_system_oauth_token` reads, and nothing else.

    Open WebUI's resolver takes a Request so it can prefer the browser's
    `oauth_session_id` cookie and reach `app.state.oauth_manager`. An embedding
    call has no request in scope: `save_docs_to_vector_db` runs in a thread pool
    and hands the coroutine to the main loop through
    `asyncio.run_coroutine_threadsafe`, and `get_embedding_function`'s closure
    is given the user and the prefix only.

    Presenting no cookies sends the resolver down its own documented fallback,
    the user's most recent stored OAuth session. That resolves the SAME user, so
    attribution is identical; what it loses is the ability to pick one specific
    session out of several for that user, which nothing here depends on.

    Threading a real Request all the way down would mean changing four upstream
    signatures in the pinned image, which is a much larger splice for no
    difference in who gets billed.
    """

    __slots__ = ('cookies', 'app')

    def __init__(self, app: Any) -> None:
        self.cookies: dict[str, str] = {}
        self.app = app


async def _resolve_token(user: Any) -> str:
    """The signed-in user's access token, or "" when none resolves.

    Imports are deferred: `open_webui.main` imports the router graph that
    reaches this module, so a module-level import would close a cycle at
    interpreter start. Same reason hive_agent_proxy.py defers its own.
    """
    from open_webui.main import app
    from open_webui.utils.middleware import get_system_oauth_token

    token = await get_system_oauth_token(_RequestStandIn(app), user)
    if not isinstance(token, dict):
        return ''
    access_token = token.get('access_token')
    return access_token.strip() if isinstance(access_token, str) else ''


async def user_token(user: Any) -> str:
    """A cached access token for `user`, or "" when none resolves.

    A failed resolution is NOT cached. Caching one would turn a single
    transient database error into thirty seconds of refused embeddings for that
    user, and the failure path is already the expensive one.
    """
    user_id = getattr(user, 'id', None)
    if not isinstance(user_id, str) or not user_id:
        return ''

    now = time.monotonic()
    cached = _token_cache.get(user_id)
    if cached is not None and cached[1] > now:
        return cached[0]

    token = await _resolve_token(user)
    if not token:
        return ''
    if len(_token_cache) >= _CACHE_MAX_ENTRIES:
        _token_cache.clear()
    _token_cache[user_id] = (token, now + _TOKEN_TTL_SECONDS)
    return token


def forget(user_id: str) -> None:
    """Drop a cached token. Exported for the tests, and for any future caller
    that learns a token has been invalidated before its TTL runs out."""
    _token_cache.pop(user_id, None)


async def attach(headers: dict[str, str], user: Any) -> dict[str, str]:
    """`headers` plus the signed-in user's bearer on the carrier header.

    Returns a new dict rather than mutating the caller's, per this repository's
    immutability convention. Raises AttributionUnavailable when there is no user
    or no resolvable credential, so the call is refused rather than billed to
    the shim account.
    """
    if user is None:
        raise AttributionUnavailable(
            'hive (#1696): an embedding call arrived with no signed-in user, so '
            'it cannot be attributed and will not be sent under the shared key'
        )

    try:
        token = await user_token(user)
    except AttributionUnavailable:
        raise
    except Exception as exc:  # noqa: BLE001 - the cause is logged, then refused
        log.exception('hive (#1696): could not resolve a credential for user %s',
                      getattr(user, 'id', None))
        raise AttributionUnavailable(
            'hive (#1696): resolving the signed-in user credential failed'
        ) from exc

    if not token:
        log.error(
            'hive (#1696): no upstream credential for user %s, refusing to embed '
            'under the shared shim key; this spend would otherwise land on a '
            'platform account instead of on the customer',
            getattr(user, 'id', None),
        )
        raise AttributionUnavailable(
            'hive (#1696): no signed-in user credential for this embedding call'
        )

    return {**headers, UPSTREAM_AUTH_HEADER: f'Bearer {token}'}
