"""Client authentication for Open WebUI's OAuth token refresh (issue #782).

Open WebUI performs the authorization code exchange through authlib, but it
does NOT perform the refresh through authlib. `_perform_token_refresh` hand
builds the refresh POST and unconditionally puts `client_id` and
`client_secret` in the form body. That is the `client_secret_post` dialect.
authlib, meanwhile, defaults `token_endpoint_auth_method` to
`client_secret_basic` whenever a client secret is configured
(authlib/oauth2/client.py, `if token_endpoint_auth_method is None`).

So out of the box the two legs of the same OAuth client speak different
dialects. Providers that enforce the registered method exactly, Supabase
among them, accept the login and then reject every refresh with
400 invalid_credentials: "client is registered for 'client_secret_basic' but
'client_secret_post' was used". Open WebUI reads that failure as a dead
session and deletes it, which locks the user out of chat entirely until they
sign in again.

The fix is to make the refresh speak whatever dialect the code exchange
already speaks, rather than to force every deployment to re-register its
OAuth client. `token_endpoint_auth_method` is carried on the authlib client's
own `client_kwargs` (Open WebUI puts it there in config.py's
`oidc_oauth_register`), so this reads the single value that already governs
the exchange and cannot drift away from it.

Resolution order matches authlib's own so that an unset value behaves
identically on both legs:

  * explicit `client_kwargs['token_endpoint_auth_method']` wins, which is what
    `OAUTH_TOKEN_ENDPOINT_AUTH_METHOD` sets,
  * otherwise `client_secret_basic` when a client secret exists,
  * otherwise `none`, the public-client case, where there is no secret to send.

This module is copied into the image at
/app/backend/open_webui/utils/hive_oauth_client_auth.py and called from both
`_perform_token_refresh` implementations by
`apply_oauth_client_auth_patch.py`. It imports nothing from Open WebUI, so
`scripts/test_owui_oauth_client_auth.py` can exercise it directly.
"""

import base64
from urllib.parse import quote

FORM_CONTENT_TYPE = "application/x-www-form-urlencoded"

CLIENT_SECRET_BASIC = "client_secret_basic"
CLIENT_SECRET_POST = "client_secret_post"
NO_CLIENT_AUTH = "none"


def resolve_auth_method(client) -> str:
    """Return the token endpoint auth method authlib would use for `client`.

    Mirrors authlib's OAuth2Client.__init__ fallback so the refresh leg and
    the authorization code exchange leg can never disagree.
    """
    client_kwargs = getattr(client, "client_kwargs", None) or {}
    configured = client_kwargs.get("token_endpoint_auth_method")
    if configured:
        return configured
    return CLIENT_SECRET_BASIC if getattr(client, "client_secret", None) else NO_CLIENT_AUTH


def basic_auth_header(client_id: str, client_secret: str) -> str:
    """Build an RFC 6749 section 2.3.1 Basic credential.

    The spec requires each half to be form-urlencoded before the base64, which
    matters for any secret containing a colon or a plus.
    """
    raw = f"{quote(client_id, safe='')}:{quote(client_secret, safe='')}"
    return "Basic " + base64.b64encode(raw.encode("utf-8")).decode("ascii")


def hive_refresh_request(client, refresh_data: dict) -> dict:
    """Return the aiohttp POST keyword arguments for a refresh request.

    Returns `{"data": ..., "headers": ...}` so the call site stays a single
    `**hive_refresh_request(client, refresh_data)` expression, which keeps the
    build-time splice to one substitution per call site.

    For `client_secret_basic` the credentials move out of the body and into
    the Authorization header. They are removed from the body rather than
    duplicated: sending both is how a request comes to look like two different
    authentication methods at once, which providers reject on principle
    (RFC 6749 section 2.3 permits exactly one).

    Every other method, `client_secret_post` and `none` included, keeps
    upstream's body exactly as built, so this is a no-op for providers that
    were already working.
    """
    headers = {"Content-Type": FORM_CONTENT_TYPE}
    client_secret = getattr(client, "client_secret", None)

    if resolve_auth_method(client) != CLIENT_SECRET_BASIC or not client_secret:
        return {"data": refresh_data, "headers": headers}

    data = {k: v for k, v in refresh_data.items() if k not in ("client_id", "client_secret")}
    headers["Authorization"] = basic_auth_header(client.client_id, client_secret)
    return {"data": data, "headers": headers}
