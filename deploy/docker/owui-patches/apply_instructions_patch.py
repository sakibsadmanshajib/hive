#!/usr/bin/env python3
"""Mount hive_instructions's router on Open WebUI's FastAPI application.

Issue #1363. Same asserted-splice pattern as apply_credits_patch.py and
apply_agent_proxy_patch.py: one anchored insertion into main.py that asserts
its own assumptions, so a digest bump that moves upstream's router block fails
the build instead of shipping an image whose settings pane 404s on save.

The routes land at /api/v1/hive/instructions, next to the credits balance at
/api/v1/hive/credits and the agent proxy at /api/v1/hive/agent, under the same
Open WebUI session gate.

TARGET is overridable so scripts/test_owui_instructions.py can drive this
against a synthesized main.py without a container.
"""

from __future__ import annotations

import ast
import os
import pathlib
import re
import sys

MAIN = pathlib.Path(
    os.environ.get('HIVE_OWUI_MAIN_PATH', '/app/backend/open_webui/main.py')
)

# The same utils include_router line the agent-proxy and credits splices anchor
# on. Each inserts immediately after the anchor, so whichever runs last still
# finds the anchor intact and lands directly after it too; order between the
# three does not matter.
ANCHOR = re.compile(
    r'^app\.include_router\(\s*utils\.router,\s*prefix=(["\'])/api/v1/utils\1,\s*tags=\[(["\'])utils\2\]\s*\)$',
    re.MULTILINE,
)

INSERTION = """
# hive: the signed-in user's own custom instructions (#1363), read and written
# against edge-api under the signed-in user's token. The storage and the chat
# injection both live there, so the text shown in settings is the text that
# shapes the replies. See deploy/docker/owui-patches/hive_instructions.py.
from open_webui.utils.hive_instructions import router as hive_instructions_router

app.include_router(hive_instructions_router, prefix='/api/v1/hive/instructions', tags=['hive'])
"""

MARKER = 'hive_instructions_router'


def main() -> int:
    source = MAIN.read_text()

    if MARKER in source:
        print(f'hive: {MAIN} already mounts the instructions router, nothing to do')
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
        print('hive: instructions insertion did not land exactly once', file=sys.stderr)
        return 1

    # Parse before writing, so a broken splice never reaches the image.
    ast.parse(patched)
    MAIN.write_text(patched)
    print('hive: mounted hive_instructions router on /api/v1/hive/instructions')
    return 0


if __name__ == '__main__':
    sys.exit(main())
