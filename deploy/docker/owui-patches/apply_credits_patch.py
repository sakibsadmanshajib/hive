#!/usr/bin/env python3
"""Mount hive_credits's router on Open WebUI's FastAPI application.

Same asserted-splice pattern as apply_agent_proxy_patch.py: one anchored
insertion into main.py that asserts its own assumptions, so a digest bump
that moves upstream's router block fails the build instead of shipping an
image whose credits endpoint 404s.

The route lands at /api/v1/hive/credits/balance, next to the agent proxy at
/api/v1/hive/agent, under the same OWUI session gate.
"""

from __future__ import annotations

import ast
import pathlib
import re
import sys

MAIN = pathlib.Path('/app/backend/open_webui/main.py')

# Anchored on the same utils include_router line the agent-proxy splice uses,
# because it is one of the last unconditional includes in main.py's block and
# both insertions can share it without touching each other's output: each
# inserts immediately after the anchor, so the second one to run finds the
# anchor intact and lands directly after it too. Order between them does not
# matter.
ANCHOR = re.compile(
    r'^app\.include_router\(\s*utils\.router,\s*prefix=(["\'])/api/v1/utils\1,\s*tags=\[(["\'])utils\2\]\s*\)$',
    re.MULTILINE,
)

INSERTION = """
# hive: the signed-in user's remaining credits for the composer banner
# (#1063), resolved server side against control-plane behind the internal
# token. See deploy/docker/owui-patches/hive_credits.py.
from open_webui.utils.hive_credits import router as hive_credits_router

app.include_router(hive_credits_router, prefix='/api/v1/hive/credits', tags=['hive'])
"""

MARKER = 'hive_credits_router'


def main() -> int:
    source = MAIN.read_text()

    if MARKER in source:
        print(f'hive: {MAIN} already mounts the credits router, nothing to do')
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
        print('hive: credits insertion did not land exactly once', file=sys.stderr)
        return 1

    # Parse before writing, so a broken splice never reaches the image.
    ast.parse(patched)
    MAIN.write_text(patched)
    print('hive: mounted hive_credits router on /api/v1/hive/credits')
    return 0


if __name__ == '__main__':
    sys.exit(main())