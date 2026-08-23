#!/usr/bin/env python3
"""Mount hive_agent_proxy's router on Open WebUI's FastAPI application.

Only the frontend of this image is built from the fork; the Python backend
stays upstream's pinned release on purpose (see the header of
deploy/docker/Dockerfile.open-webui), so a backend addition arrives as a copied
module plus one asserted splice, exactly like hive_model_picker and
hive_rag_env_config before it.

The splice is one anchored insertion and it asserts everything it assumed: that
the anchor exists exactly once, that the file was not already patched, that the
insertion landed, and that the result still parses. A digest bump that moves
upstream's router block therefore fails the build rather than silently shipping
an image whose agent surface 404s.
"""

from __future__ import annotations

import ast
import pathlib
import re
import sys

MAIN = pathlib.Path('/app/backend/open_webui/main.py')

# Anchored on the utils router because it is one of the last unconditional
# include_router calls in the block, so the insertion lands after the routers
# our module never touches and before the conditional ones.
ANCHOR = re.compile(
    r'^app\.include_router\(\s*utils\.router,\s*prefix=(["\'])/api/v1/utils\1,\s*tags=\[(["\'])utils\2\]\s*\)$',
    re.MULTILINE,
)

INSERTION = """
# hive: the agent-task lifecycle, proxied server side so the chat frontend can
# reach /v1/agent/* with the signed-in user's own token rather than the shim's
# principal. See deploy/docker/owui-patches/hive_agent_proxy.py.
from open_webui.utils.hive_agent_proxy import router as hive_agent_router

app.include_router(hive_agent_router, prefix='/api/v1/hive/agent', tags=['hive'])
"""

MARKER = 'hive_agent_router'


def main() -> int:
    source = MAIN.read_text()

    if MARKER in source:
        print(f'hive: {MAIN} already mounts the agent proxy, nothing to do')
        return 0

    matches = list(ANCHOR.finditer(source))
    if len(matches) != 1:
        print(
            f'hive: expected exactly 1 utils include_router anchor in {MAIN}, found {len(matches)}',
            file=sys.stderr,
        )
        return 1

    end = matches[0].end()
    patched = source[:end] + '\n' + INSERTION + source[end:]

    if patched.count(MARKER) != 2:
        print('hive: agent proxy insertion did not land exactly once', file=sys.stderr)
        return 1

    # Parse before writing, so a broken splice never reaches the image.
    ast.parse(patched)

    MAIN.write_text(patched)
    print('hive: mounted the agent proxy router at /api/v1/hive/agent')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
