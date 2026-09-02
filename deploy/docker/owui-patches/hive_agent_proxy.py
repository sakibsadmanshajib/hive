"""hive_agent_proxy: the agent-task lifecycle, reachable from the chat frontend.

Why this exists at all.

The chat frontend renders the agent surface natively now, so it has to call
`/v1/agent/tasks` on edge-api. It cannot do that from the browser. It holds Open
WebUI's own session token, which edge-api has never heard of, and a Supabase
access token minted through Supabase's OAuth-server grant, which does not run
this project's `custom_access_token_hook` and therefore carries no `tenant_id`.
edge-api's `JWTMiddleware` fails such a token closed, deliberately, and that is
`.wolf/decisions.md` D-023 working rather than a bug to route around.

That OAuth token is also not in the browser. Open WebUI keeps it server side and
resolves it per request through `get_system_oauth_token`, refreshing it when it
has expired. Handing it to page JavaScript would be a downgrade, not a fix.

So the hop happens here, server side, on exactly the mechanism this deployment
already runs for chat completions: Open WebUI presents the static shim key on
`Authorization`, the signed-in user's token rides alongside it, and edge-api's
`OWUIUnwrap` middleware swaps the two and grants the tenant fallback precisely
because the shim key was presented. `hive_jwt_forward.py` does the same thing
for `/v1/chat/completions` and carries the token in the JSON body under
`__metadata.upstream_auth`. That carrier cannot serve this surface, because
three of the four calls below have no JSON body at all, so the token rides on
the `X-Hive-Upstream-Auth` header instead. Same gate, same trust decision, a
carrier a bodyless request can use. See
`apps/edge-api/internal/auth/owui_unwrap.go`.

The security properties this file is responsible for, all of them load bearing:

  * The principal is whoever Open WebUI says is signed in, and nothing else.
    Every route depends on `get_verified_user`. No route reads a user id, a
    tenant id, an email or a token from the request, so a caller cannot
    influence which principal edge-api resolves.
  * Neither the shim key nor the user's token is ever returned to the browser,
    logged, or echoed in an error.
  * The upstream path is built from a fixed set of six operations, with the
    task id validated as a UUID before it is interpolated and the events
    cursor params validated as plain non-negative integers before they reach
    a URL. This is not a general-purpose proxy, which is the shape #770
    removed from this image.
  * The request body forwarded upstream is rebuilt from two named fields rather
    than passed through, so nothing a caller invents (a `__metadata` block, for
    one) reaches edge-api.
"""

from __future__ import annotations

import asyncio
import os
from typing import Any
from uuid import UUID

import aiohttp
from fastapi import APIRouter, Depends, HTTPException, Request
from fastapi.responses import JSONResponse

from open_webui.utils.auth import get_verified_user

router = APIRouter()

# One request should never hold a worker for long. The agent-task endpoints are
# all small database reads and one control-plane call; a launch that is slow is
# slow on the far side of edge-api and reports itself through the task's own
# status, not by holding this connection open.
UPSTREAM_TIMEOUT_SECONDS = 30

# Bodies here are a pack name, a goal, and since issue #1065 the text of the
# documents the person attached in the composer.
#
# The three numbers below bound what is forwarded, not what is buffered:
# `await request.body()` has already read the whole body by the time the check
# runs, which was true before these numbers changed and is uvicorn's limit to
# enforce rather than this handler's.
#
# Kept in step with maxAttachments and maxAttachmentBytes in
# apps/edge-api/internal/agenttask/handler.go, which is where the refusal is
# enforced. This copy is a shape check on a rebuilt body, not a second policy.
MAX_ATTACHMENTS = 5
MAX_ATTACHMENT_TOTAL_BYTES = 256 * 1024

# Eight times the content cap, because JSON escaping is what decides the wire
# size: a control character encodes as \uXXXX, six bytes for one. Same
# derivation as maxCreateBodyBytes in edge-api's handler, and it has to clear
# edge-api's or a legal request would be refused here with a message about the
# description being too long.
MAX_REQUEST_BODY_BYTES = 8 * MAX_ATTACHMENT_TOTAL_BYTES

# The header edge-api reads the per-user token from. Must match
# `UpstreamAuthHeader` in apps/edge-api/internal/auth/owui_unwrap.go.
UPSTREAM_AUTH_HEADER = 'X-Hive-Upstream-Auth'


def _upstream_base() -> str:
    """The `/v1` root of the Hive gateway, from Open WebUI's own configuration.

    `OPENAI_API_BASE_URL` is already `http://edge-api:8080/v1` in this
    container, and `OPENAI_API_KEY` is already the shim key, so this proxy adds
    no new configuration and gives the shim key no new place to live.
    """
    base = (os.environ.get('OPENAI_API_BASE_URL') or '').strip().rstrip('/')
    return base


def _shim_key() -> str:
    return (os.environ.get('OPENAI_API_KEY') or '').strip()


async def _user_token(request: Request, user) -> str:
    """The signed-in user's Supabase access token, resolved server side.

    Imported here rather than at module scope: `open_webui.utils.middleware`
    pulls in a large part of the application, and this module is imported from
    `main.py` while that graph is still being built.
    """
    from open_webui.utils.middleware import get_system_oauth_token

    token = await get_system_oauth_token(request, user)
    return ((token or {}).get('access_token') or '').strip()


def _task_id(raw: str) -> str:
    """Refuse anything that is not a UUID before it reaches a URL.

    edge-api validates this too. It is validated here as well because this is
    the boundary where a caller-supplied string first becomes part of a URL,
    and a boundary that trusts its input because something downstream will
    check it is a boundary that stops being safe the moment downstream moves.
    """
    try:
        return str(UUID(raw))
    except (ValueError, AttributeError, TypeError):
        raise HTTPException(status_code=400, detail='Invalid task id.')


async def _call(
    request: Request,
    user,
    method: str,
    path: str,
    payload: dict[str, Any] | None = None,
) -> JSONResponse:
    """One upstream call, with the shim key on Authorization and the user's
    token on the carrier header.

    Every failure below is reported to the browser as a plain sentence with no
    internal detail in it: no URL, no upstream error text, no credential, and
    no provider identity, which is this repository's standing rule for anything
    that crosses a customer boundary.
    """
    base = _upstream_base()
    shim = _shim_key()
    if not base or not shim:
        # A deployment that never configured the gateway. Say what is true
        # without naming what is missing.
        raise HTTPException(
            status_code=503,
            detail='The agent service is not configured on this deployment.',
        )

    token = await _user_token(request, user)
    if not token:
        # Fail closed rather than proceeding with the shim key alone, which
        # edge-api would now refuse anyway (requiresPerUserAuth covers these
        # paths) but which would otherwise have run as the shim's own account.
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
                method, f'{base}{path}', headers=headers, json=payload
            ) as response:
                body = await response.json(content_type=None)
                # edge-api's own JSON is returned verbatim, including its error
                # shape, because the front end already knows how to read it and
                # re-wrapping it here would mean two error vocabularies for one
                # surface. It is provider-blind at the source.
                return JSONResponse(status_code=response.status, content=body)
    # Timeouts are caught before ClientError on purpose, and the exception is
    # named as asyncio.TimeoutError rather than the builtin.
    #
    # Measured against the pinned image (ghcr.io/open-webui/open-webui:v0.10.2)
    # rather than assumed: Python 3.11.15, aiohttp 3.13.5, and there
    # `asyncio.TimeoutError is TimeoutError` is True, so the builtin would work
    # today. It stops working on any interpreter older than 3.11, where the two
    # are different classes and a total-timeout breach would escape as an
    # unhandled exception, which on this path means a 500 carrying a traceback.
    #
    # The ordering matters for the same reason: aiohttp's ServerTimeoutError
    # inherits from ClientError as well as from TimeoutError, so catching
    # ClientError first would report a connect timeout as unreachable.
    except asyncio.TimeoutError:
        raise HTTPException(
            status_code=504,
            detail='The agent service took too long to answer. Try again shortly.',
        )
    except aiohttp.ClientError:
        raise HTTPException(
            status_code=502,
            detail='The agent service could not be reached. Try again shortly.',
        )
    except ValueError:
        # A non-JSON upstream body. Nothing honest to hand the front end.
        raise HTTPException(
            status_code=502,
            detail='The agent service returned an unreadable response.',
        )


@router.get('/tasks')
async def list_tasks(request: Request, user=Depends(get_verified_user)):
    return await _call(request, user, 'GET', '/agent/tasks')


@router.post('/tasks')
async def create_task(request: Request, user=Depends(get_verified_user)):
    raw = await request.body()
    if len(raw) > MAX_REQUEST_BODY_BYTES:
        raise HTTPException(status_code=413, detail='That task description is too long.')
    try:
        submitted = await request.json()
    except ValueError:
        raise HTTPException(status_code=400, detail='Invalid request body.')
    if not isinstance(submitted, dict):
        raise HTTPException(status_code=400, detail='Invalid request body.')

    pack = submitted.get('pack')
    instructions = submitted.get('instructions')
    if not isinstance(pack, str) or not pack.strip():
        raise HTTPException(status_code=400, detail='Choose what kind of task this is.')
    if not isinstance(instructions, str) or not instructions.strip():
        raise HTTPException(status_code=400, detail='Describe the task first.')

    attachments = _attachments(submitted.get('attachments'))

    # Rebuilt from named fields, never forwarded wholesale. The pack
    # vocabulary itself is deliberately not duplicated here: edge-api owns it
    # and answers an unknown pack with its own error, so there is one list of
    # packs in the system rather than two that can disagree.
    body = {'pack': pack.strip(), 'instructions': instructions.strip()}
    if attachments:
        body['attachments'] = attachments
    return await _call(request, user, 'POST', '/agent/tasks', body)


def _attachments(raw):
    """Rebuild the attachment list, field by field, or refuse it.

    Same posture as the rest of this handler: nothing the browser sent is
    forwarded as it arrived, and a shape that is not the documented one is a
    400 here rather than something edge-api has to guess at. The name is left
    for edge-api and the launcher to validate as a file name; what is checked
    here is only that the body is the shape this route documents.
    """
    if raw is None:
        return []
    if not isinstance(raw, list):
        raise HTTPException(status_code=400, detail='Invalid request body.')
    if len(raw) > MAX_ATTACHMENTS:
        raise HTTPException(
            status_code=400,
            detail=f'A task can carry at most {MAX_ATTACHMENTS} attachments.',
        )
    rebuilt = []
    total = 0
    for item in raw:
        if not isinstance(item, dict):
            raise HTTPException(status_code=400, detail='Invalid request body.')
        name = item.get('name')
        content = item.get('content')
        if not isinstance(name, str) or not name.strip():
            raise HTTPException(status_code=400, detail='An attachment is missing its name.')
        if not isinstance(content, str) or not content:
            raise HTTPException(
                status_code=400,
                detail=f'{name.strip()} has no readable text yet.',
            )
        total += len(content.encode('utf-8'))
        if total > MAX_ATTACHMENT_TOTAL_BYTES:
            raise HTTPException(
                status_code=400,
                detail=f'Those attachments hold more than {MAX_ATTACHMENT_TOTAL_BYTES // 1024} KB of text.',
            )
        rebuilt.append({'name': name.strip(), 'content': content})
    return rebuilt


@router.get('/tasks/{task_id}')
async def get_task(task_id: str, request: Request, user=Depends(get_verified_user)):
    return await _call(request, user, 'GET', f'/agent/tasks/{_task_id(task_id)}')


@router.post('/tasks/{task_id}/cancel')
async def cancel_task(task_id: str, request: Request, user=Depends(get_verified_user)):
    return await _call(request, user, 'POST', f'/agent/tasks/{_task_id(task_id)}/cancel')


def _cursor_param(raw: str | None, name: str) -> str:
    """Validate one cursor/limit query parameter before it reaches a URL.

    Only plain non-negative integers survive. Anything else (negative,
    non-numeric, oversized) is a 400 here rather than edge-api's error later,
    because this is the boundary where the value first becomes part of a URL.
    """
    if raw is None:
        return ''
    if not raw.isdigit() or len(raw) > 18:
        raise HTTPException(status_code=400, detail='Invalid cursor.')
    return f'&{name}={raw}'


@router.get('/tasks/{task_id}/events')
async def list_task_events(
    task_id: str,
    request: Request,
    user=Depends(get_verified_user),
    after_seq: str | None = None,
    limit: str | None = None,
):
    id_ = _task_id(task_id)
    qs = _cursor_param(after_seq, 'after_seq') + _cursor_param(limit, 'limit')
    path = f'/agent/tasks/{id_}/events'
    if qs:
        # The leading '&' pairs with a '?' added here; edge-api reads both
        # params from its own query parsing either way.
        path += '?' + qs.lstrip('&')
    return await _call(request, user, 'GET', path)


@router.get('/tasks/{task_id}/files')
async def list_task_files(task_id: str, request: Request, user=Depends(get_verified_user)):
    return await _call(request, user, 'GET', f'/agent/tasks/{_task_id(task_id)}/files')
