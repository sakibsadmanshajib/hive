"""Build-time splice: route Open WebUI's OAuth refresh POSTs through
hive_oauth_client_auth.hive_refresh_request, so the refresh leg authenticates
the client the same way authlib authenticates the authorization code exchange
(issue #782).

Upstream builds the refresh body with `client_id` and `client_secret` in it
and posts with a single Content-Type header, which is the `client_secret_post`
dialect, while authlib defaults the exchange to `client_secret_basic`. Against
a provider that enforces the registered method, Supabase among them, the login
succeeds and every refresh fails with 400 invalid_credentials, at which point
Open WebUI deletes the OAuth session and the user cannot chat until they sign
in again. See hive_oauth_client_auth.py's header for the full chain.

There are TWO `_perform_token_refresh` implementations in this module, one on
the SSO OAuthManager and one on the MCP-facing OAuthClientManager, and both
build the request the same broken way. They share a byte-identical POST
argument pair, so one substitution fixes both call sites; this asserts the
count is exactly two so that an upstream change to either one is caught rather
than half-patched.

Asserts its own effect and fails the build otherwise, the same posture as this
Dockerfile's other patches: a future open-webui digest bump whose OAuth source
shifted breaks the build loudly instead of silently reverting to the broken
behaviour. Matching the anchor is not enough on its own, so this also asserts
that `client` is the name bound at both call sites and that the patched module
still parses.
"""

import ast
import pathlib
import re

TARGET = pathlib.Path("/app/backend/open_webui/utils/oauth.py")

# The POST argument pair as upstream writes it, byte identical at both call
# sites. Indentation is part of the anchor: it is what proves we matched the
# call site and not some other dict literal.
ANCHOR = (
    "                    data=refresh_data,\n"
    "                    headers={'Content-Type': 'application/x-www-form-urlencoded'},\n"
)
REPLACEMENT = "                    **hive_refresh_request(client, refresh_data),\n"

IMPORT_ANCHOR = "from open_webui.utils.auth import create_token, get_password_hash\n"
IMPORT_LINE = (
    "from open_webui.utils.hive_oauth_client_auth import hive_refresh_request\n"
)

EXPECTED_CALL_SITES = 2

text = TARGET.read_text()

assert text.count(ANCHOR) == EXPECTED_CALL_SITES, (
    f"expected exactly {EXPECTED_CALL_SITES} refresh POST call sites matching "
    f"the upstream data/headers pair, found {text.count(ANCHOR)} -- upstream "
    "open-webui source shifted, patch needs updating"
)

# Both call sites must have a local named `client`, which is what the helper
# reads client_secret and client_kwargs off. A rename upstream would leave the
# anchor untouched, pass the count check, and then NameError at refresh time,
# which is exactly the silent regression this patch exists to prevent.
refresh_defs = re.findall(
    r"\n    async def _perform_token_refresh\(self, session\) -> dict:\n(.*?)(?=\n    (?:async )?def |\Z)",
    text,
    re.DOTALL,
)
assert len(refresh_defs) == EXPECTED_CALL_SITES, (
    f"expected exactly {EXPECTED_CALL_SITES} _perform_token_refresh definitions, "
    f"found {len(refresh_defs)} -- upstream open-webui source shifted, patch "
    "needs updating"
)
for body in refresh_defs:
    assert re.search(r"^\s+client = (?:await )?self\.get_client\(", body, re.MULTILINE), (
        "_perform_token_refresh no longer binds a `client` local from "
        "self.get_client(...) -- upstream open-webui source shifted, patch "
        "needs updating"
    )
    assert ANCHOR in body, (
        "the refresh POST anchor is not inside a _perform_token_refresh body -- "
        "upstream open-webui source shifted, patch needs updating"
    )

assert text.count(IMPORT_ANCHOR) == 1, (
    "the import anchor is not present exactly once -- upstream open-webui "
    "source shifted, patch needs updating"
)
assert IMPORT_LINE not in text, "patch already applied"

patched = text.replace(ANCHOR, REPLACEMENT)
patched = patched.replace(IMPORT_ANCHOR, IMPORT_ANCHOR + IMPORT_LINE, 1)

ast.parse(patched)  # never write an oauth.py that cannot be imported

assert patched.count(REPLACEMENT) == EXPECTED_CALL_SITES, "splice did not apply"
assert ANCHOR not in patched, "an unpatched refresh POST call site survived"

TARGET.write_text(patched)
