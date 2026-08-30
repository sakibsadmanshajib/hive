"""Build-time splice: stop publishing the gateway's internal address to chat users.

Issue #1562. Every arm of Open WebUI's audio router that fails to parse an
upstream error body falls back to stringifying the aiohttp exception into the
HTTP `detail` it returns to the browser. aiohttp bakes the request URL into
that string, so a signed-in user who pressed Read Aloud saw this toast:

    External: 500, message='Internal Server Error',
    url='http://edge-api:8080/v1/audio/speech'

Captured in a real browser before this patch existed:
docs/proof/voice-response-format-1562/read-aloud-before-fix.png. That is what
the owner reported as "the voice mode actually calls edgeapi:8080/v1". The
browser never called it. It was shown it, and then repeated it back.

This is not cosmetic and it is not merely confusing. `edge-api:8080` resolves
on the compose network, that reachability is real, and handing every chat user
the gateway's internal service names and ports is exactly the reconnaissance a
server-side request forgery needs. Nothing else in the product publishes that
map.

Why the fallback arm is the normal path here rather than a rare one: these
helpers call `r.raise_for_status()` first and only then `await r.json()`, by
which point the body has already been released, so `r.json()` raises and the
exception arm runs on EVERY upstream failure. The leak fired every time, not
occasionally.

The fix keeps the diagnostics and drops the address. A scrubbed detail still
reads `External: 500, message='Internal Server Error', url='[redacted]'`, which
is what a user or an operator can act on, so nobody is tempted to delete the
guard to get their error text back.

Applied here rather than in vendor/open-webui because the chat image builds
only the FRONTEND from the vendored tree and takes the backend from the pinned
upstream image (see Dockerfile.open-webui), so a backend edit under vendor/ is
inert. The exact pinned literals are asserted below: a digest bump that moves
them fails the image build loudly instead of silently restoring the leak.
"""

import ast
import os
import pathlib

TARGET = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_AUDIO_PY",
        "/app/backend/open_webui/routers/audio.py",
    )
)

MARKER = "# hive (#1562)"

# Inserted once, immediately above the shared TTS error helper, so every arm
# below can reach it.
ANCHOR = (
    "async def _raise_tts_error(exc: Exception, r=None) -> None:\n"
)

HELPER = r'''def _hive_safe_detail(value) -> str:
    """Strip internal addresses out of a message bound for an API client.

    Two shapes, because aiohttp produces both and only one carries a scheme:

        ClientResponseError -> 500, message='...', url='http://edge-api:8080/v1/...'
        ClientConnectorError -> Cannot connect to host edge-api:8080 ssl:default

    A scheme-anchored rule alone therefore leaks the entire connection-refused
    family, which is the family a reader hits first when a service is down.

    Everything else in the message is kept. The status and the upstream
    sentence are what an operator reads a bug report from, and a guard that
    throws them away is a guard someone deletes.

    The host:port rule requires a letter in the host and two to five digits in
    the port, so a clock time or a bare number is left alone, and `ssl:default`
    is left alone.
    """
    text = re.sub(r"(?i)\b[a-z][a-z0-9+.-]*://[^\s'<>\"]+", "[redacted]", str(value))
    return re.sub(
        r"(?i)\b(?=[a-z0-9.-]*[a-z])[a-z0-9][a-z0-9.-]*:\d{2,5}\b", "[redacted]", text
    )


'''

# The five client-facing stringifications. The TTS helper names the exception
# `exc`; the four transcription helpers name it `e`. Each is asserted for its
# own count so a partially-shifted upstream cannot pass.
#
# Every literal is anchored on the preceding newline, which is load bearing.
# `str.count` is substring based, so without the anchor the twelve-space form
# also matches inside each sixteen-space one and both counts come out wrong.
REPLACEMENTS = [
    ("\n            detail = f'External: {exc}'\n", 1),
    ("\n                detail = f'External: {e}'\n", 2),
    ("\n            detail = f'External: {e}'\n", 2),
]

text = TARGET.read_text()

if MARKER in text:
    raise SystemExit(0)  # idempotent: a re-run is a no-op, not a double splice

# The scrubber needs `re`, which this module does not import. Spliced in
# beside the other stdlib imports rather than imported inside the function, so
# the cost is paid once at import time instead of on every error path.
IMPORT_ANCHOR = "\nimport os\n"
assert text.count(IMPORT_ANCHOR) == 1, (
    "the stdlib import block in audio.py shifted -- "
    "upstream open-webui source shifted, patch needs updating"
)
assert "\nimport re\n" not in text, (
    "audio.py already imports re, so the splice below would duplicate it -- "
    "upstream open-webui source shifted, patch needs updating"
)
text = text.replace(IMPORT_ANCHOR, "\nimport os\nimport re\n", 1)

assert text.count(ANCHOR) == 1, (
    "_raise_tts_error is not defined exactly once -- "
    "upstream open-webui source shifted, patch needs updating"
)

total_expected = 0
for old, count in REPLACEMENTS:
    assert text.count(old) == count, (
        f"expected {count} occurrence(s) of {old!r}, found {text.count(old)} -- "
        "upstream open-webui source shifted, patch needs updating"
    )
    total_expected += count

patched = text.replace(ANCHOR, HELPER + MARKER + "\n" + ANCHOR, 1)

for old, _count in REPLACEMENTS:
    body = old.lstrip("\n")
    indent = body[: len(body) - len(body.lstrip())]
    name = "exc" if "{exc}" in old else "e"
    new = (
        f"\n{indent}{MARKER} never hand an aiohttp exception to a client: it\n"
        f"{indent}# carries the internal request URL.\n"
        f"{indent}detail = f'External: {{_hive_safe_detail({name})}}'\n"
    )
    patched = patched.replace(old, new)

applied = patched.count(MARKER)
assert applied == total_expected + 1, (
    f"expected {total_expected + 1} hive markers after patching "
    f"(one helper plus {total_expected} call sites), found {applied}"
)
assert "External: {e}" not in patched and "External: {exc}" not in patched, (
    "a raw exception stringification survived the splice"
)

ast.parse(patched)  # never write a router module that cannot be imported
TARGET.write_text(patched)
