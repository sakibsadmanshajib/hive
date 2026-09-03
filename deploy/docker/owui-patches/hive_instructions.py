"""hive_instructions: the signed-in user's custom instructions, server side.

Issue #1363. A person needs one box in settings holding "how should the
assistant respond", and every chat turn of theirs has to be shaped by it. The
storage and the injection both live in edge-api
(apps/edge-api/internal/userinstructions, apps/edge-api/internal/chat), which
is what makes the text the person reads and the text the model receives the
same bytes rather than two copies that drift.

Why a shim at all, rather than the browser calling edge-api directly: the
browser holds Open WebUI's own session token, which edge-api has never heard
of. The Supabase access token edge-api does accept is kept server side and
resolved per request through `get_system_oauth_token`; handing it to page
JavaScript would be a downgrade, not a fix. So the hop happens here, on
exactly the mechanism this deployment already runs for chat completions and
for the agent-task lifecycle: Open WebUI presents the static shim key on
`Authorization`, the signed-in user's token rides on `X-Hive-Upstream-Auth`,
and edge-api's `OWUIUnwrap` middleware swaps the two. See
`deploy/docker/owui-patches/hive_agent_proxy.py`, which this file is modelled
on, and `apps/edge-api/internal/auth/owui_unwrap.go`.

The security properties this file is responsible for, all load bearing:

  * The principal is whoever Open WebUI says is signed in, and nothing else.
    Both routes depend only on `get_verified_user`. Neither reads a user id, a
    tenant id, an email or a token from the request, so a caller cannot
    influence whose instructions are read or written.
  * `/v1/user/instructions` is on edge-api's `requiresPerUserAuth` list, so a
    call that somehow arrived with the shim key alone is refused upstream
    rather than resolving to the shim account's own row.
  * Neither the shim key nor the user's token is ever returned to the browser,
    logged, or echoed in an error.
  * The forwarded body is rebuilt from one named field rather than passed
    through, so nothing a caller invents reaches edge-api.
"""

from __future__ import annotations

import asyncio
import json
import os

import aiohttp
from fastapi import APIRouter, Depends, HTTPException, Request
from fastapi.responses import JSONResponse

from open_webui.utils.auth import get_verified_user

router = APIRouter()

# Two small database round trips on the far side. 15s is generous; the browser
# reads this once when the settings pane opens and writes it when the person
# saves, never per message.
UPSTREAM_TIMEOUT_SECONDS = 15

# Eight times the 4000-character cap edge-api enforces, because JSON escaping
# decides the wire size: one control character encodes as \uXXXX, six bytes
# for one, and one character can be four bytes of UTF-8. Comfortably under
# edge-api's own 64 KiB body cap for this route, so a body this shim accepts
# is one edge-api will also accept, and the refusal a person sees names the
# character count rather than a byte count they cannot check.
MAX_REQUEST_BODY_BYTES = 8 * 4000

# Must match `UpstreamAuthHeader` in apps/edge-api/internal/auth/owui_unwrap.go.
UPSTREAM_AUTH_HEADER = 'X-Hive-Upstream-Auth'

UPSTREAM_PATH = '/user/instructions'


def _upstream_base() -> str:
    """The `/v1` root of the Hive gateway, from Open WebUI's own configuration.

    `OPENAI_API_BASE_URL` is already `http://edge-api:8080/v1` in this
    container and `OPENAI_API_KEY` is already the shim key, so this module adds
    no new configuration and gives the shim key no new place to live.
    """
    return (os.environ.get('OPENAI_API_BASE_URL') or '').strip().rstrip('/')


def _shim_key() -> str:
    return (os.environ.get('OPENAI_API_KEY') or '').strip()


async def _user_token(request: Request, user) -> str:
    """The signed-in user's Supabase access token, resolved server side.

    Imported inside the function rather than at module scope for the same
    reason hive_agent_proxy.py does it: `open_webui.utils.middleware` pulls in
    a large part of the application, and this module is imported from main.py
    while that graph is still being built.
    """
    from open_webui.utils.middleware import get_system_oauth_token

    token = await get_system_oauth_token(request, user)
    return ((token or {}).get('access_token') or '').strip()


async def _call(request: Request, user, method: str, payload: dict | None) -> JSONResponse:
    base = _upstream_base()
    shim = _shim_key()
    if not base or not shim:
        # A deployment that never configured the gateway. Say what is true
        # without naming what is missing.
        raise HTTPException(
            status_code=503,
            detail='Custom instructions are not configured on this deployment.',
        )

    token = await _user_token(request, user)
    if not token:
        # Fail closed rather than proceeding with the shim key alone. edge-api
        # refuses it anyway (this path is on requiresPerUserAuth), but a shim
        # that forwarded it would be asking to read someone else's row.
        raise HTTPException(
            status_code=401,
            detail='Your Hive sign-in could not be confirmed. Sign in again and retry.',
        )

    headers = {
        'Authorization': f'Bearer {shim}',
        UPSTREAM_AUTH_HEADER: f'Bearer {token}',
        'Accept': 'application/json',
    }
    if payload is not None:
        headers['Content-Type'] = 'application/json'

    timeout = aiohttp.ClientTimeout(total=UPSTREAM_TIMEOUT_SECONDS)
    try:
        async with aiohttp.ClientSession(timeout=timeout) as session:
            async with session.request(
                method, f'{base}{UPSTREAM_PATH}', headers=headers, json=payload
            ) as response:
                body = await response.json(content_type=None)
                # edge-api's JSON is returned verbatim, error shape included:
                # it is provider-blind at the source, and re-wrapping it here
                # would give one surface two error vocabularies.
                return JSONResponse(status_code=response.status, content=body)
    # Timeout before ClientError, and named as asyncio.TimeoutError rather than
    # the builtin, for the reason hive_agent_proxy.py documents at length: the
    # two are the same class on this image's Python 3.11 and different classes
    # on anything older, where a total-timeout breach would escape unhandled
    # and become a 500 carrying a traceback.
    except asyncio.TimeoutError:
        raise HTTPException(status_code=504, detail='Custom instructions timed out. Try again.')
    except aiohttp.ClientError:
        raise HTTPException(status_code=502, detail='Custom instructions are unavailable right now.')


@router.get('')
async def read_instructions(request: Request, user=Depends(get_verified_user)):
    """The signed-in user's instructions, or an empty string when unset."""
    return await _call(request, user, 'GET', None)


@router.put('')
async def write_instructions(request: Request, user=Depends(get_verified_user)):
    """Replace the signed-in user's instructions. Empty content clears them."""
    raw = await request.body()
    # `await request.body()` has already buffered the whole body by the time
    # this runs, which is uvicorn's limit to enforce rather than this
    # handler's. What this bound decides is what gets FORWARDED.
    if len(raw) > MAX_REQUEST_BODY_BYTES:
        raise HTTPException(status_code=413, detail='Custom instructions are too long.')

    try:
        submitted = json.loads(raw or b'{}')
    except ValueError:
        raise HTTPException(status_code=400, detail='Custom instructions could not be read.')
    if not isinstance(submitted, dict):
        raise HTTPException(status_code=400, detail='Custom instructions could not be read.')

    content = submitted.get('content')
    if content is None:
        content = ''
    if not isinstance(content, str):
        raise HTTPException(status_code=400, detail='Custom instructions must be text.')

    # Rebuilt from the one named field, never passed through, so nothing else
    # in the submitted object reaches edge-api.
    return await _call(request, user, 'PUT', {'content': content})
