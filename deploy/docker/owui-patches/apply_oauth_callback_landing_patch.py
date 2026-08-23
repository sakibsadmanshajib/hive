"""Build-time splice: send the OAuth callback's success redirect straight to
the application root instead of /auth.

Upstream redirects every OAuth callback to `/auth`, which converts the token
cookie into a stored session and then client-side navigates to `/` anyway, so
a repeat sign-in pays one extra full HTML document for nothing. The frontend
root layout now recovers the session from that cookie itself (see the
`token=` recovery block in vendor/open-webui/src/routes/+layout.svelte), so
the success leg can land directly on `/`.

The error leg is deliberately untouched: it keeps targeting /auth with
`?error=`, because the error display lives on the sign-in page. The assert
below pins that, so a future upstream change that reroutes errors cannot slip
past this patch.

Asserts its own effect and fails the build otherwise, the same posture as
this Dockerfile's other patches: a future open-webui digest bump whose OAuth
callback shifted breaks the build loudly rather than silently reintroducing
the bounce. Running this script twice fails on purpose: after the first run
the anchor is gone, and a second application would be a silent no-op.
"""

import ast
import pathlib

TARGET = pathlib.Path("/app/backend/open_webui/utils/oauth.py")
ANCHOR = "        redirect_url = f'{redirect_base_url}/auth'\n"
REPLACEMENT = "        redirect_url = f'{redirect_base_url}/'\n"
# The error leg must keep landing on the sign-in page where its display lives.
ERROR_LEG = "redirect_url}?error="

text = TARGET.read_text()

assert text.count(ANCHOR) == 1, (
    "the OAuth callback success redirect to '/auth' is not present exactly "
    "once -- upstream open-webui source shifted, patch needs updating"
)
assert ERROR_LEG in text, (
    "the OAuth callback error leg (?error= back to /auth) is missing -- "
    "upstream open-webui source shifted, patch needs updating"
)

patched = text.replace(ANCHOR, REPLACEMENT, 1)
ast.parse(patched)  # never write an oauth.py that cannot be imported
TARGET.write_text(patched)

# Assert-after: prove the splice landed and the error leg survived it.
final = TARGET.read_text()
assert final.count(REPLACEMENT) == 1, "success redirect replacement did not land"
assert final.count(ANCHOR) == 0, "stale '/auth' success target still present"
assert ERROR_LEG in final, "error leg was disturbed by the patch"
