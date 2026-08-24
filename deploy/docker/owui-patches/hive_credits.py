"""hive_credits: the signed-in chat user's remaining credits, server side.

Issue #1063. The composer banner needs one number, and the browser must not
learn anything it can use to reach billing state directly: the tenant ->
account link lives in `public.tenant_billing_accounts`, which is
service-role-only by design, and the browser holds no credential any Hive
service accepts. So this module is a thin authenticated hop: Open WebUI's own
session (`get_verified_user`) names the principal, the email rides in a POST
body (never a URL, so it cannot land in access logs), and control-plane
resolves the tenant -> account -> balance chain behind its internal token.

What this file is responsible for, all load bearing:

  * The principal is whoever Open WebUI says is signed in. The route depends
    only on `get_verified_user`; nothing from the request influences which
    identity is resolved.
  * The internal token is read per request from the environment and never
    returned, logged, or echoed in an error.
  * Every failure is reported as a plain sentence: no URL, no upstream error
    text, no identity detail. A failure here is also deliberately quiet on
    the frontend: the banner simply does not render.

Posture gate (#1063 point 5): prepaid credits are a hosted-SaaS concept.
Enterprise deployments do not set HIVE_CONTROL_PLANE_URL /
CONTROL_PLANE_INTERNAL_TOKEN on the chat container, the token check below
fails closed with 404, and no banner renders. Silent absence is the intended
enterprise posture; loud absence would be console spam in a posture where the
concept does not apply.

Why a shim at all: D-044 makes Open WebUI a view over control-plane-owned
state. This endpoint is exactly that shape: OWUI serves the trimmed number
under its own session, the ledger stays in the Go service that owns money.
"""

from __future__ import annotations

import logging
import os

import aiohttp
from fastapi import APIRouter, Depends, HTTPException
from open_webui.utils.auth import get_verified_user

log = logging.getLogger('open_webui.utils.hive_credits')

router = APIRouter()

# One small DB read on the far side; 10s is generous. The frontend polls on
# mount/focus at most every 30-60s, never per message.
UPSTREAM_TIMEOUT_SECONDS = 10


def _control_plane_base() -> str:
    return (os.environ.get('HIVE_CONTROL_PLANE_URL') or '').strip().rstrip('/')


def _internal_token() -> str:
    return (os.environ.get('CONTROL_PLANE_INTERNAL_TOKEN') or '').strip()


@router.get('/balance')
async def balance(user=Depends(get_verified_user)):
    """The signed-in user's tenant balance, trimmed to what the banner shows."""
    base = _control_plane_base()
    token = _internal_token()
    email = (getattr(user, 'email', '') or '').strip()

    if not base or not token or not email:
        # Fail closed as "nothing to show", matching the enterprise posture:
        # silent absence, not an error surface.
        raise HTTPException(status_code=404, detail='Credits are unavailable.')

    try:
        timeout = aiohttp.ClientTimeout(total=UPSTREAM_TIMEOUT_SECONDS)
        async with aiohttp.ClientSession(timeout=timeout) as session:
            async with session.post(
                f'{base}/internal/chat/credits/balance',
                json={'email': email},
                headers={'Authorization': f'Bearer {token}'},
            ) as response:
                if response.status != 200:
                    # One server-side line, no email in it. The browser stays
                    # quiet; an operator reading container logs still learns
                    # the surface is down rather than silently absent.
                    log.warning('hive credits: upstream answered %s', response.status)
                    raise HTTPException(status_code=404, detail='Credits are unavailable.')
                data = await response.json(content_type=None)
        # Coerced inside the try on purpose: a malformed payload must land in
        # the same quiet failure path, not a 500 traceback.
        balance = {
            'available_credits': int(data.get('available_credits') or 0),
            'usage_today_credits': int(data.get('usage_today_credits') or 0),
            # Deployment-configured top-up destination, passed through verbatim
            # or empty. Never derived from the request.
            'top_up_url': (os.environ.get('HIVE_CONSOLE_BILLING_URL') or '').strip(),
        }
    except HTTPException:
        raise
    except Exception:
        # Never echo upstream detail across the customer boundary.
        log.warning('hive credits: upstream call failed')
        raise HTTPException(status_code=404, detail='Credits are unavailable.')
    return balance
