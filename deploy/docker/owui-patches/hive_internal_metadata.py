"""Keep Hive's internal `__metadata` carrier off every non-Hive connection.

Issue #1578. `__metadata` is not an Open WebUI field and not an OpenAI field.
It is a Hive-internal carrier, invented so the signed-in user's credential can
reach edge-api's `OWUIUnwrap` middleware through a body that Open WebUI would
otherwise send under a single static shim key
(`apps/edge-api/internal/auth/owui_unwrap.go`). Two places write it:
`deploy/docker/pipelines/hive_jwt_forward.py` on the main chat path, and
`deploy/docker/owui-patches/hive_upstream_auth.py` at the task dispatch seam.

Nothing took it back off. `routers/openai.py::generate_chat_completion` pops
the upstream `metadata` key before forwarding and leaves `__metadata` in the
payload, so whatever is in it travels to whichever OpenAI-compatible connection
the resolved model belongs to. That is Hive's own gateway on every deployment
this repository ships today, which is why it has never leaked. It stops being
true the moment an administrator adds a second connection: pointing the
external task model (`task.model.external`) at a model served by a vendor
connection then sends `upstream_auth: Bearer <the user's Supabase token>` to
that vendor on every conversation, with no user action and no second
misconfiguration.

So the fix is a strip at the connection boundary rather than a guard at the two
writers. A writer-side guard has to be remembered by every future writer, and a
third one is cheap to add: the carrier is a plain dict key. The boundary is the
one place that knows the destination, and there are only five requests that
leave `routers/openai.py` with a body: `generate_chat_completion`,
`embeddings`, `responses`, `speech` and the default-disabled `proxy`
passthrough. All five go through this module, and
`apply_internal_metadata_boundary_1578_patch.py` asserts that count at image
build time, so a sixth added upstream fails the build rather than becoming a
quiet hole.

WHAT IS STRIPPED
----------------
The whole `__metadata` object, not `upstream_auth` alone. The token is the
worst thing in it, not the only thing: it is a Hive-internal channel, so every
field it ever carries is by definition something a vendor was not meant to
read. Stripping the object keeps that true for fields nobody has added yet.

WHAT COUNTS AS HIVE'S OWN CONNECTION
------------------------------------
Exactly one destination: the value of `OPENAI_API_BASE_URL`, which is
`http://edge-api:8080/v1` in docker-compose.yml. That is the same variable
`hive_rag_env_config.openai_connection_override` reads, and it writes it into
the persisted `openai.api_base_urls` row as a ONE element list on every boot,
so the trusted value here and the connection the container actually calls come
from the same place rather than from two opinions that can drift.

Upstream's plural `OPENAI_API_BASE_URLS` is deliberately NOT honoured. It is
upstream's multi-connection form, so trusting it would mean trusting every
connection an operator listed in it, which is the opposite of what this module
is for. Nothing in this repository sets it as an environment variable.

Comparing the URL rather than the connection index is deliberate. The index is
a position in an administrator-editable list; adding, deleting or reordering
connections in the admin panel renumbers it, and index 0 stops meaning Hive's
gateway the moment it does. `scripts/seed-owui-e2e-user.py` already merges into
an existing `openai.api_base_urls` list rather than replacing it, so a
deployment holding two entries is a real state and not a hypothetical one.

The comparison is exact, after trimming whitespace and one trailing slash on
both sides. A destination that differs from the configured gateway in any other
way, including letter case, is treated as somebody else's and the carrier is
dropped. That direction is the safe one, and the persisted row is reconciled
from this same variable at every boot, so the two agree by construction.

Absence fails closed: with the variable unset there is no destination this
module can vouch for, so nothing is forwarded. The consequence is visible and
attributable (a WARNING here, and `/v1/chat/completions` answering 401 at
edge-api, which logs its own line) rather than a silent egress. Every compose
file in this repository sets `OPENAI_API_BASE_URL` on the chat container, and
`hive_rag_env_config` already refuses to boot when it is set without its key,
so the closed case is a deployment that was never configured to reach Hive at
all.
"""

from __future__ import annotations

import json
import logging
import os
from urllib.parse import urlparse

log = logging.getLogger(__name__)

# The Hive-internal carrier. Named once, so a rename cannot leave a call site
# stripping a key that no longer exists while the real one keeps travelling.
INTERNAL_METADATA_KEY = '__metadata'

# The one variable that names Hive's own gateway, and the same one
# hive_rag_env_config.openai_connection_override reconciles the persisted
# connection row from. Upstream's plural OPENAI_API_BASE_URLS is not read here;
# see the module docstring.
GATEWAY_URL_ENV_NAME = 'OPENAI_API_BASE_URL'


def _normalize(url) -> str:
    """A base URL reduced to the form both sides of the comparison share."""
    if not isinstance(url, str):
        return ''
    return url.strip().rstrip('/')


def hive_gateway_base_url(environ=None) -> str:
    """Hive's own gateway base URL, or "" when the deployment declares none."""
    environ = os.environ if environ is None else environ
    return _normalize(environ.get(GATEWAY_URL_ENV_NAME))


def is_hive_gateway(url, environ=None) -> bool:
    """True only for the one destination this deployment declares as its own."""
    gateway = hive_gateway_base_url(environ)
    return bool(gateway) and _normalize(url) == gateway


def _safe_destination(url) -> str:
    """scheme://host:port, for the log line.

    The full URL is not logged. A connection base URL is administrator supplied
    and can legally embed userinfo, in the shape scheme://<user>:<password>@host,
    and this module exists because a credential reached somewhere it should not
    have. `urlparse().hostname` drops userinfo, and the path and query go with
    it.

    The example above is spelled with placeholders rather than as a URI
    literal on purpose: a literal of that shape is exactly what secret scanning
    flags, and one in the test fixture opened a GitGuardian incident on the
    first pass of this change.
    """
    parsed = urlparse(_normalize(url))
    if not parsed.scheme or not parsed.hostname:
        return '<unparseable destination>'
    port = f':{parsed.port}' if parsed.port else ''
    return f'{parsed.scheme}://{parsed.hostname}{port}'


def strip_internal_metadata(payload, url, environ=None):
    """`payload` with the Hive-internal carrier removed for any non-Hive destination.

    Returns the input object unchanged when there is nothing to strip, so a
    caller can compare by identity to learn whether anything was removed. When
    there is, a NEW dict is returned and the caller's own payload is left
    intact, per the repository's immutability convention.

    Never raises. This runs on the last line before a request is serialised, so
    an exception here would turn a credential-egress guard into an outage.
    """
    try:
        if not isinstance(payload, dict) or INTERNAL_METADATA_KEY not in payload:
            return payload
        if is_hive_gateway(url, environ):
            return payload

        carried = payload[INTERNAL_METADATA_KEY]
        # Field NAMES only. The values are the thing being kept out of reach.
        fields = sorted(carried) if isinstance(carried, dict) else [type(carried).__name__]
        log.warning(
            'hive (#1578): dropped the internal %s carrier (fields: %s) from a request '
            'bound for %s, which is not a Hive gateway connection. Nothing internal to '
            'Hive, including the signed-in user credential, is forwarded to a '
            'third-party connection.',
            INTERNAL_METADATA_KEY,
            fields,
            _safe_destination(url),
        )
        return {k: v for k, v in payload.items() if k != INTERNAL_METADATA_KEY}
    except Exception:  # pragma: no cover - defensive, see docstring
        log.exception('hive (#1578): failed to inspect the outgoing payload; dropping the carrier')
        if isinstance(payload, dict):
            return {k: v for k, v in payload.items() if k != INTERNAL_METADATA_KEY}
        return payload


def strip_internal_metadata_body(body, url, environ=None):
    """The same strip for a request whose body is raw bytes rather than a dict.

    `routers/openai.py::proxy` and `::speech` forward the bytes they received,
    and re-serialising every one of those would change bodies those endpoints
    are supposed to relay verbatim. So the body is rebuilt only when the
    carrier was actually there and actually had to go; anything that is not
    JSON, including an empty body, is returned untouched.
    """
    try:
        payload = json.loads(body)
    except Exception:
        return body
    sanitized = strip_internal_metadata(payload, url, environ)
    if sanitized is payload:
        return body
    return json.dumps(sanitized).encode()
